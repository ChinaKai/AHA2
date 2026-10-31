package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PetProxySettingsView struct {
	Mode                       string `json:"mode"`
	HTTPProxy                  string `json:"http_proxy,omitempty"`
	HTTPSProxy                 string `json:"https_proxy,omitempty"`
	NoProxy                    string `json:"no_proxy,omitempty"`
	ManagedProfileID           string `json:"managed_profile_id,omitempty"`
	ManagedNodeID              string `json:"managed_node_id,omitempty"`
	ManagedRefreshIntervalMins int    `json:"managed_refresh_interval_minutes,omitempty"`
	UpdatedAt                  string `json:"updated_at,omitempty"`
}

type PetSystemSettingsView struct {
	OS       string                  `json:"os"`
	Arch     string                  `json:"arch"`
	DataDir  string                  `json:"data_dir"`
	Security domain.SecuritySettings `json:"security"`
	Proxy    PetProxySettingsView    `json:"proxy"`
}

func (s *Service) petSystemReadable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowViewTasks && !assistant.PetAllowManageSystem {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启查看系统设置权限")
	}
	return assistant, nil
}

func (s *Service) petSystemWritable(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageSystem {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启管理系统设置权限")
	}
	return assistant, nil
}

func (s *Service) PetSystemSettings(ctx context.Context) (PetSystemSettingsView, error) {
	if _, err := s.petSystemReadable(ctx); err != nil {
		return PetSystemSettingsView{}, err
	}
	security, err := s.store.SecuritySettings(ctx)
	if err != nil {
		return PetSystemSettingsView{}, err
	}
	proxy, err := s.store.ProxySettings(ctx)
	if err != nil {
		return PetSystemSettingsView{}, err
	}
	updated := ""
	if !proxy.UpdatedAt.IsZero() {
		updated = proxy.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return PetSystemSettingsView{
		OS: runtime.GOOS, Arch: runtime.GOARCH, DataDir: s.store.DataDir(),
		Security: security,
		Proxy: PetProxySettingsView{
			Mode: proxy.Mode, HTTPProxy: redactProxyURL(proxy.HTTPProxy), HTTPSProxy: redactProxyURL(proxy.HTTPSProxy),
			NoProxy: proxy.NoProxy, ManagedProfileID: proxy.ManagedProfileID, ManagedNodeID: proxy.ManagedNodeID,
			ManagedRefreshIntervalMins: proxy.ManagedRefreshIntervalMins, UpdatedAt: updated,
		},
	}, nil
}

func redactProxyURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User(parsed.User.Username())
	}
	return parsed.String()
}

func (s *Service) PetRequestSecuritySettingsUpdate(ctx context.Context, validateOrigin bool, accessScope string, reason ...string) (domain.PetApproval, error) {
	if _, err := s.petSystemWritable(ctx); err != nil {
		return domain.PetApproval{}, err
	}
	reasonText, reasonErr := requiredPetApprovalReason(reason...)
	if reasonErr != nil {
		return domain.PetApproval{}, reasonErr
	}
	scope := strings.ToLower(strings.TrimSpace(accessScope))
	if scope != "" && scope != domain.AccessScopeLocal && scope != domain.AccessScopeLAN {
		return domain.PetApproval{}, fmt.Errorf("access_scope 必须是 local 或 lan")
	}
	payload, _ := json.Marshal(map[string]any{
		"validate_origin": validateOrigin,
		"access_scope":    scope,
	})
	scopeText := scope
	if scopeText == "" {
		scopeText = "inherit"
	}
	return s.CreatePetApproval(ctx, PetApprovalInput{
		Kind:   "security_settings_update",
		Title:  "修改安全设置",
		Reason: reasonText,
		Summary: fmt.Sprintf(
			"宠物申请修改安全设置：access_scope=%s，validate_origin=%t。批准后立即应用到当前服务。",
			scopeText, validateOrigin,
		),
		Risk: "high", Payload: string(payload),
	})
}
