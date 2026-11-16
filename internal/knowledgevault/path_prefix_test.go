package knowledgevault

import "testing"

func TestPrefixFromPathSegments(t *testing.T) {
	cases := []struct {
		relative string
		want     string
	}{
		// Two segments.
		{"global/note.md", "global"},
		{"projects/alpha", "projects/alpha"},
		{"archive/old", "archive/old"},
		{"root.md", "root.md"},
		// Three segments.
		{"projects/alpha/note.md", "projects/alpha"},
		{"archive/old/note.md", "archive/old"},
		{"archive/projects/index.md", "archive/projects"},
		{"global/general/note.md", "global"},
		// Four segments: nested archived project spaces only.
		{"archive/projects/old/note.md", "archive/projects/old"},
		{"archive/projects/old/index.md", "archive/projects/old"},
	}
	for _, item := range cases {
		if got := prefixFromPath(item.relative); got != item.want {
			t.Errorf("prefixFromPath(%q) = %q, want %q", item.relative, got, item.want)
		}
	}
}
