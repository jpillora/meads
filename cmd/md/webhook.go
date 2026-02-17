package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type webhookPayload struct {
	Meads  bool   `json:"meads"`
	Action string `json:"action"`
	Data   any    `json:"data"`
}

// postWebhook sends a webhook notification if WebhookURL is configured.
// Errors are logged to stderr but do not fail the calling command.
func postWebhook(g *globals, action string, data any) {
	if g == nil || g.WebhookURL == "" {
		return
	}
	payload := webhookPayload{
		Meads:  true,
		Action: action,
		Data:   data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook: marshal error: %v\n", err)
		return
	}
	client := webhookClient(g.WebhookURL)
	url := webhookHTTPURL(g.WebhookURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook: request error: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook: post error: %v\n", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "webhook: server returned %d\n", resp.StatusCode)
	}
}

// parseUnixURL splits a unix:// URL into socket path and HTTP path.
// Format: unix:///path/to/socket:/http/path
// The :/http/path suffix is optional and defaults to /.
func parseUnixURL(rawURL string) (socketPath, httpPath string) {
	rest := strings.TrimPrefix(rawURL, "unix://")
	if i := strings.LastIndex(rest, ":"); i > 0 {
		socketPath = rest[:i]
		httpPath = rest[i+1:]
	} else {
		socketPath = rest
		httpPath = "/"
	}
	if httpPath == "" {
		httpPath = "/"
	}
	return
}

// webhookClient returns an http.Client configured for the given URL.
// For unix:// URLs, it dials the Unix socket.
func webhookClient(rawURL string) *http.Client {
	if strings.HasPrefix(rawURL, "unix://") {
		socketPath, _ := parseUnixURL(rawURL)
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		}
	}
	return http.DefaultClient
}

// webhookHTTPURL converts the webhook URL to a standard HTTP URL.
// For unix:// URLs, extracts the HTTP path from the colon-separated suffix.
func webhookHTTPURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "unix://") {
		_, httpPath := parseUnixURL(rawURL)
		return "http://localhost" + httpPath
	}
	return rawURL
}
