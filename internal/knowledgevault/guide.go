package knowledgevault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// GuideRelativePath is the canonical, human-readable vault usage guide. It
	// lives at the vault root so it is a single top-level navigation page, not
	// a project/global topic document. Writes only ever target this path.
	GuideRelativePath = "knowledge-base-guide.md"
	// LegacyGuideRelativePath is where older releases kept the guide. Reads
	// still fall back to it until the one-time migration moves it to the root.
	LegacyGuideRelativePath = "global/general/knowledge-base-guide.md"
	// GuideTitle is used when the guide file has no readable title.
	GuideTitle = "知识库使用指南"

	guidePromptStart = "<!-- aha-vault:prompt:start -->"
	guidePromptEnd   = "<!-- aha-vault:prompt:end -->"
)

// Guide is the full guide document plus the slim prompt section. Content is
// the on-disk Markdown (frontmatter included); Prompt is what every Task
// receives, so it must stay short enough to survive context compaction.
type Guide struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Prompt  string `json:"prompt"`
}

// ErrGuideNotFound reports a vault without the canonical guide file.
var ErrGuideNotFound = fmt.Errorf("knowledge guide not found at %s", GuideRelativePath)

// ReadGuide reads the guide from a vault. It does not require a rebuilt index:
// the guide is the one document that must remain readable when the index is
// being rebuilt or missing.
func ReadGuide(vaultPath string) (Guide, error) {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return Guide{}, ErrGuideNotFound
	}
	data, err := readGuideFile(vaultPath, GuideRelativePath)
	if err != nil && os.IsNotExist(err) {
		// Compatibility read for a vault that has not migrated yet. The API
		// still reports the canonical root path; writes never target the old
		// location.
		data, err = readGuideFile(vaultPath, LegacyGuideRelativePath)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return Guide{}, ErrGuideNotFound
		}
		return Guide{}, fmt.Errorf("read knowledge guide: %w", err)
	}
	content := string(data)
	document := ParseDocument(content)
	title := strings.TrimSpace(document.Frontmatter.Title)
	if title == "" {
		title = FirstHeading(document.Body)
	}
	if title == "" {
		title = GuideTitle
	}
	prompt := extractPromptGuide(content)
	if prompt == "" {
		prompt = fallbackPromptGuide()
	}
	return Guide{Path: GuideRelativePath, Title: title, Content: content, Prompt: prompt}, nil
}

// readGuideFile reads one guide location with the same escape checks as the
// canonical path. A directory at the target is reported as missing.
func readGuideFile(vaultPath, relative string) ([]byte, error) {
	target := filepath.Join(vaultPath, filepath.FromSlash(relative))
	// Check existence before resolving symlinks: a missing file must stay a
	// "not found" instead of a security error.
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, os.ErrNotExist
	}
	if !IsInsideRoot(vaultPath, target) {
		return nil, fmt.Errorf("knowledge guide escapes the vault root")
	}
	return os.ReadFile(target)
}

// migrateLegacyGuide moves an older guide from global/general to the vault
// root. It is idempotent and never overwrites a root guide that already
// exists; the old file is removed only after the root copy is durable.
func migrateLegacyGuide(vaultPath string) (bool, error) {
	root := filepath.Join(vaultPath, filepath.FromSlash(GuideRelativePath))
	if _, err := os.Lstat(root); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat knowledge guide: %w", err)
	}
	data, err := readGuideFile(vaultPath, LegacyGuideRelativePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy knowledge guide: %w", err)
	}
	if err := ensureSafeWriteTarget(vaultPath, root); err != nil {
		return false, fmt.Errorf("migrate knowledge guide: %w", err)
	}
	if err := os.WriteFile(root, data, 0o644); err != nil {
		return false, fmt.Errorf("write root knowledge guide: %w", err)
	}
	legacy := filepath.Join(vaultPath, filepath.FromSlash(LegacyGuideRelativePath))
	if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
		return true, fmt.Errorf("remove legacy knowledge guide: %w", err)
	}
	return true, nil
}

// EnsureGuide creates the canonical system guide when the vault does not have
// one yet. It never overwrites an existing guide, so a hand-written replacement
// survives startup and rebuild, and repeated calls are a no-op. It reports
// whether this call created the file so callers can record it as a change.
func EnsureGuide(vaultPath string, now time.Time) (bool, error) {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return false, fmt.Errorf("ensure knowledge guide: empty vault path")
	}
	if migrated, err := migrateLegacyGuide(vaultPath); err != nil {
		return false, err
	} else if migrated {
		return true, nil
	}
	target := filepath.Join(vaultPath, filepath.FromSlash(GuideRelativePath))
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat knowledge guide: %w", err)
	}
	if err := ensureSafeWriteTarget(vaultPath, target); err != nil {
		return false, fmt.Errorf("ensure knowledge guide: %w", err)
	}
	// O_EXCL keeps a concurrent writer (another startup or a hand-written
	// file created between the check and the open) from being overwritten.
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("ensure knowledge guide: %w", err)
	}
	if _, err := file.WriteString(defaultGuideContent(now)); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return false, fmt.Errorf("write knowledge guide: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return false, fmt.Errorf("write knowledge guide: %w", err)
	}
	return true, nil
}

