package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestDeleteProjectApprovalSucceedsWhenOnlyCleanupFails(t *testing.T) {
	service, database, manager, _ := newSpaceTestService(t)
	ctx := context.Background()
	enablePetProjectAdmin(t, service)
	now := time.Now().UTC()
	project, err := service.CreateProject(ctx, domain.Project{
		ID: "p529approve", Name: "Approve Cleanup", ProjectType: "folder",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := service.PetDeleteProject(ctx, project.ID, "验证清理失败时审批仍完成")
	if err != nil {
		t.Fatal(err)
	}
	service.knowledgeVault = archiveFailureVault{
		knowledgeVaultBackend: manager,
		err:                   errors.New("simulated cleanup failure"),
	}
	if _, err := service.ResolvePetApproval(ctx, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	final := waitPetApprovalStatus(t, database, approval.ID)
	if final.Status != petApprovalApproved {
		t.Fatalf("cleanup-only failure left the approval %q", final.Status)
	}
	if !strings.Contains(final.Resolution, "归档失败") || !strings.Contains(final.Resolution, "simulated cleanup failure") {
		t.Fatalf("approval resolution does not carry the cleanup warning: %q", final.Resolution)
	}
	if _, err := database.Project(ctx, project.ID); err == nil {
		t.Fatal("project row still exists after the approved delete")
	}
}

func waitPetApprovalStatus(t *testing.T, database *store.Store, id string) domain.PetApproval {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		item, err := database.PetApproval(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Status != petApprovalExecuting {
			return item
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("approval %s did not finish executing", id)
	return domain.PetApproval{}
}
