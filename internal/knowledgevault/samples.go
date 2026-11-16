package knowledgevault

import (
	"os"
	"path/filepath"
	"time"
)

// The Phase 3 sample vault is deliberately tiny: four fixed documents under
// global/ that demonstrate the file-first workflow without importing or
// imitating real legacy knowledge. The IDs are stable so a second initialization
// attempt can never create duplicate documents.

const (
	SampleWelcomeID     = "sample-vault-welcome"
	SampleEditingID     = "sample-vault-editing"
	SampleConventionsID = "sample-vault-conventions"
	SampleAgentLessonID = "sample-agent-lesson"
)

// SampleDocument is one seed file. Paths are vault-relative and fixed.
type SampleDocument struct {
	Path        string
	Frontmatter Frontmatter
	Body        string
}

// SampleWriteResult reports which seed files were created. Skipped lists fixed
// sample paths that already existed and were therefore left untouched.
type SampleWriteResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped,omitempty"`
}

// VaultInitialized reports whether a vault manifest already exists. It is the
// one-way door that makes "init" safe: an initialized vault is never modified.
func VaultInitialized(vaultPath string) bool {
	info, err := os.Stat(filepath.Join(vaultPath, manifestFileName))
	return err == nil && !info.IsDir()
}

// SampleDocuments returns the fixed Phase 3 seed set. Content is intentionally
// about the file vault itself so it doubles as a quick tour.
func SampleDocuments(now time.Time) []SampleDocument {
	stamp := now.UTC().Format(time.RFC3339)
	return []SampleDocument{
		{
			Path: "global/general/示例/欢迎使用.md",
			Frontmatter: Frontmatter{
				ID: SampleWelcomeID, Title: "欢迎使用文件知识库", Slug: "sample-welcome",
				Scope: "global", Type: TypeReference, Status: "verified",
				Tags: []string{"示例", "入门"}, Created: stamp, Updated: stamp,
			},
			Body: `# 欢迎使用文件知识库

这是 AHA2 文件知识库的示例文档。**文件是知识正本，数据库只是可重建的索引**；新版知识页只读，编辑请使用 VSCode 或 Obsidian。

- 示例只用于体验，可以随时删除；删除文件后索引会自动更新。
- 这些示例不会导入真实旧知识，也不会修改旧知识库。
- vault 的目录结构、frontmatter 与索引说明见根目录的 README.md。

## 下一步

1. 阅读 [[编辑与同步]]，了解如何用 VSCode 或 Obsidian 修改文件。
2. 阅读 [[写作约定]]，了解目录与 frontmatter 约定。
`,
		},
		{
			Path: "global/general/示例/编辑与同步.md",
			Frontmatter: Frontmatter{
				ID: SampleEditingID, Title: "编辑与同步", Slug: "sample-editing",
				Scope: "global", Type: TypePractice, Status: "verified",
				Tags: []string{"示例", "VSCode", "Obsidian"}, Created: stamp, Updated: stamp,
			},
			Body: `# 编辑与同步

知识文件可以直接用桌面编辑器修改；AHA 不会实时监听文件变化。

## VSCode

1. 在知识页打开任意文档，点「VSCode」或用「复制路径」。
2. 直接编辑 Markdown 文件并保存。
3. 保存文件即可，索引会自动更新。

## Obsidian

1. 在 Obsidian 中把 vault 根目录打开为 vault。
2. 修改或新建 Markdown 文件。
3. 保存文件即可，网页端会看到自动更新后的内容。

> 索引由写路径同步、文件变更监听和启动对账自动维护；无需手动重建。写作约定见 [[写作约定]]。
`,
		},
		{
			Path: "global/general/示例/写作约定.md",
			Frontmatter: Frontmatter{
				ID: SampleConventionsID, Title: "写作约定", Slug: "sample-conventions",
				Scope: "global", Type: TypeReference, Status: "verified",
				Tags: []string{"示例", "约定"}, Created: stamp, Updated: stamp,
			},
			Body: `# 写作约定

## 目录结构

- global/index.md：全局知识首页。
- global/general/：跨项目参考资料。
- global/agent-lessons/：Agent 技术诊断与行为教训。
- global/inbox/：暂未归类的文档。
- projects/<slug>/：项目知识；主题是子目录。
- skills/：技能包的扁平预留位置，不进入知识树。

## Frontmatter

常用字段：id、title、slug、scope、project_id、type、tags、status、parent、created、updated。

规范 type 只有六种：navigation、decision、practice、diagnostic、reference、architecture。status 常用 verified 或 stale。

## 链接

正文里可以用 [[编辑与同步]] 这样的 wiki 链接指向其他文档；索引更新后，目标文档会显示反向链接。
`,
		},
		{
			Path: "global/agent-lessons/示例-Agent经验.md",
			Frontmatter: Frontmatter{
				ID: SampleAgentLessonID, Title: "示例：先读现有实现再改", Slug: "sample-agent-lesson",
				Scope: "global", Type: TypeDiagnostic, Status: "verified",
				Tags: []string{"示例", "Agent"}, Created: stamp, Updated: stamp,
			},
			Body: `# 示例：先读现有实现再改

这是一条用于展示 Agent Lessons 库的通用经验。

## 触发

接到一个看起来很小的修改，但目标代码与配置、数据库或前端缓存有耦合。

## 做法

1. 先读现有实现和调用链，确认改动边界，不凭文件名猜路径。
2. 修改后跑针对性的测试；涉及并发或运行时开关时补 -race。
3. 在最终说明里写清验证命令、结果和已知限制。

## 验证

- 目标测试通过，且没有跳过或改写断言来迁就实现。
- 行为变化能在真实入口复现，而不只是在单元测试里成立。
`,
		},
	}
}

// WriteSamples writes the fixed sample documents. It never overwrites an
// existing path, including a symlink: a vault that already contains the file
// keeps the owner's version and reports it as skipped.
func WriteSamples(cfg Config, now time.Time) (SampleWriteResult, error) {
	normalized, err := cfg.Normalized()
	if err != nil {
		return SampleWriteResult{}, err
	}
	result := SampleWriteResult{Created: []string{}}
	for _, sample := range SampleDocuments(now) {
		target := filepath.Join(normalized.VaultPath, filepath.FromSlash(sample.Path))
		if _, statErr := os.Lstat(target); statErr == nil {
			result.Skipped = append(result.Skipped, sample.Path)
			continue
		} else if !os.IsNotExist(statErr) {
			return result, statErr
		}
		content, renderErr := renderDocument(sample.Frontmatter, sample.Body)
		if renderErr != nil {
			return result, renderErr
		}
		if err := writeVaultFileIfChanged(normalized.VaultPath, target, []byte(content), 0o644); err != nil {
			return result, err
		}
		result.Created = append(result.Created, sample.Path)
	}
	return result, nil
}
