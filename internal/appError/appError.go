package appError

import (
	"errors"
	"fmt"
)

// https://www.datadoghq.com/blog/go-error-handling/
// https://go.dev/blog/error-handling-and-go

type Cause uint8

const (
	Internal Cause = iota
	Invalid
	Unauthenticated
	Forbidden
	NotFound
	Conflict
	Upstream
)

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

type appError struct {
	Err        error  // wrapped cause (actual error)
	Message    string // safe message (for users)
	ErrorCause Cause  // kind of error (for branching and HTTP codes)
	Op         string // operation (where in the code error happened)
}

func NewAppError(op string, cause Cause, msg string, err error) error {
	return &appError{Err: err, Message: msg, ErrorCause: cause, Op: op}
}

func (e *appError) Error() string {
	return fmt.Sprintf("%s (%v): %v", e.Op, e.ErrorCause, e.Err)
}

func PublicMessage(err error) string {
	var e *appError
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return "unexpected error"
}

func (e *appError) Unwrap() error {
	return e.Err
}

func ErrorCause(err error) Cause {
	var e *appError
	if errors.As(err, &e) {
		return e.ErrorCause
	}
	return Internal
}
