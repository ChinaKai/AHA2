package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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
		return s.PetSaveWorkbenchDocument(ctx, "core", "identity.md", content)
	case "principles", "principles.md":
		return s.PetSaveWorkbenchDocument(ctx, "core", "principles.md", content)
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
