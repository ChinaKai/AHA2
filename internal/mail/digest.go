package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// approvalDigestAttachment is the immutable attachment/link-policy part of the
// approval subject. It deliberately excludes link_download_count, revocation
// and any timestamp that changes during normal delivery.
type approvalDigestAttachment struct {
	SourceType       string `json:"source_type"`
	SourceID         string `json:"source_id"`
	FileName         string `json:"file_name"`
	SizeBytes        int64  `json:"size_bytes"`
	SHA256           string `json:"sha256"`
	DeliveryMode     string `json:"delivery_mode"`
	LinkExpiresAt    string `json:"link_expires_at,omitempty"`
	LinkMaxDownloads int    `json:"link_max_downloads,omitempty"`
}

// approvalDigestSubject is the canonical JSON object from design section 2.4.
// Field order is fixed by struct declaration, so the digest is deterministic.
type approvalDigestSubject struct {
	ContentRevision int                        `json:"content_revision"`
	AccountID       string                     `json:"account_id"`
	FromAddress     string                     `json:"from_address"`
	FromName        string                     `json:"from_name"`
	To              []string                   `json:"to"`
	CC              []string                   `json:"cc"`
	BCC             []string                   `json:"bcc"`
	ReplyTo         string                     `json:"reply_to"`
	Subject         string                     `json:"subject"`
	BodyText        string                     `json:"body_text"`
	BodyHTML        string                     `json:"body_html"`
	Attachments     []approvalDigestAttachment `json:"attachments"`
}

func digestTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func digestStringList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

// ApprovalDigest computes SHA-256 over the normalized approval subject. Any
// change to the account, From, recipients, body, attachments or link policy
// changes the digest, which forces a new draft and a new Owner approval.
// Revoking a link does not change it, matching design sections 6.1 and 7.3.
func ApprovalDigest(message domain.MailMessage, attachments []domain.MailAttachment) string {
	subject := approvalDigestSubject{
		ContentRevision: message.ContentRevision,
		AccountID:       message.AccountID,
		FromAddress:     message.FromAddress,
		FromName:        message.FromName,
		To:              digestStringList(message.To),
		CC:              digestStringList(message.CC),
		BCC:             digestStringList(message.BCC),
		ReplyTo:         message.ReplyTo,
		Subject:         message.Subject,
		BodyText:        message.BodyText,
		BodyHTML:        message.BodyHTML,
		Attachments:     make([]approvalDigestAttachment, 0, len(attachments)),
	}
	for _, attachment := range attachments {
		item := approvalDigestAttachment{
			SourceType:   string(attachment.SourceType),
			SourceID:     attachment.SourceID,
			FileName:     attachment.FileName,
			SizeBytes:    attachment.SizeBytes,
			SHA256:       attachment.SHA256,
			DeliveryMode: string(attachment.DeliveryMode),
		}
		if attachment.DeliveryMode == domain.MailAttachmentLink {
			item.LinkExpiresAt = digestTime(attachment.LinkExpiresAt)
			item.LinkMaxDownloads = attachment.LinkMaxDownloads
		}
		subject.Attachments = append(subject.Attachments, item)
	}
	encoded, err := json.Marshal(subject)
	if err != nil {
		// All fields are plain strings/ints/lists, so marshalling cannot fail.
		// Keep a deterministic fallback rather than panicking in a worker.
		return "sha256:" + hex.EncodeToString(sha256.New().Sum(nil))
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
