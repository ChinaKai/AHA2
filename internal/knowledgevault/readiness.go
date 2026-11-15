package knowledgevault

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Readiness is the backend-owned answer to "can the owner use the vault, and
// if not, what is missing". It is intentionally richer than a boolean: the Web
// UI renders every check so a migration, a stale index and an unbound project
// are distinguishable instead of all looking like "not ready".
type Readiness struct {
	Status          string           `json:"status"`
	CanEnable       bool             `json:"can_enable"`
	DefaultEnabled  bool             `json:"default_enabled"`
	SwitchVisible   bool             `json:"switch_visible"`
	Message         string           `json:"message,omitempty"`
	Missing         []string         `json:"missing,omitempty"`
	Checks          []ReadinessCheck `json:"checks"`
	UnboundProjects []string         `json:"unbound_projects,omitempty"`
}

type ReadinessCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

const (
	ReadinessReady          = "ready"
	ReadinessDegraded       = "degraded"
	ReadinessNeedsMigration = "needs_migration"
	ReadinessNeedsRebuild   = "needs_rebuild"
	ReadinessError          = "error"
)

const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"
)

// ReadinessInput is the full set of facts used to evaluate readiness. Vault
// facts come from Manager.Probe; legacy and verification facts come from the
// store. Keeping the evaluator pure makes every status testable without an
// HTTP server or a real database.
type ReadinessInput struct {
	VaultInitialized bool
	VaultWritable    bool
	IndexReady       bool
	IndexWritable    bool
	IndexStale       bool
	IndexError       string
	EntryCount       int
	IndexWarnings    int
	// ProjectBindings maps AHA2 project IDs to their vault slug.
	ProjectBindings map[string]string
	// KnownProjects are the active projects AHA2 knows about. A project that
	// has no vault binding keeps using legacy knowledge (degraded, not fatal).
	KnownProjects []string

	// Configured is the persisted "owner used the Web switch" flag. Enabled is
	// the live manager state. Together with a non-empty index they distinguish
	// an upgrade that is already running on the vault from an upgrade that has
	// not decided yet: only the former may treat legacy knowledge as a
	// read-only rollback copy instead of blocking on migration.
	Configured bool
	Enabled    bool

	LegacyEntries     int
	LegacySkills      int
	LegacySkillDirs   int
	MigrationVerified bool
	ImportVerified    bool
}

func (in ReadinessInput) HasLegacy() bool {
	return in.LegacyEntries > 0 || in.LegacySkills > 0 || in.LegacySkillDirs > 0
}

func (in ReadinessInput) Populated() bool {
	return in.MigrationVerified || in.ImportVerified
}

// InUse reports an upgrade that already runs on the vault: legacy knowledge is
// still present, the owner turned the manager on and the index has content. Its
// legacy database is a rollback copy, not a pending migration, so readiness
// must not send the owner to a migration that would overwrite those pages. A
// fresh install has no legacy and stays on the untouched ready path.
func (in ReadinessInput) InUse() bool {
	return in.HasLegacy() && in.Configured && in.Enabled && in.EntryCount > 0
}

// DefaultVaultEnabled is the startup default before the owner ever touched the
// switch: a fresh install defaults to the vault, while an upgrade with legacy
// knowledge stays on legacy until a migration/import is verified.
func DefaultVaultEnabled(hasLegacy, populated bool) bool {
	return !hasLegacy || populated
}

