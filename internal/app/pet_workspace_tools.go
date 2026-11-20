package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
)

// petCoreChangesPath is the server-written record of what the pet changed in
// its own identity and principles. The pet is the Owner's avatar and is meant
// to revise its principles as it learns, so the change is never blocked; but a
// principle about "what I may decide alone" is the pet's own boundary, and the
// Owner has to be able to see when that moved.
const (
	petCoreChangesPath     = "core/changes.md"
	petCoreChangesMaxBytes = 60000
	petCorrectionEventKind = "owner_correction"
	petCorrectionMaxAge    = 7 * 24 * time.Hour
)

// petUpdateJudgmentDocument saves identity.md or principles.md and records the
// difference for the Owner. Updating principles also closes the Owner's open
// corrections: writing the lesson down is what they were waiting for.
func (s *Service) petUpdateJudgmentDocument(ctx context.Context, name, content string) error {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	before := ""
	if root != "" {
		if data, err := os.ReadFile(filepath.Join(root, "core", name)); err == nil {
			before = string(data)
		}
	}
	if err := s.PetSaveWorkbenchDocument(ctx, "core", name, content); err != nil {
		return err
	}
	if strings.TrimSpace(before) != strings.TrimSpace(content) && root != "" {
		s.recordPetCoreChange(ctx, root, name, before, content)
	}
	if name == "principles.md" {
		_, _ = s.store.MarkOpenPetEvents(ctx, []string{petCorrectionEventKind}, nil, "absorbed")
	}
	return nil
}

func (s *Service) recordPetCoreChange(ctx context.Context, root, name, before, after string) {
	diff, added, removed := prompt.UnifiedDiff(name+"（改前）", name+"（改后）", before, after)
	if diff == "" {
		return
	}
	now := s.now()
	entry := fmt.Sprintf("## %s %s（+%d −%d 行）\n\n```diff\n%s\n```\n",
		now.Local().Format("2006-01-02 15:04"), name, added, removed, strings.TrimRight(diff, "\n"))
	path := filepath.Join(root, filepath.FromSlash(petCoreChangesPath))
	header := "# 准则与身份的变更记录\n\n由 AHA 自动记录：宠物每次修改 identity.md 或 principles.md 时写入差异，新的在上。宠物和 owner 都不需要手动维护。\n\n"
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = strings.TrimPrefix(string(data), header)
	}
	body := entry + "\n" + existing
	// Keep the newest entries: cut at an entry boundary so no diff is left half.
	if len(body) > petCoreChangesMaxBytes {
		body = body[:petCoreChangesMaxBytes]
		if cut := strings.LastIndex(body, "\n## "); cut > 0 {
			body = body[:cut+1]
		}
	}
	if err := os.WriteFile(path, []byte(header+body), 0o600); err != nil {
		return
	}
	detail, _ := json.Marshal(map[string]any{"document": name, "added": added, "removed": removed})
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: now.UTC(), Actor: "pet", Kind: "core_changed",
		Detail: string(detail), Status: "recorded",
	})
}

// PetCorrection is one decision of the pet that the Owner rejected, with the
// Owner's reason. It stays on the work state until the pet updates its
// principles, so a rejection becomes a lesson instead of only a closed approval.
type PetCorrection struct {
	At         string `json:"at"`
	ApprovalID string `json:"approval_id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Title      string `json:"title"`
	OwnerNote  string `json:"owner_note"`
}

func (s *Service) recordPetCorrection(ctx context.Context, approval domain.PetApproval, note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	detail, _ := json.Marshal(map[string]string{
		"approval_id": approval.ID, "kind": approval.Kind, "title": approval.Title, "owner_note": note,
	})
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "owner", Kind: petCorrectionEventKind,
		Detail: string(detail), Status: "open",
	})
}

// petOpenCorrections returns the corrections the pet has not yet turned into a
// principle. They age out after a week: a correction that needed no principle
// should not follow the pet around for ever.
func (s *Service) petOpenCorrections(ctx context.Context) []PetCorrection {
	events, err := s.store.ListOpenPetEventsByKind(ctx, []string{petCorrectionEventKind}, 10)
	if err != nil {
		return nil
	}
	cutoff := s.now().UTC().Add(-petCorrectionMaxAge)
	result := make([]PetCorrection, 0, len(events))
	for _, event := range events {
		if event.At.Before(cutoff) {
			continue
		}
		var payload struct {
			ApprovalID string `json:"approval_id"`
			Kind       string `json:"kind"`
			Title      string `json:"title"`
			OwnerNote  string `json:"owner_note"`
		}
		if json.Unmarshal([]byte(event.Detail), &payload) != nil || strings.TrimSpace(payload.OwnerNote) == "" {
			continue
		}
		result = append(result, PetCorrection{
			At: event.At.Format(time.RFC3339), ApprovalID: payload.ApprovalID, Kind: payload.Kind,
			Title: payload.Title, OwnerNote: payload.OwnerNote,
		})
	}
	return result
}

func normalizePetEntryDate(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return now.Format("2006-01-02"), nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return "", fmt.Errorf("日期必须是 YYYY-MM-DD")
	}
	return value, nil
}

func (s *Service) PetWriteWorklog(ctx context.Context, date, content string) error {
	date, err := normalizePetEntryDate(date, s.now().UTC())
	if err != nil {
		return err
	}
	return s.PetSaveWorkbenchDocument(ctx, "worklog", date+".md", content)
}

func (s *Service) PetUpdateCore(ctx context.Context, document, content string) error {
	document = strings.ToLower(strings.TrimSpace(document))
	switch document {
	case "identity", "identity.md":
		return s.petUpdateJudgmentDocument(ctx, "identity.md", content)
	case "principles", "principles.md":
		return s.petUpdateJudgmentDocument(ctx, "principles.md", content)
	case "loops", "loops.md":
		if len(content) > petCoreLoopsMaxBytes {
			return fmt.Errorf("loops 文档不能超过 %d 字节；请先删除已关闭项并把留痕写入当天 worklog", petCoreLoopsMaxBytes)
		}
		return s.PetSaveWorkbenchDocument(ctx, "core", "loops.md", content)
	case "agenda", "agenda.md":
		if len(content) > petCoreAgendaMaxBytes {
			return fmt.Errorf("agenda 文档不能超过 %d 字节；请先删除已完成项并把留痕写入当天 worklog", petCoreAgendaMaxBytes)
		}
		return s.PetSaveWorkbenchDocument(ctx, "core", "agenda.md", content)
	default:
		return fmt.Errorf("document 必须是 identity、principles、loops 或 agenda")
	}
}
