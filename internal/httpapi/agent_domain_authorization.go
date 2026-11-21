package httpapi

import (
	"net/http"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
)

type agentRouteMode uint8

const (
	agentRouteBusiness agentRouteMode = iota + 1
	agentRouteResource
	agentRouteMCP
)

type agentRouteAccess uint8

const (
	agentRouteRead agentRouteAccess = iota + 1
	agentRouteWrite
	agentRouteBookkeeping
	agentRouteTools
)

type agentTokenRoutePolicy struct {
	Domain string
	Access agentRouteAccess
	Mode   agentRouteMode
}

// authorizeAgentDomainRoute uses the pattern chosen by the server's ServeMux.
// An opaque resource name (including an encoded slash) cannot select a policy.
// Business-scoped endpoints retain their existing per-operation authorization;
// shared resources always pass the common domain gate. Unknown routes fail shut.
func (s *Server) authorizeAgentDomainRoute(writer http.ResponseWriter, request *http.Request, claims agentapi.Claims) bool {
	policy, known := agentTokenRoutePolicies[request.Pattern]
	if !known {
		writeError(writer, http.StatusForbidden, "agent_route_policy_missing")
		return false
	}
	if policy.Mode == agentRouteMCP {
		method, path, valid := strings.Cut(request.Pattern, " ")
		if !valid || method != http.MethodPost || request.Method != http.MethodPost || request.URL.EscapedPath() != path {
			writeError(writer, http.StatusForbidden, "agent_route_policy_mismatch")
			return false
		}
		return true // The exact registered MCP handler checks the named tool.
	}
	if policy.Mode == agentRouteBusiness && policy.Domain != "attachment" {
		return true
	}
	if policy.Mode != agentRouteResource && !(policy.Mode == agentRouteBusiness && policy.Domain == "attachment") {
		writeError(writer, http.StatusForbidden, "agent_route_policy_invalid")
		return false
	}
	if s.app == nil {
		writeError(writer, http.StatusServiceUnavailable, "agent_api_unavailable")
		return false
	}
	var err error
	if policy.Domain == "attachment" {
		// This dual-auth resource keeps ordinary collaborators' own-Task reads,
		// but an expired Turn cannot retain data access through a live token.
		_, err = s.app.AgentCallContext(request.Context(), claims, false)
	} else {
		_, err = s.app.AgentDomainCallContext(request.Context(), claims, policy.Domain, policy.Access == agentRouteWrite)
	}
	if err != nil {
		writeAgentControlError(writer, err)
		return false
	}
	return true
}
