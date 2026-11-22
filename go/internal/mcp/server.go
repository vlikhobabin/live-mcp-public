// Package mcpserver exposes the six AIDataRead MCP tools over stdio.
//
// Every tool uses the official Go MCP SDK low-level ToolHandler, which performs
// no input validation and returns the CallToolResult as-is. That mirrors the
// Python implementation's validate_input=False: locally invalid arguments
// become a normal ToolResult with the contract invalid_argument object instead
// of a protocol validation error, and no HTTP request is made. A narrow
// receiving middleware additionally turns an unknown tool name into the same
// contract-shaped ToolResult the Python server returns.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"live-mcp-for-1c/internal/aidata"
	"live-mcp-for-1c/internal/artifacts"
	"live-mcp-for-1c/internal/config"
)

const (
	// ServerName is the MCP serverInfo name shared with the Python product.
	ServerName = "live-mcp-for-1c"

	ToolValidate   = "validate_1c_query"
	ToolExecute    = "execute_1c_query"
	ToolDCSExecute = "execute_dcs_schema"
	ToolRender     = "render_dcs_schema"
	ToolPing       = "ping_service"
	ToolInfo       = "get_service_info"

	MaxQueryTextChars  = 65536
	MaxSchemaXMLBytes  = 1048576
	MaxParameters      = 64
	callToolMethodName = "tools/call"
)

type toolSpec struct {
	name        string
	description string
	schema      map[string]any
}

var toolSpecs = []toolSpec{
	{
		name:        ToolValidate,
		description: "Parse and admit a read-only 1C query without executing it.",
		schema: objectSchema(
			map[string]any{
				"query_text": queryTextProperty(
					"Read-only 1C query text to parse and admit.",
				),
				"parameters":     parametersProperty(),
				"limits":         limitsProperty(),
				"correlation_id": correlationProperty(),
				"requested_user": requestedUserProperty(),
			},
			"query_text",
		),
	},
	{
		name:        ToolExecute,
		description: "Execute an admitted read-only 1C query through AIDataRead.",
		schema: objectSchema(
			map[string]any{
				"query_text": queryTextProperty(
					"Admitted read-only 1C query text to execute.",
				),
				"parameters":     parametersProperty(),
				"output_format":  outputFormatProperty(),
				"limits":         limitsProperty(),
				"correlation_id": correlationProperty(),
				"requested_user": requestedUserProperty(),
			},
			"query_text",
		),
	},
	{
		name:        ToolDCSExecute,
		description: "Execute a supported Data Composition Schema into a value collection.",
		schema: objectSchema(
			map[string]any{
				"schema_xml":     schemaXMLProperty(),
				"parameters":     parametersProperty(),
				"output_format":  outputFormatProperty(),
				"limits":         limitsProperty(),
				"correlation_id": correlationProperty(),
				"requested_user": requestedUserProperty(),
			},
			"schema_xml",
		),
	},
	{
		name:        ToolRender,
		description: "Render a supported Data Composition Schema and optionally persist XLSX.",
		schema: objectSchema(
			map[string]any{
				"report_id":  schemaReportIDProperty(),
				"schema_xml": schemaXMLProperty(),
				"parameters": parametersProperty(),
				"output": map[string]any{
					"type": "object",
					"default": map[string]any{
						"format":    "json",
						"artifacts": []any{},
					},
				},
				"limits":         limitsProperty(),
				"correlation_id": correlationProperty(),
				"requested_user": requestedUserProperty(),
			},
			"report_id",
			"schema_xml",
		),
	},
	{
		name:        ToolPing,
		description: "Check AIDataRead service availability through GET /ping/.",
		schema:      objectSchema(map[string]any{}),
	},
	{
		name:        ToolInfo,
		description: "Read AIDataRead version, endpoints, auth, limits, and capabilities.",
		schema:      objectSchema(map[string]any{}),
	},
}

var toolsByName = func() map[string]toolSpec {
	byName := make(map[string]toolSpec, len(toolSpecs))
	for _, spec := range toolSpecs {
		byName[spec.name] = spec
	}
	return byName
}()

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		requiredNames := make([]any, 0, len(required))
		for _, name := range required {
			requiredNames = append(requiredNames, name)
		}
		schema["required"] = requiredNames
	}
	return schema
}

func queryTextProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"maxLength":   MaxQueryTextChars,
		"description": description,
	}
}

func schemaXMLProperty() map[string]any {
	return stringProperty(
		"Data Composition Schema XML with schemaVersion 1; at most 1048576 UTF-8 bytes.",
	)
}