// EvaluateReadiness computes the owner-facing state. Ordering matters:
// migration first (an upgrade must decide where knowledge lives), then
// recoverable rebuild states (which are actionable on a fresh install), then
// hard read/write errors, then non-fatal degradation.
func EvaluateReadiness(in ReadinessInput) Readiness {
	result := Readiness{
		SwitchVisible:   in.HasLegacy(),
		DefaultEnabled:  DefaultVaultEnabled(in.HasLegacy(), in.Populated()),
		Checks:          []ReadinessCheck{},
		UnboundProjects: unboundProjects(in.ProjectBindings, in.KnownProjects),
	}
	add := func(id, status, message string) {
		result.Checks = append(result.Checks, ReadinessCheck{ID: id, Status: status, Message: message})
		if status == checkFail {
			result.Missing = append(result.Missing, id)
		}
	}

	switch {
	case !in.VaultInitialized:
		add("vault_initialized", checkFail, "知识库尚未初始化（缺少 .aha-vault.json）")
	default:
		add("vault_initialized", checkOK, "知识库目录已初始化")
	}
	if in.VaultWritable {
		add("vault_writable", checkOK, "知识库目录可写")
	} else {
		add("vault_writable", checkFail, "知识库目录不可写")
	}
	if in.IndexError != "" {
		add("index_schema", checkFail, "索引无法读取："+in.IndexError)
	} else if !in.IndexReady {
		add("index_schema", checkFail, "索引尚未建立或不可读")
	} else {
		add("index_schema", checkOK, "索引 schema 可读")
	}
	if in.IndexWritable {
		add("index_writable", checkOK, "索引目录可写")
	} else {
		add("index_writable", checkFail, "索引目录不可写")
	}
	if !in.IndexReady {
		add("index_fresh", checkFail, "索引尚未建立")
	} else if in.IndexStale {
		add("index_fresh", checkFail, "知识文件比索引新，需要重建索引")
	} else {
		add("index_fresh", checkOK, "索引与知识文件一致")
	}
	if in.EntryCount > 0 {
		add("index_entries", checkOK, fmt.Sprintf("索引包含 %d 条知识", in.EntryCount))
	} else {
		add("index_entries", checkWarn, "索引为空；导入或迁移后才会出现知识")
	}
	if len(result.UnboundProjects) == 0 {
		add("manifest_binding", checkOK, "所有项目都已绑定到知识库")
	} else {
		add("manifest_binding", checkWarn, fmt.Sprintf("%d 个项目未绑定，这些项目继续使用旧知识库", len(result.UnboundProjects)))
	}
	if !in.HasLegacy() {
		add("migration", checkOK, "没有检测到旧知识库")
	} else if in.Populated() {
		add("migration", checkOK, "旧知识迁移或已有知识库导入已验证；旧数据仍保留")
	} else if in.InUse() {
		// The owner already runs on the vault. A migration would re-export the
		// legacy database over these pages, so legacy is intentionally left as
		// a read-only rollback copy and readiness must not ask for it.
		add("migration", checkOK, "文件知识库已启用并在使用；旧知识库仅作只读回滚保留，不会被迁移覆盖")
	} else {
		add("migration", checkFail, fmt.Sprintf("检测到旧知识库（%d 条知识、%d 个技能），迁移或导入尚未验证", in.LegacyEntries, maxInt(in.LegacySkills, in.LegacySkillDirs)))
	}
	if in.IndexWarnings > 0 {
		add("index_warnings", checkWarn, fmt.Sprintf("索引有 %d 条警告，请检查知识文件", in.IndexWarnings))
	}

	switch {
	case hasCheck(result.Checks, "migration", checkFail):
		result.Status = ReadinessNeedsMigration
		result.Message = "升级后需要先迁移旧知识库，或导入已有知识库，才能启用文件知识库。"
	case hasCheck(result.Checks, "index_schema", checkFail) || hasCheck(result.Checks, "index_fresh", checkFail) || !in.VaultInitialized:
		result.Status = ReadinessNeedsRebuild
		result.Message = "知识库或索引尚未就绪，请先重建索引。"
	case hasCheck(result.Checks, "vault_writable", checkFail) || hasCheck(result.Checks, "index_writable", checkFail) || in.IndexError != "":
		result.Status = ReadinessError
		result.Message = "知识库或索引不可写/不可读，请检查目录权限。"
	case len(result.UnboundProjects) > 0 || in.IndexWarnings > 0 || in.EntryCount == 0:
		result.Status = ReadinessDegraded
		result.Message = "文件知识库可以启用，但部分项目仍会回退旧知识库或索引需要关注。"
	default:
		result.Status = ReadinessReady
		result.Message = "文件知识库已就绪。"
	}
	if in.InUse() {
		// Say explicitly that the remaining legacy data is only a rollback copy;
		// the ready/degraded copy otherwise reads like the vault still needs a
		// migration.
		switch result.Status {
		case ReadinessReady:
			result.Message = "文件知识库已就绪并在使用；旧知识库仅作只读回滚保留，不会被迁移覆盖。"
		case ReadinessDegraded:
			result.Message = "文件知识库已启用，但部分项目仍回退旧知识库或索引需要关注；旧知识库仅作只读回滚保留，不会被迁移覆盖。"
		}
	}
	result.CanEnable = result.Status == ReadinessReady || result.Status == ReadinessDegraded
	if !result.DefaultEnabled && !in.Populated() && !in.InUse() {
		// The default is legacy, but the owner can still choose the vault once
		// the blocking checks clear. Needs-migration never advertises that.
		// An already-running vault keeps reporting its live readiness instead.
		result.CanEnable = false
	}
	return result
}

