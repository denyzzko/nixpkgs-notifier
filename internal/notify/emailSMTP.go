package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"time"
)

// Delivers email by direct SMTP server use (uses Go stdlib)
// It is used when EMAIL_PROVIDER env var is set to "smtp"
// Single instance is created on startup in dispatcher.New() and it is reused for all email deliveries
// Destination email address is stored in event.RecipientAddress
type SMTPSender struct {
	host     string
	port     string
	username string
	password string
	from     string
}

// Constructs an SMTPSender from environment variables passed from dispatcher.New()
func NewSMTPSender(host, port, username, password, from string) *SMTPSender {
	return &SMTPSender{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
	}
}

// Sends notification email via SMTP
// Builds RFC 2822 plain-text message
// smtp.SendMail handles STARTTLS negotiation and PLAIN AUTH
func (s *SMTPSender) Send(_ context.Context, event VersionChangeEvent) error {
	subject := fmt.Sprintf("[nixpkgs-notifier] %s updated: %s → %s",
		event.PackageName, event.OldVersion, event.NewVersion)

	body := fmt.Sprintf(
		"Package update detected\r\n\r\n"+
			"Package:     %s\r\n"+
			"Branch:      %s\r\n"+
			"Old version: %s\r\n"+
			"New version: %s\r\n"+
			"Detected at: %s\r\n",
		event.PackageName,
		event.PackageBranch,
		event.OldVersion,
		event.NewVersion,
		event.DetectedAt.UTC().Format(time.RFC3339),
	)

	msg := s.buildMessage(event.RecipientAddress, subject, body)
	return s.sendMail(event.RecipientAddress, msg)
}

// Sends testing email via SMTP to test configured channel
// Called by Dispatcher.Test when the user clicks "Test" in UI
func (s *SMTPSender) SendTest(_ context.Context, event VersionChangeEvent) error {
	msg := s.buildMessage(
		event.RecipientAddress,
		"Email channel test SUCCESSFUL!",
		"Congratulations !!!\r\n\r\nThe email channel you have configured is working :)\r\n",
	)
	return s.sendMail(event.RecipientAddress, msg)
}

// Helper to build RFC 2822 email as a raw byte string
func (s *SMTPSender) buildMessage(to, subject, body string) string {
	return "From: " + s.from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		body
}

// sendMail delivers an email via smtp.SendMail.
// Opens TCP connection to SMTP server and negotiates STARTTLS if the server supports it.
// Authenticates with PLAIN auth (if credentials are configured), delivers message and closes connection.
func (s *SMTPSender) sendMail(to, msg string) error {
	var auth smtp.Auth
	if s.username != "" || s.password != "" {
		auth = smtp.PlainAuth("", s.username, s.password, s.host)
	}
	addr := s.host + ":" + s.port

	err := smtp.SendMail(addr, auth, s.from, []string{to}, []byte(msg))
	if err != nil {
		return &SenderError{
			PublicMsg: fmt.Sprintf("failed to send email via SMTP: %v", err),
			Err:       fmt.Errorf("notify.SMTPSender: send failed: %w", err),
		}
	}
	return nil
}
