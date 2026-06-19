// Package rie is a tiny client for the AWS Lambda Runtime Interface Emulator
// invoke endpoint (POST /2015-03-31/functions/{name}/invocations).
package rie

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client invokes a single Lambda function through the RIE.
type Client struct {
	url    string
	client *http.Client
}

// New builds a Client targeting endpoint (e.g. "http://127.0.0.1:8080") and
// the given function name. timeout bounds each invocation.
func New(endpoint, function string, timeout time.Duration) *Client {
	return &Client{
		url:    fmt.Sprintf("%s/2015-03-31/functions/%s/invocations", endpoint, function),
		client: &http.Client{Timeout: timeout},
	}
}

// Invoke POSTs the payload to the RIE and returns the raw response body. A
// non-200 from the RIE is returned as an error with a (truncated) body.
func (c *Client) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating lambda request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req) //nolint:gosec // endpoint is a trusted local dev config value
	if err != nil {
		return nil, fmt.Errorf("invoking lambda: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading lambda response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		const maxErrBodyLen = 1024
		s := string(body)
		if len(s) > maxErrBodyLen {
			s = s[:maxErrBodyLen] + "...(truncated)"
		}
		return nil, fmt.Errorf("lambda returned status %d: %s", resp.StatusCode, s)
	}
	return body, nil
}
