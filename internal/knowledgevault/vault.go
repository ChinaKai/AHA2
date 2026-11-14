package knowledgevault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ManifestVersion  = 1
	manifestFileName = ".aha-vault.json"
	readmeFileName   = "README.md"
	stateDirectory   = ".aha"
	exportStateFile  = "export-state.json"
)

// Config is the Phase 1 feature switch and the two filesystem locations it
// owns. Enabled defaults to false; nothing in the legacy Web UI or API reads
// this value yet.
type Config struct {
	Enabled   bool
	DataDir   string
	VaultPath string
	IndexPath string
	// AllowLocalRemote is a test-only escape hatch for temporary bare repos.
	// Production stays SSH-only and never sets this field.
	AllowLocalRemote bool
}

// DefaultConfig keeps the vault next to the AHA2 data directory. It never
// hardcodes a drive letter.
func DefaultConfig(dataDir string) Config {
	return Config{
		Enabled:   false,
		DataDir:   dataDir,
		VaultPath: filepath.Join(dataDir, "knowledge-vault"),
		IndexPath: filepath.Join(dataDir, "knowledge-vault-index.db"),
	}
}

// ConfigFromEnv applies AHA2_KNOWLEDGE_VAULT_* overrides on top of the default.
func ConfigFromEnv(dataDir string) Config {
	config := DefaultConfig(dataDir)
	if value, ok := os.LookupEnv("AHA2_KNOWLEDGE_VAULT_ENABLED"); ok {
		config.Enabled = parseEnvBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("AHA2_KNOWLEDGE_VAULT_PATH")); value != "" {
		config.VaultPath = value
	}
	if value := strings.TrimSpace(os.Getenv("AHA2_KNOWLEDGE_VAULT_INDEX")); value != "" {
		config.IndexPath = value
	}
	return config
}

func parseEnvBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

// Normalized returns absolute paths and fills in defaults for a partial Config.
func (c Config) Normalized() (Config, error) {
	if strings.TrimSpace(c.DataDir) == "" {
		c.DataDir = "."
	}
	dataDir, err := filepath.Abs(c.DataDir)
	if err != nil {
		return Config{}, err
	}
	c.DataDir = dataDir
	if strings.TrimSpace(c.VaultPath) == "" {
		c.VaultPath = filepath.Join(dataDir, "knowledge-vault")
	} else if c.VaultPath, err = filepath.Abs(c.VaultPath); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(c.IndexPath) == "" {
		c.IndexPath = filepath.Join(dataDir, "knowledge-vault-index.db")
	} else if c.IndexPath, err = filepath.Abs(c.IndexPath); err != nil {
		return Config{}, err
	}
	return c, nil
}

