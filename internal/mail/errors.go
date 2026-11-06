// Package mail implements template rendering, sanitizing, approval digests,
// MIME assembly, implicit-TLS SMTP delivery and connection tests for the mail
// resource phase one. The durable outbox loop lives in internal/mailworker.
package mail

import (
	"errors"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Error carries a stable API error code from the design's section 4.4. The
// message never contains variable values, credentials or SMTP transcripts.
type Error struct {
	Code    domain.MailErrorCode
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func errorf(code domain.MailErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Attachment reference errors shared by the HTTP preview path.
func ErrAttachmentLimit() error {
	return errorf(domain.MailErrAttachmentTooLarge, "at most %d attachments are allowed", MaxAttachments)
}

func ErrAttachmentTaskRequired() error {
	return errorf(domain.MailErrAttachmentNotFound, "preview attachments require task_id")
}

func ErrAttachmentNotFound() error {
	return errorf(domain.MailErrAttachmentNotFound, "attachment does not belong to the current task")
}

func ErrAttachmentUnavailable() error {
	return errorf(domain.MailErrAttachmentUnavailable, "attachment source is unavailable or its hash changed")
}

func ErrAttachmentTooLarge() error {
	return errorf(domain.MailErrAttachmentTooLarge, "attachment exceeds the %d-byte per-file limit", MaxAttachmentBytes)
}

// ErrAttachmentTypeForbidden rejects executable files and scripts. The Owner
// confirmed that phase one accepts no exception for direct attachments; build
// artifacts that must be distributed have to use a controlled download link.
func ErrAttachmentTypeForbidden(name string) error {
	return errorf(domain.MailErrAttachmentUnavailable,
		"executable or script attachment %q is not allowed", name)
}

// CodeOf extracts the stable code from an error returned by this package.
func CodeOf(err error) (domain.MailErrorCode, bool) {
	var mailErr *Error
	if errors.As(err, &mailErr) {
		return mailErr.Code, true
	}
	return "", false
}
