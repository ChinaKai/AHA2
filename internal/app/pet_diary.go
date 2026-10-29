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
	root := filepath.Join(s.store.DataDir(), "pet-workspace", "diary")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []PetDiaryEntry{}, nil
		}
		return nil, err
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
	if len(items) > 30 {
		items = items[:30]
	}
	return items, nil
}

func (s *Service) WritePetDiary(ctx context.Context, date, content string) error {
	if strings.TrimSpace(date) == "" {
		date = time.Now().Format("2006-01-02")
	}
	root := filepath.Join(s.store.DataDir(), "pet-workspace", "diary")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, date+".md"), []byte(content), 0o600)
}

func (s *Service) PetDiaryPaths(ctx context.Context) ([]string, error) {
	root := filepath.Join(s.store.DataDir(), "pet-workspace", "diary")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	return []string{root}, nil
}
