package app

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetDomainRuntimeUsesCurrentBindingsPermissionsAndMode(t *testing.T) {
	f := newPetTaskFixture(t)
	task := f.createPetDraft(t, "pet domain fixture")
	assistant, err := f.database.EnsureOwnerAssistant(f.ctx, f.createdAt)
	if err != nil {
		t.Fatal(err)
	}
	set := func(view, manage bool) {
		t.Helper()
		if err := f.database.UpdateOwnerPetPermissions(f.ctx, assistant.ID, view, manage, false, false, false, true, false, false, f.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	set(true, true)
	f.service.SetDesktopMCPAvailability(func(string) bool { return true })
	seedHardwareMCPGroup(t, f.ctx, f.database, task)
	seedBrowserMCPGrant(t, f.ctx, f.database, task, "pet-grant", "active")
	servers, allowed := f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
	for _, name := range []string{coreMCPName, knowledgeMCPName, workspaceMCPName, browserMCPName, hardwareMCPName, desktopMCPName} {
		if !hasMCPServer(servers, name) {
			t.Fatalf("missing %s", name)
		}
	}
	if hasMCPServer(servers, collabMCPName) {
		t.Fatal("single pet received collaboration tools")
	}
	if len(mcpDomainTools(allowed, hardwareMCPName)) != 7 || len(mcpDomainTools(allowed, coreMCPName)) != 7 || len(mcpDomainTools(allowed, knowledgeMCPName)) != 2 {
		t.Fatalf("unexpected tools: %v", allowed)
	}
	for _, name := range mcpDomainTools(allowed, coreMCPName) {
		if name == "mcp__aha2_core__task_message" {
			t.Fatal("peer tool widened pet management")
		}
	}
	set(true, false)
	_, allowed = f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
	if len(mcpDomainTools(allowed, hardwareMCPName)) != 2 {
		t.Fatalf("revoked hardware write still injected: %v", allowed)
	}
	set(false, false)
	servers, _ = f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", "")
	if hasMCPServer(servers, hardwareMCPName) || hasMCPServer(servers, browserMCPName) || hasMCPServer(servers, workspaceMCPName) {
		t.Fatal("revoked view permission still injected")
	}
	set(true, true)
	lookalike := task
	lookalike.ID = "task-pet-project-impostor"
	if f.service.hardwareMCPInjected(f.ctx, lookalike, "") || f.service.workspaceMCPInjected(f.ctx, lookalike, "") {
		t.Fatal("another Task inherited pet management")
	}
}

func TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation(t *testing.T) {
	f := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, f, true)
	task := f.petTask(t)
	resources := make([]string, 17)
	for index := range resources {
		id := fmt.Sprintf("resource-%02d", index)
		resources[index] = id
		if err := f.database.UpsertHardwareResource(f.ctx, domain.HardwareResource{ID: id, Description: id, Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "FIXTURE", Baudrate: 9600}, Access: domain.HardwareAccessReadWrite, CreatedAt: f.createdAt, UpdatedAt: f.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: resources[:16]})
	if err != nil || len(view.Bindings) != 16 {
		t.Fatalf("16 bindings: %d err=%v", len(view.Bindings), err)
	}
	before, err := f.database.TaskHardwareBindings(f.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingHardwareDisconnector{}
	f.service.SetPetHardwareDisconnector(recorder)
	for _, ids := range [][]string{resources, {resources[0], resources[0]}, {"missing"}, {""}} {
		if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: ids}); err == nil {
			t.Fatalf("accepted invalid bindings %v", ids)
		}
		after, _ := f.database.TaskHardwareBindings(f.ctx, task.ID)
		if !reflect.DeepEqual(before, after) || len(recorder.calls) != 0 {
			t.Fatal("invalid replacement changed rows or disconnected devices")
		}
	}
	if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: resources[:16]}); err != nil {
		t.Fatal(err)
	}
	after, _ := f.database.TaskHardwareBindings(f.ctx, task.ID)
	for index := range before {
		if before[index].ID != after[index].ID || !before[index].CreatedAt.Equal(after[index].CreatedAt) {
			t.Fatal("unchanged resource lost binding identity")
		}
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("unchanged connections were closed: %v", recorder.calls)
	}
	if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: []string{resources[16]}}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 16 {
		t.Fatalf("removed connections not closed: %v", recorder.calls)
	}
	if _, err := f.database.HardwareGroup(f.ctx, task.ID, resources[0]); err == nil {
		t.Fatal("removed resource still resolves")
	}
}
