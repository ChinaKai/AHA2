// Package petcoverage inventories every registered HTTP operation and every
// literal Web API call site, and checks the pet MCP coverage ledger against
// them. It parses Go source with go/parser (the compiler's own syntax), so a
// registration cannot hide behind a receiver name, a constant or a route
// table; any registration shape it cannot resolve fails the scan.
package petcoverage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Registration is one HTTP operation as the router sees it.
type Registration struct {
	Operation   string // "METHOD /path", or "ANY /path" for a methodless pattern
	Server      string // which listener serves it (see registeringFunctions)
	Source      string // repo-relative file of the registration
	Handler     string // handler method, factory, or "inline"
	Wrappers    string // authentication/authorization wrappers, outermost first
	Boundary    string // authentication boundary derived from Wrappers
	Fingerprint string // "v2:" + hash of wrappers (with arguments and source) and handler source
	// LegacyFingerprint is the pre-v2 hash (wrapper names and handler source)
	// of the current code. MergeLedger uses it once to keep a verdict whose
	// stored v1 fingerprint proves the code has not changed since review.
	LegacyFingerprint string
	RejectOnly        bool // the handler only writes a client error
}

// registeringFunctions classifies every function that registers routes. A
// new registering function fails the scan until a reviewer classifies it.
var registeringFunctions = map[string]string{
	"internal/httpapi (*Server).routeMux":              "httpapi",
	"internal/httpapi (*Server).registerBrowserRoutes": "httpapi",
	"internal/httpapi (*Server).registerDesktopRoutes": "httpapi",
	// Called only from routeMux (workspace viewer terminals, c08feca).
	"internal/httpapi (*Server).registerWorkspaceTerminalRoutes": "httpapi",
	"internal/httpapi (*Server).MailDownloadHandler":             "mail-download",
	"internal/centersync (*Store).Handler":                       "centersync",
}

// nonRouteHandleCalls lists two-argument Handle/HandleFunc calls that are
// reviewed as not registering an HTTP route, keyed by
// "<package dir> <function> <receiver>". Every other such call on a value that
// is not a ServeMux fails the scan.
var nonRouteHandleCalls = map[string]string{}

// boundaries maps every accepted wrapper to its authentication boundary. A
// wrapper missing here fails the scan instead of silently becoming
// transport_auth.
var boundaries = map[string]string{
	"withAgentCapability":   "agent_turn",
	"withOwnerOrAgentAuth":  "owner_or_agent",
	"withAuth":              "owner_session",
	"withChannelCapability": "channel_runtime",
	"auth":                  "device_token",
	// Owner session plus CSRF, a configured-origin check and a refusal of
	// Authorization headers, for the workspace viewer terminals (c08feca).
	"withWorkspaceTerminalOwner": "owner_session",
}

// wrapperHandlerArgument records, for a reviewed wrapper whose wrapped
// handler is not its last argument, which argument it is. Every other
// wrapper takes the handler last. The remaining arguments must still be
// constants and still go into the fingerprint.
var wrapperHandlerArgument = map[string]int{
	"withWorkspaceTerminalOwner": 0, // (handler http.HandlerFunc, requireOrigin bool)
}

// fingerprintVersion prefixes every fingerprint of the current scheme.
const fingerprintVersion = "v2:"

// scanned Go roots; everything else (web, tooling output, vendored copies)
// cannot register a route served by AHA2 binaries.
var goRoots = []string{"internal", "cmd"}

type goPackage struct {
	dir       string
	fset      *token.FileSet
	files     map[string]*ast.File
	sources   map[string][]byte
	constants map[string]ast.Expr
	methods   map[string]*ast.FuncDecl // handler name -> method declaration
	// functions holds package-level functions (no receiver) by name, for a
	// handler written as a bare identifier such as
	// mux.HandleFunc(path, methodNotAllowed). Go forbids two package-level
	// functions with one name, so a name maps to at most one declaration.
	functions map[string]*ast.FuncDecl
}

