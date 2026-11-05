package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailAttachmentSnapshotsAndBuildArtifacts(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Attachments", "attachments@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "attachments-1")
	now := time.Now().UTC()

	source, err := database.CreateAttachment(ctx, taskID, "firmware.bin",
		"application/octet-stream", []byte("firmware-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Firmware", PackageName: "firmware", Version: "1.2.3",
		SizeBytes: source.Size, SHA256: source.SHA256,
		StorageType: domain.BuildArtifactStorageAHAAttachment, AttachmentID: source.ID,
		RegisteredBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	storedArtifact, err := database.BuildArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedArtifact.AttachmentID != source.ID || storedArtifact.SizeBytes != source.Size ||
		storedArtifact.SHA256 != source.SHA256 {
		t.Fatalf("build artifact = %+v", storedArtifact)
	}
	artifacts, err := database.BuildArtifactsByTask(ctx, taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != artifact.ID {
		t.Fatalf("build artifacts = %+v", artifacts)
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Missing attachment", SHA256: source.SHA256,
		StorageType: domain.BuildArtifactStorageAHAAttachment,
	}); err == nil {
		t.Fatal("aha_attachment artifact without attachment_id was accepted")
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "External", SizeBytes: 1, SHA256: source.SHA256,
		StorageType: domain.BuildArtifactStorageExternalURL,
		ExternalURL: "https://example.com/firmware.bin",
	}); err == nil {
		t.Fatal("phase one accepted an external_url build artifact")
	}
	otherTaskID, _, _ := createMailTaskFixture(t, database)
	otherSource, err := database.CreateAttachment(ctx, otherTaskID, "other.bin",
		"application/octet-stream", []byte("other-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Cross task", SizeBytes: otherSource.Size,
		SHA256: otherSource.SHA256, AttachmentID: otherSource.ID,
		StorageType: domain.BuildArtifactStorageAHAAttachment,
	}); err == nil {
		t.Fatal("build artifact accepted an attachment from another task")
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Wrong size", SizeBytes: source.Size + 1,
		SHA256: source.SHA256, AttachmentID: source.ID,
		StorageType: domain.BuildArtifactStorageAHAAttachment,
	}); err == nil {
		t.Fatal("build artifact accepted a mismatched size")
	}
	if _, err := database.CreateBuildArtifact(ctx, domain.BuildArtifact{
		TaskID: taskID, Label: "Wrong hash", SizeBytes: source.Size,
		SHA256: strings.Repeat("0", 64), AttachmentID: source.ID,
		StorageType: domain.BuildArtifactStorageAHAAttachment,
	}); err == nil {
		t.Fatal("build artifact accepted a mismatched sha256")
	}

	direct, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "firmware.bin", MediaType: "application/octet-stream",
		SizeBytes: source.Size, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.LinkTokenRef != "" {
		t.Fatal("created mail attachment leaked link_token_ref")
	}
	linkHash := strings.Repeat("b", 64)
	linkTokenRef := "mail-link/" + message.ID + "/0/placeholder"
	link, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 1,
		SourceType: domain.MailAttachmentSourceBuildArtifact, SourceID: artifact.ID,
		FileName: "firmware-link.bin", SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256,
		DeliveryMode:     domain.MailAttachmentLink,
		LinkTokenRef:     linkTokenRef,
		LinkTokenHash:    linkHash,
		LinkExpiresAt:    now.Add(7 * 24 * time.Hour),
		LinkMaxDownloads: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if link.LinkTokenRef != "" {
		t.Fatal("created link attachment leaked link_token_ref")
	}

	attachments, err := database.MailAttachments(ctx, message.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("mail attachments = %+v", attachments)
	}
	if attachments[0].ID != direct.ID || attachments[1].ID != link.ID {
		t.Fatalf("attachment order = %+v", attachments)
	}
	if attachments[0].LinkTokenRef != "" || attachments[1].LinkTokenRef != "" {
		t.Fatal("MailAttachments leaked link_token_ref")
	}
	if attachments[1].LinkTokenHash != linkHash ||
		attachments[1].LinkMaxDownloads != 20 ||
		attachments[1].LinkExpiresAt.IsZero() ||
		attachments[1].DeliveryMode != domain.MailAttachmentLink {
		t.Fatalf("link attachment snapshot = %+v", attachments[1])
	}
	if ref, err := database.MailAttachmentLinkTokenRef(ctx, link.ID); err != nil || ref != linkTokenRef {
		t.Fatalf("link token ref = %q, err=%v (want %q)", ref, err, linkTokenRef)
	}

	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "duplicate.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect,
	}); err == nil {
		t.Fatal("duplicate message/position passed the unique constraint")
	}
	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 3,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "direct-with-link.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect, LinkTokenHash: linkHash,
	}); err == nil {
		t.Fatal("direct delivery mode accepted link fields")
	}
	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 4,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "link-missing-expiry.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentLink,
		LinkTokenRef: "mail-link/missing", LinkTokenHash: strings.Repeat("c", 64),
	}); err == nil {
		t.Fatal("link delivery mode accepted a missing expiry")
	}
	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 5,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "link-missing-ref.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentLink, LinkTokenHash: strings.Repeat("d", 64),
		LinkExpiresAt: now.Add(time.Hour),
	}); err == nil {
		t.Fatal("link delivery mode accepted a missing token ref")
	}
	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 2,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "duplicate-hash.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentLink, LinkTokenHash: linkHash,
	}); err == nil {
		t.Fatal("duplicate non-empty link_token_hash passed the partial unique index")
	}
	if _, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 3,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: "inline.bin", SizeBytes: 1, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect,
		Disposition:  domain.MailAttachmentDispositionInline,
	}); err == nil {
		t.Fatal("phase one accepted inline disposition")
	}

	if err := database.RevokeMailAttachmentLink(ctx, link.ID, now); err != nil {
		t.Fatal(err)
	}
	revoked, err := database.MailAttachment(ctx, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.LinkRevokedAt.IsZero() {
		t.Fatal("link revocation was not persisted")
	}

	if _, err := database.db.ExecContext(ctx, `DELETE FROM mail_messages WHERE id=?`, message.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := database.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_attachments WHERE message_id=?`, message.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("attachments survived message delete: %d", remaining)
	}
}
