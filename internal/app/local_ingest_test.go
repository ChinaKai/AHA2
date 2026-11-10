package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestNormalizeLocalIngestContent(t *testing.T) {
	t.Parallel()
	valid, err := normalizeLocalIngestContent("  你好\nworld\t  ")
	if err != nil || valid != "你好\nworld" {
		t.Fatalf("valid=%q err=%v", valid, err)
	}
	if _, err := normalizeLocalIngestContent("  \n\t "); !errors.Is(err, ErrLocalIngestContentEmpty) {
		t.Fatalf("empty err=%v", err)
	}
	if _, err := normalizeLocalIngestContent(strings.Repeat("字", LocalOwnerIngestMaxContentRunes+1)); !errors.Is(err, ErrLocalIngestContentTooLong) {
		t.Fatalf("too long err=%v", err)
	}
	controls := []string{
		"a" + string(rune(0x00)) + "b",
		"a" + string(rune(0x1f)) + "b",
		"a" + string(rune(0x7f)) + "b",
		"a" + string(rune(0x85)) + "b",
	}
	for _, value := range controls {
		if _, err := normalizeLocalIngestContent(value); !errors.Is(err, ErrLocalIngestContentControlChar) {
			t.Fatalf("%q err=%v", value, err)
		}
	}
}

func TestNormalizeLocalIngestSourceDefaultsToVoice(t *testing.T) {
	t.Parallel()
	if source, err := normalizeLocalIngestSource(""); err != nil || source != LocalOwnerIngestSourceVoice {
		t.Fatalf("source=%q err=%v", source, err)
	}
	if source, err := normalizeLocalIngestSource("voice"); err != nil || source != LocalOwnerIngestSourceVoice {
		t.Fatalf("source=%q err=%v", source, err)
	}
	if _, err := normalizeLocalIngestSource("cli"); !errors.Is(err, ErrLocalIngestSourceInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeLocalIngestRequestID(t *testing.T) {
	t.Parallel()
	if value, err := normalizeLocalIngestRequestID("  "); err != nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if value, err := normalizeLocalIngestRequestID("voice-1_ok"); err != nil || value != "voice-1_ok" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := normalizeLocalIngestRequestID("has space"); !errors.Is(err, ErrLocalIngestRequestIDInvalid) {
		t.Fatalf("err=%v", err)
	}
	if _, err := normalizeLocalIngestRequestID(strings.Repeat("a", LocalOwnerIngestMaxClientRequestIDLength+1)); !errors.Is(err, ErrLocalIngestRequestIDInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestLocalOwnerIngestTaskRunnable(t *testing.T) {
	t.Parallel()
	for _, status := range []domain.TaskStatus{domain.TaskPreparing, domain.TaskActive, domain.TaskWaitingUser, domain.TaskFailed} {
		if !localOwnerIngestTaskRunnable(status) {
			t.Fatalf("%s should be runnable", status)
		}
	}
	for _, status := range []domain.TaskStatus{domain.TaskDraft, domain.TaskCompleted, domain.TaskCancelled, domain.TaskBlocked} {
		if localOwnerIngestTaskRunnable(status) {
			t.Fatalf("%s should not be runnable", status)
		}
	}
}

func TestSubmitLocalOwnerMessageRejectsNonWhitelistedTask(t *testing.T) {
	t.Parallel()
	service := &Service{}
	_, err := service.SubmitLocalOwnerMessage(context.Background(), LocalOwnerMessageInput{
		TaskID: "task_other", Content: "hi", Source: "voice",
	})
	if !errors.Is(err, ErrLocalIngestTaskNotAllowed) {
		t.Fatalf("err=%v", err)
	}
	if _, err := service.SubmitLocalOwnerMessage(context.Background(), LocalOwnerMessageInput{
		Content: "hi", Source: "voice",
	}); !errors.Is(err, ErrLocalIngestTaskRequired) {
		t.Fatalf("err=%v", err)
	}
}