func loadPackages(root string) (map[string]*goPackage, error) {
	packages := map[string]*goPackage{}
	for _, base := range goRoots {
		err := filepath.WalkDir(filepath.Join(root, base), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if name := entry.Name(); name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			dir, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			dir = filepath.ToSlash(dir)
			pkg := packages[dir]
			if pkg == nil {
				pkg = &goPackage{dir: dir, fset: token.NewFileSet(), files: map[string]*ast.File{}, sources: map[string][]byte{}, constants: map[string]ast.Expr{}, methods: map[string]*ast.FuncDecl{}, functions: map[string]*ast.FuncDecl{}}
				packages[dir] = pkg
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(pkg.fset, path, content, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			pkg.files[dir+"/"+entry.Name()] = file
			pkg.sources[dir+"/"+entry.Name()] = content
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, pkg := range packages {
		for _, file := range pkg.files {
			for _, declaration := range file.Decls {
				switch value := declaration.(type) {
				case *ast.GenDecl:
					if value.Tok != token.CONST {
						continue
					}
					for _, spec := range value.Specs {
						item := spec.(*ast.ValueSpec)
						for index, name := range item.Names {
							if index < len(item.Values) {
								pkg.constants[name.Name] = item.Values[index]
							}
						}
					}
				case *ast.FuncDecl:
					if value.Recv == nil && value.Body != nil && value.Name.Name != "init" && value.Name.Name != "_" {
						pkg.functions[value.Name.Name] = value
					}
					if value.Recv != nil && value.Body != nil {
						if _, exists := pkg.methods[value.Name.Name]; exists {
							// Same-named methods on different receivers would make the
							// handler fingerprint ambiguous.
							pkg.methods[value.Name.Name] = nil
						} else {
							pkg.methods[value.Name.Name] = value
						}
					}
				}
			}
		}
	}
	return packages, nil
}

func functionKey(declaration *ast.FuncDecl) string {
	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
		return declaration.Name.Name
	}
	var receiver bytes.Buffer
	printer.Fprint(&receiver, token.NewFileSet(), declaration.Recv.List[0].Type)
	return "(" + receiver.String() + ")." + declaration.Name.Name
}

// ScanStats reconciles every Handle/HandleFunc call in the scanned source:
// Calls counts the token sequences `.Handle(` / `.HandleFunc(`, and each one
// is either a route registration or a reviewed non-route call.
type ScanStats struct {
	Calls       int
	Registering int
	NonRoute    []string
}

// ScanRoutes returns every route registration under root, sorted by operation.
func ScanRoutes(root string) ([]Registration, ScanStats, error) {
	var stats ScanStats
	packages, err := loadPackages(root)
	if err != nil {
		return nil, stats, err
	}
	var result []Registration
	seen := map[string]string{}
	dirs := make([]string, 0, len(packages))
	for dir := range packages {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		pkg := packages[dir]
		names := make([]string, 0, len(pkg.files))
		for name := range pkg.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, source := range names {
			tokens := handleTokens(pkg.sources[source])
			calls := 0
			ast.Inspect(pkg.files[source], func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && isHandleCall(call) {
					calls++
				}
				return true
			})
			if tokens != calls {
				return nil, stats, fmt.Errorf("%s: %d .Handle/.HandleFunc tokens but %d parsed calls; review the scanner", source, tokens, calls)
			}
			stats.Calls += calls
			if err := refuseHandleValues(source, pkg.files[source]); err != nil {
				return nil, stats, err
			}
			accounted := 0
			for _, declaration := range pkg.files[source].Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					outside := 0
					ast.Inspect(declaration, func(node ast.Node) bool {
						if call, ok := node.(*ast.CallExpr); ok && isHandleCall(call) {
							outside++
						}
						return true
					})
					if outside > 0 {
						return nil, stats, fmt.Errorf("%s: Handle/HandleFunc call outside a function body", source)
					}
					continue
				}
				found, handled, err := scanFunction(pkg, source, function, &stats)
				if err != nil {
					return nil, stats, err
				}
				accounted += handled
				for _, item := range found {
					key := item.Server + " " + item.Operation
					if previous, exists := seen[key]; exists {
						return nil, stats, fmt.Errorf("duplicate route %s (%s and %s)", key, previous, item.Source)
					}
					seen[key] = item.Source
					result = append(result, item)
				}
			}
			if accounted != calls {
				return nil, stats, fmt.Errorf("%s: %d Handle/HandleFunc calls but %d accounted for", source, calls, accounted)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Operation != result[j].Operation {
			return result[i].Operation < result[j].Operation
		}
		return result[i].Server < result[j].Server
	})
	sort.Strings(stats.NonRoute)
	return result, stats, nil
}

// refuseHandleValues fails on any .Handle/.HandleFunc that is not called
// directly, such as a method value `add := mux.Handle`, because a later call
// through the alias registers a route the scan cannot see. A qualified name
// from another imported package (the windows.Handle type) is not a mux method.
func refuseHandleValues(source string, file *ast.File) error {
	packages := map[string]bool{}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if path != "net/http" {
			packages[name] = true
		}
	}
	var failure error
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if failure != nil {
			return false
		}
		parent := ast.Node(nil)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		stack = append(stack, node)
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
			return true
		}
		if call, isCall := parent.(*ast.CallExpr); isCall && call.Fun == selector {
			return true
		}
		if base, isIdent := selector.X.(*ast.Ident); isIdent && packages[base.Name] {
			return true
		}
		failure = fmt.Errorf("%s: .%s used as a value; register routes by calling the mux directly", source, selector.Sel.Name)
		return false
	})
	return failure
}

func isHandleCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (selector.Sel.Name == "Handle" || selector.Sel.Name == "HandleFunc")
}

// handleTokens counts `.` Handle|HandleFunc `(` in the token stream, so a call
// the AST walk cannot see (or a scanner bug) shows up as a mismatch. Comments
// and string contents are not tokens and do not count.
func handleTokens(source []byte) int {
	var lexer scanner.Scanner
	fileSet := token.NewFileSet()
	lexer.Init(fileSet.AddFile("", fileSet.Base(), len(source)), source, nil, 0)
	count := 0
	var previous [2]token.Token
	var name string
	for {
		_, tok, literal := lexer.Scan()
		if tok == token.EOF {
			return count
		}
		if tok == token.LPAREN && previous[1] == token.IDENT && previous[0] == token.PERIOD && (name == "Handle" || name == "HandleFunc") {
			count++
		}
		if tok == token.IDENT {
			name = literal
		}
		previous[0], previous[1] = previous[1], tok
	}
}

type scope struct {
	pkg      *goPackage
	function *ast.FuncDecl
	locals   map[string]ast.Expr // string variables assigned exactly once
	muxes    map[string]bool
	// enclosing holds the ancestors of the call being resolved, innermost last.
	enclosing []ast.Node
	// bound holds the loop variable of a string-slice range while one of its
	// elements is being expanded.
	bound map[string]string
	// evaluating guards eval against a local defined in terms of itself.
	evaluating map[string]bool
}

