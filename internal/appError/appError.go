// Package appError defines error type used throughout the application.
//
// Each appError carries four fields:
//   - operation where the error occurred (Op)
//   - error kind (ErrorCause)
//   - safe user-facing message (Message)
//   - underlying error (Err)
//
// Handlers use ErrorCause to map errors to HTTP status codes,
// and PublicMessage to show safe message to the user.
package appError

import (
	"errors"
	"fmt"
)

// https://www.datadoghq.com/blog/go-error-handling/
// https://go.dev/blog/error-handling-and-go

// Cause is enum that classifies kind of error for HTTP mapping and control flow branching.
type Cause uint8

const (
	Internal        Cause = iota // unexpected server-side failure
	Invalid                      // bad input from client
	Unauthenticated              // no valid session
	Forbidden                    // authenticated but not allowed
	NotFound                     // requested resource does not exist
	Conflict                     // request conflicts with current state
	Upstream                     // failure in external dependency
)

// String returns the name of the cause as a string.
func (c Cause) String() string {
	switch c {
	case Internal:
		return "Internal"
	case Invalid:
		return "Invalid"
	case Unauthenticated:
		return "Unauthenticated"
	case Forbidden:
		return "Forbidden"
	case NotFound:
		return "NotFound"
	case Conflict:
		return "Conflict"
	case Upstream:
		return "Upstream"
	default:
		return fmt.Sprintf("Cause(%d)", uint8(c))
	}
}

// appError is the internal structured error type.
type appError struct {
	Err        error  // wrapped cause (actual error)
	Message    string // safe message (for users)
	ErrorCause Cause  // kind of error (for branching and HTTP codes)
	Op         string // operation (where in the code error happened)
}

// NewAppError creates a new appError with the given operation, cause, message for user and underlying error.
func NewAppError(op string, cause Cause, msg string, err error) error {
	return &appError{Err: err, Message: msg, ErrorCause: cause, Op: op}
}

// Error returns string containing the operation, cause, and underlying error (for logs).
func (e *appError) Error() string {
	return fmt.Sprintf("%s (%v): %v", e.Op, e.ErrorCause, e.Err)
}

// PublicMessage extracts safe message for user from an appError.
// Returns "unexpected error" if the error is not appError or has no message set.
func PublicMessage(err error) string {
	var e *appError
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return "unexpected error"
}

// Unwrap returns underlying error to support errors.Is and errors.As unwrapping.
func (e *appError) Unwrap() error {
	return e.Err
}

// ErrorCause extracts Cause from appError.
// Returns Internal if error is not appError.
func ErrorCause(err error) Cause {
	var e *appError
	if errors.As(err, &e) {
		return e.ErrorCause
	}
	return Internal
}
