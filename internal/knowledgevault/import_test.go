package knowledgevault

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildExportedSourceVault(t *testing.T) Config {
	t.Helper()
	ctx := context.Background()
	database, databasePath := openTestStore(t)
	seedStandardVaultDatabase(t, database)
	source, err := DefaultConfig(t.TempDir()).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(ctx, source, databasePath, ExportOptions{Commit: false}); err != nil {
		t.Fatalf("export source vault: %v", err)
	}
	return source
}

func TestImportExistingVaultLocalCopiesRebuildsAndVerifies(t *testing.T) {
	ctx := context.Background()
	source := buildExportedSourceVault(t)
	destination, err := DefaultConfig(t.TempDir()).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	report, err := ImportExistingVault(ctx, destination, ExistingVaultImportOptions{
		Kind: "local", Path: source.VaultPath, KnownProjects: []string{"project-demo"},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if report.Source != ImportSourceExisting || report.Kind != "local" {
		t.Fatalf("report source = %+v", report)
	}
	if report.ImportedFiles == 0 || report.ProjectCount == 0 || report.IndexedEntries == 0 {
		t.Fatalf("report counts = %+v", report)
	}
	if len(report.UnboundProjects) != 0 || report.VerifiedAt.IsZero() {
		t.Fatalf("report verification = %+v", report)
	}
	vaultManifest, err := ReadManifest(destination.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	for projectID, slug := range vaultManifest.Projects {
		if _, err := os.Stat(filepath.Join(destination.VaultPath, "projects", slug, "index.md")); err != nil {
			t.Fatalf("project %s missing generated index page: %v", projectID, err)
		}
	}

	// A second import into the now non-empty target must abort instead of
	// merging or overwriting the verified vault.
	if _, err := ImportExistingVault(ctx, destination, ExistingVaultImportOptions{
		Kind: "local", Path: source.VaultPath,
	}); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("second import = %v, want ErrTargetNotEmpty", err)
	}
}

func TestImportExistingVaultRejectsNonEmptyTargetWithoutChangingIt(t *testing.T) {
	source := buildExportedSourceVault(t)
	destination, err := DefaultConfig(t.TempDir()).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination.VaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(destination.VaultPath, "keep.md")
	if err := os.WriteFile(keep, []byte("existing owner file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportExistingVault(context.Background(), destination, ExistingVaultImportOptions{
		Kind: "local", Path: source.VaultPath,
	}); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("non-empty target import = %v, want ErrTargetNotEmpty", err)
	}
	raw, err := os.ReadFile(keep)
	if err != nil || string(raw) != "existing owner file\n" {
		t.Fatalf("non-empty target was modified: %q err=%v", raw, err)
	}
}

func TestImportExistingVaultFromGitRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := buildExportedSourceVault(t)
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", source.VaultPath}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=AHA2 Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=AHA2 Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	runGit("init", "-b", "main")
	runGit("add", ".")
	runGit("commit", "-m", "source vault")

	destination, err := DefaultConfig(t.TempDir()).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	destination.AllowLocalRemote = true
	report, err := ImportExistingVault(context.Background(), destination, ExistingVaultImportOptions{
		Kind: "git", Remote: source.VaultPath, Branch: "main", KnownProjects: []string{"project-demo"},
	})
	if err != nil {
		t.Fatalf("git import: %v", err)
	}
	if report.Branch != "main" || report.Remote == "" || report.IndexedEntries == 0 {
		t.Fatalf("git import report = %+v", report)
	}
	if _, err := ReadManifest(destination.VaultPath); err != nil {
		t.Fatalf("cloned vault manifest: %v", err)
	}
	ctx := context.Background()
	destination = DefaultConfig(t.TempDir())
	destination.AllowLocalRemote = true
	manager, err := NewManager(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := BootstrapFingerprint(destination.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err = manager.ImportOverBootstrap(ctx, fingerprint, ExistingVaultImportOptions{
		Kind: "git", Remote: source.VaultPath, Branch: "main",
	}, func(ExistingVaultImportReport) error { return nil })
	if err != nil || report.BackupPath == "" || report.ProjectCount == 0 {
		t.Fatalf("git import over bootstrap = %+v, err = %v", report, err)
	}
	if status := manager.Status(ctx); !status.IndexReady || status.ProjectBindings["project-demo"] == "" {
		t.Fatalf("git import did not retain the source binding: %+v", status)
	}
}

func TestImportOverBootstrapRestoresVaultIfVerificationCannotBeRecorded(t *testing.T) {
	ctx := context.Background()
	source := DefaultConfig(t.TempDir())
	if _, err := Bootstrap(source, InitOptions{VaultName: "Source"}); err != nil {
		t.Fatal(err)
	}
	destination := DefaultConfig(t.TempDir())
	manager, err := NewManager(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	original := manager.Status(ctx)
	fingerprint, err := BootstrapFingerprint(destination.VaultPath)
	if err != nil {
		t.Fatal(err)
	}
	recordErr := errors.New("test metadata write failed")
	report, err := manager.ImportOverBootstrap(ctx, fingerprint, ExistingVaultImportOptions{
		Kind: "local", Path: source.VaultPath,
	}, func(ExistingVaultImportReport) error { return recordErr })
	if !errors.Is(err, recordErr) || report.BackupPath != "" || !manager.CanReplaceBootstrap(fingerprint) {
		t.Fatalf("failed import did not restore the original files: %+v err = %v", report, err)
	}
	if restored := manager.Status(ctx); !restored.Enabled || !restored.IndexReady || restored.VaultName != original.VaultName || restored.Source != original.Source {
		t.Fatalf("failed import did not restore the reader: %+v", restored)
	}
}

func TestImportExistingVaultNeverLeaksGitCredentials(t *testing.T) {
	const secret = "supersecret-token"
	destination, err := DefaultConfig(t.TempDir()).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportExistingVault(context.Background(), destination, ExistingVaultImportOptions{
		Kind: "git", Remote: "ssh://user:" + secret + "@example.com/repo.git",
	})
	if err == nil {
		t.Fatal("credential remote was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("import error leaked the credential: %v", err)
	}
	if masked := MaskGitRemote("ssh://user:" + secret + "@example.com/repo.git"); strings.Contains(masked, secret) {
		t.Fatalf("MaskGitRemote leaked the credential: %s", masked)
	}
}
