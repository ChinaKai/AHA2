package knowledgevault

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ExistingVaultImportOptions describes one explicit import. Opening a vault
// that already has content is never a merge: the caller must start from an
// empty target or move the old directory away first.
type ExistingVaultImportOptions struct {
	Kind          string // "local" or "git"
	Path          string // local directory
	Remote        string // SSH remote
	Branch        string
	KnownProjects []string
	Now           func() time.Time
}

type ExistingVaultImportReport struct {
	Source          string    `json:"source"`
	Kind            string    `json:"kind"`
	SourcePath      string    `json:"source_path,omitempty"`
	Remote          string    `json:"remote,omitempty"`
	Branch          string    `json:"branch,omitempty"`
	VaultPath       string    `json:"vault_path"`
	ImportedFiles   int       `json:"imported_files"`
	ProjectCount    int       `json:"project_count"`
	UnboundProjects []string  `json:"unbound_projects,omitempty"`
	IndexedEntries  int       `json:"indexed_entries"`
	Warnings        []string  `json:"warnings,omitempty"`
	VerifiedAt      time.Time `json:"verified_at"`
}

// ImportExistingVault copies or clones an existing AHA2 vault into the
// configured vault path, fills missing index pages via the normal rebuild,
// verifies the manifest/project bindings, and only then reports success. A
// successful Git fetch alone is not readiness: the index must be rebuilt and
// re-verified here or by the caller after a later pull.
func ImportExistingVault(ctx context.Context, cfg Config, options ExistingVaultImportOptions) (ExistingVaultImportReport, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return ExistingVaultImportReport{}, err
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	report := ExistingVaultImportReport{Source: ImportSourceExisting, Kind: strings.ToLower(strings.TrimSpace(options.Kind)), VaultPath: cfg.VaultPath}
	// Validate the source before touching the target so a credential or path
	// error is reported even when the target is already populated.
	switch report.Kind {
	case "local":
		sourcePath := strings.TrimSpace(options.Path)
		if sourcePath == "" {
			return report, ErrImportSourceMissing
		}
		absolute, err := filepath.Abs(sourcePath)
		if err != nil {
			return report, err
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() {
			return report, ErrImportSourceMissing
		}
		if samePath(absolute, cfg.VaultPath) {
			return report, fmt.Errorf("import source and target are the same directory")
		}
		if insidePath(cfg.VaultPath, absolute) || insidePath(absolute, cfg.VaultPath) {
			return report, fmt.Errorf("import source and target must not contain each other")
		}
		report.SourcePath = absolute
		if err := ensureEmptyTarget(cfg.VaultPath); err != nil {
			return report, err
		}
		report.ImportedFiles, err = copyDirectoryCount(absolute, cfg.VaultPath)
		if err != nil {
			_ = os.RemoveAll(cfg.VaultPath)
			return report, err
		}
	case "git":
		remote := strings.TrimSpace(options.Remote)
		if err := ValidateGitRemote(remote, cfg.AllowLocalRemote); err != nil {
			return report, err
		}
		branch := strings.TrimSpace(options.Branch)
		if branch != "" {
			if err := ValidateGitBranch(branch); err != nil {
				return report, err
			}
		}
		report.Remote = MaskGitRemote(remote)
		report.Branch = branch
		if err := ensureEmptyTarget(cfg.VaultPath); err != nil {
			return report, err
		}
		if err := cloneVaultRepository(ctx, cfg, remote, branch); err != nil {
			_ = os.RemoveAll(cfg.VaultPath)
			return report, err
		}
		report.ImportedFiles, _ = countFiles(cfg.VaultPath)
	default:
		return report, fmt.Errorf("import source must be local or git")
	}

	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		_ = os.RemoveAll(cfg.VaultPath)
		if os.IsNotExist(err) {
			return report, ErrImportManifestMissing
		}
		return report, fmt.Errorf("%w: %v", ErrImportManifestMissing, err)
	}
	report.ProjectCount = len(manifest.Projects)
	report.UnboundProjects = unboundKnownProjects(manifest.Projects, options.KnownProjects)

	index, err := OpenIndex(ctx, cfg.IndexPath)
	if err != nil {
		return report, fmt.Errorf("open knowledge index: %w", err)
	}
	rebuild, rebuildErr := index.Rebuild(ctx, cfg.VaultPath)
	_ = index.Close()
	if rebuildErr != nil {
		return report, fmt.Errorf("rebuild imported knowledge index: %w", rebuildErr)
	}
	report.IndexedEntries = rebuild.Entries
	report.Warnings = append(report.Warnings, rebuild.Warnings...)
	verifiedManifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return report, fmt.Errorf("read imported vault manifest after rebuild: %w", err)
	}
	if err := verifyExistingVault(manifest, verifiedManifest, rebuild); err != nil {
		return report, err
	}
	report.VerifiedAt = now
	return report, nil
}

func ensureEmptyTarget(vaultPath string) error {
	entries, err := os.ReadDir(vaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(entries) > 0 {
		return ErrTargetNotEmpty
	}
	return nil
}

func verifyExistingVault(original, rebuilt Manifest, report RebuildReport) error {
	if rebuilt.Version == 0 {
		return fmt.Errorf("%w: rebuilt manifest has no version", ErrMigrationVerification)
	}
	for projectID, slug := range original.Projects {
		if strings.TrimSpace(rebuilt.Projects[projectID]) != strings.TrimSpace(slug) {
			return fmt.Errorf("%w: project %s lost its vault binding %q", ErrMigrationVerification, projectID, slug)
		}
		if _, err := os.Stat(filepath.Join(report.VaultPath, "projects", slug, "index.md")); err != nil {
			return fmt.Errorf("%w: project %s is missing projects/%s/index.md", ErrMigrationVerification, projectID, slug)
		}
	}
	return nil
}

func unboundKnownProjects(bindings map[string]string, known []string) []string {
	unbound := []string{}
	seen := map[string]bool{}
	for _, projectID := range known {
		projectID = strings.TrimSpace(projectID)
		if projectID == "" || seen[projectID] {
			continue
		}
		seen[projectID] = true
		if strings.TrimSpace(bindings[projectID]) == "" {
			unbound = append(unbound, projectID)
		}
	}
	sort.Strings(unbound)
	return unbound
}

func cloneVaultRepository(ctx context.Context, cfg Config, remote, branch string) error {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		return ErrGitUnavailable
	}
	if err := os.MkdirAll(filepath.Dir(cfg.VaultPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(cfg.VaultPath); err != nil {
		return err
	}
	args := []string{"clone", "--quiet"}
	if branch != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, "--", remote, cfg.VaultPath)
	command := exec.CommandContext(ctx, gitBinary, args...)
	command.Env = gitEnvironment()
	output, runErr := command.CombinedOutput()
	if runErr != nil {
		detail := sanitizeGitOutput(string(output))
		detail = strings.ReplaceAll(detail, remote, MaskGitRemote(remote))
		if strings.TrimSpace(detail) == "" {
			detail = runErr.Error()
		}
		return &GitCommandError{Op: "clone", Detail: detail}
	}
	return nil
}

func copyDirectoryCount(source, target string) (int, error) {
	count := 0
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, err
	}
	err := filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(target, relative), 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSkillPackageFileSize {
			return nil
		}
		if err := copyFile(filePath, filepath.Join(target, relative)); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func countFiles(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && filePath != root {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	return count, err
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func insidePath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..")
}
