package app

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) PromptTemplates(ctx context.Context) ([]domain.PromptTemplate, error) {
	return s.prompts.Templates(ctx)
}

func (s *Service) UpdatePromptTemplate(ctx context.Context, id, content string) error {
	return s.prompts.UpdateTemplate(ctx, id, content, s.now().UTC())
}

func (s *Service) ResetPromptTemplate(ctx context.Context, id string) error {
	return s.prompts.ResetTemplate(ctx, id)
}
