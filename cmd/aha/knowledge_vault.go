package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

type knowledgeVaultCommon struct {
	dataDir   string
	vaultPath string
	indexPath string
}

func (options *knowledgeVaultCommon) register(flags *flag.FlagSet) {
	flags.StringVar(&options.dataDir, "data-dir", envOr("AHA2_DATA_DIR", ".data"), "AHA2 data directory")
	flags.StringVar(&options.vaultPath, "vault", os.Getenv("AHA2_KNOWLEDGE_VAULT_PATH"), "vault directory (default <data-dir>/knowledge-vault)")
	flags.StringVar(&options.indexPath, "index", os.Getenv("AHA2_KNOWLEDGE_VAULT_INDEX"), "index database (default <data-dir>/knowledge-vault-index.db)")
}

func (options knowledgeVaultCommon) config() (knowledgevault.Config, error) {
	config := knowledgevault.ConfigFromEnv(options.dataDir)
	config.DataDir = options.dataDir
	if strings.TrimSpace(options.vaultPath) != "" {
		config.VaultPath = options.vaultPath
	}
	if strings.TrimSpace(options.indexPath) != "" {
		config.IndexPath = options.indexPath
	}
	return config.Normalized()
}

func knowledgeVault(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: aha2 knowledge-vault <init|export|rebuild-index|search|status> [flags]")
	}
	switch args[0] {
	case "init":
		return knowledgeVaultInit(args[1:])
	case "export":
		return knowledgeVaultExport(args[1:])
	case "rebuild-index":
		return knowledgeVaultRebuildIndex(args[1:])
	case "search":
		return knowledgeVaultSearch(args[1:])
	case "status":
		return knowledgeVaultStatus(args[1:])
	default:
		return fmt.Errorf("unknown knowledge-vault command %q", args[0])
	}
}

func writeKnowledgeVaultReport(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func knowledgeVaultInit(args []string) error {
	flags := flag.NewFlagSet("aha2 knowledge-vault init", flag.ContinueOnError)
	options := knowledgeVaultCommon{}
	options.register(flags)
	vaultName := flags.String("name", "", "vault display name (defaults to the directory name)")
	commit := flags.Bool("commit", true, "initialize a local Git repository and commit the initial structure")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config, err := options.config()
	if err != nil {
		return err
	}
	report, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: *vaultName, Commit: *commit})
	if err != nil {
		return err
	}
	return writeKnowledgeVaultReport(os.Stdout, report)
}

func knowledgeVaultExport(args []string) error {
	flags := flag.NewFlagSet("aha2 knowledge-vault export", flag.ContinueOnError)
	options := knowledgeVaultCommon{}
	options.register(flags)
	databasePath := flags.String("database", "", "AHA2 database path (default <data-dir>/aha2.db)")
	commit := flags.Bool("commit", true, "commit exported files to the local vault Git repository")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config, err := options.config()
	if err != nil {
		return err
	}
	if strings.TrimSpace(*databasePath) == "" {
		*databasePath = filepath.Join(config.DataDir, "aha2.db")
	}
	report, err := knowledgevault.Export(context.Background(), config, *databasePath, knowledgevault.ExportOptions{Commit: *commit})
	if err != nil {
		return err
	}
	return writeKnowledgeVaultReport(os.Stdout, report)
}

func knowledgeVaultRebuildIndex(args []string) error {
	flags := flag.NewFlagSet("aha2 knowledge-vault rebuild-index", flag.ContinueOnError)
	options := knowledgeVaultCommon{}
	options.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config, err := options.config()
	if err != nil {
		return err
	}
	ctx := context.Background()
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		return err
	}
	defer index.Close()
	report, err := index.Rebuild(ctx, config.VaultPath)
	if err != nil {
		return err
	}
	return writeKnowledgeVaultReport(os.Stdout, report)
}

func knowledgeVaultSearch(args []string) error {
	query := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		query = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("aha2 knowledge-vault search", flag.ContinueOnError)
	options := knowledgeVaultCommon{}
	options.register(flags)
	scope := flags.String("scope", "", "restrict to global, project, archive, or unknown")
	projectID := flags.String("project", "", "restrict to one project ID")
	limit := flags.Int("limit", 20, "maximum results (1-200)")
	includeArchives := flags.Bool("include-archives", false, "include AHA1 archive libraries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if query == "" {
		if flags.NArg() != 1 {
			return fmt.Errorf("search requires exactly one query argument")
		}
		query = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return fmt.Errorf("search accepts exactly one query argument")
	}
	config, err := options.config()
	if err != nil {
		return err
	}
	ctx := context.Background()
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		return err
	}
	defer index.Close()
	results, err := index.Search(ctx, query, knowledgevault.SearchOptions{
		Scope: *scope, ProjectID: *projectID, Limit: *limit, IncludeArchives: *includeArchives,
	})
	if err != nil {
		return err
	}
	return writeKnowledgeVaultReport(os.Stdout, results)
}

func knowledgeVaultStatus(args []string) error {
	flags := flag.NewFlagSet("aha2 knowledge-vault status", flag.ContinueOnError)
	options := knowledgeVaultCommon{}
	options.register(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config, err := options.config()
	if err != nil {
		return err
	}
	type statusReport struct {
		Enabled      bool                        `json:"enabled"`
		VaultPath    string                      `json:"vault_path"`
		VaultExists  bool                        `json:"vault_exists"`
		IndexPath    string                      `json:"index_path"`
		IndexExists  bool                        `json:"index_exists"`
		GitAvailable bool                        `json:"git_available"`
		Manifest     *knowledgevault.Manifest    `json:"manifest,omitempty"`
		Index        *knowledgevault.IndexStatus `json:"index,omitempty"`
		GeneratedAt  string                      `json:"generated_at"`
	}
	report := statusReport{
		Enabled: config.Enabled, VaultPath: config.VaultPath, IndexPath: config.IndexPath,
		VaultExists: dirExists(config.VaultPath), IndexExists: fileExists(config.IndexPath),
		GitAvailable: commandAvailable("git"), GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if report.VaultExists {
		if manifest, readErr := knowledgevault.ReadManifest(config.VaultPath); readErr == nil {
			report.Manifest = &manifest
		}
	}
	if report.IndexExists {
		ctx := context.Background()
		index, openErr := knowledgevault.OpenIndex(ctx, config.IndexPath)
		if openErr != nil {
			return openErr
		}
		defer index.Close()
		status, statusErr := index.Status(ctx)
		if statusErr != nil {
			return statusErr
		}
		report.Index = &status
	}
	return writeKnowledgeVaultReport(os.Stdout, report)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
