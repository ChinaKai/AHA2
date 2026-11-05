package mail

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	// MaxSubjectRunes matches the design's default subject limit (200 chars).
	MaxSubjectRunes = 200
	// MaxBodyTextBytes and MaxBodyHTMLBytes bound rendered template bodies.
	MaxBodyTextBytes = 256 << 10
	MaxBodyHTMLBytes = 512 << 10
	// MaxRecipients is the To+CC+BCC cap from the design.
	MaxRecipients = 50
	// Extra variables are caller supplied and capped to prevent abuse.
	MaxExtraVariables = 32
	MaxExtraBytes     = 4 << 10
	// MaxVariableValueBytes keeps a single substitution from creating
	// unbounded intermediate content.
	MaxVariableValueBytes = 64 << 10
)

// allowedVariables is the complete phase one variable set from the design's
// section 5.1. extra.* keys are validated separately.
var allowedVariables = map[string]bool{
	"task.id":          true,
	"task.title":       true,
	"task.url":         true,
	"project.name":     true,
	"workspace.name":   true,
	"build.version":    true,
	"build.commit":     true,
	"build.branch":     true,
	"build.time":       true,
	"package.name":     true,
	"package.size":     true,
	"package.sha256":   true,
	"test.scope":       true,
	"test.notes":       true,
	"test.environment": true,
	"release.version":  true,
	"release.date":     true,
	"release.owner":    true,
	"agent.name":       true,
	"owner.name":       true,
}

var (
	placeholderNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[A-Za-z0-9_][A-Za-z0-9_-]*)+$`)
	extraKeyPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`)
)

// VariableNameAllowed reports whether a placeholder name is in the phase one
// allowlist. Only exact names and extra.<safe-key> are accepted; no expression,
// loop, filter or function syntax exists.
func VariableNameAllowed(name string) bool {
	if allowedVariables[name] {
		return true
	}
	if key, ok := strings.CutPrefix(name, "extra."); ok {
		return extraKeyPattern.MatchString(key)
	}
	return false
}

// ParsePlaceholders extracts the placeholder names from a template string.
// Malformed braces are rejected as template syntax errors instead of being
// passed through literally.
func ParsePlaceholders(value string) ([]string, error) {
	names := make([]string, 0)
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], "{{") {
			end := strings.Index(value[index+2:], "}}")
			if end < 0 {
				return nil, errorf(domain.MailErrTemplateInvalid, "unterminated placeholder")
			}
			content := value[index+2 : index+2+end]
			if strings.ContainsAny(content, "{}") || content == "" || strings.TrimSpace(content) != content {
				return nil, errorf(domain.MailErrTemplateInvalid, "invalid placeholder syntax")
			}
			if !placeholderNamePattern.MatchString(content) {
				return nil, errorf(domain.MailErrTemplateInvalid, "invalid placeholder name")
			}
			names = append(names, content)
			index += 2 + end + 2
			continue
		}
		if strings.HasPrefix(value[index:], "}}") {
			return nil, errorf(domain.MailErrTemplateInvalid, "unexpected closing braces")
		}
		index++
	}
	return names, nil
}

// validatePlaceholders checks syntax and membership for one template part.
// Unknown variable names are a separate stable error so callers can tell a
// typo from malformed syntax.
func validatePlaceholders(value string) ([]string, error) {
	names, err := ParsePlaceholders(value)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if !VariableNameAllowed(name) {
			return nil, errorf(domain.MailErrTemplateVariableUnknown, "unknown variable %q", name)
		}
	}
	return names, nil
}

// ValidateVariableNames verifies caller supplied variable names. Values are
// validated separately because extra.* has aggregate size limits.
func ValidateVariableNames(variables map[string]string) error {
	for name := range variables {
		if !VariableNameAllowed(name) {
			return errorf(domain.MailErrTemplateVariableUnknown, "unknown variable %q", name)
		}
	}
	return nil
}

// normalizeVariables validates the caller supplied variable map and enforces
// per-value and extra.* limits. It returns a copy so the caller cannot mutate
// the values used for rendering.
func normalizeVariables(input map[string]string) (map[string]string, error) {
	if err := ValidateVariableNames(input); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(input))
	extraCount := 0
	extraBytes := 0
	for name, value := range input {
		if !utf8.ValidString(value) {
			return nil, errorf(domain.MailErrTemplateInvalid, "variable %q is not valid UTF-8", name)
		}
		if strings.ContainsRune(value, 0) {
			return nil, errorf(domain.MailErrTemplateInvalid, "variable %q contains a NUL byte", name)
		}
		if len(value) > MaxVariableValueBytes {
			return nil, errorf(domain.MailErrTemplateInvalid, "variable %q is too long", name)
		}
		if strings.HasPrefix(name, "extra.") {
			extraCount++
			extraBytes += len(name) + len(value)
		}
		result[name] = value
	}
	if extraCount > MaxExtraVariables {
		return nil, errorf(domain.MailErrTemplateInvalid, "at most %d extra variables are allowed", MaxExtraVariables)
	}
	if extraBytes > MaxExtraBytes {
		return nil, errorf(domain.MailErrTemplateInvalid, "extra variables exceed %d bytes", MaxExtraBytes)
	}
	return result, nil
}

func uniqueSorted(names []string) []string {
	seen := make(map[string]bool, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
