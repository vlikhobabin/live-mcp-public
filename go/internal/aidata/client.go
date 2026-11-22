// Package aidata is the direct, fixed-route HTTP transport for the AIDataRead
// custom HTTP service.
//
// There is exactly one request per tool call: no retry, no redirect following,
// no environment proxy discovery, and no fallback route or profile. The
// service response body is the MCP result; only local/transport failures
// become LocalError values.
package aidata

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"live-mcp-for-1c/internal/config"
)

const (
	PingPath          = "/ping/"
	InfoPath          = "/info/"
	QueryValidatePath = "/v1/query/validate/"
	QueryExecutePath  = "/v1/query/execute/"
	DCSExecutePath    = "/v1/dcs/execute/"
	DCSRenderPath     = "/v1/dcs/render/"

	ServiceName = "ai-data-read"
)

// Only platform texts with a known, static meaning may be shown back to the
// MCP caller. HTTP-REFERENCE §13.3 documents this exact 403 text. Everything
// else (401 pages, proxy diagnostics, or arbitrary attacker-controlled text)
// is replaced by a constant placeholder so credentials and arbitrary upstream
// content never cross the local error boundary.
const canonicalPermissionText = "Недостаточно прав для использования ресурса с данным HTTP методом"

const bodyExcerptPlaceholder = "platform response text withheld (<redacted>)"

// LocalError is a transport, argument, or internal failure without a service
// JSON envelope.
type LocalError struct {
	ErrorKind  string
	Message    string
	HTTPStatus *int
	Details    map[string]any
}

// Error implements error.
func (e *LocalError) Error() string { return e.Message }

// Envelope is the contract-shaped MCP-local error object.
type Envelope struct {
	OK         bool           `json:"ok"`
	ErrorKind  string         `json:"error_kind"`
	Message    string         `json:"message"`
	HTTPStatus *int           `json:"http_status"`
	Details    map[string]any `json:"details"`
}

// Envelope returns the contract-shaped local error object.
func (e *LocalError) Envelope() Envelope {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	return Envelope{
		OK:         false,
		ErrorKind:  e.ErrorKind,
		Message:    e.Message,
		HTTPStatus: e.HTTPStatus,
		Details:    details,
	}
}

// Client is a bounded synchronous client for the six fixed AIDataRead routes.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// New builds a client for one explicit connection. No request is sent here.
func New(settings config.Settings) *Client {
	dialer := &net.Dialer{Timeout: settings.Timeout}
	transport := &http.Transport{
		// Never discover proxy settings from the environment.
		Proxy: nil,
		DialContext: func(
			ctx context.Context,
			network string,
			address string,
		) (net.Conn, error) {
			connection, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			wrapped := &timeoutConn{
				Conn:    connection,
				timeout: settings.Timeout,
			}
			if tracker, ok := ctx.Value(timeoutTrackerKey{}).(*connectionTracker); ok {
				tracker.add(wrapped)
			}
			return wrapped, nil
		},
		// The TLS handshake is a connect-phase operation in httpx and gets one
		// whole-handshake deadline. HTTP headers/body/writes keep the per-IO
		// deadlines applied by timeoutConn below; there is no whole-response
		// HTTP deadline.
		TLSHandshakeTimeout: settings.Timeout,
		// One request per tool call: do not reuse a connection across calls and
		// do not let net/http replay an idempotent request on a reused socket.
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			// The setting is explicit: verification is on by default and is
			// disabled only for a local diagnostic contour.
			InsecureSkipVerify: !settings.VerifyTLS,
		},
	}
	return &Client{
		baseURL:  settings.BaseURL,
		username: settings.Username,
		password: settings.Password,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// Surface a 3xx response as the service result instead of
				// following it, matching the Python client.
				return http.ErrUseLastResponse
			},
		},
	}
}

// timeoutConn applies an idle deadline to each actual socket read and write.
// It sits below net/http and TLS, so the TCP connect (handled by Dialer),
// TLS handshake, request write, response header read, and every subsequent
// response-body read all receive the same per-operation deadline. This mirrors
// httpx's read/write timeouts without a whole-response timer or extra
// goroutines, and it bounds a stalled request write when the peer accepts but
// does not read.
type timeoutConn struct {
	net.Conn
	timeout  time.Duration
	timedOut atomic.Bool
}

func (c *timeoutConn) Read(buffer []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.Conn.SetReadDeadline(
			time.Now().Add(c.timeout),
		); err != nil {
			return 0, err
		}
	}
	count, err := c.Conn.Read(buffer)
	markTimeout(c, err)
	return count, err
}

func (c *timeoutConn) Write(buffer []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.Conn.SetWriteDeadline(
			time.Now().Add(c.timeout),
		); err != nil {
			return 0, err
		}
	}
	count, err := c.Conn.Write(buffer)
	markTimeout(c, err)
	return count, err
}

func markTimeout(connection *timeoutConn, err error) {
	if err == nil {
		return
	}
	var netError net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &netError) && netError.Timeout()) {
		connection.timedOut.Store(true)
	}
}

type timeoutTrackerKey struct{}

type connectionTracker struct {
	mu          sync.Mutex
	connections []*timeoutConn
}

func (t *connectionTracker) add(connection *timeoutConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connections = append(t.connections, connection)
}

func (t *connectionTracker) timedOut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, connection := range t.connections {
		if connection.timedOut.Load() {
			return true
		}
	}
	return false
}

// Get sends one fixed GET request without a body and returns either the
// service JSON value or a local error.
func (c *Client) Get(ctx context.Context, path string) (any, *LocalError) {
	return c.request(ctx, http.MethodGet, path, nil)
}

