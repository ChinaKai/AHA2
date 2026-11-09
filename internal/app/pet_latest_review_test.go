package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCompletionUsesLatestReview(t *testing.T) {
	for _, decision := range []string{"required", "skipped"} {
		for _, tc := range []struct {
			name, firstVerdict, latestVerdict string
			latestComplete, citeOld, wantOK   bool
		}{
			{name: "old_approve_new_request_changes", firstVerdict: "Verdict: APPROVE", latestVerdict: "Verdict: REQUEST_CHANGES", latestComplete: true, citeOld: true},
			{name: "old_approve_new_unfinished", firstVerdict: "Verdict: APPROVE", latestVerdict: "Verdict: APPROVE", citeOld: true},
			{name: "old_approve_new_missing_verdict", firstVerdict: "Verdict: APPROVE", latestVerdict: "评审尚无结论", latestComplete: true, citeOld: true},
			{name: "new_request_changes", firstVerdict: "Verdict: APPROVE", latestVerdict: "Verdict: REQUEST_CHANGES", latestComplete: true},
			{name: "new_unfinished", firstVerdict: "Verdict: APPROVE", latestVerdict: "Verdict: APPROVE"},
			{name: "new_missing_verdict", firstVerdict: "Verdict: APPROVE", latestVerdict: "评审尚无结论", latestComplete: true},
			{name: "new_approve_supersedes_request_changes", firstVerdict: "Verdict: REQUEST_CHANGES", latestVerdict: "Verdict: APPROVE", latestComplete: true, wantOK: true},
			{name: "old_approve_new_approve", firstVerdict: "Verdict: APPROVE", latestVerdict: "Verdict: APPROVE", latestComplete: true, citeOld: true, wantOK: decision == "skipped"},
		} {
			t.Run(decision+"/"+tc.name, func(t *testing.T) {
				fixture, project, workspace := petReviewFixture(t)
				model := addReviewModel(t, fixture)
				source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "实现", Request: "验证最新评审门禁", Start: boolPointer(true)})
				if err != nil {
					t.Fatal(err)
				}
				waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
				createReview := func(verdict string, complete bool) domain.Task {
					t.Helper()
					fixture.service.executor = &petReviewExecutor{reply: verdict}
					review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{TaskID: source.ID, ReviewModelID: model.ID})
					if err != nil {
						t.Fatal(err)
					}
					waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
					// Owner may close a review without a verdict. Source completion must
					// still reject that latest review, not fall back to an older APPROVE.
					if complete {
						if err := fixture.service.CompleteTask(fixture.ctx, review.ID); err != nil {
							t.Fatal(err)
						}
					}
					return review.Task
				}
				first := createReview(tc.firstVerdict, true)
				latest := createReview(tc.latestVerdict, tc.latestComplete)
				if !latest.CreatedAt.After(first.CreatedAt) {
					t.Fatalf("reviews not chronologically distinct: %v %v", first.CreatedAt, latest.CreatedAt)
				}
				reviewID := ""
				if decision == "required" {
					reviewID = latest.ID
					if tc.citeOld {
						reviewID = first.ID
					}
				}
				_, err = fixture.service.PetCompleteTaskWithDecision(fixture.ctx, source.ID, decision, "以最新一次关联评审结论为准", reviewID)
				if (err == nil) != tc.wantOK {
					t.Fatalf("completion err=%v wantOK=%v", err, tc.wantOK)
				}
				if decision == "required" && tc.citeOld && (err == nil || !strings.Contains(err.Error(), "最新")) {
					t.Fatalf("old review not rejected explicitly: %v", err)
				}
				stored, err := fixture.database.Task(fixture.ctx, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.wantOK {
					if stored.Status != domain.TaskWaitingUser || stored.TaskReviewDecision != (domain.TaskReviewDecision{}) {
						t.Fatalf("rejection changed source: %#v", stored)
					}
				} else if stored.Status != domain.TaskCompleted || stored.ReviewDecision != decision || stored.ReviewTaskID != reviewID || stored.ReviewDecidedBy != "pet" || stored.ReviewDecidedAt.IsZero() {
					t.Fatalf("completion lost audit: %#v", stored)
				}
			})
		}
	}
}
