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

// mattermostPayload is the standardised JSON body for Mattermost webhook endpoints
type mattermostPayload struct {
	Text     string              `json:"text"`
	Username string              `json:"username,omitempty"`
	Channel  string              `json:"channel,omitempty"`
	Priority *mattermostPriority `json:"priority,omitempty"`
}
type mattermostPriority struct {
	Priority     string `json:"priority,omitempty"`
	RequestedAck bool   `json:"requested_ack,omitempty"`
}

// JSON body sent during test ping
// Receiving service should rely on this structure for test messages
type testWebhookPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Send POST event as JSON to webhook URL
func (s *WebhookSender) Send(ctx context.Context, event VersionChangeEvent) error {
	var data []byte
	var err error

	// produce JSON payload based on webhook type
	switch event.WebhookType {
	case "mattermost":
		text := fmt.Sprintf(
			"**Nixpkgs update:** `%s` on branch `%s` - `%s` -> `%s` (detected %s)",
			event.PackageName,
			event.PackageBranch,
			event.OldVersion,
			event.NewVersion,
			event.DetectedAt.UTC().Format(time.RFC3339),
		)

		payload := mattermostPayload{
			Text: text,
		}

		if event.WebhookUsername != "" {
			payload.Username = event.WebhookUsername
		}
		if event.WebhookChannel != "" {
			payload.Channel = event.WebhookChannel
		}
		if event.WebhookPriority != "" {
			payload.Priority = &mattermostPriority{
				Priority: event.WebhookPriority,
			}
			if event.WebhookRequestAck {
				payload.Priority.RequestedAck = true
			}
		}

		data, err = json.Marshal(payload)
	default: // "generic"
		data, err = json.Marshal(webhookPayload{
			Package:    event.PackageName,
			Branch:     event.PackageBranch,
			OldVersion: event.OldVersion,
			NewVersion: event.NewVersion,
			DetectedAt: event.DetectedAt.UTC().Format(time.RFC3339),
		})
	}
	if err != nil {
		return fmt.Errorf("notify.WebhookSender: marshal payload: %w", err)
	}

	// POST to webhook URL
	return s.post(ctx, event.RecipientAddress, data)
}

// Send POST event as minimal JSON to webhook URL for testing channel
// Called by Dispatcher.Test when the user clicks "Test" in UI
func (s *WebhookSender) SendTest(ctx context.Context, event VersionChangeEvent) error {
	var data []byte
	var err error

	// produce JSON payload based on webhook type
	switch event.WebhookType {
	case "mattermost":
		payload := mattermostPayload{
			Text: "**Nixpkgs Notifier test:** The Mattermost webhook channel you have configured is working :white_check_mark:",
		}

		if event.WebhookUsername != "" {
			payload.Username = event.WebhookUsername
		}
		if event.WebhookChannel != "" {
			payload.Channel = event.WebhookChannel
		}
		if event.WebhookPriority != "" {
			payload.Priority = &mattermostPriority{
				Priority: event.WebhookPriority,
			}
			if event.WebhookRequestAck {
				payload.Priority.RequestedAck = true
			}
		}

		data, err = json.Marshal(payload)
	default: // "generic"
		data, err = json.Marshal(testWebhookPayload{
			Type:    "test",
			Message: "Congratulations! The webhook channel you have configured is working :)",
		})
	}
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
