package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsExtendedPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"drive", `C:\repos\project`, `\\?\C:\repos\project`},
		{"lowercase drive and forward slashes", `c:/repos/project/`, `\\?\C:\repos\project`},
		{"drive root", `C:\`, `\\?\C:\`},
		{"dot segments", `C:\repos\.\nested\..\project`, `\\?\C:\repos\project`},
		{"unc", `\\server\share\project`, `\\?\UNC\server\share\project`},
		{"unc forward slashes", `//server/share/project`, `\\?\UNC\server\share\project`},
		{"unc dot segments", `\\server\share\.\project`, `\\?\UNC\server\share\project`},
		{"already extended", `\\?\C:\repos\project`, `\\?\C:\repos\project`},
		{"already unc extended", `\\?\UNC\server\share`, `\\?\UNC\server\share`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := windowsExtendedPath(tc.input)
			if err != nil {
				t.Fatalf("windowsExtendedPath(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("windowsExtendedPath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestWindowsExtendedPathRejectsInvalidPaths(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "relative\\path", `C:relative`, `\\server`, `\\server\`} {
		if _, err := windowsExtendedPath(input); err == nil {
			t.Fatalf("windowsExtendedPath(%q) succeeded, want error", input)
		}
	}
}

func TestRemoveAllLongPathDeletesDeepTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	segments := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		segments = append(segments, fmt.Sprintf("%02d-%s", i, strings.Repeat("x", 28)))
	}
	deep := filepath.Join(append([]string{root}, segments...)...)
	if len(deep) <= 260 {
		t.Fatalf("test path is not long enough: %d characters", len(deep))
	}
	payload := filepath.Join(deep, "payload.txt")

	// On Windows the plain path already exceeds MAX_PATH, so create it through
	// the extended-length form. Other platforms take the unchanged path.
	createDir, err := LocalLongPath(deep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(createDir, 0o755); err != nil {
		t.Fatalf("create deep directory: %v", err)
	}
	createFile, err := LocalLongPath(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(createFile, []byte("deep"), 0o644); err != nil {
		t.Fatalf("write deep file: %v", err)
	}
	if _, err := os.Lstat(createFile); err != nil {
		t.Fatalf("deep file missing before removal: %v", err)
	}

	if err := RemoveAllLongPath(deep); err != nil {
		t.Fatalf("RemoveAllLongPath: %v", err)
	}
	if _, err := os.Lstat(createDir); !os.IsNotExist(err) {
		t.Fatalf("deep tree still exists: %v", err)
	}
}

func TestRemoveAllLongPathRejectsRelativePath(t *testing.T) {
	t.Parallel()
	if err := RemoveAllLongPath("relative/path"); err == nil {
		t.Fatal("relative path was accepted")
	}
}
