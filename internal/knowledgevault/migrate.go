package knowledgevault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Legacy migration moves a pre-vault AHA2 database into a fresh file vault.
// The old database and skill directories are backed up first and never
// deleted; a failed run leaves the legacy data authoritative.
const (
	MigrationSourceLegacy   = "legacy-migration"
	ImportSourceExisting    = "existing-vault"
	defaultMigrationBackups = "backups"
)

var (
	// ErrNoLegacyKnowledge is returned when there is nothing to migrate.
	ErrNoLegacyKnowledge = errors.New("no legacy knowledge or skills were found")
	// ErrTargetNotEmpty protects an existing vault from an implicit merge.
	ErrTargetNotEmpty = errors.New("knowledge vault target already contains files; refusing to merge")
	// ErrImportSourceMissing reports a missing local directory or unusable Git remote.
	ErrImportSourceMissing = errors.New("existing knowledge vault source is missing")
	// ErrImportManifestMissing rejects a directory that is not an AHA2 vault.
	ErrImportManifestMissing = errors.New("existing knowledge vault has no .aha-vault.json manifest")
	// ErrMigrationVerification marks an export that wrote files but failed its
	// post-rebuild count/title/hash verification.
	ErrMigrationVerification = errors.New("knowledge migration verification failed")
)

type LegacyMigrationOptions struct {
	DataDir      string
	DatabasePath string
	BackupDir    string
	Commit       bool
	Now          func() time.Time
}

type LegacyMigrationReport struct {
	Source          string    `json:"source"`
	VaultPath       string    `json:"vault_path"`
	DatabasePath    string    `json:"database_path"`
	BackupPath      string    `json:"backup_path"`
	SourceEntries   int       `json:"source_entries"`
	ExportedEntries int       `json:"exported_entries"`
	IndexedEntries  int       `json:"indexed_entries"`
	VerifiedEntries int       `json:"verified_entries"`
	TitlesVerified  int       `json:"titles_verified"`
	HashesVerified  int       `json:"hashes_verified"`
	SampledEntries  []string  `json:"sampled_entries,omitempty"`
	SourceSkills    int       `json:"source_skills"`
	ExportedSkills  int       `json:"exported_skills"`
	VerifiedSkills  int       `json:"verified_skills"`
	Warnings        []string  `json:"warnings,omitempty"`
	VerifiedAt      time.Time `json:"verified_at"`
}

type legacySkill struct {
	ID           string
	PackageSlug  string
	Scope        string
	ProjectID    string
	Name         string
	Description  string
	Instructions string
}

