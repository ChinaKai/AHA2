package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetSkippedCannotBypassExistingReview(t *testing.T) {
	for _, tc := range []struct {
		name, verdict                   string
		complete, approvedFirst, wantOK bool
	}{
		{name: "unfinished", verdict: "Verdict: APPROVE"},
		{name: "request_changes", verdict: "Verdict: REQUEST_CHANGES", complete: true},
		{name: "missing_verdict", verdict: "Review still in progress", complete: true},
		{name: "approved_and_rejected", verdict: "Verdict: REQUEST_CHANGES", complete: true, approvedFirst: true},
		{name: "approved", verdict: "Verdict: APPROVE", complete: true, wantOK: true},
		{name: "no_reviews", wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, project, workspace := petReviewFixture(t)
			model := addReviewModel(t, fixture)
			source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "实现", Request: "修改代码", Start: boolPointer(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
			createReview := func(verdict string, complete bool) {
				t.Helper()
				fixture.service.executor = &petReviewExecutor{reply: verdict}
				review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{TaskID: source.ID, ReviewModelID: model.ID})
				if err != nil {
					t.Fatal(err)
				}
				waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
				if complete {
					// Owner completion can close a review without a verdict; it must not
					// let the pet bypass the source's existing review afterwards.
					if err := fixture.service.CompleteTask(fixture.ctx, review.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.approvedFirst {
				createReview("Verdict: APPROVE", true)
			}
			if tc.verdict != "" {
				createReview(tc.verdict, tc.complete)
			}
			_, err = fixture.service.PetCompleteTaskWithDecision(fixture.ctx, source.ID, "skipped", "不能绕过已有评审的结论", "")
			if (err == nil) != tc.wantOK {
				t.Fatalf("completion err=%v wantOK=%v", err, tc.wantOK)
			}
			stored, readErr := fixture.database.Task(fixture.ctx, source.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !tc.wantOK && (stored.Status != domain.TaskWaitingUser || stored.TaskReviewDecision != (domain.TaskReviewDecision{})) {
				t.Fatalf("rejection changed source: %#v", stored)
			}
			if tc.wantOK && (stored.Status != domain.TaskCompleted || stored.ReviewDecision != "skipped") {
				t.Fatalf("completion not persisted: %#v", stored)
			}
		})
	}
}

func TestPetReviewCompletionRejectsFabricatedReviewID(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	model := addReviewModel(t, fixture)
	fixture.service.executor = &petReviewExecutor{reply: "Verdict: APPROVE"}
	source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "实现", Request: "修改代码", Start: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
	review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{TaskID: source.ID, ReviewModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
	for _, tc := range []struct{ decision, id string }{{"required", "fabricated"}, {"required", ""}, {"skipped", "fabricated"}} {
		if _, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, review.ID, tc.decision, "评审本身无需递归评审", tc.id); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
		stored, err := fixture.database.Task(fixture.ctx, review.ID)
		if err != nil || stored.Status != domain.TaskWaitingUser || stored.TaskReviewDecision != (domain.TaskReviewDecision{}) {
			t.Fatalf("rejected audit persisted: %#v %v", stored, err)
		}
	}
	completed, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, review.ID, "skipped", "评审本身无需递归评审", "")
	if err != nil || completed.ReviewTaskID != "" || completed.Status != domain.TaskCompleted {
		t.Fatalf("safe review completion: %#v %v", completed, err)
	}
}

func TestPetReviewDecisionClearedBeforeOwnerCompletion(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "重开审计", Request: "验证生命周期", Start: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
	completed, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, task.ID, "skipped", "已有充分定向验证证据", "")
	if err != nil || completed.ReviewDecidedBy != "pet" || completed.ReviewDecidedAt.IsZero() {
		t.Fatalf("initial audit: %#v %v", completed, err)
	}
	reopened, err := fixture.service.PetReopenTask(fixture.ctx, task.ID)
	if err != nil || reopened.Status != domain.TaskWaitingUser || reopened.TaskReviewDecision != (domain.TaskReviewDecision{}) {
		t.Fatalf("reopen retained audit: %#v %v", reopened, err)
	}
	if err := fixture.service.CompleteTask(fixture.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.database.Task(fixture.ctx, task.ID)
	if err != nil || stored.Status != domain.TaskCompleted || stored.TaskReviewDecision != (domain.TaskReviewDecision{}) {
		t.Fatalf("owner inherited audit: %#v %v", stored, err)
	}
}
