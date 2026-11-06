package app

import (
	"context"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// MailTaskResources returns the accounts and templates a Task can see. Accounts
// come from enabled Task -> Workspace -> Project bindings in that order, so a
// Task binding wins when the same account is bound more than once. Templates
// are global plus the Task Project's active templates. The result never
// contains credentials or Secret references: store reads leave those empty.
func (s *Service) MailTaskResources(ctx context.Context, task domain.Task) ([]domain.MailAccount, []domain.MailTemplate, map[string]string) {
	if s == nil || s.store == nil || strings.TrimSpace(task.ID) == "" {
		return nil, nil, nil
	}
	bindings, err := s.store.TaskMailBindings(ctx, task.ID, task.WorkspaceID, task.ProjectID, 200)
	if err != nil {
		return nil, nil, nil
	}
	accounts := make([]domain.MailAccount, 0, len(bindings))
	defaultTemplates := make(map[string]string, len(bindings))
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		if seen[binding.AccountID] {
			continue
		}
		account, err := s.store.MailAccount(ctx, binding.AccountID)
		if err != nil {
			continue
		}
		seen[account.ID] = true
		accounts = append(accounts, account)
		if strings.TrimSpace(binding.DefaultTemplateID) != "" {
			defaultTemplates[account.ID] = binding.DefaultTemplateID
		}
	}
	templates := make([]domain.MailTemplate, 0)
	if global, err := s.store.MailTemplates(ctx, domain.MailTemplateScopeGlobal, "", 100); err == nil {
		for _, template := range global {
			if template.Status == domain.MailTemplateStatusActive {
				templates = append(templates, template)
			}
		}
	}
	if strings.TrimSpace(task.ProjectID) != "" {
		if project, err := s.store.MailTemplates(ctx, domain.MailTemplateScopeProject, task.ProjectID, 100); err == nil {
			for _, template := range project {
				if template.Status == domain.MailTemplateStatusActive {
					templates = append(templates, template)
				}
			}
		}
	}
	return accounts, templates, defaultTemplates
}
