package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetIdeaInput struct {
	Title             string   `json:"title"`
	Summary           string   `json:"summary"`
	Reason            string   `json:"reason"`
	Plan              string   `json:"plan"`
	ExpectedOutcome   string   `json:"expected_outcome"`
	Risk              string   `json:"risk"`
	PermissionsNeeded []string `json:"permissions_needed"`
}

// PetSubmitIdea records an owner decision, not an action. Approval means the
// pet may pursue the idea; it does not grant permissions or execute anything.
func (s *Service) PetSubmitIdea(ctx context.Context, input PetIdeaInput) (domain.PetApproval, error) {
	title := strings.TrimSpace(input.Title)
	summary := strings.TrimSpace(input.Summary)
	reason := strings.TrimSpace(input.Reason)
	plan := strings.TrimSpace(input.Plan)
	outcome := strings.TrimSpace(input.ExpectedOutcome)
	if title == "" || len([]rune(title)) > 100 {
		return domain.PetApproval{}, fmt.Errorf("想法标题不能为空且不能超过 100 个字符")
	}
	if summary == "" || len([]rune(summary)) > 2000 {
		return domain.PetApproval{}, fmt.Errorf("想法说明不能为空且不能超过 2000 个字符")
	}
	reason, reasonErr := requirePetApprovalReason(reason)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	if len([]rune(reason)) > 1000 {
		return domain.PetApproval{}, fmt.Errorf("reason 不能超过 1000 个字符")
	}
	if len([]rune(plan)) > 4000 || len([]rune(outcome)) > 1000 {
		return domain.PetApproval{}, fmt.Errorf("想法计划或预期收益过长")
	}
	risk := strings.ToLower(strings.TrimSpace(input.Risk))
	if risk != "low" && risk != "medium" && risk != "high" {
		risk = "medium"
	}
	permissions := make([]string, 0, len(input.PermissionsNeeded))
	seen := map[string]bool{}
	for _, raw := range input.PermissionsNeeded {
		permission := normalizePetPermission(raw)
		if permission == "" {
			return domain.PetApproval{}, fmt.Errorf("未知的权限名称：%s", raw)
		}
		if !seen[permission] {
			seen[permission] = true
			permissions = append(permissions, permission)
		}
	}
	pending := 0
	if approvals, err := s.PetApprovals(ctx, 50); err == nil {
		for _, approval := range approvals {
			if approval.Kind == "idea" && approval.Status == petApprovalPending {
				pending++
			}
		}
	}
	if pending >= 5 {
		return domain.PetApproval{}, fmt.Errorf("待审批的想法已经有 5 个，请先等主人处理")
	}
	summaryText := summary
	if plan != "" {
		summaryText += "\n\n计划：" + plan
	}
	if outcome != "" {
		summaryText += "\n\n预期收益：" + outcome
	}
	if len(permissions) > 0 {
		labels := make([]string, 0, len(permissions))
		for _, permission := range permissions {
			labels = append(labels, petPermissionLabels[permission])
		}
		summaryText += "\n\n需要的权限（需另行批准）：" + strings.Join(labels, "、")
	}
	payload, _ := json.Marshal(map[string]any{
		"title": title, "summary": summary, "reason": reason, "plan": plan, "expected_outcome": outcome,
		"permissions_needed": permissions,
	})
	approval, err := s.CreatePetApproval(ctx, PetApprovalInput{
		Kind: "idea", Title: "想法：" + title, Summary: truncateAssistantText(summaryText, 1900), Reason: reason, Risk: risk, Payload: string(payload),
	})
	if err != nil {
		return domain.PetApproval{}, err
	}
	s.recordPetWork(ctx, "idea_submitted", approval.ID, title)
	return approval, nil
}
