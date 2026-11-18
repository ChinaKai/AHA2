package app

import (
	"errors"
	"fmt"
	"net/http"
)

// CodedError is a failure with a stable machine-readable code and a readable
// message. The HTTP handler and the pet MCP endpoint both surface the code, so
// a rejected configuration change can never look like a successful no-op.
type CodedError struct {
	Code    string
	Message string
	Status  int
}

func (e *CodedError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// newCodedError builds a CodedError. Status defaults to 400 because most of
// these failures are invalid request combinations; callers raise it for
// permission, not-found and conflict cases.
func newCodedError(code, message string, status ...int) *CodedError {
	value := http.StatusBadRequest
	if len(status) > 0 && status[0] > 0 {
		value = status[0]
	}
	return &CodedError{Code: code, Message: message, Status: value}
}

func codedErrorf(code string, status int, format string, args ...any) *CodedError {
	return newCodedError(code, fmt.Sprintf(format, args...), status)
}

// AsCodedError reports whether err (or anything it wraps) carries a stable
// code. HTTP and MCP surfaces use it to preserve the original classification.
func AsCodedError(err error) (*CodedError, bool) {
	var target *CodedError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// Stable error codes for Task Agent configuration. Keeping them here avoids
// spelling drift between the HTTP API, pet MCP and tests.
const (
	ErrCodeModelSourceInvalid      = "model_source_invalid"
	ErrCodeModelSourceMismatch     = "model_source_mismatch"
	ErrCodeModelNotFound           = "model_not_found"
	ErrCodeModelRequired           = "model_required"
	ErrCodeWireModelRequired       = "wire_model_required"
	ErrCodeCodexAccountUnavailable = "codex_account_unavailable"
	ErrCodeModelSelectionInvalid   = "model_selection_invalid"
	ErrCodePetPermissionDenied     = "pet_permission_denied"
	ErrCodeTaskNotFound            = "task_not_found"
	ErrCodeAgentNotFound           = "agent_not_found"
	ErrCodeTaskReadOnly            = "task_read_only"
	ErrCodeTaskNotEditable         = "task_not_editable"
)
