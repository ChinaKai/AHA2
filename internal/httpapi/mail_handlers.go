package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
)

// writeMailError follows the unified error envelope from the design:
// {"ok":false,"error":{"code":"...","message":"..."}}.
func writeMailError(writer http.ResponseWriter, status int, code domain.MailErrorCode, message string) {
	errorBody := map[string]any{"code": string(code)}
	if message != "" {
		errorBody["message"] = message
	}
	writeJSON(writer, status, map[string]any{"ok": false, "error": errorBody})
}

func mailErrorStatus(code domain.MailErrorCode) int {
	switch code {
	case domain.MailErrAccountNotFound, domain.MailErrTemplateNotFound,
		domain.MailErrAttachmentNotFound, domain.MailErrBindingNotFound,
		domain.MailErrMessageNotFound:
		return http.StatusNotFound
	case domain.MailErrAccountNotBound, domain.MailErrForbidden:
		return http.StatusForbidden
	case domain.MailErrRateLimited:
		return http.StatusTooManyRequests
	case domain.MailErrTemplateVersionConflict, domain.MailErrInvalidState,
		domain.MailErrDuplicateMessage, domain.MailErrApprovalDigestMismatch,
		domain.MailErrAccountDisabled:
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

// writeMailFailure maps a rendering/store error to its stable code. Unknown
// errors become a generic message so internal paths and secrets never leak.
func writeMailFailure(writer http.ResponseWriter, err error, fallback string) {
	code, ok := mailpkg.CodeOf(err)
	if !ok {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", fallback)
		return
	}
	status := mailErrorStatus(code)
	message := err.Error()
	var mailErr *mailpkg.Error
	if errors.As(err, &mailErr) {
		message = mailErr.Message
	}
	writeMailError(writer, status, code, message)
}

func writeMailNotFound(writer http.ResponseWriter, code domain.MailErrorCode, message string) {
	writeMailError(writer, http.StatusNotFound, code, message)
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// mailOwnerID returns the Owner that will be recorded as the template/binding
// author. The route is already behind withAuth, so a session is always present.
func mailOwnerID(request *http.Request) string {
	if session, ok := sessionFromContext(request.Context()); ok {
		return session.OwnerID
	}
	return ""
}

func (s *Server) putMailCredential(accountID, credential string) error {
	if s.secrets == nil {
		return errors.New("secret store unavailable")
	}
	return s.secrets.PutMany(map[string]string{
		domain.MailAccountCredentialRef(accountID): credential,
	})
}

func (s *Server) deleteMailCredential(accountID string) {
	if s.secrets == nil {
		return
	}
	// A failed secret deletion only leaves an orphan key; it must never make a
	// deleted account usable again. Do not log the key value.
	_ = s.secrets.DeleteMany([]string{domain.MailAccountCredentialRef(accountID)})
}
