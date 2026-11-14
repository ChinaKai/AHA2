package knowledgevault

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Canonical knowledge types. New vault documents use only these values; the
// original database type is retained as legacy_type when it was different.
const (
	TypeNavigation   = "navigation"
	TypeDecision     = "decision"
	TypePractice     = "practice"
	TypeDiagnostic   = "diagnostic"
	TypeReference    = "reference"
	TypeArchitecture = "architecture"
)

var canonicalTypes = map[string]bool{
	TypeNavigation: true, TypeDecision: true, TypePractice: true,
	TypeDiagnostic: true, TypeReference: true, TypeArchitecture: true,
}

// legacyTypeAliases maps historical and human-authored type names onto the
// six canonical types. Unknown non-empty values fall back to reference and
// keep the original in legacy_type.
var legacyTypeAliases = map[string]string{
	"index": TypeNavigation, "toc": TypeNavigation, "overview": TypeNavigation,
	"home": TypeNavigation, "root": TypeNavigation, "map": TypeNavigation,
	"adr": TypeDecision, "decision-record": TypeDecision, "decisions": TypeDecision,
	"howto": TypePractice, "how-to": TypePractice, "guide": TypePractice,
	"procedure": TypePractice, "workflow": TypePractice, "convention": TypePractice,
	"rule": TypePractice, "lesson": TypePractice, "behavior": TypePractice,
	"behaviour": TypePractice, "playbook": TypePractice, "checklist": TypePractice,
	"diagnosis": TypeDiagnostic, "troubleshooting": TypeDiagnostic, "incident": TypeDiagnostic,
	"postmortem": TypeDiagnostic, "bug": TypeDiagnostic, "fix": TypeDiagnostic,
	"note": TypeReference, "fact": TypeReference, "doc": TypeReference,
	"documentation": TypeReference, "general": TypeReference, "snippet": TypeReference,
	"command": TypeReference, "technical": TypeReference, "reference-material": TypeReference,
	"design": TypeArchitecture, "spec": TypeArchitecture, "structure": TypeArchitecture,
	"topology": TypeArchitecture, "architecture-decision": TypeArchitecture,
	"solution": TypeArchitecture,
}

// NormalizeType returns the canonical type and the legacy value that must be
// preserved (empty when the input was already canonical).
func NormalizeType(raw, pathHint string) (string, string) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.NewReplacer("_", "-", " ", "-").Replace(trimmed)
	if trimmed == "" {
		return InferType(pathHint), ""
	}
	if canonicalTypes[trimmed] {
		return trimmed, ""
	}
	if mapped, ok := legacyTypeAliases[trimmed]; ok {
		return mapped, trimmed
	}
	return TypeReference, trimmed
}

// InferType chooses a canonical type when a document has no type field. The
// path is the most reliable signal: the global knowledge tree already groups
// diagnostics and behavior lessons into named folders.
func InferType(pathHint string) string {
	value := strings.ToLower(strings.ToLower(filepathSlash(pathHint)))
	switch {
	case strings.Contains(value, "agent-lessons/technical"), strings.Contains(value, "technical-diagnostics"):
		return TypeDiagnostic
	case strings.Contains(value, "agent-lessons/behavior"), strings.Contains(value, "behavior-lessons"):
		return TypePractice
	case strings.HasSuffix(value, "/index.md"), value == "index.md":
		return TypeNavigation
	case strings.Contains(value, "/general/"), strings.Contains(value, "general.md"):
		return TypeReference
	case strings.Contains(value, "decision"):
		return TypeDecision
	case strings.Contains(value, "architecture"), strings.Contains(value, "design"):
		return TypeArchitecture
	default:
		return TypeReference
	}
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

// Frontmatter is the set of optional fields the vault understands. Unknown
// YAML keys are kept in Extra so a rebuild does not silently discard data a
// future phase may rely on.
type Frontmatter struct {
	ID         string
	Title      string
	Slug       string
	Scope      string
	ProjectID  string
	Type       string
	LegacyType string
	Tags       []string
	Status     string
	Summary    string
	Revision   int
	Parent     string
	Created    string
	Updated    string
	SourceTask string
	SourceTurn string
	Confidence *float64
	Trust      string
	SortOrder  *int
	IsIndex    *bool
	Archive    *bool
	Extra      map[string]any
}

// Document is the result of parsing one Markdown file.
type Document struct {
	Raw            string
	Body           string
	Frontmatter    Frontmatter
	HasFrontmatter bool
	Warning        string
}

var frontmatterPattern = regexp.MustCompile(`(?s)\A---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|$)`)

// ParseDocument parses optional YAML frontmatter. A malformed block never
// causes the file to be dropped: the whole raw text becomes the body and a
// warning is returned so the rebuild can report the file instead of losing it.
func ParseDocument(raw string) Document {
	document := Document{Raw: raw, Body: raw}
	match := frontmatterPattern.FindStringSubmatchIndex(raw)
	if match == nil {
		return document
	}
	document.HasFrontmatter = true
	header := raw[match[2]:match[3]]
	document.Body = raw[match[1]:]
	metadata := map[string]any{}
	if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
		document.Body = raw
		document.Warning = "invalid frontmatter: " + firstLine(err.Error())
		return document
	}
	document.Frontmatter = frontmatterFromMap(metadata)
	return document
}