// MigrateLegacyKnowledge performs the full upgrade path: back up the database
// and old skill packages, export knowledge and skills into the vault, rebuild
// the index and verify counts, titles, content hashes and a deterministic
// sample. It never deletes or edits the legacy data.
func MigrateLegacyKnowledge(ctx context.Context, cfg Config, options LegacyMigrationOptions) (LegacyMigrationReport, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return LegacyMigrationReport{}, err
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	databasePath := strings.TrimSpace(options.DatabasePath)
	if databasePath == "" {
		databasePath = filepath.Join(cfg.DataDir, "aha2.db")
	}
	report := LegacyMigrationReport{
		Source: MigrationSourceLegacy, VaultPath: cfg.VaultPath, DatabasePath: databasePath,
	}
	source, err := loadSource(ctx, databasePath)
	if err != nil {
		return report, err
	}
	skills, err := loadLegacySkills(databasePath)
	if err != nil {
		return report, err
	}
	report.SourceEntries = len(source.Entries)
	report.SourceSkills = len(skills)
	if substantiveLegacyEntries(source.Entries) == 0 && len(skills) == 0 {
		// Every install seeds navigation roots/categories. They are not legacy
		// knowledge and must not turn a fresh install into a migration target.
		return report, ErrNoLegacyKnowledge
	}
	// An upgraded install may already be running on a populated vault whose
	// verification metadata is absent. Exporting the legacy database over it
	// would silently replace the owner's current pages, so refuse before the
	// backup/export writes anything. The legacy data itself is left untouched.
	if vaultHasContent(ctx, cfg) {
		return report, fmt.Errorf("%w: %s already contains indexed entries or knowledge pages; legacy migration was not started", ErrTargetNotEmpty, cfg.VaultPath)
	}
	backupDir := strings.TrimSpace(options.BackupDir)
	if backupDir == "" {
		backupDir = filepath.Join(cfg.DataDir, defaultMigrationBackups)
	}
	backupPath, err := backupLegacyKnowledge(ctx, databasePath, cfg.DataDir, backupDir, now)
	if err != nil {
		return report, fmt.Errorf("back up legacy knowledge: %w", err)
	}
	report.BackupPath = backupPath

	exportReport, err := Export(ctx, cfg, databasePath, ExportOptions{Commit: options.Commit, Now: func() time.Time { return now }})
	if err != nil {
		return report, err
	}
	report.ExportedEntries = exportReport.AccountedEntries
	report.Warnings = append(report.Warnings, exportReport.Warnings...)
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return report, fmt.Errorf("read exported vault manifest: %w", err)
	}
	exportedSkills, skillWarnings := exportLegacySkills(cfg, manifest, skills)
	report.ExportedSkills = exportedSkills
	report.Warnings = append(report.Warnings, skillWarnings...)

	index, err := OpenIndex(ctx, cfg.IndexPath)
	if err != nil {
		return report, fmt.Errorf("open knowledge index: %w", err)
	}
	if _, err := index.Rebuild(ctx, cfg.VaultPath); err != nil {
		_ = index.Close()
		return report, fmt.Errorf("rebuild knowledge index: %w", err)
	}
	indexed, err := index.Entries(ctx, "", "")
	if err != nil {
		_ = index.Close()
		return report, fmt.Errorf("read rebuilt index: %w", err)
	}
	_ = index.Close()
	report.IndexedEntries = len(indexed)

	verified, sampled, verifyWarnings, err := verifyLegacyMigration(cfg, source, skills, indexed)
	report.VerifiedEntries = verified.entries
	report.TitlesVerified = verified.titles
	report.HashesVerified = verified.hashes
	report.SampledEntries = sampled
	report.VerifiedSkills = verified.skills
	report.Warnings = append(report.Warnings, verifyWarnings...)
	if err != nil {
		return report, err
	}
	report.VerifiedAt = now
	return report, nil
}

