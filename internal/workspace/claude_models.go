package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const claudeInitializeRequest = `{"type":"control_request","request_id":"aha-claude-models","request":{"subtype":"initialize"}}` + "\n"

func probeClaudeBackend(ctx context.Context, runner Runner, item domain.Workspace) (map[string]any, bool) {
	result, err := runDetectionCommand(ctx, item, runner, Command{
		Executable: "claude", Args: []string{"--version"}, Dir: item.RootPath, Timeout: 20 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		if commandUnavailable(result, err) {
			return probeResult(probeNotInstalled, ""), false
		}
		message := "Claude 检测执行失败: " + safeProbeDetail(item, result, err)
		return probeResult(probeExecutionFailed, message), true
	}
	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		version = strings.TrimSpace(result.Stderr)
	}
	probe := map[string]any{"status": probeReady, "version": safeDisplayText(item, version)}
	authResult, authErr := runDetectionCommand(ctx, item, runner, Command{
		Executable: "claude", Args: []string{"auth", "status", "--json"}, Dir: item.RootPath,
		Env: claudeNativeEnvironment(), Timeout: 20 * time.Second,
	})
	if authErr != nil || authResult.ExitCode != 0 {
		probe["auth_status"] = "unknown"
		return probe, false
	}
	var status struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if err := json.Unmarshal([]byte(authResult.Stdout), &status); err != nil {
		probe["auth_status"] = "unknown"
		return probe, false
	}
	probe["auth_status"] = "not_logged_in"
	if status.LoggedIn {
		probe["auth_status"] = "logged_in"
	}
	if status.AuthMethod != "" {
		probe["auth_method"] = safeDisplayText(item, status.AuthMethod)
	}
	if status.APIProvider != "" {
		probe["api_provider"] = safeDisplayText(item, status.APIProvider)
	}
	if !status.LoggedIn {
		return probe, false
	}
	models, modelErr := detectClaudeModels(ctx, runner, item)
	if modelErr != nil {
		probe["models_error"] = safeDisplayText(item, modelErr.Error())
		return probe, false
	}
	probe["models"] = models
	probe["models_updated_at"] = time.Now().UTC()
	return probe, false
}

func detectClaudeModels(ctx context.Context, runner Runner, item domain.Workspace) ([]domain.ClaudeModelOption, error) {
	result, err := runDetectionCommand(ctx, item, runner, Command{
		Executable: "claude",
		Args: []string{
			"--input-format", "stream-json",
			"--output-format", "stream-json",
			"--verbose",
		},
		Dir: item.RootPath, Env: claudeNativeEnvironment(), Stdin: claudeInitializeRequest,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("%s", safeProbeDetail(item, result, err))
	}
	return parseClaudeModelResponse(result.Stdout)
}

func parseClaudeModelResponse(output string) ([]domain.ClaudeModelOption, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var envelope struct {
			Type     string `json:"type"`
			Response struct {
				Subtype string `json:"subtype"`
				Error   string `json:"error"`
				Payload struct {
					Models []struct {
						Value                    string   `json:"value"`
						ResolvedModel            string   `json:"resolvedModel"`
						DisplayName              string   `json:"displayName"`
						Description              string   `json:"description"`
						SupportsEffort           bool     `json:"supportsEffort"`
						SupportedEffortLevels    []string `json:"supportedEffortLevels"`
						SupportsAdaptiveThinking bool     `json:"supportsAdaptiveThinking"`
						SupportsFastMode         bool     `json:"supportsFastMode"`
						SupportsAutoMode         bool     `json:"supportsAutoMode"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil || envelope.Type != "control_response" {
			continue
		}
		if envelope.Response.Subtype != "success" {
			if envelope.Response.Error != "" {
				return nil, fmt.Errorf("%s", envelope.Response.Error)
			}
			return nil, fmt.Errorf("Claude initialize request failed")
		}
		models := make([]domain.ClaudeModelOption, 0, len(envelope.Response.Payload.Models))
		for _, model := range envelope.Response.Payload.Models {
			wireModel := strings.TrimSpace(model.Value)
			displayName := strings.TrimSpace(model.DisplayName)
			if wireModel == "" {
				continue
			}
			if displayName == "" {
				displayName = wireModel
			}
			models = append(models, domain.ClaudeModelOption{
				WireModel: wireModel, ResolvedModel: strings.TrimSpace(model.ResolvedModel),
				DisplayName: displayName, Description: strings.TrimSpace(model.Description),
				SupportsEffort: model.SupportsEffort, SupportedEffortLevels: model.SupportedEffortLevels,
				SupportsAdaptiveThinking: model.SupportsAdaptiveThinking,
				SupportsFastMode:         model.SupportsFastMode, SupportsAutoMode: model.SupportsAutoMode,
			})
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("Claude returned no available models")
		}
		return models, nil
	}
	return nil, fmt.Errorf("Claude returned no initialize response")
}

func ClaudeModelsFromCapabilities(capabilities map[string]any) []domain.ClaudeModelOption {
	probe, ok := capabilities["claude"].(map[string]any)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(probe["models"])
	if err != nil {
		return nil
	}
	var models []domain.ClaudeModelOption
	if json.Unmarshal(raw, &models) != nil {
		return nil
	}
	return models
}

func claudeNativeEnvironment() map[string]string {
	return map[string]string{
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": "",
		"ANTHROPIC_BASE_URL":   "",
		"ANTHROPIC_MODEL":      "",
	}
}
