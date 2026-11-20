// Package apierr defines typed API errors that map to HTTP status codes.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, format string, a ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
}

func Validation(format string, a ...any) *Error {
	return New(http.StatusBadRequest, "ValidationError", format, a...)
}
func NotFound(format string, a ...any) *Error {
	return New(http.StatusNotFound, "NotFound", format, a...)
}
func Conflict(format string, a ...any) *Error {
	return New(http.StatusConflict, "Conflict", format, a...)
}
func Dependency(format string, a ...any) *Error {
	return New(http.StatusConflict, "DependencyViolation", format, a...)
}
func InvalidState(format string, a ...any) *Error {
	return New(http.StatusConflict, "InvalidStateTransition", format, a...)
}
func Denied(format string, a ...any) *Error {
	return New(http.StatusForbidden, "AccessDenied", format, a...)
}
func Unauthenticated(format string, a ...any) *Error {
	return New(http.StatusUnauthorized, "Unauthenticated", format, a...)
}
func Quota(format string, a ...any) *Error {
	return New(http.StatusTooManyRequests, "QuotaExceeded", format, a...)
}
func NotImplemented(format string, a ...any) *Error {
	return New(http.StatusNotImplemented, "NotImplemented", format, a...)
}

// As extracts an *Error; unknown errors become InternalError (message not leaked).
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Status: http.StatusInternalServerError, Code: "InternalError", Message: "internal error"}
}
