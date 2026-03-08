package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Delivers notifications by POST-ing JSON payload to a webhook URL
// Single instance is created on startup in dispatcher.New() and it is reused for all webhook deliveries
// Destination URL is stored in event.RecipientAddress
type WebhookSender struct {
	httpClient *http.Client
}

// Constructs a WebhookSender
func NewWebhookSender() *WebhookSender {
	return &WebhookSender{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// webhookPayload is the standardised JSON body sent to webhook endpoints
// Receiving services should rely on this structure
type webhookPayload struct {
	Package    string `json:"package"`
	Branch     string `json:"branch"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	DetectedAt string `json:"detected_at"` // RFC3339 UTC timestamp
}

// JSON body sent during test ping
// Receiving service should rely on this structure for test messages
type testWebhookPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Send POST event as JSON to webhook URL
func (s *WebhookSender) Send(ctx context.Context, event VersionChangeEvent) error {
	// produce JSON payload
	data, err := json.Marshal(webhookPayload{
		Package:    event.PackageName,
		Branch:     event.PackageBranch,
		OldVersion: event.OldVersion,
		NewVersion: event.NewVersion,
		DetectedAt: event.DetectedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("notify.WebhookSender: marshal payload: %w", err)
	}

	// POST to webhook URL
	return s.post(ctx, event.RecipientAddress, data)
}

// Send POST event as minimal JSON to webhook URL for testing channel
// Called by Dispatcher.Test when the user clicks "Test" in UI
func (s *WebhookSender) SendTest(ctx context.Context, event VersionChangeEvent) error {
	// produce JSON payload
	data, err := json.Marshal(testWebhookPayload{
		Type:    "test",
		Message: "Congratulations! The webhook channel you have configured is working :)",
	})
	if err != nil {
		return fmt.Errorf("notify.WebhookSender: marshal payload: %w", err)
	}

	// POST to webhook URL
	return s.post(ctx, event.RecipientAddress, data)
}

// Helper for Send and SendTest
// Makes POST to the given URL and checks for a 2xx response
func (s *WebhookSender) post(ctx context.Context, url string, data []byte) error {
	// build HTTP POST request and set header
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("notify.WebhookSender: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// execute request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("failed to reach your webhook URL: %v", err),
			Err:       fmt.Errorf("notify.WebhookSender: HTTP request failed: %w", err),
		}
	}
	defer resp.Body.Close()

	// Any non 2xx status is treated as a delivery failure
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &SenderError{
			PublicMsg: fmt.Sprintf("webhook returned status %d", resp.StatusCode),
			Err:       fmt.Errorf("notify.WebhookSender: non-2xx status: %d", resp.StatusCode),
		}
	}
	return nil
}
