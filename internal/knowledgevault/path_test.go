package knowledgevault

import (
	"strings"
	"testing"
)

func TestValidateRelativePathRejectsTraversalAndWindowsNames(t *testing.T) {
	valid := []string{"global/index.md", "projects/demo/inbox/leaf.md", "archive/old/index.md"}
	for _, value := range valid {
		if err := ValidateRelativePath(value); err != nil {
			t.Errorf("ValidateRelativePath(%q) = %v", value, err)
		}
	}
	invalid := []string{
		"", "../escape.md", "global/../../escape.md", "/absolute.md",
		"C:/windows.md", `global\..\escape.md`, "global/CON.md", "global/nul",
		"global/COM1.md", "global/name.", "global/name ", "global/a:b.md",
	}
	for _, value := range invalid {
		if err := ValidateRelativePath(value); err == nil {
			t.Errorf("ValidateRelativePath(%q) accepted an unsafe path", value)
		}
	}
}

func TestSafeSegmentNeutralizesUnsafeInput(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "-..-etc-passwd",
		"CON":              "_CON",
		"nul.txt":          "_nul.txt",
		`a/b\c`:            "a-b-c",
		"":                 "untitled",
		"..":               "untitled",
	}
	for input, expected := range cases {
		actual := SafeSegment(input, "")
		if actual != expected {
			t.Errorf("SafeSegment(%q) = %q, want %q", input, actual, expected)
		}
	}
	if strings.ContainsAny(SafeSegment("a:b*c?", ""), `<>:"/\|?*`) {
		t.Fatal("SafeSegment left an invalid character")
	}
}

func TestSlugifyIsStableAndPortable(t *testing.T) {
	if first, second := Slugify("Deploy Before Verify"), Slugify("Deploy Before Verify"); first != second {
		t.Fatalf("Slugify is not deterministic: %q vs %q", first, second)
	}
	if slug := Slugify("Deploy Before Verify"); slug != "deploy-before-verify" {
		t.Fatalf("slug = %q", slug)
	}
	if slug := Slugify("中文标题"); !strings.HasPrefix(slug, "doc-") {
		t.Fatalf("non-ASCII fallback = %q", slug)
	}
	if slug := Slugify("CON"); strings.HasPrefix(slug, "con") && !strings.HasPrefix(slug, "doc-con") {
		t.Fatalf("reserved name was not neutralized: %q", slug)
	}
}

func TestPathGuardCaseInsensitiveCollision(t *testing.T) {
	guard := NewPathGuard(t.TempDir())
	first, changed, err := guard.Allocate("projects/demo/Foo.md", "file:1")
	if err != nil || changed {
		t.Fatalf("first allocation = %q changed=%v err=%v", first, changed, err)
	}
	second, changed, err := guard.Allocate("projects/demo/foo.md", "file:2")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a case-insensitive collision to be resolved")
	}
	if strings.EqualFold(first, second) {
		t.Fatalf("collision was not resolved: %q and %q", first, second)
	}
	repeat, changedAgain, err := guard.Allocate("projects/demo/foo.md", "file:2")
	if err != nil || changedAgain || repeat != second {
		t.Fatalf("collision allocation is not stable: %q changed=%v err=%v", repeat, changedAgain, err)
	}
}

func TestPathGuardRejectsEscape(t *testing.T) {
	guard := NewPathGuard(t.TempDir())
	if _, _, err := guard.Allocate("../escape.md", "file:1"); err == nil {
		t.Fatal("guard accepted a path outside the vault")
	}
}

func TestValidateRelativePathRejectsControlCharacters(t *testing.T) {
	for _, value := range []string{"a\nb.md", "a\tb.md", "a\rb.md", "a\x7fb.md", "a\u0085b.md"} {
		if err := ValidateRelativePath(value); err == nil {
			t.Errorf("path %q with a control character was accepted", value)
		}
	}
	if err := ValidateRelativePath("projects/demo/正常笔记.md"); err != nil {
		t.Errorf("normal Unicode path rejected: %v", err)
	}
}
