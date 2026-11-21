package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

type tokenRouteRegistration struct{ Pattern, Handler, Wrapper, Source string }

// Read the real registrations, including the finite desktop tables. Unsupported
// new registration syntax fails the test rather than silently losing a route.
func registeredAgentTokenRoutes(t *testing.T) map[string]tokenRouteRegistration {
	t.Helper()
	files := map[string]*ast.File{}
	constants := map[string]string{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = file
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.CONST {
				continue
			}
			for _, spec := range group.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					if index < len(value.Values) {
						if literal, ok := value.Values[index].(*ast.BasicLit); ok && literal.Kind == token.STRING {
							if decoded, err := strconv.Unquote(literal.Value); err == nil {
								constants[name.Name] = decoded
							}
						}
					}
				}
			}
		}
	}
	var eval func(ast.Expr) (string, bool)
	eval = func(expr ast.Expr) (string, bool) {
		switch value := expr.(type) {
		case *ast.BasicLit:
			if value.Kind == token.STRING {
				decoded, err := strconv.Unquote(value.Value)
				return decoded, err == nil
			}
		case *ast.Ident:
			decoded, ok := constants[value.Name]
			return decoded, ok
		case *ast.BinaryExpr:
			if value.Op == token.ADD {
				left, ok := eval(value.X)
				right, rightOK := eval(value.Y)
				return left + right, ok && rightOK
			}
		}
		return "", false
	}
	unwrapHandler := func(expr ast.Expr) string {
		if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
			expr = call.Args[0]
		}
		if selector, ok := expr.(*ast.SelectorExpr); ok {
			return selector.Sel.Name
		}
		return ""
	}
	protected := func(expr ast.Expr) (string, ast.Expr) {
		call, ok := expr.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return "", nil
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", nil
		}
		if selector.Sel.Name == "withAgentCapability" || selector.Sel.Name == "withOwnerOrAgentAuth" {
			return selector.Sel.Name, call.Args[0]
		}
		return "", nil
	}
	routes := map[string]tokenRouteRegistration{}
	initialAuthenticators := map[string]int{}
	add := func(pattern, handler, wrapper, source string) {
		t.Helper()
		if _, exists := routes[pattern]; exists {
			t.Fatalf("duplicate token route %s", pattern)
		}
		if pattern == "" || handler == "" {
			t.Fatal("token route has no reproducible pattern/handler")
		}
		routes[pattern] = tokenRouteRegistration{pattern, handler, wrapper, source}
	}
	for source, file := range files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				method, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || method.Sel.Name != "Authenticate" {
					return true
				}
				receiver, ok := method.X.(*ast.SelectorExpr)
				if !ok || receiver.Sel.Name != "agentCapabilities" {
					return true
				}
				key := source + ":" + function.Name.Name
				if key != "helpers.go:withAgentCapability" && key != "helpers.go:withOwnerOrAgentAuth" {
					t.Fatalf("Agent token authenticator outside audited wrappers: %s", key)
				}
				initialAuthenticators[key]++
				return true
			})
		}
		handled := map[*ast.CallExpr]bool{}
		ast.Inspect(file, func(node ast.Node) bool {
			rangeStmt, ok := node.(*ast.RangeStmt)
			if !ok {
				return true
			}
			table, ok := rangeStmt.X.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ast.Inspect(rangeStmt.Body, func(child ast.Node) bool {
				call, ok := child.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Handle" {
					return true
				}
				wrapper, _ := protected(call.Args[1])
				if wrapper == "" {
					return true
				}
				patternField, ok := call.Args[0].(*ast.SelectorExpr)
				if !ok || patternField.Sel.Name != "pattern" {
					return true
				}
				handled[call] = true
				for _, element := range table.Elts {
					row, ok := element.(*ast.CompositeLit)
					if !ok || len(row.Elts) != 2 {
						t.Fatal("unsupported protected route table row")
					}
					pattern, ok := eval(row.Elts[0])
					if !ok {
						t.Fatal("nonliteral protected route table pattern")
					}
					add(pattern, unwrapHandler(row.Elts[1]), wrapper, source)
				}
				return true
			})
			return true
		})
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 || handled[call] {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
				return true
			}
			wrapper, handler := protected(call.Args[1])
			if wrapper == "" {
				return true
			}
			pattern, ok := eval(call.Args[0])
			if !ok {
				t.Fatalf("unsupported token registration syntax in %s", source)
			}
			add(pattern, unwrapHandler(handler), wrapper, source)
			return true
		})
	}
	if initialAuthenticators["helpers.go:withAgentCapability"] != 1 || initialAuthenticators["helpers.go:withOwnerOrAgentAuth"] != 1 {
		t.Fatalf("Agent authenticators changed: %v", initialAuthenticators)
	}
	return routes
}

func TestEveryAgentTokenRouteHasExplicitPolicy(t *testing.T) {
	routes := registeredAgentTokenRoutes(t)
	if len(routes) != len(agentTokenRoutePolicies) {
		t.Fatalf("registered=%d classified=%d; update explicit policy with any token route change", len(routes), len(agentTokenRoutePolicies))
	}
	mcpHandlers := map[string]string{"petMCP": "pet", "agentChannelMCP": "channel", "agentCoreMCP": "turn", "agentKnowledgeMCP": "knowledge", "agentWorkspaceMCP": "workspace", "agentCollabMCP": "collaboration", "agentBrowserMCP": "browser", "agentHardwareMCP": "hardware", "agentDesktopMCP": "desktop", "agentMailMCP": "mail"}
	validDomains := map[string]bool{"pet": true, "turn": true, "task": true, "attachment": true, "hardware": true, "browser": true, "workspace": true, "desktop": true, "knowledge": true, "skill": true, "channel": true, "collaboration": true, "mail": true}
	for pattern, registration := range routes {
		policy, ok := agentTokenRoutePolicies[pattern]
		if !ok {
			t.Fatalf("unclassified token route %s", pattern)
		}
		if !validDomains[policy.Domain] {
			t.Fatalf("unknown policy domain at %s", pattern)
		}
		if domain, isMCP := mcpHandlers[registration.Handler]; isMCP {
			if policy.Mode != agentRouteMCP || policy.Access != agentRouteTools || policy.Domain != domain || !strings.HasPrefix(pattern, "POST ") || strings.Contains(pattern, "{") {
				t.Fatalf("MCP policy mismatch at %s", pattern)
			}
		} else if policy.Mode == agentRouteMCP {
			t.Fatalf("non-MCP handler exempted at %s", pattern)
		}
		if policy.Mode != agentRouteMCP && policy.Mode != agentRouteResource && policy.Mode != agentRouteBusiness {
			t.Fatalf("untyped authority at %s", pattern)
		}
		if registration.Wrapper == "withOwnerOrAgentAuth" && pattern != "GET /api/v1/tasks/{id}/attachments/{attachment}" {
			t.Fatalf("new dual-auth resource needs explicit audit: %s", pattern)
		}
	}
	for pattern := range agentTokenRoutePolicies {
		if _, ok := routes[pattern]; !ok {
			t.Fatalf("stale/unregistered token policy %s", pattern)
		}
	}
}
