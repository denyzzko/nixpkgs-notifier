// Package notify provides delivery abstraction and specific sender
// implementations that are used by the dispatcher to send notifications
//
// There are three types that implement Sender interface,
// so dispatcher does not need to know what transport tech is used:
//   - ResendSender  (Resend service HTTP API)
//   - SMTPSender    (raw SMTP)
//   - WebhookSender (HTTP POST to webhook URL)
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
// - Err: internal message, logged by the dispatcher (contains everything, function path, status codes, ...)
// - PublicMsg: public message, stored in the DB and shown in the UI delivery log
type SenderError struct {
	PublicMsg string
	Err       error
}

func (e *SenderError) Error() string { return e.Err.Error() }
func (e *SenderError) Unwrap() error { return e.Err }
func PublicMessage(err error) string {
	var se *SenderError
	if errors.As(err, &se) {
		return se.PublicMsg
	}
	return "internal server error"
}
