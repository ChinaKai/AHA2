package petcoverage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WebSite is one literal API call in the Web client.
type WebSite struct {
	Source     string
	Line       int
	MethodHint string // concrete method, or GET/see-source when not literal
	Expression string // literal text between the quotes, templates kept
}

var webMethod = regexp.MustCompile(`method:\s*["'](GET|POST|PUT|PATCH|DELETE)["']`)

// ScanWeb lists every string/template literal under web/src that starts with
// /api/. It covers API wrappers, raw fetch, downloads and streams alike.
func ScanWeb(root string) ([]WebSite, error) {
	var result []WebSite
	base := filepath.Join(root, "web", "src")
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(data)
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		for index := 0; index < len(source); index++ {
			quote := source[index]
			if (quote != '"' && quote != '\'' && quote != '`') || !strings.HasPrefix(source[index+1:], "/api/") {
				continue
			}
			end, err := jsLiteralEnd(source, index)
			if err != nil {
				return fmt.Errorf("%s:%d: %w", relative, strings.Count(source[:index], "\n")+1, err)
			}
			tail := source[end:]
			if semicolon := strings.IndexByte(tail, ';'); semicolon >= 0 {
				tail = tail[:semicolon+1]
			}
			hint := "GET/see-source"
			if match := webMethod.FindStringSubmatch(tail); match != nil {
				hint = match[1]
			}
			result = append(result, WebSite{Source: relative, Line: strings.Count(source[:index], "\n") + 1, MethodHint: hint, Expression: source[index+1 : end-1]})
			index = end - 1
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Line < result[j].Line
	})
	return result, nil
}

func jsLiteralEnd(source string, start int) (int, error) {
	quote := source[start]
	index := start + 1
	for index < len(source) {
		char := source[index]
		switch {
		case char == '\\':
			index += 2
			continue
		case char == quote:
			return index + 1, nil
		case quote == '`' && strings.HasPrefix(source[index:], "${"):
			index += 2
			depth := 1
			for index < len(source) && depth > 0 {
				char = source[index]
				if char == '"' || char == '\'' || char == '`' {
					end, err := jsLiteralEnd(source, index)
					if err != nil {
						return 0, err
					}
					index = end
					continue
				}
				if char == '{' {
					depth++
				} else if char == '}' {
					depth--
				}
				index++
			}
			if depth > 0 {
				return 0, fmt.Errorf("unterminated Web template expression")
			}
			continue
		}
		index++
	}
	return 0, fmt.Errorf("unterminated Web literal")
}

// sitePattern turns a call-site literal into a regexp over concrete paths:
// each ${...} may stand for any text up to a query string, and a literal
// query string is dropped.
func sitePattern(expression string) (*regexp.Regexp, error) {
	var builder strings.Builder
	builder.WriteString("^")
	for index := 0; index < len(expression); {
		if strings.HasPrefix(expression[index:], "${") {
			depth, cursor := 1, index+2
			for cursor < len(expression) && depth > 0 {
				switch expression[cursor] {
				case '{':
					depth++
				case '}':
					depth--
				case '"', '\'', '`':
					stop, err := jsLiteralEnd(expression, cursor)
					if err != nil {
						return nil, err
					}
					cursor = stop
					continue
				}
				cursor++
			}
			if depth > 0 {
				return nil, fmt.Errorf("unterminated template expression")
			}
			builder.WriteString(`[^?#]*`)
			index = cursor
			continue
		}
		if expression[index] == '?' {
			break
		}
		builder.WriteString(regexp.QuoteMeta(expression[index : index+1]))
		index++
	}
	builder.WriteString(`(?:[?#].*)?$`)
	return regexp.Compile(builder.String())
}

// samplePath replaces every {param} of a route with a concrete segment.
func samplePath(route string) string {
	var builder strings.Builder
	for index := 0; index < len(route); {
		if route[index] == '{' {
			end := strings.IndexByte(route[index:], '}')
			if end < 0 {
				builder.WriteString(route[index:])
				break
			}
			builder.WriteString("sample")
			index += end + 1
			continue
		}
		builder.WriteByte(route[index])
		index++
	}
	return strings.TrimSuffix(builder.String(), "/")
}

// UnmatchedWebSites returns call sites no registered httpapi route can serve.
// A Web call to an unregistered path means a registration escaped the scan.
func UnmatchedWebSites(sites []WebSite, routes []Registration) ([]WebSite, error) {
	var unmatched []WebSite
	for _, site := range sites {
		pattern, err := sitePattern(site.Expression)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", site.Source, site.Line, err)
		}
		found := false
		for _, route := range routes {
			if route.Server != "httpapi" {
				continue
			}
			method, path, _ := strings.Cut(route.Operation, " ")
			if site.MethodHint != "GET/see-source" && method != "ANY" && method != site.MethodHint {
				continue
			}
			sample := samplePath(path)
			if pattern.MatchString(sample) || pattern.MatchString(sample+"/") {
				found = true
				break
			}
		}
		if !found {
			unmatched = append(unmatched, site)
		}
	}
	return unmatched, nil
}
