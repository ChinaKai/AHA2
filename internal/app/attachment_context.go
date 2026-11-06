package app

import "github.com/ChinaKai/AHA2/internal/domain"

// inlineAttachmentBytes decides whether an attachment may be read into the
// per-Turn Context. The check runs before open, so an attachment registered
// above the inline cap never has its bytes read, converted to a string, or
// materialized into the workspace.
//
// The limit is a parameter so tests can exercise the branch with a small
// attachment instead of allocating a real 200MiB payload.
func inlineAttachmentBytes(item domain.Attachment, limit int64, open func() ([]byte, error)) ([]byte, bool, error) {
	if item.Size < 1 || item.Size > limit {
		return nil, false, nil
	}
	content, err := open()
	if err != nil {
		return nil, false, err
	}
	// The stored size is authoritative, but never let a metadata mismatch push
	// an oversized blob into Context.
	if int64(len(content)) > limit {
		return nil, false, nil
	}
	return content, true, nil
}

type attachmentInlineDecision struct {
	Inline          bool
	BudgetExhausted bool
	Content         []byte
	Err             error
}

// allocateInlineAttachments assigns the per-Turn inline budget newest-first.
// items must be in conversation order (oldest first), matching
// ConversationPageForAgent. The current input's attachments therefore win the
// budget even when older attachments already fill it; the returned slice stays
// in the original order so Context output order does not change.
//
// Reasons are tracked separately: BudgetExhausted is set only when the
// attachment itself fits the per-attachment cap but the remaining Turn budget
// does not. An attachment above the per-attachment cap is left with both flags
// false and is reported as exceeding the inline limit.
func allocateInlineAttachments(
	items []domain.Attachment,
	perAttachment int64,
	total int64,
	read func(domain.Attachment) ([]byte, error),
) []attachmentInlineDecision {
	decisions := make([]attachmentInlineDecision, len(items))
	used := int64(0)
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if item.Size < 1 || item.Size > perAttachment {
			continue
		}
		remaining := total - used
		if remaining < item.Size {
			decisions[index].BudgetExhausted = true
			continue
		}
		limit := perAttachment
		if remaining < limit {
			limit = remaining
		}
		content, inline, err := inlineAttachmentBytes(item, limit, func() ([]byte, error) {
			return read(item)
		})
		if err != nil {
			decisions[index].Err = err
			continue
		}
		if !inline {
			continue
		}
		decisions[index].Inline = true
		decisions[index].Content = content
		used += int64(len(content))
	}
	return decisions
}
