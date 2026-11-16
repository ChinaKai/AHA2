package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetAttachmentLimitIs200MiB(t *testing.T) {
	if petAttachmentLimit != 200<<20 {
		t.Fatalf("petAttachmentLimit = %d, want %d", petAttachmentLimit, 200<<20)
	}
	const mib = 1 << 20
	for _, testCase := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "empty", size: 0, wantErr: true},
		{name: "one byte", size: 1},
		{name: "exactly 200MiB", size: 200 * mib},
		{name: "200MiB plus one", size: 200*mib + 1, wantErr: true},
	} {
		err := validatePetAttachmentSize(testCase.size)
		if (err != nil) != testCase.wantErr {
			t.Fatalf("%s: validatePetAttachmentSize(%d) error = %v, wantErr %v", testCase.name, testCase.size, err, testCase.wantErr)
		}
	}
	if err := validatePetAttachmentSize(200*mib + 1); err == nil || err.Error() != "附件大小必须在 1 字节到 200MiB 之间" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPetTaskAttachmentUploadListAndMessage(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "附件项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "附件 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "附件任务",
		Request: "等待附件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	content := base64.StdEncoding.EncodeToString([]byte("hello attachment"))
	first, err := fixture.service.PetUploadTaskAttachment(fixture.ctx, PetUploadAttachmentInput{
		TaskID: task.ID, Name: "hello.txt", MediaType: "text/plain", ContentBase64: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.PetUploadTaskAttachment(fixture.ctx, PetUploadAttachmentInput{
		TaskID: task.ID, Name: "delete.txt", ContentBase64: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetDeleteTaskAttachment(fixture.ctx, task.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	items, err := fixture.service.PetTaskAttachments(fixture.ctx, task.ID)
	if err != nil || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("attachments=%#v err=%v", items, err)
	}
	turn, err := fixture.service.PetSendTaskMessage(fixture.ctx, PetSendTaskMessageInput{
		TaskID: task.ID, Content: "带附件", AttachmentIDs: []string{first.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID == "" {
		t.Fatal("no turn for attachment message")
	}
}

func setupPetAttachmentTask(t *testing.T, fixture *petTaskFixture, title string) domain.Task {
	t.Helper()
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "附件项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "附件 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: title,
		Request: "等待附件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.StartTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPetCopyTaskAttachmentAllowsResendAfterBinding(t *testing.T) {
	fixture := newPetTaskFixture(t)
	task := setupPetAttachmentTask(t, fixture, "附件任务")
	uploaded, err := fixture.service.PetUploadTaskAttachment(fixture.ctx, PetUploadAttachmentInput{
		TaskID: task.ID, Name: "report.txt", MediaType: "text/plain",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("hello attachment")),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstTurn, err := fixture.service.PetSendTaskMessage(fixture.ctx, PetSendTaskMessageInput{
		TaskID: task.ID, Content: "带附件", AttachmentIDs: []string{uploaded.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := fixture.database.Attachment(fixture.ctx, task.ID, uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sent.MessageID != firstTurn.InputMessageID {
		t.Fatalf("source was not bound to the first message: %#v", sent)
	}

	payload := fmt.Sprintf(`{"task_id":%q,"attachment_id":%q}`, task.ID, uploaded.ID)
	output, err := fixture.service.PetMCPTool(fixture.ctx, "aha_copy_task_attachment", json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	var copied PetAttachmentCopy
	if err := json.Unmarshal([]byte(output), &copied); err != nil {
		t.Fatalf("copy result %q: %v", output, err)
	}
	if copied.ID == "" || copied.ID == uploaded.ID {
		t.Fatalf("copy id = %q, uploaded id = %q", copied.ID, uploaded.ID)
	}
	if copied.SHA256 == "" || copied.SHA256 != sent.SHA256 ||
		copied.Name != sent.Name || copied.MediaType != sent.MediaType || copied.Size != sent.Size {
		t.Fatalf("copy metadata mismatch: uploaded=%#v copied=%#v", uploaded, copied)
	}
	cloned, err := fixture.database.Attachment(fixture.ctx, task.ID, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.MessageID != "" {
		t.Fatalf("clone is already bound: %#v", cloned)
	}
	stillSent, err := fixture.database.Attachment(fixture.ctx, task.ID, uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillSent.MessageID != firstTurn.InputMessageID {
		t.Fatalf("clone changed the source binding: %#v", stillSent)
	}

	// Let the first Round settle so the resend opens a new Turn instead of
	// being queued into the Round that is finishing.
	waitForRoundSettled(t, fixture.database, firstTurn.RoundID)
	secondTurn, err := fixture.service.PetSendTaskMessage(fixture.ctx, PetSendTaskMessageInput{
		TaskID: task.ID, Content: "重投附件", AttachmentIDs: []string{copied.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := fixture.database.Attachment(fixture.ctx, task.ID, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.MessageID != secondTurn.InputMessageID {
		t.Fatalf("clone was not bound to the resent message: %#v", rebound)
	}
	stillSent, err = fixture.database.Attachment(fixture.ctx, task.ID, uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillSent.MessageID != firstTurn.InputMessageID {
		t.Fatalf("source binding changed after the resend: %#v", stillSent)
	}
}

func TestPetCopyTaskAttachmentRejectsCrossTask(t *testing.T) {
	fixture := newPetTaskFixture(t)
	sourceTask := setupPetAttachmentTask(t, fixture, "源任务")
	otherTask, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: sourceTask.ProjectID, WorkspaceID: sourceTask.WorkspaceID,
		Title: "别的任务", Request: "等待附件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := fixture.service.PetUploadTaskAttachment(fixture.ctx, PetUploadAttachmentInput{
		TaskID: sourceTask.ID, Name: "report.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("owner bytes")),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.PetCopyTaskAttachment(fixture.ctx, PetCopyAttachmentInput{
		TaskID: otherTask.ID, AttachmentID: uploaded.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "附件不存在或不属于该 Task") {
		t.Fatalf("cross-task copy error = %v", err)
	}
	payload := fmt.Sprintf(`{"task_id":%q,"attachment_id":%q}`, otherTask.ID, uploaded.ID)
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_copy_task_attachment", json.RawMessage(payload)); err == nil {
		t.Fatal("MCP tool allowed a cross-task copy")
	}
	items, err := fixture.service.PetTaskAttachments(fixture.ctx, otherTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("cross-task copy wrote attachments: %#v", items)
	}
	sourceItems, err := fixture.service.PetTaskAttachments(fixture.ctx, sourceTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceItems) != 1 || sourceItems[0].ID != uploaded.ID {
		t.Fatalf("cross-task copy changed the source task: %#v", sourceItems)
	}
}
