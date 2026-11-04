package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var petWorkbenchFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}\.md$`)

// PetSaveWorkbenchDocument lets the owner edit the pet's core documents, diary,
// or worklog from the Web workbench. The pet itself writes these files through
// its Task; this path is for owner corrections.
func (s *Service) PetSaveWorkbenchDocument(ctx context.Context, section, name, content string) error {
	section = strings.ToLower(strings.TrimSpace(section))
	name = strings.TrimSpace(name)
	if len([]rune(content)) > 100000 {
		return fmt.Errorf("文档不能超过 100000 个字符")
	}
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	if root == "" {
		return fmt.Errorf("宠物工作区不可用")
	}
	var directory string
	switch section {
	case "core":
		if name != "identity.md" && name != "principles.md" && name != "loops.md" && name != "agenda.md" {
			return fmt.Errorf("核心文档只能编辑 identity.md、principles.md、loops.md 或 agenda.md")
		}
		directory = filepath.Join(root, "core")
	case "diary", "worklog":
		if !petWorkbenchFileName.MatchString(name) || strings.Contains(name, "..") {
			return fmt.Errorf("文件名无效")
		}
		directory = filepath.Join(root, section)
	default:
		return fmt.Errorf("未知的工作台分区")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	target := filepath.Join(directory, name)
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(directory)+string(os.PathSeparator)) {
		return fmt.Errorf("文件路径无效")
	}
	temp, err := os.CreateTemp(directory, ".pet-edit-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, target)
}
