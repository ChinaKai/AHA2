package prompt

import (
	"strings"
	"testing"
)

func TestPetTaskIdentityOwnsWorkspacePaths(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"templates/identity-pet-task.md", []string{"electronic avatar", "project orchestrator", "Do not change project code", "pet-lab", "Unclosed"}},
		{"templates/zh-CN/identity-pet-task.md", []string{"电子分身", "项目调度者", "不直接修改项目源码", "pet-lab", "未闭环"}},
	}
	common := []string{
		"core/identity.md", "core/principles.md", "core/loops.md", "core/agenda.md", "pet-lab/",
		"pet_update_core", "pet_write_diary", "pet_write_worklog",
		"aha_pet_work_state", "inspection", "open_tasks", "owner_decision",
		"aha_capabilities", "pet_say", "pet_submit_idea",
		"aha_request_permission_change", "aha_pet_permissions", "pet_open_task",
	}
	for _, test := range cases {
		data, err := templateFiles.ReadFile(test.path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		for _, want := range append(test.want, common...) {
			if !strings.Contains(body, want) {
				t.Fatalf("%s is missing %q:\n%s", test.path, want, body)
			}
		}
		for _, unwanted := range []string{
			"aha_create_provider", "aha_update_task_agent", "aha_list_prompt_templates",
			"aha_send_channel_message", "aha_reset_channel_binding", "aha_create_task",
		} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("%s still carries the command dump %q:\n%s", test.path, unwanted, body)
			}
		}
		if len([]rune(body)) > 1600 {
			t.Fatalf("%s grew back to %d runes; keep command lists in aha_capabilities:\n%s", test.path, len([]rune(body)), body)
		}
	}
}

func TestAvailableContextDoesNotDuplicatePetPaths(t *testing.T) {
	data, err := templateFiles.ReadFile("templates/section-available-context.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, unwanted := range []string{"宠物核心设定", "宠物日记", "PetCorePaths", "PetDiaryPaths"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("available context still carries duplicate pet section %q:\n%s", unwanted, body)
		}
	}
}