func schemaReportIDProperty() map[string]any {
	return stringProperty(
		"Report identifier used by the service for the artifact name.",
	)
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func parametersProperty() map[string]any {
	return map[string]any{
		"type":     "array",
		"items":    map[string]any{"type": "object"},
		"default":  []any{},
		"maxItems": MaxParameters,
	}
}

func limitsProperty() map[string]any {
	return map[string]any{"type": "object", "default": map[string]any{}}
}

func outputFormatProperty() map[string]any {
	return map[string]any{
		"type":    "string",
		"enum":    []any{"json", "csv"},
		"default": "json",
	}
}

func correlationProperty() map[string]any {
	return stringProperty("Optional correlation id echoed by the service.")
}

func requestedUserProperty() map[string]any {
	return stringProperty("Optional diagnostic user hint; never impersonation.")
}

// New builds the stdio MCP server for one explicit AIDataRead connection.
func New(settings config.Settings, version string) *mcp.Server {
	client := aidata.New(settings)
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: version},
		nil,
	)
	for _, spec := range toolSpecs {
		spec := spec
		server.AddTool(
			&mcp.Tool{
				Name:        spec.name,
				Description: spec.description,
				InputSchema: spec.schema,
			},
			func(
				ctx context.Context,
				request *mcp.CallToolRequest,
			) (*mcp.CallToolResult, error) {
				return toolResult(callTool(
					ctx,
					client,
					settings.CacheRoot,
					spec.name,
					request.Params.Arguments,
				)), nil
			},
		)
	}
	server.AddReceivingMiddleware(
		orderToolsMiddleware(),
		unknownToolMiddleware(),
	)
	return server
}

// orderToolsMiddleware keeps the protocol-visible tool order identical to the
// Python server. The SDK stores tools in a map and lists them alphabetically;
// the published contract identifies tools by name and schema, but preserving
// the documented order avoids a gratuitous behavioral difference.
func orderToolsMiddleware() mcp.Middleware {
	order := make(map[string]int, len(toolSpecs))
	for index, spec := range toolSpecs {
		order[spec.name] = index
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			request mcp.Request,
		) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			if err != nil || method != "tools/list" {
				return result, err
			}
			list, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, err
			}
			sort.SliceStable(list.Tools, func(first, second int) bool {
				return order[list.Tools[first].Name] < order[list.Tools[second].Name]
			})
			return list, nil
		}
	}
}

// unknownToolMiddleware is deliberately narrow: it only replaces the SDK's
// JSON-RPC "unknown tool" error for tools/call with the contract-shaped
// ToolResult the Python server returns. Every known tool and every other MCP
// method continues to the SDK handler unchanged.
func unknownToolMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			request mcp.Request,
		) (mcp.Result, error) {
			if method == callToolMethodName {
				if call, ok := request.(*mcp.CallToolRequest); ok &&
					call.Params != nil {
					if _, known := toolsByName[call.Params.Name]; !known {
						return toolResult(invalidArgument(
							"Unknown tool.",
							map[string]any{"tool": call.Params.Name},
						).Envelope()), nil
					}
				}
			}
			return next(ctx, method, request)
		}
	}
}

func callTool(
	ctx context.Context,
	client *aidata.Client,
	cacheRoot string,
	name string,
	raw json.RawMessage,
) any {
	spec := toolsByName[name]
	arguments, localError := validateArguments(spec.schema, raw)
	if localError != nil {
		return localError.Envelope()
	}

	var payload any
	switch name {
	case ToolPing:
		payload, localError = client.Get(ctx, aidata.PingPath)
	case ToolInfo:
		payload, localError = client.Get(ctx, aidata.InfoPath)
	default:
		body, err := buildBody(name, arguments)
		if err != nil {
			return internalErrorEnvelope()
		}
		path, ok := fixedPostPath(name)
		if !ok {
			return internalErrorEnvelope()
		}
		payload, localError = client.Post(ctx, path, body)
	}
	if localError != nil {
		return localError.Envelope()
	}

	if name == ToolRender {
		envelope, ok := payload.(map[string]any)
		if !ok {
			return payload
		}
		var output any
		if value, present := arguments["output"]; present {
			output = value
		}
		return artifacts.PersistRenderArtifacts(envelope, output, cacheRoot)
	}
	return payload
}

