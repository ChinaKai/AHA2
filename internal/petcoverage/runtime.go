package petcoverage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// VerifyMux checks the scan against a live ServeMux: for every scanned
// operation of server, a request built from its pattern must be routed by
// mux to exactly that registered pattern. A scanned route the mux does not
// serve, or serves under a different pattern, is an error.
func VerifyMux(mux *http.ServeMux, routes []Registration, server string) (int, error) {
	checked := 0
	var problems []string
	for _, route := range routes {
		if route.Server != server {
			continue
		}
		method, path, _ := strings.Cut(route.Operation, " ")
		want := route.Operation
		if method == "ANY" {
			method, want = http.MethodPatch, path
		}
		request := httptest.NewRequest(method, concretePath(path), nil)
		if _, pattern := mux.Handler(request); pattern != want {
			problems = append(problems, fmt.Sprintf("%s: live mux routes it to %q", route.Operation, pattern))
		}
		checked++
	}
	if len(problems) > 0 {
		return checked, fmt.Errorf("scan and live %s mux disagree:\n%s", server, strings.Join(problems, "\n"))
	}
	return checked, nil
}

// concretePath fills every wildcard of a ServeMux path pattern.
func concretePath(pattern string) string {
	var builder strings.Builder
	for index := 0; index < len(pattern); {
		if pattern[index] != '{' {
			builder.WriteByte(pattern[index])
			index++
			continue
		}
		end := strings.IndexByte(pattern[index:], '}')
		if end < 0 {
			builder.WriteString(pattern[index:])
			break
		}
		name := pattern[index+1 : index+end]
		switch {
		case name == "$":
		case strings.HasSuffix(name, "..."):
			builder.WriteString("sample/rest")
		default:
			builder.WriteString("sample")
		}
		index += end + 1
	}
	return builder.String()
}
