// Package notify provides sender implementations used by the dispatcher to deliver notifications.
//
// Sender interface abstracts the delivery transport so the dispatcher does not need
// to know, which channel type it is sending to.
//
// Three implementations are provided:
//   - ResendSender:  delivers email via the Resend HTTP API
//   - SMTPSender:    delivers email via raw SMTP
//   - WebhookSender: delivers notifications via HTTP POST to a webhook URL
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const resendAPIURL = "https://api.resend.com/emails"

// Delivers email through Resend email service (https://resend.com)
// It is used when EMAIL_PROVIDER env var is set to "resend" (resp. anything other then "smtp")
// Single instance is created on startup in dispatcher.New() and it is reused for all email deliveries
// Destination email address is stored in event.RecipientAddress
// Uses rate limiter to satisfy Resend's rate limiting for free accounts
type ResendSender struct {
	apiKey     string // Resend secret API key (RESEND_API_KEY env var)
	fromAddr   string
	httpClient *http.Client
	limiter    *rate.Limiter // rate limit (1 req/s)
}

// Constructs a ResendSender from environment variables passed from dispatcher.New()
// Rate limiter is set to 1 request/second with a burst of 1 to stay within free-tier limits
// you can increase these values if on a paid plan
func NewResendSender(apiKey, fromAddr string) *ResendSender {
	return &ResendSender{
		apiKey:     apiKey,
		fromAddr:   fromAddr,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(1), 1), // 1 requests/sec, burst of 1 (because of Resend rate limits)
	}
}

// JSON body expected by Resend API (POST https://api.resend.com/emails)
type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"` // plain-text (Resend supports also "html")
}

// JSON body returned by Resend API
// On success ID contains message ID, on failure Error contains message
type resendResponse struct {
	ID    string `json:"id"`
	Error string `json:"message,omitempty"`
}

// Sends notification email via Resend API service
// Waits on rate limiter before sending if recent calls already consumed budget
func (s *ResendSender) Send(ctx context.Context, event VersionChangeEvent) error {
	// Wait for a rate-limit token (respects ctx cancellation)
	err := s.limiter.Wait(ctx)
	if err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("email delivery was cancelled: %v", err),
			Err:       fmt.Errorf("notify.ResendSender: rate limiter: %w", err),
		}
	}

	subject := fmt.Sprintf("[nixpkgs-notifier] %s updated: %s → %s",
		event.PackageName, event.OldVersion, event.NewVersion)

	body := fmt.Sprintf(
		"Package update detected\n\n"+
			"Package:     %s\n"+
			"Branch:      %s\n"+
			"Old version: %s\n"+
			"New version: %s\n"+
			"Detected at: %s\n",
		event.PackageName,
		event.PackageBranch,
		event.OldVersion,
		event.NewVersion,
		event.DetectedAt.UTC().Format(time.RFC3339),
	)

	// produce JSON payload
	data, err := json.Marshal(resendRequest{
		From:    s.fromAddr,
		To:      []string{event.RecipientAddress},
		Subject: subject,
		Text:    body,
	})
	if err != nil {
		return fmt.Errorf("notify.ResendSender: marshal request: %w", err)
	}

	// POST the payload to the Resend API
	return s.post(ctx, data)
}

// Sends testing email via Resend API service
// Waits on rate limiter before sending if recent calls already consumed budget
// Called by Dispatcher.Test when user clicks "Test" in UI
func (s *ResendSender) SendTest(ctx context.Context, event VersionChangeEvent) error {
	err := s.limiter.Wait(ctx)
	if err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("email delivery was cancelled: %v", err),
			Err:       fmt.Errorf("notify.ResendSender: rate limiter: %w", err),
		}
	}

	// produce JSON payload
	data, err := json.Marshal(resendRequest{
		From:    s.fromAddr,
		To:      []string{event.RecipientAddress},
		Subject: "Email channel test SUCCESSFUL!",
		Text:    "Congratulations !!!\n\nThe email channel you have configured is working :)\n",
	})
	if err != nil {
		return fmt.Errorf("notify.ResendSender: marshal request: %w", err)
	}

	// POST the payload to the Resend API
	return s.post(ctx, data)
}

// Helper used by Send and SendTest
// Makes POST request, decodes response, and returns API errors if any
func (s *ResendSender) post(ctx context.Context, data []byte) error {
	// build HTTP POST request and set headers
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("notify.ResendSender: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	// execute request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("failed to reach email delivery service: %v", err),
			Err:       fmt.Errorf("notify.ResendSender: HTTP request failed: %w", err),
		}
	}
	defer resp.Body.Close()

	// decode Resend API response
	var result resendResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("notify.ResendSender: decode response: %w", err)
	}

	// Resend returns 2xx on success, anything else means the API rejected the request
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &SenderError{
			PublicMsg: fmt.Sprintf("email delivery service rejected the request: %s", result.Error),
			Err:       fmt.Errorf("notify.ResendSender: Resend API error (status=%d): %s", resp.StatusCode, result.Error),
		}
	}

	return nil
}
