package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetMCPCompleteTaskReviewDecision(t *testing.T) {
	fixture, project, workspace := petReviewFixture(t)
	found := false
	for _, tool := range fixture.service.PetMCPTools(fixture.ctx) {
		if tool.Name != "aha_complete_task" {
			continue
		}
		found = true
		required, _ := tool.Parameters["required"].([]string)
		if !slices.Contains(required, "review_decision") {
			t.Fatalf("decision is not required: %#v", tool.Parameters)
		}
		properties := tool.Parameters["properties"].(map[string]any)
		for _, name := range []string{"review_decision", "review_reason", "review_task_id"} {
			if properties[name] == nil {
				t.Fatalf("missing property %s", name)
			}
		}
	}
	if !found {
		t.Fatal("aha_complete_task not found")
	}
	for _, filesystem := range []string{"read-only", "workspace-write", "danger-full-access"} {
		t.Run(filesystem, func(t *testing.T) {
			task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
				ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "自主评审决定", Request: "修改后由宠物判断是否评审",
				Filesystem: filesystem, Start: boolPointer(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForTask(t, fixture.database, task.ID, domain.TaskWaitingUser)
			for _, tc := range []struct {
				decision, reason, reviewID, want string
			}{
				{want: "review_decision"},
				{reviewID: "legacy-review", want: "review_decision"},
				{decision: "auto", want: "review_decision"},
				{decision: "skipped", want: "review_reason"},
				{decision: "skipped", reason: " \t\n", want: "review_reason"},
				{decision: "required", want: "review_task_id"},
				{decision: "skipped", reason: "已有充分定向验证证据", reviewID: "misleading", want: "review_task_id"},
			} {
				input := map[string]string{"task_id": task.ID, "review_reason": tc.reason, "review_task_id": tc.reviewID}
				if tc.decision != "" {
					input["review_decision"] = tc.decision
				}
				args, _ := json.Marshal(input)
				if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_complete_task", args); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("%#v accepted or wrong error: %v", tc, err)
				}
				unchanged, err := fixture.database.Task(fixture.ctx, task.ID)
				if err != nil || unchanged.Status != domain.TaskWaitingUser || unchanged.ReviewDecision != "" {
					t.Fatalf("rejected declaration persisted: %#v err=%v", unchanged, err)
				}
			}
			args, _ := json.Marshal(map[string]string{"task_id": task.ID, "review_decision": "skipped", "review_reason": "  已有充分定向验证证据  "})
			raw, err := fixture.service.PetMCPTool(fixture.ctx, "aha_complete_task", args)
			if err != nil {
				t.Fatal(err)
			}
			var completed domain.Task
			if err := json.Unmarshal([]byte(raw), &completed); err != nil {
				t.Fatal(err)
			}
			assertDecision := func(task domain.Task) {
				t.Helper()
				if task.Status != domain.TaskCompleted || task.ReviewDecision != "skipped" || task.ReviewReason != "已有充分定向验证证据" || task.ReviewTaskID != "" || task.ReviewDecidedBy != "pet" || task.ReviewDecidedAt.IsZero() {
					t.Fatalf("missing persisted declaration: %#v", task)
				}
			}
			assertDecision(completed)
			stored, err := fixture.database.Task(fixture.ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertDecision(stored)
			detail, err := fixture.service.PetTaskDetail(fixture.ctx, task.ID)
			if err != nil || detail.TaskReviewDecision != stored.TaskReviewDecision {
				t.Fatalf("Task query lost decision: %#v err=%v", detail, err)
			}
			// A retry must not rewrite the original decision or timestamp.
			if _, err := fixture.service.PetCompleteTaskWithDecision(fixture.ctx, task.ID, "skipped", "不同重试理由不覆盖原决定", ""); err != nil {
				t.Fatal(err)
			}
			retried, err := fixture.database.Task(fixture.ctx, task.ID)
			if err != nil || retried.TaskReviewDecision != stored.TaskReviewDecision {
				t.Fatalf("retry rewrote audit: %#v err=%v", retried, err)
			}
		})
	}
}
