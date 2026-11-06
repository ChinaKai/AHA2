package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
)

// This exercises the runTurn wiring, not just the allocator: the blob is
// deleted, so any AttachmentContent call for the over-cap attachment fails the
// Turn. A passing Turn plus a metadata.md Context file proves the content was
// never read or materialized.
func TestRunTurnKeepsOverCapAttachmentMetadataOnly(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := fixture.createPetDraft(t, "context attachment wiring")
	content := make([]byte, prompt.MaxInlineAttachmentBytes+1)
	copy(content, []byte("BIG_ATTACHMENT_MARKER"))
	attachment, err := fixture.database.CreateAttachment(fixture.ctx, task.ID, "big.bin", "application/octet-stream", content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(filepath.Dir(fixture.databasePath), "attachments", "blobs", attachment.SHA256[:2], attachment.SHA256)
	if err := os.Remove(blob); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}

	turn, err := fixture.service.SubmitAgentMessageWithAttachments(fixture.ctx, task.ID, "main", "inspect the attachment", []string{attachment.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, fixture.database, turn.ID, domain.TurnSucceeded)

	prompts, _ := fixture.service.executor.(*stubExecutor).requestHistory()
	if len(prompts) == 0 {
		t.Fatal("turn did not reach the executor")
	}
	lastPrompt := prompts[len(prompts)-1]
	if !strings.Contains(lastPrompt, "big.bin") {
		t.Fatalf("prompt does not reference the attachment:\n%s", lastPrompt)
	}
	if strings.Contains(lastPrompt, "BIG_ATTACHMENT_MARKER") {
		t.Fatal("over-cap attachment content leaked into the prompt")
	}

	storedTask := fixture.petTask(t)
	workspace, err := fixture.database.Workspace(fixture.ctx, storedTask.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	workDir := strings.TrimSpace(storedTask.TaskWorkspacePath)
	if workDir == "" {
		workDir = workspace.RootPath
	}
	contextRoot := filepath.Join(workDir, ".aha2-context", storedTask.ID, "main")
	indexPath := filepath.Join(contextRoot, "attachments", "index.md")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("attachments/index.md was not materialized: %v", err)
	}
	for _, expected := range []string{"big.bin", attachment.SHA256, "/api/v1/tasks/" + task.ID + "/attachments/" + attachment.ID} {
		if !strings.Contains(string(index), expected) {
			t.Fatalf("attachments/index.md missing %q:\n%s", expected, index)
		}
	}
	// The index template is localized; either wording proves the metadata line.
	if !strings.Contains(string(index), "metadata only") && !strings.Contains(string(index), "仅元数据") {
		t.Fatalf("attachments/index.md does not mark the attachment metadata-only:\n%s", index)
	}
	metadataPath := filepath.Join(contextRoot, "attachments", attachment.ID, "metadata.md")
	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("metadata.md was not materialized: %v", err)
	}
	for _, expected := range []string{attachment.SHA256, "not inlined", "4 MiB", "/api/v1/tasks/" + task.ID + "/attachments/" + attachment.ID} {
		if !strings.Contains(string(metadata), expected) {
			t.Fatalf("metadata.md missing %q:\n%s", expected, metadata)
		}
	}
	if strings.Contains(string(metadata), "BIG_ATTACHMENT_MARKER") {
		t.Fatal("metadata.md contains attachment content")
	}
	inlinePath := filepath.Join(contextRoot, "attachments", attachment.ID, "big.bin")
	if _, err := os.Stat(inlinePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("over-cap attachment was materialized: %v", err)
	}
}
