package prompt

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func attachmentIndexTemplates(t *testing.T) map[string]domain.PromptTemplate {
	t.Helper()
	content, err := templateFiles.ReadFile("templates/context-attachments-index.md")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]domain.PromptTemplate{
		"context.attachments-index": {ID: "context.attachments-index", Content: strings.TrimSpace(string(content))},
	}
}

func TestAttachmentResourcesKeepLargeAttachmentsMetadataOnly(t *testing.T) {
	t.Parallel()
	input := BuildInput{
		Task:      domain.Task{ID: "task-1"},
		Agent:     domain.TaskAgent{AgentID: "main"},
		Workspace: domain.Workspace{Transport: "native", RootPath: "/repo"},
	}
	bigHash := strings.Repeat("a", 64)
	budgetHash := strings.Repeat("c", 64)
	smallHash := strings.Repeat("b", 64)
	big := domain.Attachment{
		ID: "attachment-big", TaskID: "task-1", Name: "big.bin", MediaType: "application/octet-stream",
		Size: 8 << 20, SHA256: bigHash,
	}
	budget := domain.Attachment{
		ID: "attachment-budget", TaskID: "task-1", Name: "old.bin", MediaType: "application/octet-stream",
		Size: 5, SHA256: budgetHash,
	}
	small := domain.Attachment{
		ID: "attachment-small", TaskID: "task-1", Name: "note.txt", MediaType: "text/plain",
		Size: 5, SHA256: smallHash,
	}
	input.Attachments = []AttachmentResource{
		{Attachment: big, Inline: false},
		{Attachment: budget, Inline: false, BudgetExhausted: true},
		{Attachment: small, Content: "hello", Inline: true},
	}
	resources, err := attachmentResources(input, contextRootFor(input, "/repo"), attachmentIndexTemplates(t), templateData{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 4 {
		t.Fatalf("resources = %d, want index + two metadata files + inline detail", len(resources))
	}
	index := resources[0]
	if index.ID != "attachments-index" || !strings.Contains(index.Content, "big.bin") ||
		!strings.Contains(index.Content, "old.bin") || !strings.Contains(index.Content, "note.txt") {
		t.Fatalf("index does not list every attachment: %#v", index)
	}
	for _, expected := range []string{bigHash, "metadata only", "/api/v1/tasks/task-1/attachments/attachment-big", "attachment-big/metadata.md"} {
		if !strings.Contains(index.Content, expected) {
			t.Fatalf("index missing %q:\n%s", expected, index.Content)
		}
	}
	if !strings.Contains(index.Content, "per-Turn Context inline budget was exhausted by newer attachments") {
		t.Fatalf("index missing budget reason:\n%s", index.Content)
	}
	var metadata, budgetMetadata, inline *ContextResource
	for position := range resources {
		switch resources[position].ID {
		case "attachment-attachment-big-metadata":
			metadata = &resources[position]
		case "attachment-attachment-budget-metadata":
			budgetMetadata = &resources[position]
		case "attachment-attachment-small":
			inline = &resources[position]
		}
	}
	if metadata == nil || budgetMetadata == nil {
		t.Fatal("metadata-only resources were not created")
	}
	if !strings.HasSuffix(metadata.Path, "metadata.md") || !strings.HasSuffix(budgetMetadata.Path, "metadata.md") {
		t.Fatalf("metadata paths = %q, %q", metadata.Path, budgetMetadata.Path)
	}
	for _, expected := range []string{bigHash, "not inlined", "4 MiB", "/api/v1/tasks/task-1/attachments/attachment-big"} {
		if !strings.Contains(metadata.Content, expected) {
			t.Fatalf("metadata file missing %q:\n%s", expected, metadata.Content)
		}
	}
	if !strings.Contains(budgetMetadata.Content, "per-Turn Context inline budget was exhausted by newer attachments") {
		t.Fatalf("budget metadata missing budget reason:\n%s", budgetMetadata.Content)
	}
	if strings.Contains(budgetMetadata.Content, "4 MiB") {
		t.Fatalf("budget metadata reported the size reason:\n%s", budgetMetadata.Content)
	}
	if len(metadata.Content) > 1024 || len(budgetMetadata.Content) > 1024 {
		t.Fatalf("metadata files are unexpectedly large: %d, %d bytes", len(metadata.Content), len(budgetMetadata.Content))
	}
	if inline == nil || inline.Content != "hello" || inline.EntryPoint {
		t.Fatalf("inline attachment resource = %#v", inline)
	}
}

func TestAttachmentDetailResourceCountsBytesNotRunes(t *testing.T) {
	t.Parallel()
	input := BuildInput{Task: domain.Task{ID: "task-1"}, Agent: domain.TaskAgent{AgentID: "main"}}
	content := "汉"
	resource := attachmentDetailResource(input, "attachment-x", "/tmp/x.bin", "x", content)
	if resource.Chars != len(content) {
		t.Fatalf("Chars = %d, want byte length %d", resource.Chars, len(content))
	}
	if resource.EntryPoint {
		t.Fatal("attachment detail must not be an entry point")
	}
}
