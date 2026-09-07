package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/legacyimport"
	"github.com/ChinaKai/AHA2/internal/store"
)

type projectMappings map[string]string

func (values *projectMappings) String() string {
	items := make([]string, 0, len(*values))
	for source, target := range *values {
		items = append(items, source+"="+target)
	}
	return strings.Join(items, ",")
}

func (values *projectMappings) Set(value string) error {
	parts := strings.SplitN(strings.TrimSpace(value), "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("project map must be LEGACY_PROJECT_KEY=AHA2_PROJECT_ID")
	}
	if *values == nil {
		*values = projectMappings{}
	}
	if _, exists := (*values)[strings.TrimSpace(parts[0])]; exists {
		return fmt.Errorf("duplicate project map for %s", strings.TrimSpace(parts[0]))
	}
	(*values)[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	return nil
}

type importAHA1Options struct {
	sourceDir             string
	dataDir               string
	apply                 bool
	confirmServiceStopped bool
	projectMap            projectMappings
}

func parseImportAHA1Options(args []string) (importAHA1Options, error) {
	flags := flag.NewFlagSet("aha2 import-aha1-knowledge", flag.ContinueOnError)
	options := importAHA1Options{}
	flags.StringVar(&options.sourceDir, "source", "", "AHA1 .aha/knowledge directory")
	flags.StringVar(&options.dataDir, "data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	flags.BoolVar(&options.apply, "apply", false, "apply the import; the default is a read-only dry-run")
	flags.BoolVar(&options.confirmServiceStopped, "confirm-service-stopped", false, "confirm no AHA2 process is using the target data directory")
	flags.Var(&options.projectMap, "project-map", "explicit LEGACY_PROJECT_KEY=AHA2_PROJECT_ID mapping; repeat as needed")
	if err := flags.Parse(args); err != nil {
		return importAHA1Options{}, err
	}
	if flags.NArg() != 0 {
		return importAHA1Options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(options.sourceDir) == "" {
		return importAHA1Options{}, fmt.Errorf("--source is required")
	}
	if options.apply && !options.confirmServiceStopped {
		return importAHA1Options{}, fmt.Errorf("--apply requires --confirm-service-stopped after AHA2 has been stopped")
	}
	return options, nil
}

func importAHA1Knowledge(args []string) error {
	options, err := parseImportAHA1Options(args)
	if err != nil {
		return err
	}
	dataDir, err := filepath.Abs(options.dataDir)
	if err != nil {
		return err
	}
	databasePath := filepath.Join(dataDir, "aha2.db")
	ctx := context.Background()
	if !options.apply {
		temporary, err := os.MkdirTemp("", "aha2-aha1-knowledge-dry-run-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temporary)
		if err := snapshotSQLiteDatabase(databasePath, filepath.Join(temporary, "aha2.db")); err != nil {
			return fmt.Errorf("snapshot dry-run target: %w", err)
		}
		skillsSource := filepath.Join(dataDir, "knowledge", "skills")
		if info, statErr := os.Stat(skillsSource); statErr == nil && info.IsDir() {
			if err := copyImportTree(skillsSource, filepath.Join(temporary, "knowledge", "skills")); err != nil {
				return err
			}
		}
		database, err := store.Open(ctx, filepath.Join(temporary, "aha2.db"))
		if err != nil {
			return fmt.Errorf("open dry-run snapshot: %w", err)
		}
		defer database.Close()
		plan, err := legacyimport.Analyze(ctx, database, legacyimport.Options{SourceDir: options.sourceDir, ProjectMap: options.projectMap})
		if err != nil {
			return err
		}
		return writeImportReport(os.Stdout, plan.Report)
	}
	backupDir, err := backupAHA2KnowledgeData(dataDir, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("backup AHA2 data: %w", err)
	}
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	plan, err := legacyimport.Analyze(ctx, database, legacyimport.Options{SourceDir: options.sourceDir, ProjectMap: options.projectMap})
	if err != nil {
		return err
	}
	report, err := legacyimport.Execute(ctx, database, plan)
	if err != nil {
		return err
	}
	if err := legacyimport.Verify(ctx, database); err != nil {
		return fmt.Errorf("verify AHA1 knowledge import: %w", err)
	}
	second, err := legacyimport.Analyze(ctx, database, legacyimport.Options{SourceDir: options.sourceDir, ProjectMap: options.projectMap})
	if err != nil {
		return fmt.Errorf("verify AHA1 knowledge import idempotence: %w", err)
	}
	if second.Report.ArchiveProjects != 0 || second.Report.KnowledgeCreate != 0 || second.Report.KnowledgeUpdate != 0 || second.Report.KnowledgeDelete != 0 || second.Report.ProposalCreate != 0 || second.Report.ProposalUpdate != 0 || second.Report.SkillCreate != 0 || second.Report.SkillUpdate != 0 {
		return fmt.Errorf("AHA1 knowledge import is not idempotent: a second dry-run still reports changes")
	}
	result := struct {
		legacyimport.Report
		BackupDir  string `json:"backup_dir"`
		Idempotent bool   `json:"idempotent"`
	}{Report: report, BackupDir: backupDir, Idempotent: true}
	return writeImportReport(os.Stdout, result)
}

func writeImportReport(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func backupAHA2KnowledgeData(dataDir string, now time.Time) (string, error) {
	absoluteDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	databasePath := filepath.Join(absoluteDataDir, "aha2.db")
	if info, err := os.Stat(databasePath); err != nil || info.IsDir() {
		return "", fmt.Errorf("AHA2 database does not exist at %s", databasePath)
	}
	backupRoot := filepath.Join(absoluteDataDir, "backups")
	backupDir := filepath.Join(backupRoot, "pre-aha1-knowledge-import-"+now.UTC().Format("20060102-150405.000000000"))
	if !pathWithin(backupRoot, backupDir) {
		return "", fmt.Errorf("backup path escapes AHA2 data directory")
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	if err := snapshotSQLiteDatabase(databasePath, filepath.Join(backupDir, "aha2.db")); err != nil {
		return "", err
	}
	skillsSource := filepath.Join(absoluteDataDir, "knowledge", "skills")
	if info, err := os.Stat(skillsSource); err == nil && info.IsDir() {
		if err := copyImportTree(skillsSource, filepath.Join(backupDir, "knowledge", "skills")); err != nil {
			return "", err
		}
	}
	return backupDir, nil
}

func snapshotSQLiteDatabase(source, target string) error {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	location := "file:" + filepath.ToSlash(absolute) + "?mode=ro"
	database, err := sql.Open("sqlite", location)
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := database.Exec("VACUUM INTO ?", target); err != nil {
		return err
	}
	return nil
}

func copyImportTree(source, target string) error {
	return filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		return copyImportFile(filePath, destination)
	})
}

func copyImportFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o600)
}

func pathWithin(base, target string) bool {
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteBase, absoluteTarget)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
