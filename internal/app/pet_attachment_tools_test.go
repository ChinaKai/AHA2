package app

import (
	"encoding/base64"
	"testing"
)

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
