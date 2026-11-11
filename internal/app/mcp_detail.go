package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Service) MCPServerDetail(ctx context.Context, id string) (domain.MCPServer, error) {
	item, err := s.MCPServerByID(ctx, id)
	if err != nil {
		return domain.MCPServer{}, err
	}
	if item.Builtin && item.ID == "mcp_pet" {
		item.Tools = petMCPToolDetails()
		item.Health = "ok"
		item.LastError = ""
	}
	return item, nil
}

func (s *Service) probeMCPServer(ctx context.Context, item domain.MCPServer) (domain.MCPServer, error) {
	if item.Builtin && item.ID == "mcp_pet" {
		item.Tools = petMCPToolDetails()
		item.Health = "ok"
		item.LastError = ""
		item.LastCheckedAt = s.now().UTC()
		return item, nil
	}
	if item.Transport == "stdio" && item.Command != "" {
		tools, fetchErr := fetchStdioTools(ctx, item)
		return s.persistMCPProbe(ctx, item, tools, fetchErr), fetchErr
	}
	if item.Transport == "http" && item.URL != "" {
		tools, fetchErr := fetchMCPTools(ctx, item)
		return s.persistMCPProbe(ctx, item, tools, fetchErr), fetchErr
	}
	return item, nil
}

// persistMCPProbe records one tools/list probe. A failed probe keeps the last
// known tool list: an unavailable server must not silently empty the allowlist
// a Turn still needs. The error is redacted with the server's own env/header
// values before it can reach the database or a log line. Only the probe columns
// are written, so an Owner config edit that raced the probe is not overwritten.
func (s *Service) persistMCPProbe(ctx context.Context, item domain.MCPServer, tools []domain.MCPTool, fetchErr error) domain.MCPServer {
	item.LastCheckedAt = s.now().UTC()
	item.UpdatedAt = item.LastCheckedAt
	if fetchErr == nil {
		item.Tools = tools
		item.Health = "ok"
		item.LastError = ""
	} else {
		item.Health = "error"
		item.LastError = redactPetMCPText(fetchErr.Error(), item)
	}
	if err := s.store.UpdateMCPServerProbe(ctx, item.ID, item.Tools, item.Health, item.LastError, item.LastCheckedAt, item.UpdatedAt); err != nil {
		log.Printf("mcp probe cache write failed: server=%s", item.ID)
	}
	return item
}

func (s *Service) TestMCPServer(ctx context.Context, id string) (domain.MCPServer, error) {
	item, err := s.MCPServerByID(ctx, id)
	if err != nil {
		return domain.MCPServer{}, err
	}
	item, err = s.probeMCPServer(ctx, item)
	if err != nil {
		return item, err
	}
	if len(item.Tools) == 0 && !item.Builtin {
		return item, fmt.Errorf("MCP 未返回工具列表")
	}
	return item, nil
}

func petMCPToolDetails() []domain.MCPTool {
	tools := allPetMCPTools()
	result := make([]domain.MCPTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, domain.MCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
	}
	return result
}

func fetchMCPTools(ctx context.Context, item domain.MCPServer) ([]domain.MCPTool, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	headers := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	for key, value := range item.Headers {
		headers[key] = value
	}
	send := func(method string, params any) ([]domain.MCPTool, error) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		request, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, item.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if response.StatusCode >= 400 {
			return nil, fmt.Errorf("MCP HTTP %d", response.StatusCode)
		}
		text := string(raw)
		if strings.HasPrefix(strings.TrimSpace(text), "event:") || strings.Contains(text, "\ndata:") {
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "data:") {
					text = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				}
			}
		}
		var decoded struct {
			Result struct {
				Tools []domain.MCPTool `json:"tools"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, err
		}
		if decoded.Error != nil {
			return nil, fmt.Errorf("MCP error")
		}
		return decoded.Result.Tools, nil
	}
	_, _ = send("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "aha2", "version": "1"}})
	return send("tools/list", map[string]any{})
}

func fetchStdioTools(ctx context.Context, item domain.MCPServer) ([]domain.MCPTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command, args := mcpStdioCommand(item)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for key, value := range item.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	reader := bufio.NewReader(stdout)
	send := func(payload any) error {
		encoded, _ := json.Marshal(payload)
		_, err := stdin.Write(append(encoded, '\n'))
		return err
	}
	readID := func(id int) ([]domain.MCPTool, error) {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil, fmt.Errorf("MCP stdio: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
			}
			var envelope struct {
				ID     int `json:"id"`
				Result struct {
					Tools []domain.MCPTool `json:"tools"`
				} `json:"result"`
				Error any `json:"error"`
			}
			if json.Unmarshal([]byte(line), &envelope) != nil || envelope.ID != id {
				continue
			}
			if envelope.Error != nil {
				return nil, fmt.Errorf("MCP stdio error: %v", envelope.Error)
			}
			return envelope.Result.Tools, nil
		}
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "aha2", "version": "1"}}}); err != nil {
		return nil, err
	}
	if _, err := readID(1); err != nil {
		return nil, err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	return readID(2)
}
