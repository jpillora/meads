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

// webhookClient returns an http.Client configured for the given URL.
// For unix:// URLs, it dials the Unix socket.
func webhookClient(rawURL string) *http.Client {
	if strings.HasPrefix(rawURL, "unix://") {
		socketPath := strings.TrimPrefix(rawURL, "unix://")
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
// For unix:// URLs, returns http://localhost/ since the transport handles routing.
func webhookHTTPURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "unix://") {
		return "http://localhost/"
	}
	return rawURL
}
