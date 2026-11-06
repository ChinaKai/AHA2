package app

import "github.com/ChinaKai/AHA2/internal/domain"

// inlineAttachmentBytes decides whether an attachment may be read into the
// per-Turn Context. The check runs before open, so an attachment registered
// above the inline cap never has its bytes read, converted to a string, or
// materialized into the workspace.
//
// The limit is a parameter so tests can exercise the branch with a small
// attachment instead of allocating a real 200MiB payload.
// inlineAttachmentLimit combines the per-attachment cap with the remaining
// per-Turn budget. Zero means the budget is exhausted and nothing else should
// be read this Turn.
func inlineAttachmentLimit(perAttachment, total, used int64) int64 {
	remaining := total - used
	if remaining <= 0 {
		return 0
	}
	if remaining < perAttachment {
		return remaining
	}
	return perAttachment
}

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
