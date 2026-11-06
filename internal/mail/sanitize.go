package mail

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// allowedTags is the phase one safe subset from the design's section 5.2.
// Anything outside this set (script, iframe, object, embed, form, style, svg,
// math, ...) is rejected rather than silently stripped, because executable
// markup must never reach a snapshot.
var allowedTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "br": true, "hr": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true,
	"ul": true, "ol": true, "li": true,
	"blockquote": true, "code": true, "pre": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true,
	"tr": true, "th": true, "td": true, "caption": true,
	"a": true, "img": true,
}

// allowedAttrs is deliberately tag specific. Event handlers, srcdoc, srcset
// and every other attribute are rejected by omission. `style` is only allowed
// on table elements, and only declarations from tableStyleAllowedProperties
// survive validation. Other tags still cannot carry a style attribute.
var allowedAttrs = map[string]map[string]bool{
	"a":     {"href": true, "title": true},
	"img":   {"src": true, "alt": true, "width": true, "height": true},
	"table": {"style": true},
	"tr":    {"style": true},
	"td":    {"colspan": true, "rowspan": true, "style": true},
	"th":    {"colspan": true, "rowspan": true, "style": true},
}

// tableStyleAllowedProperties is the table-only CSS subset. Every property is
// presentational: no URLs, no positioning, no layout takeover, no imports.
var tableStyleAllowedProperties = map[string]bool{
	"border": true, "border-collapse": true, "border-spacing": true,
	"border-color": true, "border-style": true, "border-width": true,
	"border-top": true, "border-right": true, "border-bottom": true, "border-left": true,
	"padding": true, "padding-top": true, "padding-right": true, "padding-bottom": true, "padding-left": true,
	"text-align": true, "vertical-align": true,
	"width": true, "min-width": true, "max-width": true,
	"height": true, "min-height": true, "max-height": true,
	"background-color": true, "color": true,
	"font-weight": true, "font-size": true, "line-height": true, "white-space": true,
}

// Forbidden anywhere in a table style value. url()/expression()/imports and
// !important are rejected explicitly even though the character checks below
// would also reject their punctuation, so the error stays understandable.
var tableStyleForbiddenValueFragments = []string{
	"url(", "expression", "@import", "!important", "javascript:", "behavior", "-moz-binding", "position", "display",
}

var cidValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$`)

type htmlMode int

const (
	// htmlTemplateMode validates an Owner-authored template. Placeholders in
	// href and cid: src values are allowed and re-validated after substitution.
	htmlTemplateMode htmlMode = iota
	// htmlFinalMode validates the final substituted HTML and rejects anything
	// that is not already safe.
	htmlFinalMode
)

type htmlState struct {
	mode         htmlMode
	allowedCIDs  map[string]bool
	placeholders []string
	warnings     []string
}

// processHTML parses and validates an HTML fragment. In final mode it returns a
// canonically re-serialized fragment containing only allowlisted nodes. In
// template mode only validation and placeholder collection happen.
func processHTML(input string, mode htmlMode, allowedCIDs map[string]bool) (string, []string, []string, error) {
	// Validate placeholder grammar only for Owner-authored templates. Final
	// HTML may legitimately contain braces from a variable value; it has
	// already been escaped and must only be checked for structure and URLs.
	if mode == htmlTemplateMode {
		if _, err := ParsePlaceholders(input); err != nil {
			return "", nil, nil, err
		}
	}
	contextNode := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(input), contextNode)
	if err != nil {
		return "", nil, nil, errorf(domain.MailErrTemplateInvalid, "HTML could not be parsed")
	}
	state := &htmlState{mode: mode, allowedCIDs: allowedCIDs}
	for _, node := range nodes {
		if err := validateHTMLNode(node, state); err != nil {
			return "", nil, nil, err
		}
	}
	if mode != htmlFinalMode {
		return "", uniqueSorted(state.placeholders), state.warnings, nil
	}
	var builder strings.Builder
	for _, node := range nodes {
		// Comments are harmless but can hide stale placeholders from editors.
		// Drop them from the canonical output.
		if node.Type == html.CommentNode {
			continue
		}
		if err := html.Render(&builder, node); err != nil {
			return "", nil, nil, errorf(domain.MailErrTemplateInvalid, "HTML could not be rendered")
		}
	}
	return builder.String(), uniqueSorted(state.placeholders), state.warnings, nil
}

func validateHTMLNode(node *html.Node, state *htmlState) error {
	switch node.Type {
	case html.TextNode:
		if !utf8.ValidString(node.Data) || strings.ContainsRune(node.Data, 0) {
			return errorf(domain.MailErrTemplateInvalid, "HTML text contains invalid characters")
		}
		if state.mode != htmlTemplateMode {
			return nil
		}
		names, err := ParsePlaceholders(node.Data)
		if err != nil {
			return err
		}
		for _, name := range names {
			if !VariableNameAllowed(name) {
				return errorf(domain.MailErrTemplateVariableUnknown, "unknown variable %q", name)
			}
		}
		state.placeholders = append(state.placeholders, names...)
		return nil
	case html.CommentNode:
		// Conditional comments (including nested markup and CDATA-derived
		// comments) can hide external images from a shallow walker. Reject
		// comments at any depth instead of trying to sanitize inside them.
		return errorf(domain.MailErrTemplateInvalid, "HTML comments are not allowed")
	case html.DoctypeNode:
		return errorf(domain.MailErrTemplateInvalid, "doctype is not allowed")
	case html.ElementNode:
		return validateHTMLElement(node, state)
	default:
		return errorf(domain.MailErrTemplateInvalid, "unsupported HTML node")
	}
}

func validateHTMLElement(node *html.Node, state *htmlState) error {
	tag := strings.ToLower(node.Data)
	if !allowedTags[tag] {
		return errorf(domain.MailErrTemplateInvalid, "HTML tag %q is not allowed", tag)
	}
	allowed := allowedAttrs[tag]
	seenAttrs := make(map[string]bool, len(node.Attr))
	for _, attribute := range node.Attr {
		name := strings.ToLower(attribute.Key)
		if seenAttrs[name] {
			return errorf(domain.MailErrTemplateInvalid, "duplicate HTML attribute %q", name)
		}
		seenAttrs[name] = true
		if !allowed[name] {
			return errorf(domain.MailErrTemplateInvalid, "HTML attribute %q is not allowed on <%s>", name, tag)
		}
		if !utf8.ValidString(attribute.Val) || strings.ContainsRune(attribute.Val, 0) {
			return errorf(domain.MailErrTemplateInvalid, "HTML attribute contains invalid characters")
		}
		if strings.Contains(attribute.Val, "{{") && name != "href" && name != "src" {
			return errorf(domain.MailErrTemplateInvalid, "placeholders are not allowed in %s", name)
		}
		if name == "style" {
			if err := validateTableStyle(tag, attribute.Val); err != nil {
				return err
			}
			continue
		}
		if name == "href" || name == "src" {
			if err := validateURLAttribute(attribute.Val, name, state); err != nil {
				return err
			}
			if state.mode == htmlTemplateMode {
				names, err := ParsePlaceholders(attribute.Val)
				if err != nil {
					return err
				}
				state.placeholders = append(state.placeholders, names...)
			}
		}
	}
	if tag == "img" && !seenAttrs["src"] {
		return errorf(domain.MailErrTemplateInvalid, "img requires a cid: src")
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := validateHTMLNode(child, state); err != nil {
			return err
		}
	}
	return nil
}

// validateTableStyle parses a table-only inline style strictly. Only known
// presentational properties are accepted, values are limited to plain CSS
// tokens (no functions, quotes, escapes, comments or other syntax), and
// forbidden constructs such as url(), expression(), @import and !important are
// rejected outright.
func validateTableStyle(tag, value string) error {
	switch tag {
	case "table", "tr", "th", "td":
	default:
		return errorf(domain.MailErrTemplateInvalid, "style is not allowed on <%s>", tag)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return errorf(domain.MailErrTemplateInvalid, "table style contains invalid characters")
	}
	for _, declaration := range strings.Split(value, ";") {
		declaration = strings.TrimSpace(declaration)
		if declaration == "" {
			continue
		}
		property, rawValue, found := strings.Cut(declaration, ":")
		if !found {
			return errorf(domain.MailErrTemplateInvalid, "table style declaration is invalid")
		}
		property = strings.ToLower(strings.TrimSpace(property))
		rawValue = strings.TrimSpace(rawValue)
		if !tableStyleAllowedProperties[property] {
			return errorf(domain.MailErrTemplateInvalid, "style property %q is not allowed", property)
		}
		if rawValue == "" {
			return errorf(domain.MailErrTemplateInvalid, "style property %q has an empty value", property)
		}
		lowered := strings.ToLower(rawValue)
		for _, forbidden := range tableStyleForbiddenValueFragments {
			if strings.Contains(lowered, forbidden) {
				return errorf(domain.MailErrTemplateInvalid, "style property %q contains a forbidden value", property)
			}
		}
		for _, r := range rawValue {
			if r < 0x20 || r == 0x7f || r > 0x7e || strings.ContainsRune("()\\\"'<>{};/:", r) {
				return errorf(domain.MailErrTemplateInvalid, "style property %q contains an unsafe character", property)
			}
		}
	}
	return nil
}

func validateURLAttribute(value, attribute string, state *htmlState) error {
	hasPlaceholder := strings.Contains(value, "{{")
	if state.mode == htmlTemplateMode {
		if attribute == "href" {
			if hasPlaceholder {
				if strings.TrimSpace(stripPlaceholders(value)) == "" {
					// A bare placeholder is validated after substitution.
					return nil
				}
				return validateStrictURLWith(replacePlaceholders(value, "x"), "href", nil, true)
			}
			return validateStrictURLWith(value, "href", nil, false)
		}
		// Images may only use cid: references. The individual content id is
		// checked against the attachment snapshot after substitution.
		if !strings.HasPrefix(strings.ToLower(value), "cid:") {
			return errorf(domain.MailErrTemplateInvalid, "img src must use cid:")
		}
		if hasPlaceholder {
			return validateStrictURLWith(replacePlaceholders(value, "x"), "src", nil, false)
		}
		return validateStrictURLWith(value, "src", nil, false)
	}
	if attribute == "href" {
		return validateStrictURLWith(value, "href", nil, false)
	}
	return validateStrictURLWith(value, "src", state.allowedCIDs, false)
}

// validateStrictURLWith enforces the design's URL policy with url.Parse:
// https/mailto for links, cid for embedded images, no userinfo, no relative or
// protocol-relative URLs, ASCII/punycode hosts only, no whitespace/control
// obfuscation and no mailto query/header parameters.
func validateStrictURLWith(raw, attribute string, allowedCIDs map[string]bool, templateMailto bool) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return errorf(domain.MailErrTemplateInvalid, "URL is empty or has surrounding whitespace")
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errorf(domain.MailErrTemplateInvalid, "URL contains whitespace or control characters")
		}
		if strings.ContainsRune("\\\"'<>`", r) {
			return errorf(domain.MailErrTemplateInvalid, "URL contains an unsafe character")
		}
	}
	if strings.HasPrefix(raw, "//") {
		return errorf(domain.MailErrTemplateInvalid, "protocol-relative URLs are not allowed")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return errorf(domain.MailErrTemplateInvalid, "URL must be absolute")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch attribute {
	case "href":
		if parsed.User != nil {
			return errorf(domain.MailErrTemplateInvalid, "URL must not contain userinfo")
		}
		switch scheme {
		case "https":
			if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
				return errorf(domain.MailErrTemplateInvalid, "https URL requires a host")
			}
			if !asciiHost(parsed.Hostname()) {
				return errorf(domain.MailErrTemplateInvalid, "URL host must be ASCII or punycode")
			}
		case "mailto":
			if parsed.Host != "" {
				return errorf(domain.MailErrTemplateInvalid, "mailto URL must not contain a host")
			}
			if parsed.RawQuery != "" || parsed.ForceQuery {
				return errorf(domain.MailErrTemplateInvalid, "mailto query parameters are not allowed")
			}
			target := parsed.Opaque
			if target == "" {
				target = parsed.Path
			}
			if target == "" {
				return errorf(domain.MailErrTemplateInvalid, "mailto URL is missing a recipient")
			}
			if !templateMailto {
				if _, err := NormalizeRecipient(target); err != nil {
					return errorf(domain.MailErrTemplateInvalid, "mailto recipient is invalid")
				}
			}
		default:
			return errorf(domain.MailErrTemplateInvalid, "URL scheme %q is not allowed", scheme)
		}
	case "src":
		if scheme != "cid" {
			return errorf(domain.MailErrTemplateInvalid, "image URL scheme %q is not allowed", scheme)
		}
		if parsed.User != nil || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return errorf(domain.MailErrTemplateInvalid, "cid reference is invalid")
		}
		cid := parsed.Opaque
		if cid == "" {
			cid = parsed.Path
		}
		if !cidValuePattern.MatchString(cid) {
			return errorf(domain.MailErrTemplateInvalid, "cid reference is invalid")
		}
		if allowedCIDs != nil && !allowedCIDs[cid] {
			return errorf(domain.MailErrTemplateInvalid, "cid reference does not match a selected attachment")
		}
	}
	return nil
}

func asciiHost(host string) bool {
	for _, r := range host {
		if r > 0x7f {
			return false
		}
	}
	return true
}

func stripPlaceholders(value string) string {
	return replacePlaceholders(value, "")
}

func replacePlaceholders(value, replacement string) string {
	var builder strings.Builder
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], "{{") {
			if end := strings.Index(value[index+2:], "}}"); end >= 0 {
				builder.WriteString(replacement)
				index += 2 + end + 2
				continue
			}
		}
		builder.WriteByte(value[index])
		index++
	}
	return builder.String()
}
