package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Request represents the payload accepted by the Leo Lambda.
// Provide either Args or Cmd, but not both.
type Request struct {
	Args []string `json:"args,omitempty"`
	Cmd  string   `json:"cmd,omitempty"`
}

// Response mirrors the Lambda response payload.
type Response struct {
	ExitCode  int               `json:"exitCode"`
	Duration  float64           `json:"duration"`
	Stdout    string            `json:"stdout"`
	Stderr    string            `json:"stderr"`
	Truncated bool              `json:"truncated"`
	Meta      map[string]string `json:"meta"`
}

// Client wraps HTTP interactions with the Lambda endpoint.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option customises a new Client.
type Option func(*Client)

// WithHTTPClient sets a custom http.Client on the Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// New constructs a Client pointed at the given Lambda URL.
func New(baseURL string, opts ...Option) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	cli := &Client{baseURL: baseURL, httpClient: http.DefaultClient}
	for _, opt := range opts {
		opt(cli)
	}
	if cli.httpClient == nil {
		cli.httpClient = http.DefaultClient
	}
	return cli, nil
}

// Invoke executes the supplied request against the Lambda endpoint.
func (c *Client) Invoke(ctx context.Context, req Request) (*Response, error) {
	if c == nil {
		return nil, fmt.Errorf("sdk Client is nil")
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= 300 {
		return nil, parseError(resp.StatusCode, body)
	}

	var out Response
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (r Request) validate() error {
	if len(r.Args) == 0 {
		if strings.TrimSpace(r.Cmd) == "" {
			return fmt.Errorf("either args or cmd must be provided")
		}
		return nil
	}
	if strings.TrimSpace(r.Cmd) != "" {
		return fmt.Errorf("provide either args or cmd, not both")
	}
	return nil
}

// InvokeError captures a non-successful Lambda response.
type InvokeError struct {
	StatusCode int
	Message    string
	Body       []byte
}

// Error implements the error interface.
func (e *InvokeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("lambda responded with status %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("lambda responded with status %d", e.StatusCode)
}

func parseError(status int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return &InvokeError{StatusCode: status, Message: payload.Error, Body: body}
	}
	trimmed := strings.TrimSpace(string(body))
	return &InvokeError{StatusCode: status, Message: trimmed, Body: body}
}
