package httpapi

import (
	"net"
	"net/http"
	"sync/atomic"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// accessScopeGate answers or refuses a request based on the caller's address.
//
// The listen address is fixed at launch and AHA2 binds every interface by
// default, so this is the only thing that can narrow who is served without
// restarting the process. It is deliberately a request filter rather than a
// socket option so a change takes effect immediately.
type accessScopeGate struct {
	scope atomic.Value // string, one of the domain.AccessScope* values
}

func newAccessScopeGate(scope string) *accessScopeGate {
	gate := &accessScopeGate{}
	gate.set(scope)
	return gate
}

func (gate *accessScopeGate) set(scope string) {
	gate.scope.Store(domain.NormalizeAccessScope(scope))
}

func (gate *accessScopeGate) current() string {
	scope, _ := gate.scope.Load().(string)
	return domain.NormalizeAccessScope(scope)
}

// allows reports whether a request from remoteAddr may be served.
func (gate *accessScopeGate) allows(remoteAddr string) bool {
	if gate.current() != domain.AccessScopeLocal {
		return true
	}
	// Only this machine. A caller we cannot parse is refused rather than trusted:
	// an unparseable address means the assumption behind the check is wrong.
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func (s *Server) accessScopeAllows(remoteAddr string) bool {
	if s.accessScope == nil {
		// No gate configured means no restriction, which preserves behaviour for
		// embedders that construct a server directly.
		return true
	}
	return s.accessScope.allows(remoteAddr)
}

// enforceAccessScope wraps the handler tree. It runs outside authentication so a
// refused caller learns nothing about which endpoints exist.
func (s *Server) enforceAccessScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !s.accessScopeAllows(request.RemoteAddr) {
			writeError(writer, http.StatusForbidden, "access_scope_local_only")
			return
		}
		next.ServeHTTP(writer, request)
	})
}