func scanFunction(pkg *goPackage, source string, function *ast.FuncDecl, stats *ScanStats) ([]Registration, int, error) {
	current := &scope{pkg: pkg, function: function, locals: map[string]ast.Expr{}, muxes: map[string]bool{}}
	if function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			if isServeMuxType(field.Type) {
				for _, name := range field.Names {
					current.muxes[name.Name] = true
				}
			}
		}
	}
	assigned := map[string]int{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || index >= len(value.Rhs) {
					continue
				}
				assigned[name.Name]++
				current.locals[name.Name] = value.Rhs[index]
				if call, ok := value.Rhs[index].(*ast.CallExpr); ok && isSelector(call.Fun, "http", "NewServeMux") {
					current.muxes[name.Name] = true
				}
			}
		}
		return true
	})
	for name, count := range assigned {
		if count != 1 {
			delete(current.locals, name)
		}
	}
	var result []Registration
	var failure error
	var stack []ast.Node
	handled := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		if failure != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		current.enclosing = stack
		if !isHandleCall(call) {
			return true
		}
		handled++
		method := call.Fun.(*ast.SelectorExpr)
		var printedReceiver bytes.Buffer
		printer.Fprint(&printedReceiver, token.NewFileSet(), method.X)
		where := fmt.Sprintf("%s %s %s.%s", source, functionKey(function), printedReceiver.String(), method.Sel.Name)
		if isServeMuxMethodExpression(method.X) {
			failure = fmt.Errorf("%s: method-expression registration is not supported; call Handle on the mux", where)
			return false
		}
		if len(call.Args) != 2 {
			// ServeMux.Handle/HandleFunc always take two arguments; anything else
			// (for example the windows.Handle conversion) is not a route, unless
			// an argument spells a route pattern.
			for _, argument := range call.Args {
				if pattern, ok := current.eval(argument); ok && looksLikePattern(pattern) {
					failure = fmt.Errorf("%s: %d-argument call carries route pattern %q", where, len(call.Args), pattern)
					return false
				}
			}
			stats.NonRoute = append(stats.NonRoute, fmt.Sprintf("%s: %d arguments, not ServeMux.%s", where, len(call.Args), method.Sel.Name))
			return true
		}
		receiver, _ := method.X.(*ast.Ident)
		if receiver == nil || !current.muxes[receiver.Name] {
			if reason, ok := nonRouteHandleCalls[pkg.dir+" "+functionKey(function)+" "+printedReceiver.String()]; ok {
				stats.NonRoute = append(stats.NonRoute, where+": "+reason)
				return true
			}
			if receiver != nil && receiver.Name == "http" {
				failure = fmt.Errorf("%s: route registered on http.DefaultServeMux", where)
			} else {
				failure = fmt.Errorf("%s: route registered on an unrecognized receiver (not a local http.NewServeMux() or *http.ServeMux parameter)", where)
			}
			return false
		}
		stats.Registering++
		server, classified := registeringFunctions[pkg.dir+" "+functionKey(function)]
		if !classified {
			failure = fmt.Errorf("%s: %s registers routes but is not classified in registeringFunctions", source, functionKey(function))
			return false
		}
		items, err := current.registrations(source, server, call)
		if err != nil {
			failure = err
			return false
		}
		result = append(result, items...)
		return true
	})
	return result, handled, failure
}

