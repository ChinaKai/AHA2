package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PetMarkdownEntry struct {
	Date    string `json:"date"`
	Content string `json:"content"`
}

// PetWorklog returns the pet's own work notes. The pet writes them with its
// workspace file tools; AHA only reads them for the workbench.
func (s *Service) PetWorklog(ctx context.Context) ([]PetMarkdownEntry, error) {
	entries, _, err := s.PetWorklogPage(ctx, 30)
	return entries, err
}

func (s *Service) PetWorklogPage(ctx context.Context, limit int) ([]PetMarkdownEntry, int, error) {
	return readPetMarkdownEntries(filepath.Join(s.petWorkspaceRoot(ctx), "worklog"), limit)
}

func readPetMarkdownEntries(root string, limit int) ([]PetMarkdownEntry, int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []PetMarkdownEntry{}, 0, nil
		}
		return nil, 0, err
	}
	items := make([]PetMarkdownEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(root, entry.Name()))
		if readErr != nil {
			continue
		}
		items = append(items, PetMarkdownEntry{Date: strings.TrimSuffix(entry.Name(), ".md"), Content: string(raw)})
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