func frontmatterFromMap(metadata map[string]any) Frontmatter {
	known := map[string]bool{
		"id": true, "title": true, "slug": true, "scope": true, "project_id": true,
		"type": true, "legacy_type": true, "tags": true, "status": true, "summary": true,
		"revision": true, "parent": true, "created": true, "updated": true,
		"source_task": true, "source_turn": true, "confidence": true, "trust": true,
		"sort_order": true, "is_index": true, "archive": true,
	}
	item := Frontmatter{Extra: map[string]any{}}
	for key, value := range metadata {
		key = strings.ToLower(strings.TrimSpace(key))
		if !known[key] {
			item.Extra[key] = value
			continue
		}
		switch key {
		case "id":
			item.ID = stringValue(value)
		case "title":
			item.Title = stringValue(value)
		case "slug":
			item.Slug = stringValue(value)
		case "scope":
			item.Scope = strings.ToLower(stringValue(value))
		case "project_id":
			item.ProjectID = stringValue(value)
		case "type":
			item.Type = stringValue(value)
		case "legacy_type":
			item.LegacyType = stringValue(value)
		case "status":
			item.Status = strings.ToLower(stringValue(value))
		case "summary":
			item.Summary = stringValue(value)
		case "revision":
			if parsed, ok := intValue(value); ok && parsed > 0 {
				item.Revision = parsed
			}
		case "parent":
			item.Parent = stringValue(value)
		case "created":
			item.Created = timeValue(value)
		case "updated":
			item.Updated = timeValue(value)
		case "source_task":
			item.SourceTask = stringValue(value)
		case "source_turn":
			item.SourceTurn = stringValue(value)
		case "trust":
			item.Trust = stringValue(value)
		case "tags":
			item.Tags = stringSlice(value)
		case "confidence":
			if parsed, ok := floatValue(value); ok {
				item.Confidence = &parsed
			}
		case "sort_order":
			if parsed, ok := intValue(value); ok {
				item.SortOrder = &parsed
			}
		case "is_index":
			if parsed, ok := boolValue(value); ok {
				item.IsIndex = &parsed
			}
		case "archive":
			if parsed, ok := boolValue(value); ok {
				item.Archive = &parsed
			}
		}
	}
	if len(item.Extra) == 0 {
		item.Extra = nil
	}
	return item
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func timeValue(value any) string {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func stringSlice(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case nil:
		return result
	case string:
		for _, part := range strings.Split(typed, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				result = append(result, part)
			}
		}
	case []any:
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				result = append(result, text)
			}
		}
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		return parsed, err == nil
	}
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return parsed, err == nil
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}

// FirstHeading returns the first ATX heading outside a fenced code block.
func FirstHeading(body string) string {
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// IndexSummary returns a short plain-text summary from the first meaningful
// line of a document body.
func IndexSummary(body string, limit int) string {
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<!--") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, ">-*+0123456789.) "))
		if line == "" {
			continue
		}
		return truncateRunes(line, limit)
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// renderFrontmatter produces deterministic YAML. Field order is fixed by the
// struct declaration so repeated exports are byte-identical.
func renderFrontmatter(item Frontmatter) (string, error) {
	type yamlFrontmatter struct {
		ID         string   `yaml:"id,omitempty"`
		Title      string   `yaml:"title,omitempty"`
		Slug       string   `yaml:"slug,omitempty"`
		Scope      string   `yaml:"scope,omitempty"`
		ProjectID  string   `yaml:"project_id,omitempty"`
		Type       string   `yaml:"type,omitempty"`
		LegacyType string   `yaml:"legacy_type,omitempty"`
		Tags       []string `yaml:"tags,omitempty"`
		Status     string   `yaml:"status,omitempty"`
		Summary    string   `yaml:"summary,omitempty"`
		Revision   int      `yaml:"revision,omitempty"`
		Parent     string   `yaml:"parent,omitempty"`
		Created    string   `yaml:"created,omitempty"`
		Updated    string   `yaml:"updated,omitempty"`
		SourceTask string   `yaml:"source_task,omitempty"`
		SourceTurn string   `yaml:"source_turn,omitempty"`
		Confidence *float64 `yaml:"confidence,omitempty"`
		Trust      string   `yaml:"trust,omitempty"`
		SortOrder  *int     `yaml:"sort_order,omitempty"`
		IsIndex    *bool    `yaml:"is_index,omitempty"`
		Archive    *bool    `yaml:"archive,omitempty"`
	}
	encoded, err := yaml.Marshal(yamlFrontmatter{
		ID: item.ID, Title: item.Title, Slug: item.Slug, Scope: item.Scope,
		ProjectID: item.ProjectID, Type: item.Type, LegacyType: item.LegacyType,
		Tags: item.Tags, Status: item.Status, Summary: item.Summary, Revision: item.Revision, Parent: item.Parent,
		Created: item.Created, Updated: item.Updated,
		SourceTask: item.SourceTask, SourceTurn: item.SourceTurn,
		Confidence: item.Confidence, Trust: item.Trust,
		SortOrder: item.SortOrder, IsIndex: item.IsIndex, Archive: item.Archive,
	})
	if err != nil {
		return "", err
	}
	return "---\n" + string(encoded) + "---\n", nil
}

func encodeExtra(extra map[string]any) string {
	if len(extra) == 0 {
		return ""
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return ""
	}
	return string(encoded)
}