func (current *scope) registrations(source, server string, call *ast.CallExpr) ([]Registration, error) {
	if pattern, ok := current.eval(call.Args[0]); ok {
		item, err := current.registration(source, server, pattern, call.Args[1], nil)
		return []Registration{item}, err
	}
	// mux.Handle("GET "+base, ...) inside `for _, base := range []string{...}`:
	// expand once per element of that literal string slice. A bare
	// route.field pattern is the struct-table shape handled below.
	_, tableField := call.Args[0].(*ast.SelectorExpr)
	if loop, name := current.stringRangeFor(call.Args[0]); loop != nil && !tableField {
		values, err := current.stringRangeValues(source, loop)
		if err != nil {
			return nil, err
		}
		var result []Registration
		for _, value := range values {
			current.bound = map[string]string{name: value}
			pattern, ok := current.eval(call.Args[0])
			current.bound = nil
			if !ok {
				return nil, fmt.Errorf("%s: unresolvable route pattern in %s", source, functionKey(current.function))
			}
			item, err := current.registration(source, server, pattern, call.Args[1], nil)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	}
	// mux.Handle(route.pattern, wrap(route.handler)) inside a range over a
	// literal table: expand every row of that table.
	field, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok {
		return nil, fmt.Errorf("%s: unresolvable route pattern in %s", source, functionKey(current.function))
	}
	variable, ok := field.X.(*ast.Ident)
	var loop *ast.RangeStmt
	for index := len(current.enclosing) - 1; ok && index >= 0 && loop == nil; index-- {
		if candidate, isRange := current.enclosing[index].(*ast.RangeStmt); isRange {
			if value, isIdent := candidate.Value.(*ast.Ident); isIdent && value.Name == variable.Name {
				loop = candidate
			}
		}
	}
	if !ok || loop == nil {
		return nil, fmt.Errorf("%s: unresolvable route pattern in %s", source, functionKey(current.function))
	}
	table, ok := loop.X.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("%s: route table in %s is not a literal", source, functionKey(current.function))
	}
	structType, ok := table.Type.(*ast.ArrayType)
	if !ok {
		return nil, fmt.Errorf("%s: route table in %s is not a slice literal", source, functionKey(current.function))
	}
	fields, ok := structType.Elt.(*ast.StructType)
	if !ok {
		return nil, fmt.Errorf("%s: route table in %s has no inline struct type", source, functionKey(current.function))
	}
	var order []string
	for _, item := range fields.Fields.List {
		for _, name := range item.Names {
			order = append(order, name.Name)
		}
	}
	var result []Registration
	for _, element := range table.Elts {
		row, ok := element.(*ast.CompositeLit)
		if !ok {
			return nil, fmt.Errorf("%s: route table row in %s is not a literal", source, functionKey(current.function))
		}
		values := map[string]ast.Expr{}
		for index, value := range row.Elts {
			if keyed, ok := value.(*ast.KeyValueExpr); ok {
				values[keyed.Key.(*ast.Ident).Name] = keyed.Value
			} else if index < len(order) {
				values[order[index]] = value
			}
		}
		pattern, ok := current.eval(values[field.Sel.Name])
		if !ok {
			return nil, fmt.Errorf("%s: unresolvable route table pattern in %s", source, functionKey(current.function))
		}
		item, err := current.registration(source, server, pattern, call.Args[1], func(expr ast.Expr) ast.Expr {
			if selector, ok := expr.(*ast.SelectorExpr); ok {
				if base, ok := selector.X.(*ast.Ident); ok && base.Name == variable.Name {
					return values[selector.Sel.Name]
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (current *scope) registration(source, server, pattern string, handler ast.Expr, substitute func(ast.Expr) ast.Expr) (Registration, error) {
	method, route, found := strings.Cut(pattern, " ")
	if !found {
		method, route = "ANY", pattern
	}
	item := Registration{Operation: method + " " + strings.TrimSpace(route), Server: server, Source: source}
	var wrappers []string     // wrapper names, outermost first
	var wrapperCalls []string // wrapper names with their non-handler arguments
	var body ast.Node
	var handlerCall ast.Node // a factory call, so its literal arguments count
	expr := handler
	for depth := 0; ; depth++ {
		if depth > 16 {
			return item, fmt.Errorf("%s: handler for %s is nested too deeply", source, item.Operation)
		}
		if substitute != nil {
			if replaced := substitute(expr); replaced != nil {
				expr = replaced
			}
		}
		switch value := expr.(type) {
		case *ast.CallExpr:
			if isSelector(value.Fun, "http", "HandlerFunc") && len(value.Args) == 1 {
				expr = value.Args[0]
				continue
			}
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || len(value.Args) == 0 {
				return item, fmt.Errorf("%s: unsupported handler expression for %s", source, item.Operation)
			}
			name := selector.Sel.Name
			// The handler argument of a wrapper is its last argument, or the
			// position reviewed in wrapperHandlerArgument; every other
			// argument must be a resolvable constant. A call whose arguments
			// include a handler is a wrapper and must be reviewed.
			_, registered := boundaries[name]
			handlerIndex := len(value.Args) - 1
			if position, declared := wrapperHandlerArgument[name]; declared && registered {
				if position >= len(value.Args) {
					return item, fmt.Errorf("%s: wrapper %s on %s has no argument %d", source, name, item.Operation, position)
				}
				handlerIndex = position
			}
			if registered || containsHandlerLike(value.Args, substitute) {
				if !registered {
					return item, fmt.Errorf("%s: wrapper %s on %s has no reviewed boundary", source, name, item.Operation)
				}
				var parameters []string
				for index, argument := range value.Args {
					if index == handlerIndex {
						continue
					}
					literal, ok := current.evalArgument(argument)
					if !ok {
						return item, fmt.Errorf("%s: wrapper %s on %s has a non-constant argument", source, name, item.Operation)
					}
					parameters = append(parameters, strconv.Quote(literal))
				}
				wrappers = append(wrappers, name)
				wrapperCalls = append(wrapperCalls, name+"("+strings.Join(parameters, ",")+")")
				expr = value.Args[handlerIndex]
				continue
			}
			for _, argument := range value.Args {
				if _, ok := current.eval(argument); !ok {
					return item, fmt.Errorf("%s: handler factory %s on %s has a non-constant argument", source, name, item.Operation)
				}
			}
			// Handler factory such as s.savePetWorkbenchDocument("core").
			item.Handler = name
			handlerCall = value
			body = current.pkg.methods[name]
		case *ast.SelectorExpr:
			item.Handler = value.Sel.Name
			body = current.pkg.methods[value.Sel.Name]
		case *ast.Ident:
			// A bare identifier is a package-level handler function in the
			// registering package. It must not be a local or parameter of the
			// registering function: that would be a handler value assigned
			// somewhere else, which the scan cannot see.
			if current.declaresLocal(value.Name) {
				return item, fmt.Errorf("%s: handler for %s is the local value %s; pass the function or method directly", source, item.Operation, value.Name)
			}
			declaration := current.pkg.functions[value.Name]
			if declaration == nil {
				return item, fmt.Errorf("%s: handler %s for %s is not a package-level function in %s", source, value.Name, item.Operation, current.pkg.dir)
			}
			item.Handler = value.Name
			body = declaration
		case *ast.FuncLit:
			item.Handler = "inline"
			body = value
		default:
			return item, fmt.Errorf("%s: unsupported handler expression for %s", source, item.Operation)
		}
		break
	}
	if _, isWrapper := boundaries[item.Handler]; isWrapper {
		return item, fmt.Errorf("%s: %s resolved to the wrapper %s instead of its handler", source, item.Operation, item.Handler)
	}
	if body == nil || body == (*ast.FuncDecl)(nil) {
		return item, fmt.Errorf("%s: handler %s for %s has no unique declaration in %s", source, item.Handler, item.Operation, current.pkg.dir)
	}
	item.Wrappers = strings.Join(wrapperCalls, ">")
	item.Boundary = "transport_auth"
	if len(wrappers) > 0 {
		item.Boundary = boundaries[wrappers[0]] // the outermost wrapper authenticates first
	}
	// The fingerprint covers everything a reviewed verdict depends on: the
	// wrapper chain with its arguments, each wrapper's own source, a factory's
	// arguments and the handler's source.
	var fingerprint bytes.Buffer
	fingerprint.WriteString(item.Wrappers + "\n")
	for _, name := range wrappers {
		declaration := current.pkg.methods[name]
		if declaration == nil {
			return item, fmt.Errorf("%s: wrapper %s on %s has no unique declaration in %s", source, name, item.Operation, current.pkg.dir)
		}
		if err := printer.Fprint(&fingerprint, current.pkg.fset, declaration); err != nil {
			return item, err
		}
		fingerprint.WriteString("\n")
	}
	if handlerCall != nil {
		if err := printer.Fprint(&fingerprint, current.pkg.fset, handlerCall); err != nil {
			return item, err
		}
		fingerprint.WriteString("\n")
	}
	var handlerSource bytes.Buffer
	if err := printer.Fprint(&handlerSource, current.pkg.fset, body); err != nil {
		return item, err
	}
	fingerprint.Write(handlerSource.Bytes())
	sum := sha256.Sum256(fingerprint.Bytes())
	item.Fingerprint = fingerprintVersion + hex.EncodeToString(sum[:6])
	legacy := sha256.Sum256([]byte(strings.Join(wrappers, ">") + "\n" + handlerSource.String()))
	item.LegacyFingerprint = hex.EncodeToString(legacy[:6])
	item.RejectOnly = rejectOnly(body)
	return item, nil
}

// stringRangeFor finds the innermost enclosing `for _, v := range` whose
// value variable v appears in the pattern expression. Only that shape is
// expanded; anything else stays unresolvable.
func (current *scope) stringRangeFor(pattern ast.Expr) (*ast.RangeStmt, string) {
	used := map[string]bool{}
	ast.Inspect(pattern, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			used[ident.Name] = true
		}
		return true
	})
	for index := len(current.enclosing) - 1; index >= 0; index-- {
		loop, ok := current.enclosing[index].(*ast.RangeStmt)
		if !ok {
			continue
		}
		value, ok := loop.Value.(*ast.Ident)
		if !ok || !used[value.Name] || loop.Tok != token.DEFINE {
			continue
		}
		return loop, value.Name
	}
	return nil, ""
}

// stringRangeValues returns the elements of a range over a literal
// []string{...} whose elements all evaluate to constants. The loop variable
// must not be reassigned inside the loop.
func (current *scope) stringRangeValues(source string, loop *ast.RangeStmt) ([]string, error) {
	table, ok := loop.X.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("%s: route loop in %s does not range over a literal", source, functionKey(current.function))
	}
	slice, ok := table.Type.(*ast.ArrayType)
	if !ok || slice.Len != nil {
		return nil, fmt.Errorf("%s: route loop in %s does not range over a slice literal", source, functionKey(current.function))
	}
	if element, ok := slice.Elt.(*ast.Ident); !ok || element.Name != "string" {
		return nil, fmt.Errorf("%s: route loop in %s ranges over a non-string slice", source, functionKey(current.function))
	}
	name := loop.Value.(*ast.Ident).Name
	reassigned := false
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, left := range assign.Lhs {
				if ident, ok := left.(*ast.Ident); ok && ident.Name == name {
					reassigned = true
				}
			}
		}
		return !reassigned
	})
	if reassigned {
		return nil, fmt.Errorf("%s: route loop variable %s is reassigned in %s", source, name, functionKey(current.function))
	}
	values := make([]string, 0, len(table.Elts))
	for _, element := range table.Elts {
		value, ok := current.eval(element)
		if !ok {
			return nil, fmt.Errorf("%s: route loop in %s has a non-constant element", source, functionKey(current.function))
		}
		values = append(values, value)
	}
	return values, nil
}

