// Package config loads the six documented process-environment settings for the
// standalone AIDataRead MCP server.
//
// The minimal product has exactly one connection and no discovery, profile
// selection, .env loading, compatibility aliases, or fallback. These settings
// are read from the process environment only and no network connection is
// opened while they are loaded.
package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	BaseURLEnv   = "LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL"
	UsernameEnv  = "LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME"
	PasswordEnv  = "LIVE_MCP_FOR_1C_HTTP_SERVICE_PASSWORD"
	VerifyTLSEnv = "LIVE_MCP_FOR_1C_VERIFY_TLS"
	TimeoutEnv   = "LIVE_MCP_FOR_1C_HTTP_TIMEOUT_SECONDS"
	CacheRootEnv = "LIVE_MCP_FOR_1C_CACHE_ROOT"

	DefaultTimeoutSeconds  = 60.0
	DefaultArtifactDirName = "dcs-artifacts"

	// Nanoseconds must fit a positive time.Duration (2^63), not merely a
	// positive float number of seconds.
	maxDurationNanoseconds = 1 << 63
)

// Settings is one resolved AIDataRead connection and local artifact cache
// configuration. Password is never included in String.
type Settings struct {
	BaseURL   string
	Username  string
	Password  string
	VerifyTLS bool
	Timeout   time.Duration
	CacheRoot string
}

// String redacts the password so accidental diagnostics never echo it.
func (s Settings) String() string {
	return fmt.Sprintf(
		"Settings{BaseURL:%q, Username:%q, Password:<redacted>, VerifyTLS:%t, Timeout:%s, CacheRoot:%q}",
		s.BaseURL, s.Username, s.VerifyTLS, s.Timeout, s.CacheRoot,
	)
}

// ArtifactDir returns the lazily created directory for generated XLSX files.
func (s Settings) ArtifactDir() string {
	return filepath.Join(s.CacheRoot, DefaultArtifactDirName)
}

// Load reads the settings from an os.Environ-shaped slice. Only the three
// required variables are mandatory; optional values use the documented
// defaults. No file is read and no directory is created.
func Load(environ []string) (Settings, error) {
	env := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}

	baseURL, err := required(env, BaseURLEnv)
	if err != nil {
		return Settings{}, err
	}
	// The base URL is explicitly trimmed, matching the Python implementation.
	baseURL = strings.TrimSpace(baseURL)
	// The username and password are explicit credential components:
	// whitespace is part of the value and must not be normalized away.
	username, err := required(env, UsernameEnv)
	if err != nil {
		return Settings{}, err
	}
	password, err := required(env, PasswordEnv)
	if err != nil {
		return Settings{}, err
	}
	verifyTLS, err := parseBoolean(env, VerifyTLSEnv, true)
	if err != nil {
		return Settings{}, err
	}
	timeout, err := parseTimeout(env)
	if err != nil {
		return Settings{}, err
	}
	cacheRoot, err := parseCacheRoot(env)
	if err != nil {
		return Settings{}, err
	}
	if err := validateBaseURL(baseURL); err != nil {
		return Settings{}, err
	}
	if username == "" {
		return Settings{}, fmt.Errorf("%s must not be empty.", UsernameEnv)
	}
	return Settings{
		BaseURL:   baseURL,
		Username:  username,
		Password:  password,
		VerifyTLS: verifyTLS,
		Timeout:   timeout,
		CacheRoot: cacheRoot,
	}, nil
}

func required(env map[string]string, name string) (string, error) {
	value, ok := env[name]
	if !ok {
		return "", fmt.Errorf("missing required environment variable: %s", name)
	}
	return value, nil
}

func validateBaseURL(baseURL string) error {
	if baseURL == "" || strings.HasSuffix(baseURL, "/") {
		return fmt.Errorf(
			"%s must be the publication base including the service root and must not end with '/'.",
			BaseURLEnv,
		)
	}
	if strings.ContainsAny(baseURL, "?#") {
		return fmt.Errorf("%s must not contain a query or fragment delimiter.", BaseURLEnv)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%s is not a valid absolute http(s) URL.", BaseURLEnv)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf(
			"%s must be an absolute http(s) URL without userinfo, query, or fragment.",
			BaseURLEnv,
		)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf(
			"%s must be an absolute http(s) URL without userinfo, query, or fragment.",
			BaseURLEnv,
		)
	}
	if parsed.User != nil {
		return fmt.Errorf(
			"%s must not contain userinfo and must use a valid port.",
			BaseURLEnv,
		)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number <= 0 || number >= 65536 {
			return fmt.Errorf(
				"%s must not contain userinfo and must use a valid port.",
				BaseURLEnv,
			)
		}
	}
	return nil
}

func parseBoolean(env map[string]string, name string, fallback bool) (bool, error) {
	raw, ok := env[name]
	if !ok {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be one of true/false, 1/0, yes/no, or on/off.", name)
}

func parseTimeout(env map[string]string) (time.Duration, error) {
	raw, ok := env[TimeoutEnv]
	if !ok {
		return time.Duration(DefaultTimeoutSeconds * float64(time.Second)), nil
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive number.", TimeoutEnv)
	}
	// Convert to nanoseconds first and require [1, 2^63) so neither rounding
	// down to zero nor overflow into a negative time.Duration can pass.
	nanoseconds := seconds * float64(time.Second)
	if nanoseconds < 1 || nanoseconds >= maxDurationNanoseconds {
		return 0, fmt.Errorf("%s must be a positive number.", TimeoutEnv)
	}
	return time.Duration(nanoseconds), nil
}

func parseCacheRoot(env map[string]string) (string, error) {
	raw, ok := env[CacheRootEnv]
	if !ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s could not be resolved: %v", CacheRootEnv, err)
		}
		return filepath.Join(home, ".cache", "live-mcp-for-1c"), nil
	}
	// Preserve the explicit value exactly: no trim, no "~" expansion and no
	// other path rewriting. A non-absolute value is a configuration error
	// rather than a silent fallback to the default cache.
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%s must be an absolute directory path when set.", CacheRootEnv)
	}
	return raw, nil
}
