package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallerInstallDeleteStaysInsideTheInstallScope guards the upgrade
// cleanup added for task-314: removing the retired {app}\agent-proxy directory
// is useful, but an [InstallDelete] entry with a broader or relative Name would
// delete files Inno never installed. Every entry must stay under {app} or
// {group}.
func TestInstallerInstallDeleteStaysInsideTheInstallScope(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "installer", "windows", "AHA2.iss"))
	if err != nil {
		t.Fatal(err)
	}
	section := installerSection(t, string(content), "[InstallDelete]")
	if !strings.Contains(section, `Type: filesandordirs; Name: "{app}\agent-proxy"`) {
		t.Fatal("InstallDelete no longer removes the retired {app}\\agent-proxy directory")
	}
	entries := 0
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if !strings.HasPrefix(line, "Type:") {
			continue
		}
		entries++
		name, ok := installerEntryName(line)
		if !ok {
			t.Fatalf("InstallDelete entry has no Name: %q", line)
		}
		if !strings.HasPrefix(name, `{app}\`) && !strings.HasPrefix(name, `{group}\`) {
			t.Fatalf("InstallDelete entry escapes the install scope: %q", line)
		}
		if strings.Contains(name, `..`) {
			t.Fatalf("InstallDelete entry contains a parent reference: %q", line)
		}
	}
	if entries == 0 {
		t.Fatal("InstallDelete section is empty")
	}
}

// installerSection returns the body of an Inno Setup section up to the next
// section header.
func installerSection(t *testing.T, content, header string) string {
	t.Helper()
	start := strings.Index(content, header)
	if start < 0 {
		t.Fatalf("section not found: %s", header)
	}
	body := content[start+len(header):]
	if next := strings.Index(body, "\n["); next >= 0 {
		body = body[:next]
	}
	return body
}

// installerEntryName extracts the quoted Name parameter from one entry line.
func installerEntryName(line string) (string, bool) {
	const marker = `Name: "`
	start := strings.Index(line, marker)
	if start < 0 {
		return "", false
	}
	rest := line[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
