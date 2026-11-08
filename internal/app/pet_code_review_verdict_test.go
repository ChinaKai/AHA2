package app

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCodeReviewVerdict(t *testing.T) {
	for _, tc := range []struct {
		result string
		want   string
	}{
		{result: ""},
		{result: " \r\n\t"},
		{result: "\r\n \t\r\nVerdict: APPROVE\r\n证据", want: petCodeReviewApprove},
		{result: "verdict: request_changes\n必须修复", want: petCodeReviewRequestChanges},
		{result: "示例\nVerdict: APPROVE"},
		{result: "Verdict: APPROVED"},
		{result: "Verdict: REQUEST_CHANGESxyz"},
	} {
		if got := petCodeReviewVerdict(tc.result); got != tc.want {
			t.Errorf("petCodeReviewVerdict(%q)=%q want %q", tc.result, got, tc.want)
		}
	}
}

func TestPetReviewCompletionRequiresFinalVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result string
		valid  bool
	}{
		{name: "approve", result: "Verdict: APPROVE\n检查通过", valid: true},
		{name: "request changes", result: "Verdict: REQUEST_CHANGES\n必须修复", valid: true},
		{name: "blank lines and CRLF", result: "\r\n \t\r\nVerdict: APPROVE\r\n证据", valid: true},
		{name: "missing", result: "review without verdict"},
		{name: "body reference", result: "示例：\nVerdict: APPROVE"},
		{name: "quote", result: "> Verdict: APPROVE"},
		{name: "inline reference", result: "参考 Verdict: APPROVE"},
		{name: "approved prefix", result: "Verdict: APPROVED"},
		{name: "approve suffix", result: "Verdict: APPROVE pending"},
		{name: "request changes suffix", result: "Verdict: REQUEST_CHANGESxyz"},
		{name: "request changes punctuation", result: "Verdict: REQUEST_CHANGES."},
		{name: "unknown verdict", result: "Verdict: REJECT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, project, workspace := petReviewFixture(t)
			reviewModel := addReviewModel(t, fixture)
			fixture.service.executor = &petReviewExecutor{reply: tc.result}
			source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "实现", Request: "修改代码", Start: boolPointer(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
			review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
				TaskID: source.ID, ReviewModelID: reviewModel.ID, Filesystem: "danger-full-access",
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
			turn, err := fixture.database.LatestTurnForAgent(fixture.ctx, review.ID, "main")
			if err != nil || turn.Status != domain.TurnSucceeded || turn.Result != strings.TrimSpace(tc.result) {
				t.Fatalf("unexpected executed review: %#v err=%v", turn, err)
			}
			completed, err := fixture.service.PetCompleteTask(fixture.ctx, review.ID)
			if !tc.valid {
				if err == nil || !strings.Contains(err.Error(), "首个非空行") {
					t.Fatalf("invalid verdict completed review: %v", err)
				}
				task, err := fixture.database.Task(fixture.ctx, review.ID)
				if err != nil || task.Status != domain.TaskWaitingUser {
					t.Fatalf("rejected review state changed: %#v err=%v", task, err)
				}
				return
			}
			if err != nil || completed.Status != domain.TaskCompleted {
				t.Fatalf("valid verdict rejected: %#v err=%v", completed, err)
			}
			completed, err = fixture.service.PetCompleteTaskWithReview(fixture.ctx, source.ID, review.ID)
			if tc.name == "request changes" {
				if err == nil || !strings.Contains(err.Error(), "要求修改") {
					t.Fatalf("REQUEST_CHANGES approved source: %v", err)
				}
			} else if err != nil || completed.Status != domain.TaskCompleted {
				t.Fatalf("APPROVE did not complete source: %#v err=%v", completed, err)
			}
		})
	}
}

func TestPetReviewCompletionDoesNotReuseOlderVerdict(t *testing.T) {
	for _, verdict := range []string{"Verdict: APPROVE", "Verdict: REQUEST_CHANGES"} {
		t.Run(verdict, func(t *testing.T) {
			fixture, project, workspace := petReviewFixture(t)
			reviewModel := addReviewModel(t, fixture)
			executor := &petReviewExecutor{reply: verdict}
			fixture.service.executor = executor
			source, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "实现", Request: "修改代码", Start: boolPointer(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, source.ID, domain.TaskWaitingUser)
			review, err := fixture.service.PetRequestCodeReview(fixture.ctx, PetRequestCodeReviewInput{
				TaskID: source.ID, ReviewModelID: reviewModel.ID, Filesystem: "danger-full-access",
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
			first, err := fixture.database.LatestTurnForAgent(fixture.ctx, review.ID, "main")
			if err != nil || first.Status != domain.TurnSucceeded || first.Result != verdict {
				t.Fatalf("first verdict not executed: %#v err=%v", first, err)
			}
			if _, err := fixture.service.PetCompleteTask(fixture.ctx, review.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.PetReopenTask(fixture.ctx, review.ID); err != nil {
				t.Fatal(err)
			}
			executor.reply = "补充结果，没有最终结论"
			if _, err := fixture.service.SubmitPetDelegatedMessage(fixture.ctx, review.ID, "继续评审"); err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, review.ID, domain.TaskWaitingUser)
			latest, err := fixture.database.LatestTurnForAgent(fixture.ctx, review.ID, "main")
			if err != nil || latest.ID == first.ID || latest.Sequence <= first.Sequence || latest.Status != domain.TurnSucceeded || latest.Result != executor.reply {
				t.Fatalf("new successful turn missing: %#v err=%v", latest, err)
			}
			if _, err := fixture.service.PetCompleteTask(fixture.ctx, review.ID); err == nil || !strings.Contains(err.Error(), "首个非空行") {
				t.Fatalf("older verdict reused: %v", err)
			}
		})
	}
}
