package petcoverage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
)

// VerifyMux checks the scan against a live ServeMux in both directions.
// Forward: for every scanned operation of server, a request built from its
// pattern must be routed by mux to exactly that registered pattern. Reverse:
// every pattern the mux holds must be a scanned operation of server, so a
// registration the scan missed (an alias, a method expression, generated
// code) fails here even though no source rule matched it.
func VerifyMux(mux *http.ServeMux, routes []Registration, server string) (int, error) {
	checked := 0
	var problems []string
	scanned := map[string]bool{}
	for _, route := range routes {
		if route.Server != server {
			continue
		}
		method, path, _ := strings.Cut(route.Operation, " ")
		want := route.Operation
		if method == "ANY" {
			method, want = http.MethodPatch, path
		}
		scanned[want] = true
		request := httptest.NewRequest(method, concretePath(path), nil)
		if _, pattern := mux.Handler(request); pattern != want {
			problems = append(problems, fmt.Sprintf("%s: live mux routes it to %q", route.Operation, pattern))
		}
		checked++
	}
	live, err := LivePatterns(mux)
	if err != nil {
		return checked, err
	}
	for _, pattern := range live {
		if !scanned[pattern] {
			problems = append(problems, fmt.Sprintf("live mux serves %q, which the scan did not find", pattern))
		}
	}
	if len(live) != checked {
		problems = append(problems, fmt.Sprintf("live mux holds %d patterns, scan has %d", len(live), checked))
	}
	if len(problems) > 0 {
		return checked, fmt.Errorf("scan and live %s mux disagree:\n%s", server, strings.Join(problems, "\n"))
	}
	return checked, nil
}

// LivePatterns lists every pattern registered on mux. net/http has no public
// enumeration, so this reads the conflict index the mux keeps of all its
// patterns (Go 1.22+ routingIndex: segments and multis). If that layout
// changes, it returns an error instead of an empty list, so the check fails
// closed rather than passing vacuously.
func LivePatterns(mux *http.ServeMux) ([]string, error) {
	index := reflect.ValueOf(mux).Elem().FieldByName("index")
	if !index.IsValid() {
		return nil, fmt.Errorf("http.ServeMux has no routing index; update LivePatterns for this Go version")
	}
	segments, multis := index.FieldByName("segments"), index.FieldByName("multis")
	if segments.Kind() != reflect.Map || multis.Kind() != reflect.Slice {
		return nil, fmt.Errorf("http.ServeMux routing index changed shape; update LivePatterns")
	}
	seen := map[string]bool{}
	add := func(list reflect.Value) error {
		for position := 0; position < list.Len(); position++ {
			text := list.Index(position).Elem().FieldByName("str")
			if text.Kind() != reflect.String {
				return fmt.Errorf("http pattern has no original string; update LivePatterns")
			}
			seen[text.String()] = true
		}
		return nil
	}
	iterator := segments.MapRange()
	for iterator.Next() {
		if err := add(iterator.Value()); err != nil {
			return nil, err
		}
	}
	if err := add(multis); err != nil {
		return nil, err
	}
	patterns := make([]string, 0, len(seen))
	for pattern := range seen {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no patterns read from a mux; update LivePatterns")
	}
	return patterns, nil
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
