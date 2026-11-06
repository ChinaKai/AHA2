package app

import (
	"errors"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestInlineAttachmentBytesSkipsOversizedBeforeReading(t *testing.T) {
	t.Parallel()
	const limit = 1 << 20
	opened := false
	item := domain.Attachment{ID: "attachment-big", Size: 2 << 20}
	content, inline, err := inlineAttachmentBytes(item, limit, func() ([]byte, error) {
		opened = true
		return []byte("should not be read"), nil
	})
	if err != nil || inline || content != nil {
		t.Fatalf("oversized attachment: content=%q inline=%v err=%v", content, inline, err)
	}
	if opened {
		t.Fatal("oversized attachment opened its content")
	}
}

func TestInlineAttachmentBytesReadsSmallAttachment(t *testing.T) {
	t.Parallel()
	item := domain.Attachment{ID: "attachment-small", Size: 5}
	content, inline, err := inlineAttachmentBytes(item, 1<<20, func() ([]byte, error) {
		return []byte("hello"), nil
	})
	if err != nil || !inline || string(content) != "hello" {
		t.Fatalf("small attachment: content=%q inline=%v err=%v", content, inline, err)
	}
}

func TestInlineAttachmentBytesPropagatesReadError(t *testing.T) {
	t.Parallel()
	want := errors.New("blob missing")
	_, _, err := inlineAttachmentBytes(domain.Attachment{ID: "attachment-1", Size: 1}, 1<<20, func() ([]byte, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestInlineAttachmentLimitHonorsPerTurnBudget(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name          string
		perAttachment int64
		total         int64
		used          int64
		want          int64
	}{
		{name: "fresh turn", perAttachment: 4 << 20, total: 16 << 20, used: 0, want: 4 << 20},
		{name: "remaining smaller", perAttachment: 4 << 20, total: 16 << 20, used: 14 << 20, want: 2 << 20},
		{name: "exhausted", perAttachment: 4 << 20, total: 16 << 20, used: 16 << 20, want: 0},
		{name: "over budget", perAttachment: 4 << 20, total: 16 << 20, used: 17 << 20, want: 0},
	} {
		if got := inlineAttachmentLimit(testCase.perAttachment, testCase.total, testCase.used); got != testCase.want {
			t.Fatalf("%s: limit = %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

func TestInlineAttachmentBytesRejectsContentLargerThanMetadata(t *testing.T) {
	t.Parallel()
	const limit = 1 << 20
	_, inline, err := inlineAttachmentBytes(domain.Attachment{ID: "attachment-1", Size: 1}, limit, func() ([]byte, error) {
		return make([]byte, limit+1), nil
	})
	if err != nil || inline {
		t.Fatalf("mismatched blob: inline=%v err=%v", inline, err)
	}
}
