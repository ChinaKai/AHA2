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
		"pet_update_core", "pet_write_worklog",
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
			"diary", "日记", "aha_create_provider", "aha_update_task_agent", "aha_list_prompt_templates",
			"aha_send_channel_message", "aha_reset_channel_binding", "aha_create_task",
		} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("%s still carries the command dump %q:\n%s", test.path, unwanted, body)
			}
		}
		// Measure the logical text: a CRLF checkout adds one \r per line that
		// never reaches the prompt as content.
		if runes := len([]rune(strings.ReplaceAll(body, "\r\n", "\n"))); runes > 1600 {
			t.Fatalf("%s grew back to %d runes; keep command lists in aha_capabilities:\n%s", test.path, runes, body)
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

func TestPetIdentityMigratesPreviousBuiltinWithoutOverwritingCustom(t *testing.T) {
	cases := []struct {
		path         string
		replacements [][2]string
	}{
		{path: "templates/identity-pet-task.md", replacements: [][2]string{
			{"- `pet_update_core`: core/identity.md, core/principles.md, core/loops.md, core/agenda.md; `pet_write_worklog`: worklog/YYYY-MM-DD.md. Approvals/ideas stay in the approval center; agenda/loops cite IDs; worklog = evidence. pet-lab/ writable; other paths read-only.", "- `pet_update_core`: core/identity.md, core/principles.md, core/loops.md, core/agenda.md; `pet_write_diary`: diary/YYYY-MM-DD.md; `pet_write_worklog`: worklog/YYYY-MM-DD.md. Approvals/ideas stay in the approval center; agenda/loops cite IDs; worklog = evidence, diary = personal. pet-lab/ writable; other paths read-only."},
		}},
		{path: "templates/zh-CN/identity-pet-task.md", replacements: [][2]string{
			{"- 核心文档 `core/identity.md`、`core/principles.md`、`core/loops.md`、`core/agenda.md` 用 `pet_update_core` 写入；`loops.md` 只保留未闭环事项，`agenda.md` 只保留可自主推进项，完成后删除并留痕 worklog；巡检发现写 `worklog/YYYY-MM-DD.md`，用 `pet_write_worklog`。", "- 核心文档 `core/identity.md`、`core/principles.md`、`core/loops.md`、`core/agenda.md` 用 `pet_update_core` 写入；`loops.md` 只保留未闭环事项，`agenda.md` 只保留可自主推进项，完成后删除并留痕 worklog；日记写 `diary/YYYY-MM-DD.md`，巡检发现写 `worklog/YYYY-MM-DD.md`，分别用 `pet_write_diary`、`pet_write_worklog`。"},
			{"- 审批/想法只在审批中心创建一次；agenda/loops 只写引用和状态，worklog 只写证据。", "- 审批/想法只在审批中心创建一次；agenda/loops 只写引用和状态，worklog 只写证据，diary 只写叙事。"},
			{"- 宠物工作区可写：`pet-lab/` 可直接读写运行；核心文档和 worklog 仍用上述工具；其他项目只读诊断。", "- 宠物工作区可写：`pet-lab/` 可直接读写运行；核心文档和 diary/worklog 仍用上述工具；其他项目只读诊断。"},
		}},
	}
	for _, tc := range cases {
		data, err := templateFiles.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ReplaceAll(string(data), "\r\n", "\n")
		for _, pair := range tc.replacements {
			body = strings.ReplaceAll(body, pair[0], pair[1])
		}
		for _, legacy := range []string{body, strings.ReplaceAll(body, "\n", "\r\n")} {
			if !isSupersededBuiltinOverride("identity.pet-task", legacy) {
				t.Fatalf("previous builtin was not migrated: %s", tc.path)
			}
			if isSupersededBuiltinOverride("identity.pet-task", legacy+"\nOwner custom instruction") {
				t.Fatalf("custom override would be overwritten: %s", tc.path)
			}
		}
	}
}
