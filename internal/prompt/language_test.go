package prompt

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestBuildAppendsOwnerLanguageDirective(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	base := BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-language", Code: "task-language", Title: "Language", TaskWorkspacePath: "/repo",
			CollaborationMode: "single", MaxAgents: 1,
		},
		Agent:       domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
		UserMessage: "hello",
	}
	tests := []struct {
		language string
		want     string
	}{
		{language: domain.LanguageEnglish, want: "Use English for your user-facing answer"},
		{language: domain.LanguageSimplified, want: "使用简体中文回复主人"},
		{language: domain.LanguageAuto, want: "Owner's latest message"},
	}
	for _, test := range tests {
		input := base
		input.Language = test.language
		result, err := engine.Build(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.EffectivePrompt, test.want) {
			t.Fatalf("language %q prompt missing %q", test.language, test.want)
		}
		if test.language == domain.LanguageSimplified && !strings.Contains(result.EffectivePrompt, "你负责整合、验证") {
			t.Fatalf("zh-CN prompt did not use translated resident templates")
		}
		if test.language == domain.LanguageEnglish && !strings.Contains(result.EffectivePrompt, "You own integration") {
			t.Fatalf("en-US prompt did not use English resident templates")
		}
		if test.language == domain.LanguageEnglish && regexp.MustCompile(`\p{Han}`).MatchString(result.EffectivePrompt) {
			t.Fatalf("en-US prompt contains Chinese control text: %s", result.EffectivePrompt)
		}
	}
}

func TestPetIdentityTemplateFollowsLanguage(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	byID := func(language string) string {
		templates, err := engine.TemplatesForLanguage(ctx, language)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range templates {
			if item.ID == "identity.pet-task" {
				return item.Content
			}
		}
		t.Fatal("identity.pet-task template missing")
		return ""
	}
	english := byID(domain.LanguageEnglish)
	if !strings.Contains(english, "You are AHA2's pet") || regexp.MustCompile(`\p{Han}`).MatchString(english) {
		t.Fatalf("en-US pet identity is not English: %s", english)
	}
	chinese := byID(domain.LanguageSimplified)
	if !strings.Contains(chinese, "你是 AHA2 的宠物") {
		t.Fatalf("zh-CN pet identity changed: %s", chinese)
	}
}

func TestEnglishPromptControlTextHasNoChinese(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	entry := func(id, scope, title string) domain.KnowledgeEntry {
		return domain.KnowledgeEntry{
			ID: id, Scope: scope, Title: title, Body: "English body.", Slug: id,
			Type: "practice", Revision: 1, Status: "verified",
		}
	}
	result, err := engine.Build(ctx, BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-language", Code: "task-language", Title: "Language", TaskWorkspacePath: "/repo",
			CollaborationMode: "single", MaxAgents: 1,
		},
		Agent:            domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:         domain.RuntimeConfigSnapshot{Backend: "codex"},
		UserMessage:      "hello",
		Language:         domain.LanguageEnglish,
		KnowledgeEnabled: true,
		GlobalKnowledge:  []domain.KnowledgeEntry{entry("global-entry", "global", "Global Entry")},
		ProjectKnowledge: []domain.KnowledgeEntry{entry("project-entry", "project", "Project Entry")},
		StaleKnowledge:   []domain.KnowledgeEntry{entry("stale-entry", "project", "Stale Entry")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\p{Han}`).MatchString(result.EffectivePrompt) {
		t.Fatalf("en-US prompt with knowledge contains Chinese control text: %s", result.EffectivePrompt)
	}
}

func TestChinesePromptTemplatesParse(t *testing.T) {
	for _, item := range builtinTemplates {
		name := strings.ReplaceAll(item.ID, ".", "-") + ".md"
		data, err := templateFiles.ReadFile("templates/zh-CN/" + name)
		if err != nil {
			continue
		}
		if _, err := template.New(item.ID).Parse(string(data)); err != nil {
			t.Fatalf("%s: %v", item.ID, err)
		}
	}
}

func TestTemplatesForLanguageLocalizesCatalog(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	templates, err := engine.TemplatesForLanguage(ctx, domain.LanguageSimplified)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range templates {
		if item.ID != "core.default" {
			continue
		}
		found = true
		if item.Name != "AHA 核心边界" {
			t.Fatalf("zh-CN core name = %q", item.Name)
		}
		if !strings.Contains(item.Content, "以选定的 Task Workspace") {
			t.Fatalf("zh-CN core content was not translated: %q", item.Content)
		}
	}
	if !found {
		t.Fatal("core.default template missing")
	}
}
