package legacyimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAHA1KnowledgeImportIsSanitizedHierarchicalAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetDir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(targetDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	existingProject := domain.Project{
		ID: "project-existing", Name: "Existing", ProjectType: "git",
		RepositoryIdentity: "https://example.com/org/repo.git", KnowledgePolicy: "enabled",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateProject(ctx, existingProject); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateKnowledge(ctx, domain.KnowledgeEntry{
		ID: "knowledge-existing-empty-slug", Scope: "project", ProjectID: existingProject.ID,
		Type: "practice", Title: "Existing empty slug", Body: "Valid legacy AHA2 knowledge.",
		Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1,
		CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "aha-knowledge.json"), `{"schema_version":1}`)
	writeTestFile(t, filepath.Join(source, "general", "wiki", "serial.md"), `---
{"id":"duplicate-id","title":"Serial guide","slug":"serial","type":"wiki","confidence":0.9,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}
---

# Serial guide

password: should-not-survive

![diagram](assets/diagram.svg)

[[old-target]]

`+"```text"+`
[example](missing.md)
`+"```"+`
`)
	writeTestFile(t, filepath.Join(source, "general", "wiki", "assets", "diagram.svg"), `<svg></svg>`)
	writeTestFile(t, filepath.Join(source, "personal", "wiki", "private-note.md"), `---
{"title":"Personal note","type":"wiki"}
---

# Personal note

Reusable but isolated.
`)
	writeTestFile(t, filepath.Join(source, "projects", "legacy-repo", "project.json"), `{"display_name":"Legacy Repo","bindings":[{"remote":"git@example.com:org/repo.git"}]}`)
	writeTestFile(t, filepath.Join(source, "projects", "legacy-repo", "navigation", "index.md"), `---
{"id":"duplicate-id","title":"Project navigation","slug":"index","type":"navigation","confidence":0.95}
---

# Project navigation
`)
	writeTestFile(t, filepath.Join(source, "projects", "legacy-repo", "navigation", "modules", "camera.md"), `---
{"title":"Camera module","type":"navigation","confidence":0.9}
---

# Camera module

Module details.
`)
	writeTestFile(t, filepath.Join(source, "projects", "unmapped", "worklog", "task.md"), `---
title: "Imported worklog"
type: task_worklog
confidence: 0.8
---

# Imported worklog

Useful result.
`)
	writeTestFile(t, filepath.Join(source, ".pending", "candidate.json"), `{"id":"cand-old","scope":"personal","title":"Pending note","body":"token=must-not-survive","status":"pending","created_at":"2026-01-03T00:00:00Z","updated_at":"2026-01-03T00:00:00Z","meta":{"confidence":0.7}}`)
	writeTestFile(t, filepath.Join(source, "skills", "demo", "SKILL.md"), `---
name: "Demo"
description: "Demo skill"
---

Follow the demo procedure.
`)
	writeTestFile(t, filepath.Join(source, "skills", "demo", "scripts", "run.sh"), "#!/bin/sh\nTOKEN=should-not-survive\necho ok\n")
	writeTestBytes(t, filepath.Join(source, "skills", "demo", "scripts", "cache.pyc"), []byte{0, 1, 2})

	plan, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.SourceDocuments != 5 || plan.Report.SourceSkills != 1 || plan.Report.SourceSkillFiles != 3 || plan.Report.SkillFilesImported != 2 {
		t.Fatalf("unexpected source inventory: %#v", plan.Report)
	}
	if plan.Report.MatchedProjects != 1 || plan.Report.ArchiveProjects != 2 {
		t.Fatalf("unexpected project mapping: %#v", plan.Report)
	}
	if plan.Report.Redactions != 3 || len(plan.Report.UnresolvedAssets) != 0 || plan.Report.SourceAssets != 1 || plan.Report.AssetsEmbedded != 1 {
		t.Fatalf("sensitive data or assets were not accounted for: %#v", plan.Report)
	}
	if plan.Report.MissingLocalLinks != 0 {
		t.Fatalf("link-like code sample was treated as a document link: %#v", plan.Report.LinkIssues)
	}
	if plan.Report.WikiLinks != 1 || plan.Report.WikiLinksAnnotated != 1 {
		t.Fatalf("legacy WikiLink was not annotated: %#v", plan.Report)
	}
	if plan.Report.ProposalCreate != 1 || plan.Report.SkillCreate != 1 || plan.Report.KnowledgeCreate == 0 {
		t.Fatalf("unexpected import actions: %#v", plan.Report)
	}

	report, err := Execute(ctx, database, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied {
		t.Fatal("import was not marked applied")
	}
	if err := Verify(ctx, database); err != nil {
		t.Fatal(err)
	}
	entries, err := database.ListKnowledge(ctx, "global", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Body, "should-not-survive") || strings.Contains(entry.Body, "must-not-survive") {
			t.Fatalf("secret remained in imported knowledge: %s", entry.Title)
		}
		if entry.Title == "Personal note" {
			t.Fatal("personal knowledge leaked into global scope")
		}
		if entry.Title == "Serial guide" && strings.Contains(entry.Body, "# Serial guide") {
			t.Fatal("duplicate first heading was retained")
		}
		if entry.Title == "Serial guide" && !strings.Contains(entry.Body, "data:image/svg+xml;base64,") {
			t.Fatal("referenced knowledge asset was not embedded")
		}
		if entry.Title == "Serial guide" && !strings.Contains(entry.Body, "AHA1 WikiLink，原目标不存在") {
			t.Fatal("unresolved WikiLink was not annotated")
		}
	}
	projectEntries, err := database.ListKnowledge(ctx, "project", existingProject.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var modules, camera domain.KnowledgeEntry
	for _, entry := range projectEntries {
		switch entry.Slug {
		case "modules":
			modules = entry
		case "camera":
			camera = entry
		}
	}
	if modules.ID == "" || modules.Type != "navigation" || modules.Title != "模块索引" || camera.ParentID != modules.ID {
		t.Fatalf("navigation folders were not preserved as a hierarchy: modules=%#v camera=%#v", modules, camera)
	}
	personalID := stableID("project_aha1", "personal-archive")
	proposals, err := database.ListKnowledgeProposals(ctx, "project", personalID)
	if err != nil || len(proposals) != 1 || strings.Contains(proposals[0].Proposed.Body, "must-not-survive") {
		t.Fatalf("pending proposal was not sanitized: %#v, %v", proposals, err)
	}
	archiveID := stableID("project_aha1", "unmapped")
	archiveEntries, err := database.ListKnowledge(ctx, "project", archiveID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archiveEntries {
		if entry.Type == "task_worklog" && entry.Status != domain.KnowledgeCandidate {
			t.Fatalf("worklog was published as verified knowledge: %#v", entry)
		}
	}
	skills, err := database.ListSkills(ctx, "global", "", false)
	if err != nil || len(skills) != 1 {
		t.Fatalf("imported skills=%#v err=%v", skills, err)
	}
	for _, file := range skills[0].PackageFiles {
		if strings.Contains(file.Content, "should-not-survive") {
			t.Fatalf("secret remained in imported skill file %s", file.Path)
		}
	}
	archiveRoot, err := database.EnsureKnowledgeRoot(ctx, "project", archiveID)
	if err != nil {
		t.Fatal(err)
	}
	worklogDirectory := domain.KnowledgeEntry{
		ID: stableID("knowledge_aha1_dir", "project", "unmapped", "worklog"), Scope: "project", ProjectID: archiveID,
		ParentID: archiveRoot.ID, Slug: "worklog", Type: "practice", Title: "历史工作记录", Body: "AHA1 导入目录：`worklog`。",
		Status: domain.KnowledgeVerified, Confidence: 1, Revision: 1, CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if err := database.CreateKnowledge(ctx, worklogDirectory); err != nil {
		t.Fatal(err)
	}
	legacyWorklog := domain.KnowledgeEntry{
		ID: stableID("knowledge_aha1", "project", "unmapped", "worklog/task.md"), Scope: "project", ProjectID: archiveID,
		ParentID: worklogDirectory.ID, Slug: "task", Type: "task_worklog", Title: "Imported worklog", Body: "old worklog",
		Status: domain.KnowledgeCandidate, Confidence: 0.8, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateKnowledge(ctx, legacyWorklog); err != nil {
		t.Fatal(err)
	}
	cleanup, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Report.WorklogsSkipped != 1 || cleanup.Report.KnowledgeDelete != 2 {
		t.Fatalf("existing worklog cleanup was not planned: %#v", cleanup.Report)
	}
	if _, err := Execute(ctx, database, cleanup); err != nil {
		t.Fatal(err)
	}

	second, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if second.Report.ArchiveProjects != 0 || second.Report.KnowledgeCreate != 0 || second.Report.KnowledgeUpdate != 0 || second.Report.KnowledgeDelete != 0 || second.Report.ProposalCreate != 0 || second.Report.ProposalUpdate != 0 || second.Report.SkillCreate != 0 || second.Report.SkillUpdate != 0 {
		t.Fatalf("second run was not a no-op: %#v", second.Report)
	}
}

func TestAHA1RealKnowledgeImport(t *testing.T) {
	source := os.Getenv("AHA1_KNOWLEDGE_TEST_SOURCE")
	if source == "" {
		t.Skip("set AHA1_KNOWLEDGE_TEST_SOURCE to run the legacy library integration test")
	}
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	plan, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.SourceDocuments == 0 || plan.Report.SourceSkills == 0 {
		t.Fatalf("legacy source was not inventoried: %#v", plan.Report)
	}
	if _, err := Execute(ctx, database, plan); err != nil {
		t.Fatal(err)
	}
	libraries, err := database.ListKnowledgeLibraries(ctx)
	if err != nil || len(libraries) != plan.Report.ArchiveProjects {
		t.Fatalf("pending knowledge libraries=%d want=%d err=%v", len(libraries), plan.Report.ArchiveProjects, err)
	}
	if err := Verify(ctx, database); err != nil {
		t.Fatal(err)
	}
	second, err := Analyze(ctx, database, Options{SourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if second.Report.ArchiveProjects != 0 || second.Report.KnowledgeCreate != 0 || second.Report.KnowledgeUpdate != 0 || second.Report.KnowledgeDelete != 0 || second.Report.ProposalCreate != 0 || second.Report.ProposalUpdate != 0 || second.Report.SkillCreate != 0 || second.Report.SkillUpdate != 0 {
		for _, item := range second.knowledge {
			if item.Action == "update" {
				t.Logf("unexpected update: %s %s", item.Entry.ID, item.Entry.Title)
			}
		}
		t.Fatalf("real source second run was not a no-op: %#v", second.Report)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	writeTestBytes(t, path, []byte(content))
}

func writeTestBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