// declaresLocal reports whether name is a parameter, receiver, named result
// or a variable declared anywhere in the registering function, which would
// shadow a package-level function of that name.
func (current *scope) declaresLocal(name string) bool {
	function := current.function
	for _, list := range []*ast.FieldList{function.Recv, function.Type.Params, function.Type.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, ident := range field.Names {
				if ident.Name == name {
					return true
				}
			}
		}
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for _, left := range value.Lhs {
					if ident, ok := left.(*ast.Ident); ok && ident.Name == name {
						found = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, ident := range value.Names {
				if ident.Name == name {
					found = true
				}
			}
		case *ast.RangeStmt:
			for _, expr := range []ast.Expr{value.Key, value.Value} {
				if ident, ok := expr.(*ast.Ident); ok && ident.Name == name {
					found = true
				}
			}
		case *ast.FuncLit:
			for _, field := range value.Type.Params.List {
				for _, ident := range field.Names {
					if ident.Name == name {
						found = true
					}
				}
			}
		}
		return true
	})
	return found
}

func (current *scope) eval(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			decoded, err := strconv.Unquote(value.Value)
			return decoded, err == nil
		}
	case *ast.Ident:
		if bound, ok := current.bound[value.Name]; ok {
			return bound, true
		}
		if local, ok := current.locals[value.Name]; ok {
			// A local assigned from itself (x = x + "/y") has no single
			// value; refuse it instead of recursing forever.
			if current.evaluating[value.Name] {
				return "", false
			}
			if current.evaluating == nil {
				current.evaluating = map[string]bool{}
			}
			current.evaluating[value.Name] = true
			defer delete(current.evaluating, value.Name)
			return current.eval(local)
		}
		if constant, ok := current.pkg.constants[value.Name]; ok {
			return current.eval(constant)
		}
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := current.eval(value.X)
			right, rightOK := current.eval(value.Y)
			return left + right, leftOK && rightOK
		}
	case *ast.ParenExpr:
		return current.eval(value.X)
	}
	return "", false
}

