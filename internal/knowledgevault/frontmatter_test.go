package knowledgevault

import (
	"strings"
	"testing"
)

func TestParseDocumentReadsKnownFields(t *testing.T) {
	raw := `---
id: knowledge_demo
title: "标题: 带冒号"
slug: demo
scope: project
project_id: project-demo
type: practice
legacy_type: guide
tags: [deploy, verify]
status: verified
parent: knowledge_root_project_project-demo
created: 2026-01-02T03:04:05Z
updated: 2026-02-03T04:05:06Z
source_task: task-1
source_turn: turn-2
confidence: 0.85
trust: high
sort_order: 4
is_index: false
custom_field: keep-me
---

# Demo

Body text.
`
	document := ParseDocument(raw)
	if document.Warning != "" {
		t.Fatalf("unexpected warning: %s", document.Warning)
	}
	if !document.HasFrontmatter {
		t.Fatal("expected frontmatter")
	}
	frontmatter := document.Frontmatter
	if frontmatter.ID != "knowledge_demo" || frontmatter.ProjectID != "project-demo" {
		t.Fatalf("identity fields were not parsed: %+v", frontmatter)
	}
	if frontmatter.Title != "标题: 带冒号" {
		t.Fatalf("title = %q", frontmatter.Title)
	}
	if len(frontmatter.Tags) != 2 || frontmatter.Tags[1] != "verify" {
		t.Fatalf("tags = %#v", frontmatter.Tags)
	}
	if frontmatter.Confidence == nil || *frontmatter.Confidence != 0.85 {
		t.Fatalf("confidence = %#v", frontmatter.Confidence)
	}
	if frontmatter.SortOrder == nil || *frontmatter.SortOrder != 4 {
		t.Fatalf("sort order = %#v", frontmatter.SortOrder)
	}
	if frontmatter.IsIndex == nil || *frontmatter.IsIndex {
		t.Fatalf("is_index = %#v", frontmatter.IsIndex)
	}
	if frontmatter.Extra["custom_field"] != "keep-me" {
		t.Fatalf("unknown frontmatter field was not preserved: %#v", frontmatter.Extra)
	}
	if !strings.Contains(document.Body, "# Demo") || strings.Contains(document.Body, "custom_field") {
		t.Fatalf("body = %q", document.Body)
	}
}

func TestParseDocumentInvalidFrontmatterKeepsRaw(t *testing.T) {
	raw := "---\nid: [unterminated\n---\n\n# Still here\n"
	document := ParseDocument(raw)
	if document.Warning == "" {
		t.Fatal("expected a warning for malformed frontmatter")
	}
	if document.Body != raw {
		t.Fatalf("malformed file lost its original text: %q", document.Body)
	}
}

func TestParseDocumentWithoutFrontmatter(t *testing.T) {
	document := ParseDocument("# Title\n\nBody\n")
	if document.HasFrontmatter || document.Warning != "" {
		t.Fatalf("unexpected parse result: %+v", document)
	}
	if document.Body != "# Title\n\nBody\n" {
		t.Fatalf("body = %q", document.Body)
	}
}

func TestNormalizeType(t *testing.T) {
	cases := []struct {
		raw, hint, canonical, legacy string
	}{
		{"practice", "", TypePractice, ""},
		{"navigation", "", TypeNavigation, ""},
		{"how-to", "", TypePractice, "how-to"},
		{"troubleshooting", "", TypeDiagnostic, "troubleshooting"},
		{"adr", "", TypeDecision, "adr"},
		{"design", "", TypeArchitecture, "design"},
		{"note", "", TypeReference, "note"},
		{"ancient-custom-type", "", TypeReference, "ancient-custom-type"},
		{"", "global/agent-lessons/technical-diagnostics.md", TypeDiagnostic, ""},
		{"", "global/agent-lessons/behavior-lessons.md", TypePractice, ""},
		{"", "global/general/index.md", TypeNavigation, ""},
		{"", "projects/demo/inbox/leaf.md", TypeReference, ""},
	}
	for _, test := range cases {
		canonical, legacy := NormalizeType(test.raw, test.hint)
		if canonical != test.canonical || legacy != test.legacy {
			t.Errorf("NormalizeType(%q, %q) = (%q, %q), want (%q, %q)", test.raw, test.hint, canonical, legacy, test.canonical, test.legacy)
		}
	}
}

func TestNormalizeTypeCoversMigrationInventory(t *testing.T) {
	cases := map[string]string{
		"navigation":   TypeNavigation,
		"decision":     TypeDecision,
		"practice":     TypePractice,
		"lesson":       TypePractice,
		"diagnostic":   TypeDiagnostic,
		"diagnosis":    TypeDiagnostic,
		"reference":    TypeReference,
		"technical":    TypeReference,
		"fact":         TypeReference,
		"architecture": TypeArchitecture,
		"design":       TypeArchitecture,
		"solution":     TypeArchitecture,
	}
	for raw, want := range cases {
		canonical, legacy := NormalizeType(raw, "")
		if canonical != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", raw, canonical, want)
		}
		if canonicalTypes[raw] && legacy != "" {
			t.Errorf("canonical type %q unexpectedly kept legacy %q", raw, legacy)
		}
	}
}

func TestFirstHeadingIgnoresFencedCode(t *testing.T) {
	body := "```\n# not a title\n```\n\n# Real title\n"
	if heading := FirstHeading(body); heading != "Real title" {
		t.Fatalf("heading = %q", heading)
	}
}

func TestRenderFrontmatterIsDeterministic(t *testing.T) {
	item := Frontmatter{
		ID: "knowledge_demo", Title: "Demonstration", Slug: "demo", Scope: "project",
		ProjectID: "project-demo", Type: TypePractice, Status: "verified",
		Tags: []string{"a", "b"}, IsIndex: boolPointer(false), SortOrder: intPointer(0),
	}
	first, err := renderFrontmatter(item)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderFrontmatter(item)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("frontmatter rendering is not deterministic:\n%s\n---\n%s", first, second)
	}
	if !strings.HasPrefix(first, "---\n") || !strings.HasSuffix(first, "---\n") {
		t.Fatalf("invalid frontmatter framing: %q", first)
	}
	parsed := ParseDocument(first + "\n# Body\n")
	if parsed.Frontmatter.ID != item.ID || parsed.Frontmatter.Slug != item.Slug {
		t.Fatalf("round trip lost fields: %+v", parsed.Frontmatter)
	}
}

func TestStripGeneratedIndex(t *testing.T) {
	body := "Root body.\n\n" + indexBlockStart + "\n## 文档\n\n- [[a|A]]\n" + indexBlockEnd + "\n"
	if stripped := StripGeneratedIndex(body); stripped != "Root body." {
		t.Fatalf("stripped = %q", stripped)
	}
}
