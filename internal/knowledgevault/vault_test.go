package knowledgevault

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigKeepsVaultDisabled(t *testing.T) {
	config := DefaultConfig("/tmp/aha2-data")
	if config.Enabled {
		t.Fatal("knowledge vault must be disabled by default")
	}
	if config.VaultPath != filepath.Join("/tmp/aha2-data", "knowledge-vault") {
		t.Fatalf("vault path = %q", config.VaultPath)
	}
	if config.IndexPath != filepath.Join("/tmp/aha2-data", "knowledge-vault-index.db") {
		t.Fatalf("index path = %q", config.IndexPath)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("AHA2_KNOWLEDGE_VAULT_ENABLED", "true")
	t.Setenv("AHA2_KNOWLEDGE_VAULT_PATH", "/tmp/custom-vault")
	t.Setenv("AHA2_KNOWLEDGE_VAULT_INDEX", "/tmp/custom-index.db")
	config := ConfigFromEnv("/tmp/data")
	if !config.Enabled || config.VaultPath != "/tmp/custom-vault" || config.IndexPath != "/tmp/custom-index.db" {
		t.Fatalf("config = %+v", config)
	}
	t.Setenv("AHA2_KNOWLEDGE_VAULT_ENABLED", "0")
	if ConfigFromEnv("/tmp/data").Enabled {
		t.Fatal("explicit false value was ignored")
	}
}

func TestBootstrapCreatesStructureAndManifest(t *testing.T) {
	root := t.TempDir()
	config := DefaultConfig(root)
	report, err := Bootstrap(config, InitOptions{VaultName: "AHA2 Knowledge", Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if report.VaultID == "" || report.VaultName != "AHA2 Knowledge" {
		t.Fatalf("report = %+v", report)
	}
	for _, relative := range []string{
		".aha-vault.json", "README.md", "global/index.md",
		"global/general", "global/agent-lessons", "global/skills", "global/inbox",
		"projects", "skills", ".trash",
	} {
		if _, err := os.Stat(filepath.Join(config.VaultPath, filepath.FromSlash(relative))); err != nil {
			t.Errorf("missing %s: %v", relative, err)
		}
	}
	manifest, err := ReadManifest(config.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ManifestVersion || manifest.ObsidianVault != "AHA2 Knowledge" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Projects == nil || manifest.Archives == nil {
		t.Fatalf("manifest maps must not be nil: %+v", manifest)
	}
	if manifest.Skills == nil {
		t.Fatalf("manifest skills map must not be nil: %+v", manifest)
	}
	if len(manifest.Reserved) == 0 || len(manifest.ReservedFiles) == 0 {
		t.Fatalf("reserved paths missing: %+v", manifest)
	}
	index := readExportedFile(t, config.VaultPath, "global/index.md")
	if !strings.HasPrefix(index, "---\nid: knowledge_root_global") {
		t.Fatalf("global index frontmatter = %q", index)
	}
	if !strings.Contains(index, "全局知识首页") {
		t.Fatalf("global index title missing: %q", index)
	}
}

func TestBootstrapPreservesManifestIdentity(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	first, err := Bootstrap(config, InitOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(config, InitOptions{Commit: false})
	if err != nil {
		t.Fatal(err)
	}
	if first.VaultID != second.VaultID {
		t.Fatalf("vault id changed across bootstrap: %q vs %q", first.VaultID, second.VaultID)
	}
}

func TestBootstrapCommitsLocalGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	config := DefaultConfig(t.TempDir())
	report, err := Bootstrap(config, InitOptions{Commit: true, Now: func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.GitWarning != "" {
		t.Fatalf("git warning: %s", report.GitWarning)
	}
	if !report.Committed {
		t.Fatal("expected the initial structure to be committed")
	}
	command := exec.Command("git", "-C", config.VaultPath, "log", "--oneline")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "Initialize AHA2 knowledge vault") {
		t.Fatalf("commit message missing: %s", output)
	}
	if remotes, err := exec.Command("git", "-C", config.VaultPath, "remote").CombinedOutput(); err == nil && strings.TrimSpace(string(remotes)) != "" {
		t.Fatalf("vault must not configure a remote: %s", remotes)
	}
}
