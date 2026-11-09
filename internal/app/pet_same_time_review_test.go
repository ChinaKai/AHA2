package app

import (
	"slices"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCompletionUsesLaterInsertedReviewAtSameTime(t *testing.T) {
	for _, decision := range []string{"required", "skipped"} {
		for _, approve := range []bool{false, true} {
			verdict := "Verdict: REQUEST_CHANGES"
			if approve {
				verdict = "Verdict: APPROVE"
			}
			t.Run(decision+"/"+verdict, func(t *testing.T) {
				fixture, project, workspace := petReviewFixture(t)
				model := addReviewModel(t, fixture)
				at := fixture.createdAt.Truncate(time.Second)
				fixture.service.now = func() time.Time { return at }
				source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "同时间评审", Request: "验证插入顺序", Start: boolPointer(true)})
				if err != nil {
					t.Fatal(err)
				}
				waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
				createReview := func(id, result string) domain.Task {
					t.Helper()
					fixture.service.executor = &petReviewExecutor{reply: result}
					review, err := fixture.service.CreateTask(fixture.ctx, CreateTaskInput{
						ID: id, ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "评审", Request: "给出最终结论",
						ModelID: model.ID, ReasoningEffort: "high", Filesystem: "read-only", Approval: "never",
						CollaborationMode: "single", MaxAgents: 1, Isolation: "inplace", StartMode: "manual",
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := fixture.database.CreatePetCodeReviewLink(fixture.ctx, review.ID, source.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := fixture.service.StartTask(fixture.ctx, review.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := fixture.service.SubmitPetDelegatedMessage(fixture.ctx, review.ID, "给出最终结论"); err != nil {
						t.Fatal(err)
					}
					waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
					if _, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, review.ID, "skipped", "独立评审无需递归评审", ""); err != nil {
						t.Fatal(err)
					}
					return review
				}
				// The later review has a lexically smaller ID. Neither random ID nor
				// equal timestamps may make the earlier APPROVE authoritative.
				a := createReview("z-review-earlier", "Verdict: APPROVE")
				b := createReview("a-review-later", verdict)
				if !a.CreatedAt.Equal(b.CreatedAt) || !a.CreatedAt.Equal(at) {
					t.Fatalf("timestamps differ: %v %v", a.CreatedAt, b.CreatedAt)
				}
				ids, err := fixture.database.PetCodeReviewsForSource(fixture.ctx, source.ID)
				if err != nil || !slices.Equal(ids, []string{a.ID, b.ID}) {
					t.Fatalf("latest review is not B: %v %v", ids, err)
				}
				assertRejected := func(reviewID string) {
					t.Helper()
					if _, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, source.ID, decision, "以最新评审为准", reviewID); err == nil {
						t.Fatal("unsafe completion accepted")
					}
					stored, err := fixture.database.Task(fixture.ctx, source.ID)
					if err != nil || stored.Status != domain.TaskWaitingUser || stored.TaskReviewDecision != (domain.TaskReviewDecision{}) {
						t.Fatalf("rejection changed task: %#v %v", stored, err)
					}
				}
				if decision == "required" {
					assertRejected(a.ID)
				}
				reviewID := ""
				if decision == "required" {
					reviewID = b.ID
				}
				if !approve {
					assertRejected(reviewID)
					return
				}
				completed, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, source.ID, decision, "最新评审已经通过", reviewID)
				if err != nil || completed.Status != domain.TaskCompleted || completed.ReviewDecision != decision || completed.ReviewTaskID != reviewID {
					t.Fatalf("latest APPROVE rejected: %#v %v", completed, err)
				}
			})
		}
	}
}
