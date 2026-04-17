package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// privateIPRanges holds all IP ranges that should never be target of a webhook request (SSRF protection).
// It is populated by the init() function before main() is called.
var privateIPRanges []*net.IPNet

func init() {
	cidrs := []string{
		// IPv4
		"0.0.0.0/8",          // host
		"10.0.0.0/8",         // private
		"100.64.0.0/10",      // shared address space
		"127.0.0.0/8",        // loopback
		"169.254.0.0/16",     // link-local
		"172.16.0.0/12",      // private
		"192.168.0.0/16",     // private
		"198.18.0.0/15",      // benchmarking
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // broadcast
		// IPv6
		"::1/128",   // loopback
		"::/128",    // unspecified
		"fc00::/7",  // unique local
		"fe80::/10", // link-local
	}
	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		privateIPRanges = append(privateIPRanges, network)
	}
}

// isPrivateIP returns whether given IP falls within any IP range from privateIPRanges.
func isPrivateIP(ip net.IP) bool {
	for _, network := range privateIPRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateWebhookURL checks if rawURL is safe to send requests to.
// It rejects non-http(s) schemes and URLs that resolve to private or reserved IP addresses (privateIPRanges).
// Also does DNS resolution so domains pointing to internal IPs are caught.
func ValidateWebhookURL(rawURL string) error {
	// parse and validate URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https")
	}

	// strip port if present
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL has no host")
	}

	// resolve hostname to IP addresses (catches domains that point to private IPs)
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("could not resolve webhook hostname %q", host)
	}

	// reject if any resolved IP falls within a private or reserved range
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("webhook URL resolves to a private or reserved IP address")
		}
	}
	return nil
}

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
	// guard against SSRF - validate URL before making request
	if err := ValidateWebhookURL(url); err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("webhook URL rejected: %v", err),
			Err:       fmt.Errorf("notify.WebhookSender: SSRF check failed: %w", err),
		}
	}

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
