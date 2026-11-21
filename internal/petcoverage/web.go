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

// WebSite is one literal API call in the Web client. Identical calls in one
// file are folded into Occurrences, so moving code does not churn the ledger.
type WebSite struct {
	Source      string
	MethodHint  string // concrete method, or GET/see-source when not literal
	Expression  string // literal text between the quotes, templates kept
	Occurrences int
}

var webMethod = regexp.MustCompile(`method:\s*["'](GET|POST|PUT|PATCH|DELETE)["']`)

// dynamicBase accepts a same-origin prefix made only of template expressions,
// slashes and colons (for example ${protocol}//${location.host}); a literal
// host such as https://openrouter.ai/api/v1 is an external API, not ours.
var dynamicBase = regexp.MustCompile(`^(?:\$\{[^{}]*\}|[/:])+$`)

// ScanWeb lists every string/template literal under web/src that names an
// AHA2 API path: it starts with /api/, or reaches /api/ through a dynamic
// same-origin base. Comments, regular expressions and other strings are
// lexed and skipped, and expressions inside templates are scanned too.
func ScanWeb(root string) ([]WebSite, error) {
	counts := map[WebSite]int{}
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
		_, err = lexTS(source, 0, false, func(start, end int) {
			tail := source[end:]
			if semicolon := strings.IndexByte(tail, ';'); semicolon >= 0 {
				tail = tail[:semicolon+1]
			}
			hint := "GET/see-source"
			if match := webMethod.FindStringSubmatch(tail); match != nil {
				hint = match[1]
			}
			for _, found := range apiPaths(source[start+1 : end-1]) {
				if !found.embedded {
					counts[WebSite{Source: relative, MethodHint: hint, Expression: found.expression}]++
				} else {
					// An attribute inside markup (src="/api/...") has no fetch options.
					counts[WebSite{Source: relative, MethodHint: "GET/see-source", Expression: found.expression}]++
				}
			}
		})
		if err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	result := make([]WebSite, 0, len(counts))
	for site, count := range counts {
		site.Occurrences = count
		result = append(result, site)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Expression != b.Expression {
			return a.Expression < b.Expression
		}
		return a.MethodHint < b.MethodHint
	})
	return result, nil
}

type apiPath struct {
	expression string
	embedded   bool
}

// apiPaths finds the AHA2 API paths a literal names: the whole literal when
// it starts with /api/ (or reaches it through a same-origin dynamic base), and
// every quoted /api/ path embedded in its text, such as src="/api/...".
// ${...} regions are masked so their own nested literals, which the lexer
// visits separately, are not counted twice.
func apiPaths(content string) []apiPath {
	masked := []byte(content)
	for index := 0; index+1 < len(content); index++ {
		if content[index] == '\\' {
			index++
			continue
		}
		if content[index] == '$' && content[index+1] == '{' {
			close, err := lexTS(content, index+2, true, nil)
			if err != nil {
				return nil
			}
			for cursor := index + 2; cursor < close; cursor++ {
				masked[cursor] = 'x'
			}
			index = close
		}
	}
	var result []apiPath
	text := string(masked)
	for offset := 0; ; {
		found := strings.Index(text[offset:], "/api/")
		if found < 0 {
			return result
		}
		index := offset + found
		offset = index + len("/api/")
		switch {
		case index == 0 || dynamicBase.MatchString(content[:index]):
			result = append(result, apiPath{expression: content})
			return result // the literal is one API expression
		case text[index-1] == '"' || text[index-1] == '\'':
			stop := strings.IndexByte(text[index:], text[index-1])
			if stop < 0 {
				stop = len(text) - index
			}
			result = append(result, apiPath{expression: content[index : index+stop], embedded: true})
		}
	}
}

var regexKeywords = map[string]bool{"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true, "new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true}