func unboundProjects(bindings map[string]string, known []string) []string {
	if len(known) == 0 {
		return nil
	}
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

func hasCheck(checks []ReadinessCheck, id, status string) bool {
	for _, check := range checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// Probe is the filesystem part of readiness. It works whether or not the
// manager switch is on, so the settings panel can explain an empty or broken
// vault before the owner enables it.
type Probe struct {
	VaultReady       bool
	VaultInitialized bool
	VaultWritable    bool
	IndexReady       bool
	IndexWritable    bool
	IndexStale       bool
	IndexError       string
	EntryCount       int
	IndexWarnings    int
	RebuiltAt        string
	Manifest         Manifest
	ManifestOK       bool
}

func (m *Manager) Probe(ctx context.Context) Probe {
	config := m.config
	probe := Probe{}
	if info, err := os.Stat(config.VaultPath); err == nil && info.IsDir() {
		probe.VaultReady = true
	}
	if manifest, err := ReadManifest(config.VaultPath); err == nil {
		probe.Manifest = manifest
		probe.ManifestOK = true
		probe.VaultInitialized = true
	}
	probe.VaultWritable = directoryWritable(config.VaultPath)
	probe.IndexWritable = directoryWritable(filepath.Dir(config.IndexPath))
	if info, err := os.Stat(config.IndexPath); err == nil && !info.IsDir() {
		if index, openErr := openReadOnlyIndex(ctx, config.IndexPath); openErr == nil {
			if status, statusErr := index.Status(ctx); statusErr == nil {
				probe.IndexReady = true
				probe.IndexWarnings = status.Warnings
				probe.RebuiltAt = status.RebuiltAt
				if count, countErr := index.VisibleEntryCount(ctx); countErr == nil {
					probe.EntryCount = count
				}
				if latest, latestErr := latestKnowledgeModTime(config.VaultPath); latestErr == nil && !latest.IsZero() {
					probe.IndexStale = info.ModTime().Before(latest.Add(-time.Second))
				}
			} else {
				probe.IndexError = statusErr.Error()
			}
			_ = index.Close()
		} else {
			probe.IndexError = openErr.Error()
		}
	}
	return probe
}

// directoryWritable creates and removes a hidden probe file. Permission bits
// alone are unreliable on Windows, so an actual write is the honest check; it
// runs only from the settings/readiness calls, never from a knowledge read.
func directoryWritable(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(dir, ".aha-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

func latestKnowledgeModTime(vaultPath string) (time.Time, error) {
	latest := time.Time{}
	err := filepath.WalkDir(vaultPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if filePath == vaultPath {
				return nil
			}
			name := strings.ToLower(entry.Name())
			if skippedDirectoryNames[name] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, err
}
