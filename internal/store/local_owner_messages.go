package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ErrLocalIngestRequestConflict means the same client_request_id was reused for
// a different Task. Treating it as a duplicate would silently drop the second
// request, so the caller must surface it instead.
var ErrLocalIngestRequestConflict = errors.New("client_request_id is already bound to another task")

// LocalOwnerMessageByClientRequestID looks up a local ingest message by its
// idempotency key. Callers use it before running task-state validations so a
// retry cannot fail just because the first call already advanced the Task.
func (s *Store) LocalOwnerMessageByClientRequestID(ctx context.Context, clientRequestID string) (domain.Message, bool, error) {
	return localOwnerMessageByRequestID(ctx, s.db, clientRequestID)
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func localOwnerMessageByRequestID(ctx context.Context, queryer rowQuerier, clientRequestID string) (domain.Message, bool, error) {
	var message domain.Message
	var createdAt string
	err := queryer.QueryRowContext(ctx, `
		SELECT id,task_id,role,sender,content,source,client_request_id,created_at
		FROM messages WHERE client_request_id=?`,
		clientRequestID,
	).Scan(
		&message.ID, &message.TaskID, &message.Role, &message.Sender, &message.Content,
		&message.Source, &message.ClientRequestID, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Message{}, false, nil
	}
	if err != nil {
		return domain.Message{}, false, err
	}
	message.CreatedAt = parseTime(createdAt)
	return message, true, nil
}

func isClientRequestIDConflict(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: messages.client_request_id")
}
