package app

import (
	"github.com/ChinaKai/AHA2/internal/domain"
	"testing"
)

func TestPetHardwareNewResourceAvoidsExistingBindingAlias(t *testing.T) {
	f := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, f, true)
	task := f.petTask(t)
	for _, id := range []string{"resource-a", "resource-b"} {
		if err := f.database.UpsertHardwareResource(f.ctx, domain.HardwareResource{ID: id, Description: id, Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "FIXTURE", Baudrate: 9600}, Access: domain.HardwareAccessReadWrite, CreatedAt: f.createdAt, UpdatedAt: f.createdAt}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.database.ReplaceTaskHardwareBindings(f.ctx, task.ID, []domain.TaskHardwareBinding{{TaskID: task.ID, ID: "resource-b", ResourceID: "resource-a"}}); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: []string{"resource-a", "resource-b"}})
	if err != nil || len(view.Bindings) != 2 {
		t.Fatalf("valid resources rejected: %v", err)
	}
	if view.Bindings[0].ID != "resource-b" || view.Bindings[0].ResourceID != "resource-a" || view.Bindings[1].ID == "resource-b" || view.Bindings[1].ResourceID != "resource-b" {
		t.Fatal("existing alias was replaced or collided with new resource")
	}
}
