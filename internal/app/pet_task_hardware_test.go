package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetTaskHardwareBindings(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageSystem: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	resource := domain.HardwareResource{
		ID: "hardware-test", Description: "测试串口", Mode: "serial",
		Serial: domain.HardwareSerialConfig{Device: "/dev/ttyUSB0", Baudrate: 115200},
		Access: "read-write", CreatedAt: now, UpdatedAt: now,
	}
	if err := fixture.database.UpsertHardwareResource(fixture.ctx, resource); err != nil {
		t.Fatal(err)
	}
	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "硬件项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "硬件 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "硬件任务",
		Request: "等待硬件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetUpdateTaskHardware(fixture.ctx, PetUpdateTaskHardwareInput{
		TaskID: task.ID, ResourceIDs: []string{resource.ID},
	}); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.PetTaskHardware(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Bindings) != 1 || view.Bindings[0].ResourceID != resource.ID {
		t.Fatalf("hardware view=%#v", view)
	}
	if _, err := fixture.service.PetRequestDeleteHardwareResource(fixture.ctx, resource.ID); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("delete hardware without reason was accepted: %v", err)
	}
	approval, err := fixture.service.PetRequestDeleteHardwareResource(fixture.ctx, resource.ID, "清理测试硬件资源")
	if err != nil || approval.Kind != "delete_hardware_resource" {
		t.Fatalf("delete hardware approval=%#v err=%v", approval, err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.HardwareResource(fixture.ctx, resource.ID); err == nil {
		t.Fatal("approved hardware deletion did not delete the resource")
	}
}
