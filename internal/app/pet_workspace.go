package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	petCoreIdentityPath   = "core/identity.md"
	petCorePrinciplesPath = "core/principles.md"
	petCoreLoopsPath      = "core/loops.md"
	petCoreAgendaPath     = "core/agenda.md"
	petLabDir             = "pet-lab"

	// The loops and agenda ledgers are injected into every wake-up, unlike the
	// diary and worklog. Keep them bounded so a long-lived pet cannot silently
	// crowd out the rest of its context; the pet compacts them by deleting
	// resolved items.
	petCoreLoopsMaxBytes  = 16 * 1024
	petCoreLoopsSoftBytes = 8 * 1024
	petCoreLoopsSoftItems = 20
	petCoreAgendaMaxBytes = 16 * 1024
)

// The core files hold everything that makes the pet itself: its name, shape,
// personality, habits and principles. AHA only writes these minimal templates
// when a file is missing; after that the pet owns them, and every later start
// leaves an existing file untouched.
var (
	petCoreIdentitySeed = `# 身份

- 名字：
- 形象：
- 性格：
- 习惯：
`
	petCorePrinciplesSeed = `# 做事准则

-
`
	petCoreLoopsSeed = `# 开放闭环

- 只记录尚未真正闭环的事项；已关闭的立即删除，需要留痕就写入当天 worklog。
- 每项写：状态（open/waiting/blocked）| 事项 | 下一步 | 关闭条件
- 每次最终回复附一行巡检摘要；只列新增、变化或需要主人决定的未闭环事项，没有变化时回复 NOOP。
`
	petCoreAgendaSeed = `# 自主议程

- 只记录宠物自己可以推进、不需要主人立即决策的事项。
- 每项写：状态（next/doing/waiting）| 目标 | 下一步 | 验收标准 | 是否需要主人决策
- 每次唤醒最多推进一项；真正闭环后才删除，留痕写入当天 worklog。
- 先关闭最早的未闭环宠物任务；waiting/blocked 需决定就提交 owner_decision，failed 先重试一次再升级。
- 最终回复附一行巡检摘要；只写新增、变化、未闭环，无变化回 NOOP。
- 完成但留下主人决策的事项改为 waiting，不能删除；同一决策只创建一次。
- 需要主人决定时用 pet_request_approval 创建 kind=owner_decision，并在本议程或 loops 写 waiting 项；不能只写在最终回复里。
- 没有可推进项时，从最近 worklog/loops 选一个低风险、可验证的 AHA 使用体验改进并启动 Task；不要用 NOOP 逃避。
- 小工具/原型写在 pet-lab/，可以直接读写和运行；需要改 AHA2 源码时创建普通 Task。
- 创建任务默认 high 推理和完全访问；简单/只读任务可显式降级，复杂任务选择更强的模型。
- 由宠物自行判断是否需要评审；调用 aha_complete_task 必须显式申报 review_decision=required 或 skipped，skipped 必须提供非空 review_reason（建议至少 8 字符）。需要评审时用 aha_request_code_review 创建独立、不同模型、high 及以上推理、approval=never、inplace 的 Review Task；评审可运行 git diff、测试和构建，但不得修改、提交、合并或部署源码；源 Task 必须等待 Verdict: APPROVE 后才能完成。评审 Task 自身无需递归评审，但最新成功结果仍必须有最终 verdict。
`
	petLabReadmeSeed = `# 宠物实验室

- 这里可以直接写脚本、原型和临时文件，也可以运行它们。
- 只在此目录内直接写；core/、diary/、worklog/ 仍用宠物工具。
- 不要写工作区之外；不要在这里保存凭据或秘密。
`
)

// petWorkspaceRoot is the directory the pet Task actually runs in. Prefer the
// registered workspace so a relocated or custom pet workspace keeps working;
// fall back to the default layout while the workspace is first being created.
func (s *Service) petWorkspaceRoot(ctx context.Context) string {
	if s == nil || s.store == nil {
		return ""
	}
	if workspace, err := s.store.Workspace(ctx, petWorkspaceID); err == nil && strings.TrimSpace(workspace.RootPath) != "" {
		return workspace.RootPath
	}
	return filepath.Join(s.store.DataDir(), "pet-workspace")
}

// ensurePetWorkspaceFiles creates the pet's stable home and seeds any missing
// core file. It never overwrites an existing file, because the pet is the only
// writer of its identity and principles after the first start.
func (s *Service) ensurePetWorkspaceFiles(ctx context.Context, assistant *domain.OwnerAssistant) error {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "diary"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "worklog"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, petLabDir), 0o700); err != nil {
		return err
	}
	identity := petCoreIdentitySeed
	principles := petCorePrinciplesSeed
	if assistant != nil {
		if content := petCoreDocument("身份", assistant.PetIdentity); content != "" {
			identity = content
		}
		if content := petCoreDocument("做事准则", assistant.PetPrinciples); content != "" {
			principles = content
		}
	}
	if err := writePetCoreFile(filepath.Join(root, petCoreIdentityPath), identity); err != nil {
		return err
	}
	if err := writePetCoreFile(filepath.Join(root, petCorePrinciplesPath), principles); err != nil {
		return err
	}
	if err := writePetCoreFile(filepath.Join(root, petCoreLoopsPath), petCoreLoopsSeed); err != nil {
		return err
	}
	if err := writePetCoreFile(filepath.Join(root, petCoreAgendaPath), petCoreAgendaSeed); err != nil {
		return err
	}
	return writePetCoreFile(filepath.Join(root, petLabDir, "README.md"), petLabReadmeSeed)
}

type PetWorkspaceDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// PetCoreDocuments returns the pet-owned identity and principles files for the
// workbench. Missing files are seeded first, then read; the pet remains the only
// writer after that.
func (s *Service) PetCoreDocuments(ctx context.Context) ([]PetWorkspaceDocument, error) {
	assistant, assistantErr := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if assistantErr == nil {
		if err := s.ensurePetWorkspaceFiles(ctx, &assistant); err != nil {
			return nil, err
		}
	} else if err := s.ensurePetWorkspaceFiles(ctx, nil); err != nil {
		return nil, err
	}
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return []PetWorkspaceDocument{}, nil
	}
	items := []PetWorkspaceDocument{}
	for _, relative := range []string{petCoreIdentityPath, petCorePrinciplesPath, petCoreLoopsPath, petCoreAgendaPath} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		items = append(items, PetWorkspaceDocument{Name: filepath.Base(relative), Content: string(data)})
	}
	return items, nil
}

// PetOpenLoops returns the pet's durable open-loop ledger. It is injected into
// aha_pet_work_state so commitments survive context compaction and several
// wake-ups without the pet having to remember them. Archived sections stay on
// disk for the owner, but are deliberately left out of the prompt.
func (s *Service) PetOpenLoops(ctx context.Context) string {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return ""
	}
	if err := s.ensurePetWorkspaceFiles(ctx, nil); err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, petCoreLoopsPath))
	if err != nil {
		return ""
	}
	return activePetLoops(string(data))
}

func (s *Service) petOpenLoopsPath(ctx context.Context) string {
	return s.petCorePath(ctx, petCoreLoopsPath)
}

// PetAgenda returns the pet's own initiative queue. Unlike open loops, which
// often wait on the owner, agenda items are things the pet can advance without
// a new decision.
func (s *Service) PetAgenda(ctx context.Context) string {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return ""
	}
	if err := s.ensurePetWorkspaceFiles(ctx, nil); err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, petCoreAgendaPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (s *Service) petAgendaPath(ctx context.Context) string {
	return s.petCorePath(ctx, petCoreAgendaPath)
}

func (s *Service) petLabPath(ctx context.Context) string {
	return s.petCorePath(ctx, petLabDir)
}

func (s *Service) petCorePath(ctx context.Context, relative string) string {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return relative
	}
	return filepath.Join(root, relative)
}

// activePetLoops drops a trailing archived section (## 已关闭, ## Closed, ...)
// while keeping later sections such as 备注. Without this, moving resolved
// items to an archive made the file grow forever and every wake-up paid for the
// whole history.
func activePetLoops(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	skipLevel := 0
	for _, line := range lines {
		level, title := petMarkdownHeading(line)
		if skipLevel > 0 {
			if level == 0 || level > skipLevel {
				continue
			}
			skipLevel = 0
		}
		if level == 2 && isPetClosedLoopsHeading(title) {
			skipLevel = level
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func petMarkdownHeading(line string) (int, string) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, ""
	}
	return level, strings.ToLower(strings.TrimSpace(trimmed[level:]))
}

func isPetClosedLoopsHeading(title string) bool {
	for _, prefix := range []string{"已关闭", "已闭环", "closed", "archive", "archived"} {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

// petLoopsNote warns the pet when the active ledger is getting large. The hard
// limit prevents writes, so this soft note is the chance to compact first.
func petLoopsNote(loops string) string {
	return petLedgerNote("开放闭环", petCoreLoopsPath, loops, petCoreLoopsSoftBytes, petCoreLoopsSoftItems)
}

func petAgendaNote(agenda string) string {
	return petLedgerNote("自主议程", petCoreAgendaPath, agenda, petCoreLoopsSoftBytes, petCoreLoopsSoftItems)
}

func petLedgerNote(label, path, content string, softBytes, softItems int) string {
	if content == "" {
		return ""
	}
	items := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			items++
		}
	}
	if len(content) <= softBytes && items <= softItems {
		return ""
	}
	return fmt.Sprintf("%s偏多（%d 条 / %d 字节）。请先用 pet_update_core 压缩 %s：删除已关闭项并把留痕写入当天 worklog，只保留尚未闭环的事项。", label, items, len(content), path)
}

func writePetCoreFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

// petCoreDocument migrates a legacy stored identity/principles value into the
// markdown file format. Empty values fall back to the minimal seed.
func petCoreDocument(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if strings.HasPrefix(body, "#") {
		return body + "\n"
	}
	return "# " + title + "\n\n" + body + "\n"
}
