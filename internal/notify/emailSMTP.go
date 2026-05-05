package notify

import (
	"context"
	"fmt"
	"strconv"

	gomail "github.com/wneessen/go-mail"
)

// Delivers email by direct SMTP server use (uses Go stdlib)
// It is used when EMAIL_PROVIDER env var is set to "smtp"
// Single instance is created on startup in dispatcher.New() and it is reused for all email deliveries
// Destination email address is stored in event.RecipientAddress
type SMTPSender struct {
	host         string
	port         string
	username     string
	password     string
	from         string
	heloHostname string // defaults to host if empty
}

// Constructs an SMTPSender from environment variables passed from dispatcher.New()
func NewSMTPSender(host, port, username, password, from, heloHostname string) *SMTPSender {
	if heloHostname == "" {
		heloHostname = host
	}

	return &SMTPSender{
		host:         host,
		port:         port,
		username:     username,
		password:     password,
		from:         from,
		heloHostname: heloHostname,
	}
}

// Sends notification email via SMTP
func (s *SMTPSender) Send(ctx context.Context, event VersionChangeEvent) error {
	var subject, body string

	if event.IsFirstAppearance {
		subject = fmt.Sprintf("%s appeared in nixpkgs: %s",
			event.PackageName, event.NewVersion)

		body = fmt.Sprintf(
			"Package appeared in nixpkgs for the first time!\r\n\r\n"+
				"Package:     %s\r\n"+
				"Branch:      %s\r\n"+
				"Version:     %s\r\n"+
				"Detected at: %s\r\n",
			event.PackageName,
			event.PackageBranch,
			event.NewVersion,
			event.DetectedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		)
	} else {
		subject = fmt.Sprintf("%s updated: %s → %s",
			event.PackageName, event.OldVersion, event.NewVersion)

		body = fmt.Sprintf(
			"Package version change detected!\r\n\r\n"+
				"Package:     %s\r\n"+
				"Branch:      %s\r\n"+
				"Old version: %s\r\n"+
				"New version: %s\r\n"+
				"Detected at: %s\r\n",
			event.PackageName,
			event.PackageBranch,
			event.OldVersion,
			event.NewVersion,
			event.DetectedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		)
	}

	return s.sendMail(ctx, event.RecipientAddress, subject, body)
}

// Sends testing email via SMTP to test configured channel
// Called by Dispatcher.Test when the user clicks "Test" in UI
func (s *SMTPSender) SendTest(ctx context.Context, event VersionChangeEvent) error {
	return s.sendMail(ctx,
		event.RecipientAddress,
		"Email channel test SUCCESSFUL!",
		"Congratulations !!!\r\n\r\nThe email channel you have configured is working :)\r\n",
	)
}

// sendMail builds and delivers email via go-mail package.
// go-mail handles EHLO hostname, Message-Id generation, STARTTLS and AUTH automatically.
func (s *SMTPSender) sendMail(ctx context.Context, to string, subject string, body string) error {
	// build message
	m := gomail.NewMsg()
	err := m.From(s.from)
	if err != nil {
		return s.wrapErr("invalid from address", err)
	}
	err = m.To(to)
	if err != nil {
		return s.wrapErr("invalid to address", err)
	}
	m.Subject(subject)
	m.SetBodyString(gomail.TypeTextPlain, body)

	// build client options
	port, err := strconv.Atoi(s.port)
	if err != nil {
		return s.wrapErr("invalid port", err)
	}

	opts := []gomail.Option{
		gomail.WithPort(port),
		gomail.WithHELO(s.heloHostname),
		gomail.WithTLSPolicy(gomail.TLSOpportunistic), // use STARTTLS if available
	}
	if s.username != "" && s.password != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(s.username),
			gomail.WithPassword(s.password),
		)
	}

	// create client with configured options
	c, err := gomail.NewClient(s.host, opts...)
	if err != nil {
		return s.wrapErr("failed to create SMTP client", err)
	}

	// open connection, deliver message and close connection
	err = c.DialAndSendWithContext(ctx, m)
	if err != nil {
		return s.wrapErr("failed to send email", err)
	}
	return nil
}

func (s *SMTPSender) wrapErr(context string, err error) *SenderError {
	return &SenderError{
		PublicMsg: fmt.Sprintf("failed to send email via SMTP: %s: %v", context, err),
		Err:       fmt.Errorf("notify.SMTPSender: %s: %w", context, err),
	}
}
