package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PetDiaryEntry struct {
	Date    string `json:"date"`
	Content string `json:"content"`
}

func (s *Service) PetDiary(ctx context.Context) ([]PetDiaryEntry, error) {
	entries, _, err := s.PetDiaryPage(ctx, 30)
	return entries, err
}

func (s *Service) PetDiaryPage(ctx context.Context, limit int) ([]PetDiaryEntry, int, error) {
	return readPetMarkdownEntries(filepath.Join(s.petWorkspaceRoot(ctx), "diary"), limit)
}

// PetWorklog returns the pet's own work notes. The pet writes them with its
// workspace file tools; AHA only reads them for the workbench.
func (s *Service) PetWorklog(ctx context.Context) ([]PetDiaryEntry, error) {
	entries, _, err := s.PetWorklogPage(ctx, 30)
	return entries, err
}

func (s *Service) PetWorklogPage(ctx context.Context, limit int) ([]PetDiaryEntry, int, error) {
	return readPetMarkdownEntries(filepath.Join(s.petWorkspaceRoot(ctx), "worklog"), limit)
}

func readPetMarkdownEntries(root string, limit int) ([]PetDiaryEntry, int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []PetDiaryEntry{}, 0, nil
		}
		return nil, 0, err
	}
	items := make([]PetDiaryEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(root, entry.Name()))
		if readErr != nil {
			continue
		}
		items = append(items, PetDiaryEntry{Date: strings.TrimSuffix(entry.Name(), ".md"), Content: string(raw)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Date > items[j].Date })
	total := len(items)
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, total, nil
}

func (s *Service) WritePetDiary(ctx context.Context, date, content string) error {
	if strings.TrimSpace(date) == "" {
		date = time.Now().Format("2006-01-02")
	}
	root := filepath.Join(s.petWorkspaceRoot(ctx), "diary")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, date+".md"), []byte(content), 0o600)
}
