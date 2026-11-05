package mail

import (
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func digestMessage() domain.MailMessage {
	return domain.MailMessage{
		ContentRevision: 1, AccountID: "mail_account_1",
		FromAddress: "noreply@example.com", FromName: "AHA",
		To: []string{"qa@example.com"}, CC: []string{"cc@example.com"},
		Subject: "subject", BodyText: "text", BodyHTML: "<p>html</p>",
	}
}

func digestAttachment() domain.MailAttachment {
	return domain.MailAttachment{
		ID: "mail_attachment_1", SourceType: domain.MailAttachmentSourceTaskAttachment,
		SourceID: "attachment_1", FileName: "report.zip", SizeBytes: 123,
		SHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DeliveryMode: domain.MailAttachmentDirect,
	}
}

func TestApprovalDigestIsStableAndCoversTheApprovalSubject(t *testing.T) {
	message := digestMessage()
	attachments := []domain.MailAttachment{digestAttachment()}
	base := ApprovalDigest(message, attachments)
	if base == "" || base[:7] != "sha256:" {
		t.Fatalf("digest = %q", base)
	}
	if again := ApprovalDigest(message, attachments); again != base {
		t.Fatalf("digest is not deterministic: %q vs %q", base, again)
	}

	mutations := map[string]func(*domain.MailMessage, *[]domain.MailAttachment){
		"revision": func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.ContentRevision++ },
		"account":  func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.AccountID = "mail_account_2" },
		"from":     func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.FromAddress = "other@example.com" },
		"from name": func(m *domain.MailMessage, _ *[]domain.MailAttachment) {
			m.FromName = "Other"
		},
		"to":      func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.To = []string{"other@example.com"} },
		"cc":      func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.CC = nil },
		"bcc":     func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.BCC = []string{"audit@example.com"} },
		"subject": func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.Subject = "changed" },
		"text":    func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.BodyText = "changed" },
		"html":    func(m *domain.MailMessage, _ *[]domain.MailAttachment) { m.BodyHTML = "<p>changed</p>" },
		"attachment source": func(_ *domain.MailMessage, a *[]domain.MailAttachment) {
			(*a)[0].SourceID = "attachment_2"
		},
		"attachment name": func(_ *domain.MailMessage, a *[]domain.MailAttachment) {
			(*a)[0].FileName = "other.zip"
		},
		"attachment hash": func(_ *domain.MailMessage, a *[]domain.MailAttachment) {
			(*a)[0].SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
	}
	for name, mutate := range mutations {
		changedMessage := digestMessage()
		changedAttachments := []domain.MailAttachment{digestAttachment()}
		mutate(&changedMessage, &changedAttachments)
		if got := ApprovalDigest(changedMessage, changedAttachments); got == base {
			t.Fatalf("%s change did not alter the digest", name)
		}
	}

	// Link policy is part of the approved content.
	link := digestAttachment()
	link.DeliveryMode = domain.MailAttachmentLink
	link.LinkExpiresAt = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	link.LinkMaxDownloads = 20
	linkDigest := ApprovalDigest(message, []domain.MailAttachment{link})
	link.LinkMaxDownloads = 10
	if changed := ApprovalDigest(message, []domain.MailAttachment{link}); changed == linkDigest {
		t.Fatal("link download limit is not covered by the digest")
	}
	link.LinkMaxDownloads = 20
	link.LinkExpiresAt = link.LinkExpiresAt.Add(time.Hour)
	if changed := ApprovalDigest(message, []domain.MailAttachment{link}); changed == linkDigest {
		t.Fatal("link expiry is not covered by the digest")
	}

	// Revocation is allowed after approval and must not change the digest.
	link.LinkExpiresAt = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	link.LinkRevokedAt = time.Now().UTC()
	link.LinkDownloadCount = 5
	if got := ApprovalDigest(message, []domain.MailAttachment{link}); got != linkDigest {
		t.Fatalf("revocation or download count changed the digest: %s vs %s", got, linkDigest)
	}
}