func isIdentByte(value byte) bool {
	return value == '_' || value == '$' || (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9')
}

func lineOf(source string, index int) int { return strings.Count(source[:index], "\n") + 1 }

// lexTS walks source from `from` and calls visit(start, end) for every string
// or template literal, including literals nested in ${...}. It skips comments
// and regular-expression literals. With untilBrace it stops at the `}` that
// closes an enclosing ${ and returns its index; otherwise it returns len.
func lexTS(source string, from int, untilBrace bool, visit func(start, end int)) (int, error) {
	previous, word, depth := byte(0), "", 0
	for index := from; index < len(source); {
		char := source[index]
		next := byte(0)
		if index+1 < len(source) {
			next = source[index+1]
		}
		switch {
		case char == '/' && next == '/':
			stop := strings.IndexByte(source[index:], '\n')
			if stop < 0 {
				index = len(source)
				continue
			}
			index += stop + 1
		case char == '/' && next == '*':
			stop := strings.Index(source[index+2:], "*/")
			if stop < 0 {
				return 0, fmt.Errorf("line %d: unterminated comment", lineOf(source, index))
			}
			index += stop + 4
		case char == '"' || char == '\'' || char == '`':
			end, err := lexLiteral(source, index, visit)
			if err != nil {
				return 0, err
			}
			if visit != nil {
				visit(index, end)
			}
			index, previous, word = end, 'a', ""
		case char == '/' && (previous == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", previous) >= 0 || regexKeywords[word]):
			end, err := regexEnd(source, index)
			if err != nil {
				return 0, err
			}
			index, previous, word = end, 'a', ""
		case isIdentByte(char):
			begin := index
			for index < len(source) && isIdentByte(source[index]) {
				index++
			}
			word, previous = source[begin:index], 'a'
		default:
			switch char {
			case '{':
				depth++
			case '}':
				if untilBrace && depth == 0 {
					return index, nil
				}
				depth--
			}
			if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
				previous, word = char, ""
			}
			index++
		}
	}
	if untilBrace {
		return 0, fmt.Errorf("line %d: unterminated template expression", lineOf(source, from))
	}
	return len(source), nil
}

// lexLiteral returns the end (one past the closing quote) of the literal at
// start. Template expressions are lexed, so their nested literals, comments
// and regular expressions cannot end the template early.
func lexLiteral(source string, start int, visit func(start, end int)) (int, error) {
	quote := source[start]
	for index := start + 1; index < len(source); {
		char := source[index]
		switch {
		case char == '\\':
			index += 2
			continue
		case char == quote:
			return index + 1, nil
		case char == '\n' && quote != '`':
			return 0, fmt.Errorf("line %d: unterminated Web literal", lineOf(source, start))
		case quote == '`' && strings.HasPrefix(source[index:], "${"):
			close, err := lexTS(source, index+2, true, visit)
			if err != nil {
				return 0, err
			}
			index = close + 1
			continue
		}
		index++
	}
	return 0, fmt.Errorf("line %d: unterminated Web literal", lineOf(source, start))
}

func regexEnd(source string, start int) (int, error) {
	inClass := false
	for index := start + 1; index < len(source); index++ {
		switch source[index] {
		case '\\':
			index++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '\n':
			return 0, fmt.Errorf("line %d: unterminated regular expression", lineOf(source, start))
		case '/':
			if !inClass {
				index++
				for index < len(source) && source[index] >= 'a' && source[index] <= 'z' {
					index++
				}
				return index, nil
			}
		}
	}
	return 0, fmt.Errorf("line %d: unterminated regular expression", lineOf(source, start))
}

// sitePattern turns a call-site literal into a regexp over concrete paths:
// each ${...} may stand for any text up to a query string, and a literal
// query string is dropped.
func sitePattern(expression string) (*regexp.Regexp, error) {
	var builder strings.Builder
	builder.WriteString("^")
	for index := 0; index < len(expression); {
		if strings.HasPrefix(expression[index:], "${") {
			close, err := lexTS(expression, index+2, true, nil)
			if err != nil {
				return nil, err
			}
			cursor := close + 1
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
		expression := site.Expression
		if index := strings.Index(expression, "/api/"); index > 0 {
			expression = expression[index:] // same-origin dynamic base
		}
		pattern, err := sitePattern(expression)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", site.Source, site.Expression, err)
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