// vaultHasContent reports whether legacy migration would overwrite owner
// content: any indexed entry or any Markdown page besides the generated
// README. Hidden state directories are not knowledge, but skills are counted
// because the migration copies legacy skill packages into them. The check is
// deliberately conservative; the caller refuses rather than merging.
func vaultHasContent(ctx context.Context, cfg Config) bool {
	if info, err := os.Stat(cfg.IndexPath); err == nil && !info.IsDir() {
		if index, openErr := openReadOnlyIndex(ctx, cfg.IndexPath); openErr == nil {
			if status, statusErr := index.Status(ctx); statusErr == nil && status.Entries > 0 {
				_ = index.Close()
				return true
			}
			_ = index.Close()
		}
	}
	found := false
	_ = filepath.WalkDir(cfg.VaultPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if filePath == cfg.VaultPath {
				return nil
			}
			switch strings.ToLower(entry.Name()) {
			case ".aha", ".git", ".trash", ".obsidian":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		if strings.EqualFold(entry.Name(), readmeFileName) {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	return found
}

type migrationVerification struct {
	entries int
	titles  int
	hashes  int
	skills  int
}

func verifyLegacyMigration(cfg Config, source sourceData, skills []legacySkill, indexed []IndexEntry) (migrationVerification, []string, []string, error) {
	result := migrationVerification{}
	byID := map[string]IndexEntry{}
	for _, entry := range indexed {
		byID[entry.ID] = entry
	}
	warnings := []string{}
	for _, item := range source.Entries {
		entry, ok := byID[item.ID]
		if !ok {
			return result, nil, warnings, fmt.Errorf("%w: entry %s is missing from the rebuilt index", ErrMigrationVerification, item.ID)
		}
		if strings.TrimSpace(entry.Title) != strings.TrimSpace(item.Title) {
			return result, nil, warnings, fmt.Errorf("%w: entry %s title = %q, want %q", ErrMigrationVerification, item.ID, entry.Title, item.Title)
		}
		result.titles++
		filePath := filepath.Join(cfg.VaultPath, filepath.FromSlash(entry.Path))
		raw, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return result, nil, warnings, fmt.Errorf("%w: read %s: %v", ErrMigrationVerification, entry.Path, readErr)
		}
		sum := sha256.Sum256(raw)
		if strings.TrimSpace(entry.ContentHash) != hex.EncodeToString(sum[:]) {
			return result, nil, warnings, fmt.Errorf("%w: %s content hash does not match the file", ErrMigrationVerification, entry.Path)
		}
		result.hashes++
	}
	result.entries = len(source.Entries)
	sampled := sampleEntries(source.Entries)
	sampledIDs := make([]string, 0, len(sampled))
	for _, item := range sampled {
		sampledIDs = append(sampledIDs, item.ID)
		entry, ok := byID[item.ID]
		if !ok {
			continue
		}
		probe := firstSubstantialLine(item.Body)
		if probe != "" && !strings.Contains(strings.ReplaceAll(entry.Body, "\r\n", "\n"), probe) {
			return result, sampledIDs, warnings, fmt.Errorf("%w: sampled entry %s body did not survive the export", ErrMigrationVerification, item.ID)
		}
	}
	for _, item := range skills {
		slug := strings.TrimSpace(item.PackageSlug)
		if slug == "" {
			slug = strings.TrimSpace(item.ID)
		}
		if !skillDirectoryExists(cfg, item) {
			return result, nil, warnings, fmt.Errorf("%w: skill %s was not written to the vault", ErrMigrationVerification, slug)
		}
		result.skills++
	}
	return result, sampledIDs, warnings, nil
}

// substantiveLegacyEntries counts documents an owner would recognize as
// knowledge. Navigation roots/categories are generated on every install.
func substantiveLegacyEntries(entries []sourceEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.IsIndex || strings.EqualFold(strings.TrimSpace(entry.Type), "navigation") {
			continue
		}
		count++
	}
	return count
}

func sampleEntries(entries []sourceEntry) []sourceEntry {
	documents := make([]sourceEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsIndex {
			documents = append(documents, entry)
		}
	}
	if len(documents) <= 5 {
		return documents
	}
	indices := []int{0, len(documents) / 4, len(documents) / 2, len(documents) * 3 / 4, len(documents) - 1}
	seen := map[int]bool{}
	result := []sourceEntry{}
	for _, index := range indices {
		if index < 0 || index >= len(documents) || seen[index] {
			continue
		}
		seen[index] = true
		result = append(result, documents[index])
	}
	return result
}

func firstSubstantialLine(body string) string {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if len([]rune(trimmed)) >= 8 {
			return trimmed
		}
	}
	return ""
}

func backupLegacyKnowledge(ctx context.Context, databasePath, dataDir, backupDir string, now time.Time) (string, error) {
	name := "knowledge-migration-" + now.Format("20060102-150405")
	target := filepath.Join(backupDir, name)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", err
	}
	backupDB := filepath.Join(target, "aha2.db")
	_ = os.Remove(backupDB)
	db, err := sql.Open("sqlite", SQLiteFileDSN(databasePath, "_pragma=busy_timeout(5000)"))
	if err != nil {
		return "", err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, backupDB); err != nil {
		db.Close()
		// A read-only or locked database can still be copied byte-for-byte.
		// SQLite's WAL is checkpointed by VACUUM, so this is a last resort.
		if copyErr := copyFile(databasePath, backupDB); copyErr != nil {
			return "", fmt.Errorf("vacuum backup failed (%v) and file copy failed: %w", err, copyErr)
		}
	} else {
		db.Close()
	}
	legacySkills := filepath.Join(dataDir, "knowledge", "skills")
	if info, statErr := os.Stat(legacySkills); statErr == nil && info.IsDir() {
		if err := copyDirectory(legacySkills, filepath.Join(target, "knowledge", "skills")); err != nil {
			return "", err
		}
	}
	return target, nil
}

