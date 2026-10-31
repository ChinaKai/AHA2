package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	petCoreIdentityPath   = "core/identity.md"
	petCorePrinciplesPath = "core/principles.md"
	petCoreLoopsPath      = "core/loops.md"
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

- 记录自己创建/参与的任务、想法和审批，直到真正闭环。
- 每项写：状态（open/waiting/blocked/done）| 事项 | 下一步 | 关闭条件
- 闭环后删除，或移到“已闭环”。
- 每次最终回复列出未闭环事项及原因；没有则写“无”。
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
	return writePetCoreFile(filepath.Join(root, petCoreLoopsPath), petCoreLoopsSeed)
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
	for _, relative := range []string{petCoreIdentityPath, petCorePrinciplesPath, petCoreLoopsPath} {
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
// aha_pet_work_state and the autonomous trigger so commitments survive context
// compaction and several wake-ups without the pet having to remember them.
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
	return string(data)
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
