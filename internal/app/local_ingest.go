package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	// LocalOwnerIngestSecretRef is the Secret Store key holding the bearer
	// secret for the loopback owner-message ingest endpoint.
	LocalOwnerIngestSecretRef = "local-owner-messages/secret"
	// LocalOwnerIngestSourceVoice is the only accepted non-empty provenance.
	LocalOwnerIngestSourceVoice = "voice"
	// LocalOwnerIngestMaxContentRunes bounds one spoken message after trimming.
	LocalOwnerIngestMaxContentRunes = 4000
	// LocalOwnerIngestMaxClientRequestIDLength bounds the idempotency key.
	LocalOwnerIngestMaxClientRequestIDLength = 128
)

var (
	ErrLocalIngestTaskRequired       = errors.New("task_id is required")
	ErrLocalIngestTaskNotAllowed     = errors.New("task is not allowed for local owner ingest")
	ErrLocalIngestTaskNotFound       = errors.New("task not found")
	ErrLocalIngestContentEmpty       = errors.New("content is required")
	ErrLocalIngestContentTooLong     = errors.New("content is too long")
	ErrLocalIngestContentControlChar = errors.New("content contains control characters")
	ErrLocalIngestSourceInvalid      = errors.New("source must be voice")
	ErrLocalIngestRequestIDInvalid   = errors.New("client_request_id is invalid")
)

type LocalOwnerMessageInput struct {
	TaskID          string
	Content         string
	Source          string
	ClientRequestID string
}

type LocalOwnerMessageResult struct {
	Turn      domain.Turn
	MessageID string
	Duplicate bool
}

// SubmitLocalOwnerMessage writes a local bridge message as the owner into an
// allowlisted Task and drives the normal Turn pipeline. It never approves
// anything, executes tools, or changes permissions by itself.
func (s *Service) SubmitLocalOwnerMessage(ctx context.Context, input LocalOwnerMessageInput) (LocalOwnerMessageResult, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return LocalOwnerMessageResult{}, ErrLocalIngestTaskRequired
	}
	if !localOwnerIngestTaskAllowed(taskID) {
		return LocalOwnerMessageResult{}, ErrLocalIngestTaskNotAllowed
	}
	content, err := normalizeLocalIngestContent(input.Content)
	if err != nil {
		return LocalOwnerMessageResult{}, err
	}
	source, err := normalizeLocalIngestSource(input.Source)
	if err != nil {
		return LocalOwnerMessageResult{}, err
	}
	clientRequestID, err := normalizeLocalIngestRequestID(input.ClientRequestID)
	if err != nil {
		return LocalOwnerMessageResult{}, err
	}
	if clientRequestID != "" {
		existing, found, lookupErr := s.store.LocalOwnerMessageByClientRequestID(ctx, clientRequestID)
		if lookupErr != nil {
			return LocalOwnerMessageResult{}, lookupErr
		}
		if found {
			if existing.TaskID != taskID {
				return LocalOwnerMessageResult{}, store.ErrLocalIngestRequestConflict
			}
			// Scheduling is idempotent: if the first attempt inserted the
			// message but failed before starting its Turn, this retry repairs
			// delivery without inserting a second message.
			turn, started, scheduleErr := s.scheduleAgent(ctx, taskID, "main")
			if scheduleErr != nil {
				return LocalOwnerMessageResult{}, scheduleErr
			}
			result := LocalOwnerMessageResult{MessageID: existing.ID, Duplicate: true}
			if started {
				result.Turn = turn
			}
			return result, nil
		}
	}
	submitted, err := s.submitMessageWithSourceResult(
		ctx, taskID, "main", content, nil, "", "", "", nil,
		"owner", "owner_message", false,
		&localOwnerMessageMeta{Source: source, ClientRequestID: clientRequestID},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalOwnerMessageResult{}, ErrLocalIngestTaskNotFound
	}
	if err != nil {
		return LocalOwnerMessageResult{}, err
	}
	return LocalOwnerMessageResult{
		Turn: submitted.Turn, MessageID: submitted.MessageID, Duplicate: submitted.Duplicate,
	}, nil
}

func localOwnerIngestTaskAllowed(taskID string) bool {
	return taskID == petTaskID
}

func normalizeLocalIngestContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", ErrLocalIngestContentEmpty
	}
	if utf8.RuneCountInString(content) > LocalOwnerIngestMaxContentRunes {
		return "", ErrLocalIngestContentTooLong
	}
	for _, value := range content {
		switch value {
		case '\n', '\r', '\t':
			continue
		}
		if value < 0x20 || (value >= 0x7f && value <= 0x9f) {
			return "", ErrLocalIngestContentControlChar
		}
	}
	return content, nil
}

func normalizeLocalIngestSource(raw string) (string, error) {
	source := strings.TrimSpace(raw)
	if source == "" {
		// This endpoint exists for the voice bridge; an omitted source is still
		// voice provenance rather than an unknown write path.
		return LocalOwnerIngestSourceVoice, nil
	}
	if source != LocalOwnerIngestSourceVoice {
		return "", ErrLocalIngestSourceInvalid
	}
	return source, nil
}

func normalizeLocalIngestRequestID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > LocalOwnerIngestMaxClientRequestIDLength {
		return "", ErrLocalIngestRequestIDInvalid
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", ErrLocalIngestRequestIDInvalid
		}
	}
	return value, nil
}