func loadLegacySkills(databasePath string) ([]legacySkill, error) {
	db, err := openReadOnlySQLite(databasePath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,package_slug,scope,project_id,name,description,instructions FROM skills ORDER BY id`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("read legacy skills: %w", err)
	}
	defer rows.Close()
	result := []legacySkill{}
	for rows.Next() {
		var item legacySkill
		if err := rows.Scan(&item.ID, &item.PackageSlug, &item.Scope, &item.ProjectID, &item.Name, &item.Description, &item.Instructions); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func exportLegacySkills(cfg Config, manifest Manifest, skills []legacySkill) (int, []string) {
	warnings := []string{}
	written := 0
	for _, item := range skills {
		slug := strings.TrimSpace(item.PackageSlug)
		if slug == "" {
			slug = strings.TrimSpace(item.ID)
		}
		slug = SafeSegment(slug, "skill")
		var relative string
		switch strings.ToLower(strings.TrimSpace(item.Scope)) {
		case "global":
			relative = path.Join("global", "skills", slug)
		case "project":
			projectSlug := strings.TrimSpace(manifest.Projects[strings.TrimSpace(item.ProjectID)])
			if projectSlug == "" {
				warnings = append(warnings, fmt.Sprintf("skill %s: project %s is not bound to a vault project; keeping the legacy package", slug, item.ProjectID))
				continue
			}
			relative = path.Join("projects", projectSlug, "skills", slug)
		default:
			warnings = append(warnings, fmt.Sprintf("skill %s: unsupported scope %q; keeping the legacy package", slug, item.Scope))
			continue
		}
		sourceDir := filepath.Join(cfg.DataDir, "knowledge", "skills", slug)
		targetDir := filepath.Join(cfg.VaultPath, filepath.FromSlash(relative))
		if info, err := os.Stat(sourceDir); err == nil && info.IsDir() {
			if err := copyDirectory(sourceDir, targetDir); err != nil {
				warnings = append(warnings, fmt.Sprintf("skill %s copy: %v", slug, err))
				continue
			}
			written++
			continue
		}
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			warnings = append(warnings, fmt.Sprintf("skill %s: %v", slug, err))
			continue
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", item.Name, item.Description, strings.TrimSpace(item.Instructions))
		if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			warnings = append(warnings, fmt.Sprintf("skill %s: %v", slug, err))
			continue
		}
		written++
	}
	return written, warnings
}

func skillDirectoryExists(cfg Config, item legacySkill) bool {
	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		return false
	}
	slug := strings.TrimSpace(item.PackageSlug)
	if slug == "" {
		slug = strings.TrimSpace(item.ID)
	}
	slug = SafeSegment(slug, "skill")
	var relative string
	if strings.EqualFold(item.Scope, "global") {
		relative = path.Join("global", "skills", slug)
	} else {
		projectSlug := manifest.Projects[strings.TrimSpace(item.ProjectID)]
		if projectSlug == "" {
			return false
		}
		relative = path.Join("projects", projectSlug, "skills", slug)
	}
	info, err := os.Stat(filepath.Join(cfg.VaultPath, filepath.FromSlash(relative), "SKILL.md"))
	return err == nil && !info.IsDir()
}

func copyDirectory(source, target string) error {
	return filepath.WalkDir(source, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSkillPackageFileSize {
			return nil
		}
		return copyFile(filePath, destination)
	})
}

func copyFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}
