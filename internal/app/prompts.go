package app

import (
	"context"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
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

// PromptEntryCatalog is the two-entry view of the template registry for the
// phase 3 template convergence. It is metadata only and does not change the
// legacy template list or write path.
func (s *Service) PromptEntryCatalog(ctx context.Context) (prompt.EntryCatalog, error) {
	return s.prompts.EntryCatalog(ctx, s.promptLanguage(ctx))
}

// PreviewPromptTemplateMigration reports how legacy overrides map onto entry
// anchors. It is read-only and never rewrites overrides.
func (s *Service) PreviewPromptTemplateMigration(ctx context.Context) (prompt.MigrationPreview, error) {
	return s.prompts.PreviewMigration(ctx)
}

// PreviewPromptEntryAnchor renders one entry anchor with sample data for the
// read-only Web preview. It never falls back to another anchor or template.
func (s *Service) PreviewPromptEntryAnchor(ctx context.Context, entryID, key string) (prompt.AnchorPreview, error) {
	return s.prompts.PreviewEntryAnchor(ctx, entryID, key, s.promptLanguage(ctx))
}

// PreviewPromptScenario renders a complete demo Prompt Snapshot through the
// production builder. It is read-only: no Turn/Task, channel message, or model
// call is created, and no draft content is accepted from the page.
func (s *Service) PreviewPromptScenario(ctx context.Context, scenarioID, message, source string) (prompt.ScenarioPreview, error) {
	return s.prompts.PreviewScenario(ctx, scenarioID, message, source, s.promptLanguage(ctx))
}
