package app

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) promptLanguage(ctx context.Context) string {
	language := domain.LanguageAuto
	if settings, err := s.store.GeneralSettings(ctx); err == nil {
		language = settings.Language
	}
	return language
}

func (s *Service) PromptTemplates(ctx context.Context) ([]domain.PromptTemplate, error) {
	return s.prompts.TemplatesForLanguage(ctx, s.promptLanguage(ctx))
}

func (s *Service) UpdatePromptTemplate(ctx context.Context, id, content string) error {
	return s.prompts.UpdateTemplate(ctx, id, content, s.now().UTC())
}

func (s *Service) ResetPromptTemplate(ctx context.Context, id string) error {
	return s.prompts.ResetTemplate(ctx, id)
}