// legacyGuideReplacements are the retired Agent endpoint spellings and the
// one-line descriptions that only made sense for the old candidates batch.
// RepairGuideEndpoints applies them verbatim so an existing guide stops
// teaching a URL that writes the legacy tables.
var legacyGuideReplacements = []struct{ old, new string }{
	{"/api/v1/agent/knowledge/candidates", "/api/v1/agent/knowledge/entries"},
	{"/api/v1/agent/knowledge/{id}/feedback", "/api/v1/agent/knowledge/feedback"},
	{"/api/v1/agent/knowledge/{knowledge}/feedback", "/api/v1/agent/knowledge/feedback"},
	{"POST /api/v1/agent/knowledge/feedback`：提交 ", "POST /api/v1/agent/knowledge/feedback`（`entry_id` + `kind`）：提交 "},
	{"（每批 1–20 条，标题 ≤300 字符，正文 ≤20000 字符）", "（mode=create|revise，单篇写入，标题 ≤300 字符，正文 ≤20000 字符）"},
	{"（1–20 条，标题 ≤300、正文 ≤20000 字符）", "（mode=create|revise，单篇写入，标题 ≤300、正文 ≤20000 字符）"},
	{"：提交候选，每批 1–20 条。", "：创建或修订单篇知识，`mode=create|revise`。"},
	{"候选标题不超过 300 字符，正文不超过 20000 字符。", "创建时标题不超过 300 字符，正文不超过 20000 字符；修订需携带 `entry_id` 和当前 `base_revision`。"},
}

// RepairGuideEndpoints rewrites the retired Agent endpoint strings in an
// existing canonical guide. It never creates the guide (EnsureGuide does
// that), never deletes or reformats surrounding text, and returns false
// without writing when the file is already current, so every startup can
// safely run it.
func RepairGuideEndpoints(vaultPath string) (bool, error) {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return false, fmt.Errorf("repair knowledge guide: empty vault path")
	}
	target := filepath.Join(vaultPath, filepath.FromSlash(GuideRelativePath))
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat knowledge guide: %w", err)
	}
	if !IsInsideRoot(vaultPath, target) {
		return false, fmt.Errorf("knowledge guide escapes the vault root")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read knowledge guide: %w", err)
	}
	content := string(data)
	repaired := content
	for _, replacement := range legacyGuideReplacements {
		repaired = strings.ReplaceAll(repaired, replacement.old, replacement.new)
	}
	if repaired == content {
		return false, nil
	}
	if err := writeVaultFileIfChanged(vaultPath, target, []byte(repaired), 0o644); err != nil {
		return false, fmt.Errorf("update knowledge guide endpoints: %w", err)
	}
	return true, nil
}

func extractPromptGuide(content string) string {
	start := strings.Index(content, guidePromptStart)
	if start < 0 {
		return ""
	}
	start += len(guidePromptStart)
	end := strings.Index(content[start:], guidePromptEnd)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}

func fallbackPromptGuide() string {
	return strings.TrimSpace(`
# 知识库使用指南（精简）
- 知识库正本是 vault 文件：global/ 为跨项目知识，projects/<slug>/ 为项目知识；项目文档不得跨项目引用。
- 六种 type：navigation、decision、practice、diagnostic、reference、architecture。写入前先搜索相同主题，优先更新已有文档。
- 根下叶子文档放 inbox/；过时用 stale、错误用 wrong、有帮助用 helped 反馈，不要直接改状态。
- 禁止凭据、密钥、个人隐私、大段日志/二进制、无证据猜测和可直接执行的攻击载荷。
- Agent API：GET /api/v1/agent/knowledge、GET /api/v1/agent/knowledge/{id}、GET /api/v1/agent/knowledge/guide、POST /api/v1/agent/knowledge/entries（mode=create|revise，单篇写入，标题 ≤300 字符，正文 ≤20000 字符）、POST /api/v1/agent/knowledge/feedback（entry_id + kind）。
`)
}

