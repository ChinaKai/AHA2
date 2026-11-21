package petcoverage

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	LedgerPath   = "docs/pet-mcp/coverage.tsv"
	WebPath      = "docs/pet-mcp/web-call-sites.tsv"
	ToolsPath    = "docs/pet-mcp/task-mcp-tools.tsv"
	pendingValue = "pending"
)

var ledgerFields = []string{"operation", "server", "source", "handler", "boundary", "fingerprint", "mcp", "permission", "protected_flow", "tests", "status"}
var webFields = []string{"source", "line", "method_hint", "expression"}
var toolFields = []string{"tool", "pet_access", "permission", "tests", "status"}

// Final statuses close a row. Everything else keeps the gate open.
var finalStatus = map[string]bool{"direct": true, "equivalent": true, "protected_handoff": true, "transport_na": true, "web_prohibited_na": true}
var openStatus = map[string]bool{pendingValue: true, "implemented": true, "implemented_unverified": true, "stale": true}

// Row is one ledger line. Metadata columns are regenerated from the scan;
// the reviewed columns are only ever written by a person.
type Row map[string]string

func (row Row) key() string { return row["server"] + " " + row["operation"] }

// Catalog maps an MCP server (aha2_core, aha2_pet, ...) to its tool names.
type Catalog map[string]map[string]bool

// NewCatalog builds a Catalog from server -> names.
func NewCatalog(input map[string][]string) Catalog {
	catalog := Catalog{}
	for server, names := range input {
		catalog[server] = map[string]bool{}
		for _, name := range names {
			catalog[server][name] = true
		}
	}
	return catalog
}

func readTSV(path string) ([]Row, []string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = '\t'
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, nil, nil
	}
	header := records[0]
	rows := make([]Row, 0, len(records)-1)
	for _, record := range records[1:] {
		row := Row{}
		for index, field := range header {
			if index < len(record) {
				row[field] = record[index]
			}
		}
		rows = append(rows, row)
	}
	return rows, header, nil
}