// SkillLocation records where a flat vault skill belongs. Phase 1 does not
// migrate skills; the map exists so later phases can keep skills flat under
// skills/<slug> while still knowing their scope and project.
type SkillLocation struct {
	Scope     string `json:"scope,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

// Manifest is the machine-readable vault identity used by later phases to
// generate Obsidian links and to map stable project slugs back to IDs.
type Manifest struct {
	Version          int                      `json:"version"`
	VaultID          string                   `json:"vault_id"`
	VaultName        string                   `json:"vault_name"`
	ObsidianVault    string                   `json:"obsidian_vault"`
	Projects         map[string]string        `json:"projects"`
	ProjectNames     map[string]string        `json:"project_names,omitempty"`
	Archives         map[string]string        `json:"archives,omitempty"`
	ArchiveNames     map[string]string        `json:"archive_names,omitempty"`
	Skills           map[string]SkillLocation `json:"skills"`
	Reserved         []string                 `json:"reserved"`
	ReservedPatterns []string                 `json:"reserved_patterns,omitempty"`
	ReservedFiles    []string                 `json:"reserved_files"`
	CreatedAt        string                   `json:"created_at,omitempty"`
	UpdatedAt        string                   `json:"updated_at,omitempty"`
	Export           *ExportSummary           `json:"export,omitempty"`
}

type ExportSummary struct {
	Total            int    `json:"total"`
	Global           int    `json:"global"`
	Projects         int    `json:"projects"`
	Archives         int    `json:"archives"`
	ArchiveLibraries int    `json:"archive_libraries,omitempty"`
	Orphans          int    `json:"orphans,omitempty"`
	ProjectInbox     int    `json:"project_inbox"`
	Groups           int    `json:"groups"`
	ExportedAt       string `json:"exported_at,omitempty"`
}

func defaultManifest(vaultPath, vaultName string, now time.Time) Manifest {
	name := strings.TrimSpace(vaultName)
	if name == "" {
		name = filepath.Base(vaultPath)
	}
	return Manifest{
		Version:       ManifestVersion,
		VaultID:       newVaultID(),
		VaultName:     name,
		ObsidianVault: name,
		Projects:      map[string]string{},
		ProjectNames:  map[string]string{},
		Archives:      map[string]string{},
		ArchiveNames:  map[string]string{},
		Skills:        map[string]SkillLocation{},
		Reserved: []string{
			".trash", ".aha", ".git", ".obsidian", "orphans", "skills",
			"global/skills", "global/inbox",
		},
		ReservedPatterns: []string{
			"projects/*/skills", "projects/*/inbox",
			"archive/*/skills", "archive/*/inbox",
		},
		ReservedFiles: []string{manifestFileName, readmeFileName, ".gitignore"},
		CreatedAt:     now.UTC().Format(time.RFC3339),
		UpdatedAt:     now.UTC().Format(time.RFC3339),
	}
}

func newVaultID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "vault-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buffer)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// ReadManifest reads the vault manifest. A missing manifest is reported as a
// distinct error so callers can decide whether to create one.
func ReadManifest(vaultPath string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(vaultPath, manifestFileName))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", manifestFileName, err)
	}
	if manifest.Projects == nil {
		manifest.Projects = map[string]string{}
	}
	if manifest.ProjectNames == nil {
		manifest.ProjectNames = map[string]string{}
	}
	if manifest.Archives == nil {
		manifest.Archives = map[string]string{}
	}
	if manifest.ArchiveNames == nil {
		manifest.ArchiveNames = map[string]string{}
	}
	if manifest.Skills == nil {
		manifest.Skills = map[string]SkillLocation{}
	}
	return manifest, nil
}

func writeManifest(vaultPath string, manifest Manifest) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeVaultFileIfChanged(vaultPath, filepath.Join(vaultPath, manifestFileName), encoded, 0o644)
}

func manifestsEquivalent(left, right Manifest) bool {
	leftCopy, rightCopy := left, right
	leftCopy.UpdatedAt, rightCopy.UpdatedAt = "", ""
	leftCopy.CreatedAt, rightCopy.CreatedAt = "", ""
	leftEncoded, _ := json.Marshal(leftCopy)
	rightEncoded, _ := json.Marshal(rightCopy)
	return string(leftEncoded) == string(rightEncoded)
}

// InitReport describes what Bootstrap created.
type InitReport struct {
	VaultPath    string   `json:"vault_path"`
	ManifestPath string   `json:"manifest_path"`
	VaultID      string   `json:"vault_id"`
	VaultName    string   `json:"vault_name"`
	Created      []string `json:"created"`
	GitWarning   string   `json:"git_warning,omitempty"`
	Committed    bool     `json:"committed"`
}

// InitOptions controls bootstrap behavior in tests and for manual commands.
type InitOptions struct {
	VaultName string
	Commit    bool
	Now       func() time.Time
}

// Bootstrap creates the directory skeleton, manifest, README, placeholder
// root index, and (when git is available) the first local commit.
func Bootstrap(cfg Config, options InitOptions) (InitReport, error) {
	cfg, err := cfg.Normalized()
	if err != nil {
		return InitReport{}, err
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	report := InitReport{VaultPath: cfg.VaultPath, ManifestPath: filepath.Join(cfg.VaultPath, manifestFileName)}

	directories := []string{
		"", "global", "global/general", "global/agent-lessons", "global/skills", "global/inbox",
		"projects", "skills", ".trash", stateDirectory,
	}
	for _, relative := range directories {
		path := filepath.Join(cfg.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(path, 0o755); err != nil {
			return report, fmt.Errorf("create vault directory %s: %w", relative, err)
		}
	}

	manifest, err := ReadManifest(cfg.VaultPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return report, err
		}
		manifest = defaultManifest(cfg.VaultPath, options.VaultName, now)
		report.Created = append(report.Created, manifestFileName)
	}
	if strings.TrimSpace(options.VaultName) != "" && options.VaultName != manifest.VaultName {
		manifest.VaultName = options.VaultName
		manifest.ObsidianVault = options.VaultName
	}
	if err := writeManifest(cfg.VaultPath, manifest); err != nil {
		return report, err
	}
	report.VaultID = manifest.VaultID
	report.VaultName = manifest.VaultName

	readmePath := filepath.Join(cfg.VaultPath, readmeFileName)
	if err := writeVaultFileIfChanged(cfg.VaultPath, readmePath, []byte(vaultReadme(manifest)), 0o644); err != nil {
		return report, err
	}
	gitignorePath := filepath.Join(cfg.VaultPath, ".gitignore")
	gitignore := gitIgnoreBody
	if err := writeVaultFileIfChanged(cfg.VaultPath, gitignorePath, []byte(gitignore), 0o644); err != nil {
		return report, err
	}
	for _, relative := range []string{"global/general", "global/agent-lessons", "global/skills", "global/inbox", "projects", "skills", ".trash"} {
		keep := filepath.Join(cfg.VaultPath, filepath.FromSlash(relative), ".gitkeep")
		if err := writeVaultFileIfChanged(cfg.VaultPath, keep, []byte{}, 0o644); err != nil {
			return report, err
		}
	}

	globalIndex := filepath.Join(cfg.VaultPath, "global", "index.md")
	if _, err := os.Stat(globalIndex); os.IsNotExist(err) {
		content, renderErr := renderDocument(Frontmatter{
			ID: "knowledge_root_global", Title: "全局知识首页", Slug: "index",
			Scope: "global", Type: TypeNavigation, Status: "verified",
			IsIndex: boolPointer(true), Created: now.Format(time.RFC3339), Updated: now.Format(time.RFC3339),
		}, defaultGlobalRootBody)
		if renderErr != nil {
			return report, renderErr
		}
		if err := writeVaultFileIfChanged(cfg.VaultPath, globalIndex, []byte(content), 0o644); err != nil {
			return report, err
		}
		report.Created = append(report.Created, "global/index.md")
	}

	if options.Commit {
		warning, committed := commitVault(cfg.VaultPath, "Initialize AHA2 knowledge vault")
		report.GitWarning = warning
		report.Committed = committed
	}
	return report, nil
}

const defaultGlobalRootBody = "这是 AHA2 全局知识页面。通用知识以人类阅读为主，Agent 仅在任务主题明确相关时按需读取；Agent 经验教训用于沉淀可跨项目复用的技术诊断与行为教训。请根据分类、标题与摘要选择阅读路径。"

func vaultReadme(manifest Manifest) string {
	return `# ` + manifest.VaultName + `

这是 AHA2 知识库的文件正本（Phase 1 staging）。**文件是知识正本，数据库只是可重建的索引。**

## 结构

- ` + "`global/index.md`" + `：全局知识入口。
- ` + "`global/general/`" + `：面向人类阅读的跨项目参考资料。
- ` + "`global/agent-lessons/`" + `：可复用的 Agent 技术诊断与行为教训。
- ` + "`global/inbox/`" + `：未归类到主题组的全局文档。
- ` + "`projects/<slug>/index.md`" + `：项目知识入口；主题组是子目录，根下叶子文档在 ` + "`inbox/`" + `。
- ` + "`archive/<slug>/`" + `：AHA1 归档知识库，单独标记，不并入主库。
- ` + "`orphans/`" + `：导出时找不到所属项目或 scope 的孤儿条目隔离区；正文保留，报告逐条列出。
- ` + "`skills/<slug>/`" + `：技能包的扁平预留位置；Phase 1 不迁移技能。
- ` + "`.trash/`" + `：删除暂存，不作为有效知识。
- ` + "`.aha-vault.json`" + `：vault 身份、项目 ID 与稳定目录 slug 的映射。

## Frontmatter

文档使用 YAML frontmatter，可选字段包括 ` + "`id`、`title`、`slug`、`scope`、`project_id`、`type`、`tags`、`status`、`parent`、`created`、`updated`、`source_task`、`source_turn`、`confidence`、`legacy_type`、`trust`" + `。
规范 ` + "`type`" + ` 只有六种：` + "`navigation`、`decision`、`practice`、`diagnostic`、`reference`、`architecture`" + `。旧类型保留在 ` + "`legacy_type`" + `。

## Index 与搜索

索引是独立的 SQLite 文件，不在 vault 内，也不改变旧知识表。入口文档的 ` + "`## 文档`" + ` 区块由索引生成；重新构建索引时该区块会被忽略，正文本身保持稳定。

## Git 与 Obsidian

vault 根目录是一个本地 Git 仓库（无远端）。目录、frontmatter 和 ` + "`[[wiki链接]]`" + ` 可以直接作为 Obsidian vault 打开，不需要预置 ` + "`.obsidian/`" + ` 目录。
`
}

func boolPointer(value bool) *bool        { return &value }
func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }

func renderDocument(frontmatter Frontmatter, body string) (string, error) {
	header, err := renderFrontmatter(frontmatter)
	if err != nil {
		return "", err
	}
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n \t")
	if body == "" {
		return header, nil
	}
	return header + "\n" + body + "\n", nil
}

// ensureSafeWriteTarget verifies that a write cannot leave the vault through a
// symlinked parent directory and refuses to overwrite an existing symlink.
// Lexical containment alone is not enough because a directory inside the vault
// may point elsewhere.
func ensureSafeWriteTarget(root, target string) error {
	root = filepath.Clean(root)
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write outside vault root: %s", target)
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if !IsInsideRoot(root, directory) {
		return fmt.Errorf("refusing to write through a symlink outside the vault: %s", target)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite a symbolic link: %s", target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeVaultFileIfChanged(root, path string, content []byte, mode os.FileMode) error {
	if err := ensureSafeWriteTarget(root, path); err != nil {
		return err
	}
	return writeFileIfChanged(path, content, mode)
}

func writeFileIfChanged(path string, content []byte, mode os.FileMode) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(content) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	// Use a random, exclusively-created temporary file in the target
	// directory. A deterministic "<path>.tmp" can be pre-planted as a
	// symlink, which os.WriteFile would follow and overwrite outside the
	// vault before the rename.
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

// commitVault initializes a local (never remote) Git repository when needed and
// commits all changes. Missing git is not fatal: the caller records the
// warning and continues with a plain directory.
func commitVault(vaultPath, message string) (string, bool) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "git is not available; vault remains a plain directory", false
	}
	run := func(args ...string) (string, error) {
		command := exec.Command(gitPath, append([]string{"-C", vaultPath}, args...)...)
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		output, err := command.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".git")); os.IsNotExist(err) {
		if output, initErr := run("init", "-q"); initErr != nil {
			return "git init failed: " + firstLine(output), false
		}
	}
	if output, addErr := run("add", "-A"); addErr != nil {
		return "git add failed: " + firstLine(output), false
	}
	output, commitErr := run("-c", "user.name=AHA2", "-c", "user.email=aha2@localhost", "commit", "-q", "-m", message)
	if commitErr != nil {
		combined := strings.ToLower(output)
		if strings.Contains(combined, "nothing to commit") || strings.Contains(combined, "no changes added") {
			return "", false
		}
		return "git commit failed: " + firstLine(output), false
	}
	return "", true
}

// SortedKeys returns manifest keys in deterministic order.
func SortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