// Post sends one fixed POST request and returns either the service JSON value
// or a local error. The body bytes are already encoded and forwarded unchanged.
func (c *Client) Post(ctx context.Context, path string, body []byte) (any, *LocalError) {
	return c.request(ctx, http.MethodPost, path, body)
}

// PostQueryValidate sends one fixed validate request. It is kept as a named
// wrapper for the validate-specific tests and callers.
func (c *Client) PostQueryValidate(ctx context.Context, body []byte) (any, *LocalError) {
	return c.Post(ctx, QueryValidatePath, body)
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (any, *LocalError) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	// The tracker is per request: with keep-alives disabled there is normally
	// exactly one connection, but the transport can close it and surface a
	// generic "use of closed network connection" after a write deadline. The
	// tracker preserves the timeout classification without retrying or adding
	// goroutines.
	tracker := &connectionTracker{}
	requestContext := context.WithValue(ctx, timeoutTrackerKey{}, tracker)
	request, err := http.NewRequestWithContext(
		requestContext,
		method,
		c.baseURL+path,
		reader,
	)
	if err != nil {
		return nil, &LocalError{
			ErrorKind: "internal_error",
			Message:   "Internal MCP error.",
			Details:   map[string]any{"endpoint_path": path},
		}
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	request.SetBasicAuth(c.username, c.password)
	// There is exactly one request per tool call: no transport retry. Clearing
	// GetBody prevents the transport from replaying the request after a
	// connection failure that occurred before any bytes were sent.
	request.GetBody = nil

	response, err := c.httpClient.Do(request)
	if err != nil {
		if tracker.timedOut() {
			return nil, timeoutError(path)
		}
		return nil, transportError(err, path)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		// A body read can fail with a read deadline after headers were already
		// received. net/http can replace the original deadline error with a
		// generic closed-connection error, so consult the per-request tracker
		// before falling back to error-chain classification.
		if tracker.timedOut() {
			return nil, timeoutError(path)
		}
		return nil, transportError(err, path)
	}

	payload, ok := decodeJSON(raw)
	if ok {
		if isSuccessEnvelope(payload, response.StatusCode) {
			return payload, nil
		}
		// A 4xx/5xx body is the service result only when the complete error
		// envelope is present. success:false plus service alone is not
		// enough: a partial object must not be mistaken for a CFE error.
		if isErrorEnvelope(payload, response.StatusCode) {
			return payload, nil
		}
	}

	status := response.StatusCode
	if status == http.StatusUnauthorized {
		return nil, &LocalError{
			ErrorKind:  "authentication_failed",
			Message:    "AIDataRead authentication failed.",
			HTTPStatus: &status,
			Details: map[string]any{
				"endpoint_path": path,
				"body_excerpt":  safeExcerpt(raw),
			},
		}
	}
	if status == http.StatusForbidden {
		return nil, &LocalError{
			ErrorKind:  "permission_denied",
			Message:    "AIDataRead denied access to the HTTP method.",
			HTTPStatus: &status,
			Details: map[string]any{
				"endpoint_path": path,
				"body_excerpt":  safeExcerpt(raw),
			},
		}
	}
	return nil, &LocalError{
		ErrorKind:  "upstream_invalid_response",
		Message:    "AIDataRead did not return the expected JSON service envelope.",
		HTTPStatus: &status,
		Details: map[string]any{
			"endpoint_path": path,
			"http_status":   status,
		},
	}
}

func timeoutError(path string) *LocalError {
	return &LocalError{
		ErrorKind: "upstream_timeout",
		Message:   "AIDataRead request timed out.",
		Details:   map[string]any{"endpoint_path": path},
	}
}

func transportError(err error, path string) *LocalError {
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return timeoutError(path)
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return timeoutError(path)
	}
	return &LocalError{
		ErrorKind: "upstream_connection_error",
		Message:   "AIDataRead request failed before a response was received.",
		Details:   map[string]any{"endpoint_path": path},
	}
}

func isSuccessEnvelope(payload any, httpStatus int) bool {
	if httpStatus != http.StatusOK {
		return false
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	success, ok := object["success"].(bool)
	if !ok || !success {
		return false
	}
	service, ok := object["service"].(string)
	return ok && service == ServiceName
}

func isErrorEnvelope(payload any, httpStatus int) bool {
	if httpStatus < 400 || httpStatus > 599 {
		return false
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	success, ok := object["success"].(bool)
	if !ok || success {
		return false
	}
	service, ok := object["service"].(string)
	if !ok || service != ServiceName {
		return false
	}
	if _, ok := object["code"].(string); !ok {
		return false
	}
	if _, ok := object["message"].(string); !ok {
		return false
	}
	_, ok = object["details"].(map[string]any)
	return ok
}

// safeExcerpt returns only a known canonical platform text or a fixed
// placeholder. The upstream body is never copied, excerpted, trimmed-to-fit,
// or partially redacted.
func safeExcerpt(raw []byte) string {
	normalized := strings.TrimSpace(string(raw))
	if normalized == canonicalPermissionText {
		return normalized
	}
	return bodyExcerptPlaceholder
}

// decodeJSON parses exactly one UTF-8 JSON value. Numbers are kept as
// json.Number so large/typed JSON numbers are forwarded unchanged instead of
// being converted to float64. Invalid UTF-8 is rejected: encoding/json would
// silently replace it with U+FFFD and could turn a malformed service response
// into a successful envelope.
func decodeJSON(raw []byte) (any, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return payload, true
}