func defaultGuideContent(now time.Time) string {
	frontmatter := Frontmatter{
		Title: GuideTitle, Slug: "knowledge-base-guide", Scope: "global",
		Type: TypeNavigation, Status: "verified",
		Created: now.UTC().Format(time.RFC3339), Updated: now.UTC().Format(time.RFC3339),
	}
	body := strings.TrimSpace(`
本指南说明 AHA2 知识库的正本位置、写入边界和 Agent 使用方式。知识正文以 vault 文件为准，数据库只是可重建的索引。

## 目录结构

- ` + "`knowledge-base-guide.md`" + `：vault 根目录的本指南，唯一正本。
- ` + "`global/index.md`" + `：全局知识入口。
- ` + "`global/general/`" + `：跨项目通用知识。
- ` + "`global/agent-lessons/`" + `：可复用的 Agent 技术诊断与行为教训。
- ` + "`global/inbox/`" + `：尚未归入主题组的全局文档。
- ` + "`projects/<slug>/index.md`" + `：项目知识入口；主题组是子目录，根下叶子文档放 ` + "`projects/<slug>/inbox/`" + `。
- ` + "`archive/<slug>/`" + `：AHA1 归档库，单独标记，不并入主库。
- ` + "`orphans/`" + `：无法判断归属的导出隔离区，不参与正常检索。
- ` + "`global/skills/<slug>/`" + `、` + "`projects/<slug>/skills/<slug>/`" + `：技能包。
- ` + "`.trash/`" + `：删除暂存；不作为有效知识。

## global 与 project 的边界

- 只有可跨项目复用的方法、决策和参考资料写入 ` + "`global/`" + `。
- 依赖某个仓库、分支、部署环境或客户配置的内容写入对应 ` + "`projects/<slug>/`" + `。
- 项目知识不得被其他项目引用；跨项目复用时先在 global 建立脱敏后的通用版本。
- 不确定归属时先放项目 ` + "`inbox/`" + `，不要凭空创建新项目目录。

## 六种规范 type

- ` + "`navigation`" + `：目录页、入口和阅读路径。
- ` + "`decision`" + `：已做出的取舍、原因和被否决方案。
- ` + "`practice`" + `：可复用的操作步骤与约定。
- ` + "`diagnostic`" + `：故障现象、定位过程和根因。
- ` + "`reference`" + `：稳定的参数、接口、路径和清单。
- ` + "`architecture`" + `：模块边界、数据流和长期结构。

旧类型只能保留在 ` + "`legacy_type`" + `，新文档必须使用上述六种之一。

## 先搜后写

写入前先搜索标题、slug、标签和正文关键词。已有主题应更新原文档，而不是新建近似重复项。新文档必须有明确标题、摘要和 ` + "`type`" + `；目录页由重建索引自动维护，不要手工改写 ` + "`<!-- aha-vault:index:start -->`" + ` 到 ` + "`<!-- aha-vault:index:end -->`" + ` 之间的内容。

## inbox

根目录下没有主题组的叶子文档放入 ` + "`inbox/`" + `；inbox 是待整理区，不作为长期分类。整理时移动到主题子目录，并保留原有正文。

## stale / wrong / helped

- ` + "`stale`" + `：内容可能过时，需要结合当前代码重新验证。
- ` + "`wrong`" + `：已确认错误，不能继续作为事实。
- ` + "`helped`" + `：本文档对当前任务有帮助，用于反馈排序。

不要直接把 stale/wrong 改回 verified。修订必须基于本轮代码、测试或文档证据，通过候选提案提交。

## 禁止内容

- 密码、令牌、私钥、Cookie、连接串等凭据与密钥。
- 个人隐私、客户敏感数据、未脱敏的内部地址。
- 大段原始日志、二进制内容、完整数据库转储。
- 没有证据的猜测、攻击指令，以及可直接执行的恶意载荷。

## Agent API 端点与限制

- ` + "`GET /api/v1/agent/knowledge`" + `：列出当前 Task 可读知识。
- ` + "`GET /api/v1/agent/knowledge/{id}`" + `：读取单篇知识。
- ` + "`GET /api/v1/agent/knowledge/guide`" + `：读取本指南。
- ` + "`POST /api/v1/agent/knowledge/entries`" + `：创建或修订单篇知识，` + "`mode=create|revise`" + `。
- ` + "`POST /api/v1/agent/knowledge/feedback`" + `：用 ` + "`entry_id`" + ` 提交 ` + "`stale`" + `、` + "`wrong`" + ` 或 ` + "`helped`" + `。

创建时标题不超过 300 字符，正文不超过 20000 字符；修订需携带 ` + "`entry_id`" + ` 和当前 ` + "`base_revision`" + `。写入来源跟随读取来源：vault 生效时直接写文件，旧库项目仍走待审提案。

## Task Prompt 精简版

以下区块会被注入 Task Prompt；修改时保持简短，不要在这里放凭据或长示例。

` + guidePromptStart + `
# 知识库使用指南（精简）
- vault 是知识正本：global/ 跨项目，projects/<slug>/ 项目内；项目文档不得跨项目引用。
- 六种 type：navigation、decision、practice、diagnostic、reference、architecture。
- 先搜索再写；优先更新已有文档。根下叶子放 inbox/。
- 过时用 stale、错误用 wrong、有帮助用 helped 反馈，不直接改状态。
- 禁止凭据、隐私、大段日志/二进制、无证据猜测和恶意载荷。
- API：GET /api/v1/agent/knowledge、GET /api/v1/agent/knowledge/{id}、GET /api/v1/agent/knowledge/guide、POST /api/v1/agent/knowledge/entries（mode=create|revise，单篇写入，标题 ≤300、正文 ≤20000 字符）、POST /api/v1/agent/knowledge/feedback（entry_id + kind）。
` + guidePromptEnd + `
`)
	return renderDocumentPanicFree(frontmatter, body)
}

func renderDocumentPanicFree(frontmatter Frontmatter, body string) string {
	content, err := renderDocument(frontmatter, body)
	if err != nil {
		return body
	}
	return content
}
