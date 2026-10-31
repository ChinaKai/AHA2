package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type petPromptData struct {
	Nickname    string
	Identity    string
	Principles  string
	Snapshot    string
	Layout      string
	Tools       string
	Page        string
	IdleMinutes int
}

func (s *Service) petToolPrompt() string {
	var builder strings.Builder
	for _, tool := range allPetMCPTools() {
		fmt.Fprintf(&builder, "- %s：%s\n", tool.Name, tool.Description)
	}
	return strings.TrimSpace(builder.String())
}

func (s *Service) renderPetAssistantPrompt(ctx context.Context, nickname, identity, principles, snapshot string, layout PetLayout) (string, error) {
	if s.prompts == nil {
		return "", fmt.Errorf("prompt engine unavailable")
	}
	rendered, err := s.prompts.RenderTemplate(ctx, "identity.pet-task", petPromptData{})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(snapshot) != "" {
		rendered += "\n\n只读快照：\n" + snapshot
	}
	if layout.ViewportWidth > 0 {
		rendered += "\n\n当前页面布局：\n" + petLayoutPrompt(layout)
	}
	return rendered, nil
}

func (s *Service) renderPetBrainPrompt(ctx context.Context, assistant domain.OwnerAssistant, idleMinutes int, page, snapshot string, layout PetLayout) (string, error) {
	if s.prompts == nil {
		return "", fmt.Errorf("prompt engine unavailable")
	}
	return s.prompts.RenderTemplate(ctx, "identity.pet-brain", petPromptData{
		Nickname: assistantNickname(&assistant), Snapshot: snapshot, Layout: petLayoutPrompt(layout),
		Tools: s.petToolPrompt(), Page: page, IdleMinutes: idleMinutes,
	})
}

func (s *Service) renderPetTaskPrompt(ctx context.Context, assistant domain.OwnerAssistant) (string, error) {
	if s.prompts == nil {
		return "", fmt.Errorf("prompt engine unavailable")
	}
	return s.prompts.RenderTemplate(ctx, "identity.pet-task", petPromptData{
		Nickname: assistantNickname(&assistant), Tools: s.petToolPrompt(),
	})
}