func fixedPostPath(name string) (string, bool) {
	switch name {
	case ToolValidate:
		return aidata.QueryValidatePath, true
	case ToolExecute:
		return aidata.QueryExecutePath, true
	case ToolDCSExecute:
		return aidata.DCSExecutePath, true
	case ToolRender:
		return aidata.DCSRenderPath, true
	}
	return "", false
}

// validateArguments mirrors the Python local validation: unknown and missing
// fields are rejected first, then each explicitly provided field is checked
// against its published schema in the order the caller sent it.
func validateArguments(
	schema map[string]any,
	raw json.RawMessage,
) (map[string]any, *aidata.LocalError) {
	fields, localError := decodeArguments(raw)
	if localError != nil {
		return nil, localError
	}
	properties := schema["properties"].(map[string]any)

	arguments := make(map[string]any, len(fields))
	order := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, seen := arguments[field.name]; !seen {
			order = append(order, field.name)
		}
		arguments[field.name] = field.value
	}

	unknown := make([]string, 0)
	for name := range arguments {
		if _, ok := properties[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, invalidArgument(
			"Unknown tool argument(s).",
			map[string]any{"unknown_fields": unknown},
		)
	}

	if required, ok := schema["required"].([]any); ok {
		for _, name := range required {
			requiredName := name.(string)
			if _, present := arguments[requiredName]; !present {
				return nil, invalidArgument(
					fmt.Sprintf("Missing required argument: %s.", requiredName),
					map[string]any{"field": requiredName},
				)
			}
		}
	}

	for _, name := range order {
		property := properties[name].(map[string]any)
		if localError := validateArgument(name, property, arguments[name]); localError != nil {
			return nil, localError
		}
	}
	return arguments, nil
}

type argumentField struct {
	name  string
	value any
}

func decodeArguments(raw json.RawMessage) ([]argumentField, *aidata.LocalError) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		// JSON null is treated as an empty argument object, matching the
		// Python handler's `arguments or {}`.
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, malformedArguments()
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, malformedArguments()
	}
	fields := make([]argumentField, 0)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, malformedArguments()
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, malformedArguments()
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, malformedArguments()
		}
		fields = append(fields, argumentField{name: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, malformedArguments()
	}
	return fields, nil
}

func malformedArguments() *aidata.LocalError {
	return invalidArgument("Tool arguments must be a JSON object.", nil)
}

