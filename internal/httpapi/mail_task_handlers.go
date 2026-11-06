package httpapi

import (
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// taskMailOptions is the Owner-side projection the Web Task mail panel and
// compose dialog need: effective accounts (with the binding that granted
// them), visible templates, sendable Task attachments, registered build
// artifacts and recent message snapshots. It never returns credential
// references or Secret values.
func (s *Server) taskMailOptions(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	task, err := s.store.Task(ctx, request.PathValue("id"))
	if err != nil {
		if isNoRows(err) {
			writeError(writer, http.StatusNotFound, "task_not_found")
			return
		}
		writeAgentMailFailure(writer, err)
		return
	}
	bindings, err := s.store.TaskMailBindings(ctx, task.ID, task.WorkspaceID, task.ProjectID, 200)
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	accounts := make([]map[string]any, 0, len(bindings))
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
		aliases := make([]string, 0, len(account.Aliases))
		for _, alias := range account.Aliases {
			if strings.TrimSpace(alias.Address) != "" {
				aliases = append(aliases, strings.TrimSpace(alias.Address))
			}
		}
		accounts = append(accounts, map[string]any{
			"account_id":            account.ID,
			"binding_id":            binding.ID,
			"binding_scope":         string(binding.ScopeType),
			"from_address":          account.Address,
			"display_name":          account.DisplayName,
			"aliases":               aliases,
			"credential_configured": account.CredentialConfigured,
			"status":                string(account.Status),
			"default_template_id":   binding.DefaultTemplateID,
			"approval_mode":         string(binding.ApprovalMode),
			"allowed_recipients":    binding.AllowedRecipients,
		})
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

	attachments, _ := s.store.ListTaskAttachmentsIncludingDrafts(ctx, task.ID)
	artifactItems, _ := s.store.BuildArtifactsByTask(ctx, task.ID, 100)
	artifacts := make([]domain.BuildArtifact, 0, len(artifactItems))
	for _, artifact := range artifactItems {
		// Phase one only allows Task attachments; an external URL registration
		// is reserved and must not become a compose option.
		if artifact.StorageType == domain.BuildArtifactStorageAHAAttachment {
			artifacts = append(artifacts, artifact)
		}
	}

	messages, err := s.store.ListMailMessages(ctx, store.MailMessageFilter{TaskID: task.ID, Limit: 50})
	if err != nil {
		writeAgentMailFailure(writer, err)
		return
	}
	cards := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		messageAttachments, _ := s.store.MailAttachments(ctx, message.ID, 100)
		deliveries, _ := s.store.MailDeliveriesByMessage(ctx, message.ID, 20)
		cards = append(cards, map[string]any{
			"message": message, "attachments": messageAttachments, "deliveries": deliveries,
		})
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "task_id": task.ID, "accounts": accounts, "templates": templates,
		"attachments": attachments, "build_artifacts": artifacts, "messages": cards,
	})
}