func encodeTSV(fields []string, rows []Row) []byte {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.Comma = '\t'
	_ = writer.Write(fields)
	for _, row := range rows {
		record := make([]string, len(fields))
		for index, field := range fields {
			record[index] = row[field]
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	return output.Bytes()
}

// MergeLedger regenerates metadata from the scan and keeps reviewed columns.
// A previously reviewed row whose server, source, handler, boundary or handler
// fingerprint changed is reset to "stale": its old verdict no longer describes
// the code and must be re-verified. New operations start pending; operations
// no longer registered are dropped and reported.
func MergeLedger(previous []Row, routes []Registration) (rows []Row, stale, removed []string) {
	byKey := map[string]Row{}
	for _, row := range previous {
		server := row["server"]
		if server == "" {
			server = "httpapi" // ledger v1 had a single listener
		}
		byKey[server+" "+row["operation"]] = row
	}
	seen := map[string]bool{}
	for _, route := range routes {
		row := Row{"operation": route.Operation, "server": route.Server, "source": route.Source, "handler": route.Handler, "boundary": route.Boundary, "fingerprint": route.Fingerprint}
		key := row.key()
		seen[key] = true
		old, existed := byKey[key]
		for _, field := range ledgerFields[6:] {
			row[field] = pendingValue
			if existed && old[field] != "" {
				row[field] = old[field]
			}
		}
		if existed && row["status"] != pendingValue && row["status"] != "stale" {
			oldSource := old["source"]
			if !strings.Contains(oldSource, "/") && oldSource != "" {
				oldSource = "internal/httpapi/" + oldSource // v1 paths were package-relative
			}
			if oldSource != route.Source || old["handler"] != route.Handler || old["boundary"] != route.Boundary || old["fingerprint"] != route.Fingerprint {
				row["status"] = "stale"
				stale = append(stale, key)
			}
		}
		rows = append(rows, row)
	}
	for key := range byKey {
		if !seen[key] {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	return rows, stale, removed
}

var testReference = regexp.MustCompile(`^([A-Za-z0-9_./-]+_test\.go):(Test[A-Za-z0-9_]+)$`)
var toolReference = regexp.MustCompile(`\b(aha2_[a-z]+)\.([a-z][a-z0-9_]*)\b`)
var mcpProtocolRoute = regexp.MustCompile(`^POST /api/v1/(agent/[a-z]+|pet)/mcp$`)

// checkTests requires every reference to name an existing top-level Go test.
func checkTests(root, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("tests is empty")
	}
	for _, token := range strings.Split(value, ";") {
		token = strings.TrimSpace(token)
		match := testReference.FindStringSubmatch(token)
		if match == nil {
			return fmt.Errorf("test reference %q is not path_test.go:TestName", token)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(match[1])))
		if err != nil {
			return fmt.Errorf("test file %s does not exist", match[1])
		}
		if !regexp.MustCompile(`(?m)^func ` + match[2] + `\(t \*testing\.T\)`).Match(data) {
			return fmt.Errorf("test %s is not declared in %s", match[2], match[1])
		}
	}
	return nil
}

func checkTools(catalog Catalog, value string, required bool) error {
	matches := toolReference.FindAllStringSubmatch(value, -1)
	if required && len(matches) == 0 {
		return fmt.Errorf("mcp %q names no server.tool", value)
	}
	for _, match := range matches {
		if !catalog[match[1]][match[2]] {
			return fmt.Errorf("mcp tool %s.%s does not exist in the built-in catalog", match[1], match[2])
		}
	}
	return nil
}

func meaningful(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != pendingValue
}

// ValidateRow applies the evidence rules. A row may stay open, but whatever
// it claims must be true; a closing status needs complete, checkable proof.
func ValidateRow(root string, catalog Catalog, route Registration, row Row) error {
	status := row["status"]
	if !finalStatus[status] && !openStatus[status] {
		return fmt.Errorf("unknown status %q", status)
	}
	if meaningful(row["tests"]) {
		if err := checkTests(root, row["tests"]); err != nil {
			return err
		}
	}
	if meaningful(row["mcp"]) && row["mcp"] != "none" {
		if err := checkTools(catalog, row["mcp"], false); err != nil {
			return err
		}
	}
	if !finalStatus[status] {
		return nil
	}
	for _, field := range []string{"mcp", "permission", "protected_flow", "tests"} {
		if !meaningful(row[field]) {
			return fmt.Errorf("%s status needs %s evidence", status, field)
		}
	}
	switch status {
	case "direct", "equivalent", "protected_handoff":
		if err := checkTools(catalog, row["mcp"], true); err != nil {
			return err
		}
	case "transport_na":
		protocol := mcpProtocolRoute.MatchString(route.Operation) && strings.HasSuffix(route.Handler, "MCP")
		if row["mcp"] != "none" {
			return errors.New("transport_na rows must set mcp to none")
		}
		if !protocol && route.Boundary != "transport_auth" && route.Boundary != "device_token" && route.Boundary != "channel_runtime" {
			return fmt.Errorf("transport_na is only for MCP protocol, transport, device or channel-runtime routes, not %s", route.Boundary)
		}
	case "web_prohibited_na":
		if row["mcp"] != "none" {
			return errors.New("web_prohibited_na rows must set mcp to none")
		}
		if !route.RejectOnly {
			return errors.New("web_prohibited_na needs a handler that only rejects the request")
		}
	}
	return nil
}

// Report summarizes one check.
type Report struct {
	Operations, Final, Open, WebSites, Tools, ToolsFinal int
	Stale, Removed                                       []string
}

// Check regenerates every inventory, compares it with the checked-in files
// and validates evidence. With update it rewrites the files first. With
// complete it also requires every operation and tool row to be closed.
func Check(root string, catalog Catalog, update, complete bool) (Report, error) {
	var report Report
	routes, err := ScanRoutes(root)
	if err != nil {
		return report, err
	}
	previous, _, err := readTSV(filepath.Join(root, LedgerPath))
	if err != nil {
		return report, err
	}
	rows, stale, removed := MergeLedger(previous, routes)
	report.Stale, report.Removed = stale, removed
	sites, err := ScanWeb(root)
	if err != nil {
		return report, err
	}
	unmatched, err := UnmatchedWebSites(sites, routes)
	if err != nil {
		return report, err
	}
	if len(unmatched) > 0 {
		var lines []string
		for _, site := range unmatched {
			lines = append(lines, fmt.Sprintf("%s:%d %s %s", site.Source, site.Line, site.MethodHint, site.Expression))
		}
		return report, fmt.Errorf("Web calls with no registered route (a registration escaped the scan):\n%s", strings.Join(lines, "\n"))
	}
	webRows := make([]Row, 0, len(sites))
	for _, site := range sites {
		webRows = append(webRows, Row{"source": site.Source, "line": fmt.Sprint(site.Line), "method_hint": site.MethodHint, "expression": site.Expression})
	}
	toolsPrevious, _, err := readTSV(filepath.Join(root, ToolsPath))
	if err != nil {
		return report, err
	}
	toolRows := mergeTools(toolsPrevious, catalog)
	files := map[string][]byte{
		LedgerPath: encodeTSV(ledgerFields, rows),
		WebPath:    encodeTSV(webFields, webRows),
		ToolsPath:  encodeTSV(toolFields, toolRows),
	}
	for _, path := range []string{LedgerPath, WebPath, ToolsPath} {
		target := filepath.Join(root, filepath.FromSlash(path))
		if update {
			if err := os.WriteFile(target, files[path], 0o644); err != nil {
				return report, err
			}
			continue
		}
		current, _ := os.ReadFile(target)
		if !bytes.Equal(current, files[path]) {
			return report, fmt.Errorf("%s differs from the scan; run the coverage tool with -update and review every new, stale or removed row", path)
		}
	}
	byKey := map[string]Registration{}
	for _, route := range routes {
		byKey[route.Server+" "+route.Operation] = route
	}
	var problems []string
	for _, row := range rows {
		if err := ValidateRow(root, catalog, byKey[row.key()], row); err != nil {
			problems = append(problems, row.key()+": "+err.Error())
		}
		if finalStatus[row["status"]] {
			report.Final++
		} else {
			report.Open++
		}
	}
	for _, row := range toolRows {
		if err := validateTool(root, catalog, row); err != nil {
			problems = append(problems, "tool "+row["tool"]+": "+err.Error())
		}
		if toolFinal[row["status"]] {
			report.ToolsFinal++
		}
	}
	report.Operations, report.WebSites, report.Tools = len(rows), len(sites), len(toolRows)
	if len(problems) > 0 {
		return report, fmt.Errorf("ledger evidence is invalid:\n%s", strings.Join(problems, "\n"))
	}
	if complete && (report.Open > 0 || report.ToolsFinal < report.Tools) {
		return report, fmt.Errorf("coverage is incomplete: %d open operations, %d open tool rows", report.Open, report.Tools-report.ToolsFinal)
	}
	return report, nil
}

// The ordinary-Task tool ledger lists every built-in MCP tool except the
// pet's own server, and records how a pet Turn reaches the same capability.
var toolFinal = map[string]bool{"direct": true, "equivalent": true, "mode_gated": true}
var toolOpen = map[string]bool{pendingValue: true, "implemented": true, "stale": true}

func mergeTools(previous []Row, catalog Catalog) []Row {
	old := map[string]Row{}
	for _, row := range previous {
		old[row["tool"]] = row
	}
	var names []string
	for server, tools := range catalog {
		if server == "aha2_pet" {
			continue
		}
		for tool := range tools {
			names = append(names, server+"."+tool)
		}
	}
	sort.Strings(names)
	rows := make([]Row, 0, len(names))
	for _, name := range names {
		row := Row{"tool": name}
		for _, field := range toolFields[1:] {
			row[field] = pendingValue
			if value := old[name][field]; value != "" {
				row[field] = value
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func validateTool(root string, catalog Catalog, row Row) error {
	status := row["status"]
	if !toolFinal[status] && !toolOpen[status] {
		return fmt.Errorf("unknown status %q", status)
	}
	if meaningful(row["tests"]) {
		if err := checkTests(root, row["tests"]); err != nil {
			return err
		}
	}
	if !toolFinal[status] {
		return nil
	}
	for _, field := range []string{"pet_access", "permission", "tests"} {
		if !meaningful(row[field]) {
			return fmt.Errorf("%s status needs %s evidence", status, field)
		}
	}
	if status == "equivalent" {
		// The equivalent must be a real tool other than the row itself.
		matches := toolReference.FindAllStringSubmatch(row["pet_access"], -1)
		if len(matches) == 0 {
			return errors.New("equivalent needs pet_access to name the pet-reachable server.tool")
		}
		return checkTools(catalog, row["pet_access"], true)
	}
	return nil
}