func validateArgument(
	name string,
	property map[string]any,
	value any,
) *aidata.LocalError {
	switch property["type"] {
	case "string":
		text, ok := value.(string)
		if !ok {
			return invalidArgument(
				fmt.Sprintf("Argument %s must be a string.", name),
				map[string]any{"field": name, "expected": "string"},
			)
		}
		if allowed, ok := property["enum"].([]any); ok {
			if !containsString(allowed, text) {
				return invalidArgument(
					fmt.Sprintf("Argument %s has an unsupported value.", name),
					map[string]any{"field": name, "allowed": allowed},
				)
			}
		}
		// Only query_text/schema_xml must be non-empty. report_id is required
		// by the render route but an empty string is valid.
		if (name == "query_text" || name == "schema_xml") && text == "" {
			return invalidArgument(
				fmt.Sprintf("Argument %s must not be empty.", name),
				map[string]any{"field": name},
			)
		}
		if name == "query_text" && utf8.RuneCountInString(text) > MaxQueryTextChars {
			return invalidArgument(
				fmt.Sprintf("Argument %s exceeds the maximum length.", name),
				map[string]any{
					"field":     name,
					"maxLength": MaxQueryTextChars,
				},
			)
		}
		if name == "schema_xml" {
			if !utf8.ValidString(text) {
				return invalidArgument(
					fmt.Sprintf("Argument %s is not valid UTF-8 text.", name),
					map[string]any{"field": name},
				)
			}
			if len(text) > MaxSchemaXMLBytes {
				return invalidArgument(
					fmt.Sprintf("Argument %s exceeds the maximum UTF-8 size.", name),
					map[string]any{
						"field":    name,
						"maxBytes": MaxSchemaXMLBytes,
					},
				)
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return invalidArgument(
				fmt.Sprintf("Argument %s must be an array.", name),
				map[string]any{"field": name, "expected": "array"},
			)
		}
		if itemsSchema, ok := property["items"].(map[string]any); ok &&
			itemsSchema["type"] == "object" {
			for _, item := range items {
				if _, ok := item.(map[string]any); !ok {
					return invalidArgument(
						fmt.Sprintf("Argument %s must contain objects only.", name),
						map[string]any{
							"field":    name,
							"expected": "array of objects",
						},
					)
				}
			}
		}
		if name == "parameters" && len(items) > MaxParameters {
			return invalidArgument(
				fmt.Sprintf("Argument %s exceeds the maximum item count.", name),
				map[string]any{"field": name, "maxItems": MaxParameters},
			)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return invalidArgument(
				fmt.Sprintf("Argument %s must be an object.", name),
				map[string]any{"field": name, "expected": "object"},
			)
		}
	}
	return nil
}

func containsString(allowed []any, value string) bool {
	for _, item := range allowed {
		if candidate, ok := item.(string); ok && candidate == value {
			return true
		}
	}
	return false
}

type querySpec struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

type schemaSpec struct {
	Format string `json:"format"`
	XML    string `json:"xml"`
}

type requestBody struct {
	SchemaVersion *int        `json:"schemaVersion,omitempty"`
	Query         *querySpec  `json:"query,omitempty"`
	Schema        *schemaSpec `json:"schema,omitempty"`
	ReportID      *string     `json:"reportId,omitempty"`
	Parameters    *any        `json:"parameters,omitempty"`
	Output        *any        `json:"output,omitempty"`
	Limits        *any        `json:"limits,omitempty"`
	CorrelationID *string     `json:"correlationId,omitempty"`
	RequestedUser *string     `json:"requestedUser,omitempty"`
}

func buildBody(name string, arguments map[string]any) ([]byte, error) {
	switch name {
	case ToolValidate:
		return encodeJSON(buildQueryBody(arguments))
	case ToolExecute:
		body := buildQueryBody(arguments)
		addOutputFormat(&body, arguments)
		return encodeJSON(body)
	case ToolDCSExecute:
		body := buildDCSBody(arguments)
		addOutputFormat(&body, arguments)
		return encodeJSON(body)
	case ToolRender:
		body := buildDCSBody(arguments)
		reportID := arguments["report_id"].(string)
		body.ReportID = &reportID
		if output, ok := arguments["output"]; ok {
			value := output
			body.Output = &value
		}
		return encodeJSON(body)
	}
	return nil, fmt.Errorf("tool %q has no HTTP body", name)
}

func buildQueryBody(arguments map[string]any) requestBody {
	body := requestBody{
		Query: &querySpec{
			Language: "1c",
			Text:     arguments["query_text"].(string),
		},
	}
	if value, ok := arguments["parameters"]; ok {
		body.Parameters = &value
	}
	if value, ok := arguments["limits"]; ok {
		body.Limits = &value
	}
	if value, ok := arguments["correlation_id"]; ok {
		text := value.(string)
		body.CorrelationID = &text
	}
	if value, ok := arguments["requested_user"]; ok {
		text := value.(string)
		body.RequestedUser = &text
	}
	return body
}

func buildDCSBody(arguments map[string]any) requestBody {
	schemaVersion := 1
	body := requestBody{
		SchemaVersion: &schemaVersion,
		Schema: &schemaSpec{
			Format: "DataCompositionSchemaXml",
			XML:    arguments["schema_xml"].(string),
		},
	}
	if value, ok := arguments["parameters"]; ok {
		body.Parameters = &value
	}
	if value, ok := arguments["limits"]; ok {
		body.Limits = &value
	}
	if value, ok := arguments["correlation_id"]; ok {
		text := value.(string)
		body.CorrelationID = &text
	}
	if value, ok := arguments["requested_user"]; ok {
		text := value.(string)
		body.RequestedUser = &text
	}
	return body
}

func addOutputFormat(body *requestBody, arguments map[string]any) {
	if value, ok := arguments["output_format"]; ok {
		var output any = map[string]any{"format": value}
		body.Output = &output
	}
}

// encodeJSON preserves present empty containers and exact json.Number values
// while disabling HTML escaping, so a semantically identical request cannot
// grow past the service maxRequestBytes.
func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func toolResult(payload any) *mcp.CallToolResult {
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		payload = internalErrorEnvelope()
		text, _ = json.MarshalIndent(payload, "", "  ")
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(text)}},
		StructuredContent: payload,
	}
}

func invalidArgument(message string, details map[string]any) *aidata.LocalError {
	if details == nil {
		details = map[string]any{}
	}
	return &aidata.LocalError{
		ErrorKind: "invalid_argument",
		Message:   message,
		Details:   details,
	}
}

func internalErrorEnvelope() aidata.Envelope {
	return (&aidata.LocalError{
		ErrorKind: "internal_error",
		Message:   "Internal MCP error.",
		Details:   map[string]any{},
	}).Envelope()
}
