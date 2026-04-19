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
	"context"
	"errors"
	"time"
)

// VersionChangeEvent carries all data a sender needs to compose and deliver a notification
// It is assembled by Dispatcher.resolveSender() from PendingFailedNotification (db row) during delivery process
type VersionChangeEvent struct {
	PackageName       string
	PackageBranch     string
	OldVersion        string
	NewVersion        string
	DetectedAt        time.Time
	RecipientAddress  string // email address or webhook URL
	WebhookType       string // "generic" or "mattermost" - empty for emails
	WebhookUsername   string // mattermost only
	WebhookChannel    string // mattermost only
	WebhookPriority   string // mattermost only
	WebhookRequestAck bool   // mattermost only
}

// Sender is interface that Dispatcher uses for delivery mechanism
// Implemented by notification channel types (Resend/SMTP/Webhook)
// Implementing a new channel type requires satisfying this interface
type Sender interface {
	// Real delivery (called by dispatch loop for every notification that needs to be sent)
	Send(ctx context.Context, event VersionChangeEvent) error
	// Test "ping" (triggered by user through "Test" button in UI, sends dummy message)
	SendTest(ctx context.Context, event VersionChangeEvent) error
}

// Wraps a delivery failure with two separate messages:
// - PublicMsg: public message, stored in the DB and shown in the UI delivery log
// - Err: internal message, logged by the dispatcher (contains everything, function path, status codes, ...)
type SenderError struct {
	PublicMsg string
	Err       error
}

// Error returns the internal error message.
func (e *SenderError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error to support errors.Is and errors.As unwrapping.
func (e *SenderError) Unwrap() error {
	return e.Err
}

// PublicMessage extracts safe message for user from SenderError.
// Returns "internal server error" if error is not SenderError.
func PublicMessage(err error) string {
	var se *SenderError
	if errors.As(err, &se) {
		return se.PublicMsg
	}
	return "internal server error"
}
