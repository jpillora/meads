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

// postWebhook sends a webhook notification if WebhookURI is configured.
// Errors are logged to stderr but do not fail the calling command.
func postWebhook(g *globals, action string, data any) {
	if g == nil || g.WebhookURI == "" {
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
	client := webhookClient(g.WebhookURI)
	url := webhookHTTPURL(g.WebhookURI)
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

// parseUnixURI splits a unix:// URI into socket path and HTTP path.
// Format: unix://[/path/to/socket]/http/path
// Bracketed socket disambiguates paths containing colons or other characters.
// Without brackets, the whole authority+path is treated as the socket and
// the HTTP path defaults to /.
func parseUnixURI(rawURI string) (socketPath, httpPath string) {
	rest := strings.TrimPrefix(rawURI, "unix://")
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end > 0 {
			socketPath = rest[1:end]
			httpPath = rest[end+1:]
			if httpPath == "" {
				httpPath = "/"
			}
			return
		}
	}
	socketPath = rest
	httpPath = "/"
	return
}

// webhookClient returns an http.Client configured for the given URI.
// For unix:// URIs, it dials the Unix socket.
func webhookClient(rawURI string) *http.Client {
	if strings.HasPrefix(rawURI, "unix://") {
		socketPath, _ := parseUnixURI(rawURI)
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

// webhookHTTPURL converts the webhook URI to a standard HTTP URL.
// For unix:// URIs, extracts the HTTP path from the colon-separated suffix.
func webhookHTTPURL(rawURI string) string {
	if strings.HasPrefix(rawURI, "unix://") {
		_, httpPath := parseUnixURI(rawURI)
		return "http://localhost" + httpPath
	}
	return rawURI
}
