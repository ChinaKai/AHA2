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

func TestAllocateInlineAttachmentsPrioritizesNewest(t *testing.T) {
	t.Parallel()
	const (
		perAttachment = 4 << 20
		total         = 6 << 20
	)
	items := []domain.Attachment{
		{ID: "old-1", Size: 3 << 20},
		{ID: "old-2", Size: 3 << 20},
		{ID: "newest", Size: 3 << 20},
	}
	readIDs := []string{}
	decisions := allocateInlineAttachments(items, perAttachment, total, func(item domain.Attachment) ([]byte, error) {
		readIDs = append(readIDs, item.ID)
		return make([]byte, item.Size), nil
	})
	if !decisions[2].Inline || decisions[2].BudgetExhausted {
		t.Fatalf("newest attachment should be inlined: %#v", decisions[2])
	}
	if !decisions[1].Inline || decisions[1].BudgetExhausted {
		t.Fatalf("second newest attachment should fill the remaining budget: %#v", decisions[1])
	}
	if decisions[0].Inline || !decisions[0].BudgetExhausted {
		t.Fatalf("oldest attachment should be downgraded for budget: %#v", decisions[0])
	}
	if len(readIDs) != 2 || readIDs[0] != "newest" || readIDs[1] != "old-2" {
		t.Fatalf("read order = %v, want newest then old-2", readIDs)
	}
}

func TestAllocateInlineAttachmentsKeepsTooLargeSeparateFromBudget(t *testing.T) {
	t.Parallel()
	items := []domain.Attachment{
		{ID: "too-large", Size: 5 << 20},
		{ID: "newest", Size: 1 << 20},
	}
	opened := map[string]bool{}
	decisions := allocateInlineAttachments(items, 4<<20, 16<<20, func(item domain.Attachment) ([]byte, error) {
		opened[item.ID] = true
		return make([]byte, item.Size), nil
	})
	if decisions[0].Inline || decisions[0].BudgetExhausted {
		t.Fatalf("over-cap attachment must not be treated as budget-exhausted: %#v", decisions[0])
	}
	if opened["too-large"] {
		t.Fatal("over-cap attachment content was read")
	}
	if !decisions[1].Inline {
		t.Fatalf("newest attachment should still be inlined: %#v", decisions[1])
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
