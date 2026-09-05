package store

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const maxSkillPackageFileSize = 2 << 20

func skillSlug(value string) string {
	var result []rune
	separator := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result = append(result, character)
			separator = false
			continue
		}
		if len(result) > 0 && !separator {
			result = append(result, '-')
			separator = true
		}
	}
	return strings.Trim(string(result), "-")
}

func (s *Store) uniqueSkillSlug(base, id string) string {
	slug := skillSlug(base)
	if slug == "" {
		slug = id
	}
	candidate := slug
	for index := 2; ; index++ {
		var existing string
		err := s.db.QueryRow(`SELECT id FROM skills WHERE package_slug=?`, candidate).Scan(&existing)
		if err != nil || existing == id {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", slug, index)
	}
}

func (s *Store) SkillPackagePath(item domain.Skill) string {
	slug := item.PackageSlug
	if slug == "" {
		slug = item.ID
	}
	return filepath.Join(s.dataDir, "knowledge", "skills", slug)
}

func skillMarkdown(item domain.Skill) string {
	description := strings.ReplaceAll(strings.TrimSpace(item.Description), "\n", " ")
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", strconv.Quote(item.Name), strconv.Quote(description), strings.TrimSpace(item.Instructions))
}

func (s *Store) writeSkillPackage(item *domain.Skill) error {
	if item.PackageSlug == "" {
		item.PackageSlug = s.uniqueSkillSlug(item.Name, item.ID)
	}
	root := s.SkillPackagePath(*item)
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skillMarkdown(*item)), 0o600); err != nil {
		return err
	}
	item.SourcePath = root
	return nil
}

func (s *Store) hydrateSkillPackage(item *domain.Skill) error {
	root := s.SkillPackagePath(*item)
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); os.IsNotExist(err) {
		if err := s.writeSkillPackage(item); err != nil {
			return err
		}
	}
	item.SourcePath = root
	item.Files = nil
	item.PackageFiles = nil
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxSkillPackageFileSize {
			return err
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		item.Files = append(item.Files, relative)
		if utf8.Valid(data) && !strings.ContainsRune(string(data), '\x00') {
			item.PackageFiles = append(item.PackageFiles, domain.SkillFile{Path: relative, Content: string(data)})
		}
		return nil
	})
	sort.Strings(item.Files)
	sort.Slice(item.PackageFiles, func(left, right int) bool { return item.PackageFiles[left].Path < item.PackageFiles[right].Path })
	for _, file := range item.PackageFiles {
		if file.Path != "SKILL.md" {
			continue
		}
		parts := strings.SplitN(file.Content, "---", 3)
		if len(parts) == 3 {
			item.Instructions = strings.TrimSpace(parts[2])
		}
		break
	}
	return err
}

func (s *Store) removeSkillPackage(item domain.Skill) error {
	root := s.SkillPackagePath(item)
	base := filepath.Join(s.dataDir, "knowledge", "skills")
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	if absoluteRoot == absoluteBase || !strings.HasPrefix(strings.ToLower(absoluteRoot), strings.ToLower(absoluteBase+string(os.PathSeparator))) {
		return fmt.Errorf("skill package path escapes managed root")
	}
	return os.RemoveAll(absoluteRoot)
}

func (s *Store) UpdateSkillPackage(ctx context.Context, item domain.Skill, baseVersion int, files []domain.SkillFile) (domain.Skill, error) {
	if item.Version != baseVersion {
		return domain.Skill{}, fmt.Errorf("skill version conflict")
	}
	validated := make([]domain.SkillFile, 0, len(files))
	seen := map[string]bool{}
	hasEntry := false
	for _, file := range files {
		name := path.Clean(strings.ReplaceAll(strings.TrimSpace(file.Path), `\`, "/"))
		if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return domain.Skill{}, fmt.Errorf("invalid skill package path %q", file.Path)
		}
		if seen[name] || len(file.Content) > maxSkillPackageFileSize || strings.ContainsRune(file.Content, '\x00') {
			return domain.Skill{}, fmt.Errorf("invalid skill package file %q", name)
		}
		seen[name] = true
		hasEntry = hasEntry || name == "SKILL.md"
		validated = append(validated, domain.SkillFile{Path: name, Content: file.Content})
	}
	if !hasEntry {
		return domain.Skill{}, fmt.Errorf("SKILL.md is required")
	}
	for _, file := range validated {
		if file.Path == "SKILL.md" && !validSkillEntry(file.Content) {
			return domain.Skill{}, fmt.Errorf("SKILL.md frontmatter requires name and description")
		}
	}
	base := filepath.Join(s.dataDir, "knowledge", "skills")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return domain.Skill{}, err
	}
	stage, err := os.MkdirTemp(base, ".skill-stage-")
	if err != nil {
		return domain.Skill{}, err
	}
	defer os.RemoveAll(stage)
	for _, file := range validated {
		target := filepath.Join(stage, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return domain.Skill{}, err
		}
		if err := os.WriteFile(target, []byte(file.Content), 0o600); err != nil {
			return domain.Skill{}, err
		}
	}
	root := s.SkillPackagePath(item)
	backup := root + ".backup"
	_ = os.RemoveAll(backup)
	if err := os.Rename(root, backup); err != nil {
		return domain.Skill{}, err
	}
	if err := os.Rename(stage, root); err != nil {
		_ = os.Rename(backup, root)
		return domain.Skill{}, err
	}
	item.Version++
	item.SourcePath = root
	result, err := s.db.ExecContext(ctx, `
		UPDATE skills SET package_slug=?,scope=?,project_id=?,name=?,description=?,instructions=?,version=?,status=?,enabled=?,source_path=?,updated_at=? WHERE id=? AND version=?`,
		item.PackageSlug, item.Scope, item.ProjectID, item.Name, item.Description, item.Instructions, item.Version,
		item.Status, boolInt(item.Enabled), root, timeString(item.UpdatedAt), item.ID, baseVersion,
	)
	if err != nil {
		_ = os.RemoveAll(root)
		_ = os.Rename(backup, root)
		return domain.Skill{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		_ = os.RemoveAll(root)
		_ = os.Rename(backup, root)
		return domain.Skill{}, fmt.Errorf("skill version conflict")
	}
	_ = os.RemoveAll(backup)
	return s.Skill(ctx, item.ID)
}

func validSkillEntry(content string) bool {
	parts := strings.SplitN(strings.TrimSpace(content), "---", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" {
		return false
	}
	frontmatter := "\n" + strings.ToLower(parts[1]) + "\n"
	return strings.Contains(frontmatter, "\nname:") && strings.Contains(frontmatter, "\ndescription:") && strings.TrimSpace(parts[2]) != ""
}
