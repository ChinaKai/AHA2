package httpapi

import (
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
)

// authorizeAgentDomainRoute runs after token authentication. Its finite path
// families are the existing Agent APIs, never owner Web routes. MCP dispatch
// determines read/write from the named tool and uses the same app boundary.
func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
	path := request.URL.Path
	if strings.HasSuffix(path, "/mcp") {
		return true
	}
	domainName := ""
	for prefix, name := range map[string]string{"/api/v1/agent/hardware": "hardware", "/api/v1/agent/browser": "browser", "/api/v1/agent/processes": "workspace", "/api/v1/agent/project/workspaces": "workspace", "/api/v1/agent/project/runtimes": "workspace", "/api/v1/agent/desktop": "desktop"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			domainName = name
			break
		}
	}
	if domainName == "" {
		return true
	}
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
		return false
	}
	_, err := s.app.AgentDomainCallContext(request.Context(), claims, domainName, request.Method != http.MethodGet)
	if err != nil {
		writeAgentControlError(writer, err)
		return false
	}
	return true
}
