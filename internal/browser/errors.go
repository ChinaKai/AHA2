package browser

import (
	"fmt"
	"net/http"
)

// Error is a typed, user-facing browser failure. Code is a stable machine
// token the Web UI and tests can assert; Message must never carry tokens or
// page contents.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func newError(status int, code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Status: status}
}

func badRequest(code, format string, args ...any) *Error {
	return newError(http.StatusBadRequest, code, format, args...)
}

func conflict(code, format string, args ...any) *Error {
	return newError(http.StatusConflict, code, format, args...)
}

func forbidden(code, format string, args ...any) *Error {
	return newError(http.StatusForbidden, code, format, args...)
}

func notFound(code, format string, args ...any) *Error {
	return newError(http.StatusNotFound, code, format, args...)
}

// Code reports the typed error code, or an empty string for other errors.
func Code(err error) string {
	if typed, ok := err.(*Error); ok {
		return typed.Code
	}
	return ""
}

// Status reports the HTTP status a handler should use for err.
func Status(err error) int {
	if typed, ok := err.(*Error); ok {
		return typed.Status
	}
	return http.StatusInternalServerError
}
