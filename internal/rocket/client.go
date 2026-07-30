package rocket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Credentials is everything needed to talk to a server as a given user.
// It is what we persist between runs so launch does not require a login.
type Credentials struct {
	ServerURL string `json:"server_url"`
	UserID    string `json:"user_id"`
	AuthToken string `json:"auth_token"`
	Username  string `json:"username"`
}

func (c Credentials) Valid() bool {
	return c.ServerURL != "" && c.UserID != "" && c.AuthToken != ""
}

// Client is a REST client for one Rocket.Chat server. It is safe for
// concurrent use.
type Client struct {
	baseURL *url.URL
	http    *http.Client

	mu    sync.RWMutex
	creds Credentials
}

// NewClient builds a client for serverURL, which may be given as
// "chat.example.com", "https://chat.example.com" or with a trailing slash.
func NewClient(serverURL string) (*Client, error) {
	u, err := normalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL: u,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		creds: Credentials{ServerURL: u.String()},
	}, nil
}

func normalizeServerURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("rocket: empty server URL")
	}
	if !strings.Contains(trimmed, "://") {
		// HTTPS is the right default for a real server, but a bare loopback
		// address is almost always a local dev instance served over plain HTTP.
		if isLoopbackHost(trimmed) {
			trimmed = "http://" + trimmed
		} else {
			trimmed = "https://" + trimmed
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("rocket: invalid server URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("rocket: server URL %q has no host", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("rocket: unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

// ServerURL returns the normalized base URL.
func (c *Client) ServerURL() string { return c.baseURL.String() }

// WebSocketURL returns the DDP endpoint for this server.
func (c *Client) WebSocketURL() string {
	scheme := "wss"
	if c.baseURL.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s%s/websocket", scheme, c.baseURL.Host, c.baseURL.Path)
}

// Credentials returns a copy of the current credentials.
func (c *Client) Credentials() Credentials {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.creds
}

// SetCredentials installs an auth token, e.g. one restored from disk.
func (c *Client) SetCredentials(creds Credentials) {
	c.mu.Lock()
	defer c.mu.Unlock()
	creds.ServerURL = c.baseURL.String()
	c.creds = creds
}

// Authenticated reports whether the client holds a token.
func (c *Client) Authenticated() bool { return c.Credentials().Valid() }

type request struct {
	method   string
	endpoint string // e.g. "subscriptions.get"
	query    url.Values
	body     any
	headers  map[string]string
	noAuth   bool
}

// do performs a REST call and decodes the JSON body into out.
func (c *Client) do(ctx context.Context, r request, out any) error {
	endpoint := c.baseURL.String() + "/api/v1/" + r.endpoint
	if len(r.query) > 0 {
		endpoint += "?" + r.query.Encode()
	}

	var bodyReader io.Reader
	if r.body != nil {
		encoded, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("rocket: encode %s body: %w", r.endpoint, err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, r.method, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("rocket: build %s request: %w", r.endpoint, err)
	}
	req.Header.Set("Accept", "application/json")
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !r.noAuth {
		creds := c.Credentials()
		if !creds.Valid() {
			return fmt.Errorf("rocket: %s requires authentication", r.endpoint)
		}
		req.Header.Set("X-Auth-Token", creds.AuthToken)
		req.Header.Set("X-User-Id", creds.UserID)
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("rocket: %s: %w", r.endpoint, err)
	}
	defer resp.Body.Close()

	// Cap the read so a misconfigured host cannot exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("rocket: %s: read body: %w", r.endpoint, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(r.endpoint, resp.StatusCode, raw)
	}

	// A 200 can still carry {"success": false}.
	var envelope struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
		Message string `json:"message"`
		Type    string `json:"errorType"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if envelope.Success != nil && !*envelope.Success {
			return &APIError{
				StatusCode: resp.StatusCode,
				ErrorType:  envelope.Type,
				Message:    firstNonEmpty(envelope.Error, envelope.Message, "request failed"),
				Endpoint:   r.endpoint,
			}
		}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("rocket: %s: decode response: %w", r.endpoint, err)
	}
	return nil
}

func parseAPIError(endpoint string, status int, raw []byte) error {
	apiErr := &APIError{StatusCode: status, Endpoint: endpoint}
	var payload struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
		Type    string `json:"errorType"`
		Details struct {
			Method string `json:"method"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		apiErr.ErrorType = payload.Type
		switch e := payload.Error.(type) {
		case string:
			apiErr.Message = e
		case map[string]any:
			// Meteor-style: {"error": {"error": "totp-required", "reason": "..."}}
			if reason, ok := e["reason"].(string); ok {
				apiErr.Message = reason
			}
			if kind, ok := e["error"].(string); ok && apiErr.ErrorType == "" {
				apiErr.ErrorType = kind
			}
		}
		if apiErr.Message == "" {
			apiErr.Message = payload.Message
		}
	}
	if apiErr.Message == "" {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		apiErr.Message = snippet
	}
	return apiErr
}

// isLoopbackHost reports whether a scheme-less address points at this machine.
func isLoopbackHost(address string) bool {
	host := address
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