// containsHandlerLike reports a call whose arguments include a handler: such
// a call is a wrapper whatever position the handler is in.
func containsHandlerLike(arguments []ast.Expr, substitute func(ast.Expr) ast.Expr) bool {
	for _, argument := range arguments {
		if isHandlerLike(argument, substitute) {
			return true
		}
	}
	return false
}

// evalArgument evaluates a wrapper's non-handler argument: a string constant
// as in eval, or the predeclared true/false when no local or package
// constant shadows them.
func (current *scope) evalArgument(expr ast.Expr) (string, bool) {
	if value, ok := current.eval(expr); ok {
		return value, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || (ident.Name != "true" && ident.Name != "false") {
		return "", false
	}
	if _, shadowed := current.pkg.constants[ident.Name]; shadowed || current.declaresLocal(ident.Name) {
		return "", false
	}
	return ident.Name, true
}

// isHandlerLike reports an expression that can only be an http.Handler or
// handler function: a method value, a function literal, http.HandlerFunc(...)
// or a call that itself wraps one.
func isHandlerLike(expr ast.Expr, substitute func(ast.Expr) ast.Expr) bool {
	if substitute != nil {
		if replaced := substitute(expr); replaced != nil {
			expr = replaced
		}
	}
	switch value := expr.(type) {
	case *ast.SelectorExpr, *ast.FuncLit:
		return true
	case *ast.CallExpr:
		if isSelector(value.Fun, "http", "HandlerFunc") {
			return true
		}
		for _, argument := range value.Args {
			if isHandlerLike(argument, substitute) {
				return true
			}
		}
	}
	return false
}

func looksLikePattern(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	method, route, found := strings.Cut(value, " ")
	return found && strings.HasPrefix(strings.TrimSpace(route), "/") && method != "" && strings.ToUpper(method) == method
}

// isServeMuxMethodExpression matches (*http.ServeMux).Handle style calls.
func isServeMuxMethodExpression(expr ast.Expr) bool {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	return isSelector(expr, "http", "ServeMux")
}

func isServeMuxType(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	return ok && isSelector(star.X, "http", "ServeMux")
}

func isSelector(expr ast.Expr, base, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == base
}

func isBasicLiteral(expr ast.Expr) bool {
	_, ok := expr.(*ast.BasicLit)
	return ok
}

// rejectOnly reports a handler that performs no work: every call in it is
// one of the response-writing helpers below, and it writes a client error.
// Only such handlers may be ledgered as web_prohibited_na.
func rejectOnly(body ast.Node) bool {
	var block *ast.BlockStmt
	switch value := body.(type) {
	case *ast.FuncDecl:
		block = value.Body
	case *ast.FuncLit:
		block = value.Body
	}
	if block == nil {
		return false
	}
	allowed := map[string]bool{"writeError": true, "writeErrorDetail": true, "writeJSON": true, "Error": true, "Header": true, "Set": true, "PathValue": true, "WriteHeader": true}
	inert, rejects := true, false
	ast.Inspect(block, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			inert = false
		case *ast.CallExpr:
			name := ""
			switch fun := value.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			if !allowed[name] {
				inert = false
			}
			for _, argument := range value.Args {
				if isClientErrorStatus(argument) {
					rejects = true
				}
			}
		}
		return true
	})
	return inert && rejects
}

func isClientErrorStatus(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "StatusMethodNotAllowed", "StatusGone", "StatusNotFound", "StatusForbidden", "StatusBadRequest", "StatusNotImplemented":
		return isSelector(expr, "http", selector.Sel.Name)
	}
	return false
}
