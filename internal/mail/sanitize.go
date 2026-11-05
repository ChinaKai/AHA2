package mail

import (
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

// allowedAttrs is deliberately tag specific. Event handlers, style, srcdoc,
// srcset and every other attribute are rejected by omission.
var allowedAttrs = map[string]map[string]bool{
	"a":   {"href": true, "title": true},
	"img": {"src": true, "alt": true, "width": true, "height": true},
	"td":  {"colspan": true, "rowspan": true},
	"th":  {"colspan": true, "rowspan": true},
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
	// Validate placeholder grammar across the whole fragment before parsing so
	// malformed braces in attribute values cannot slip through a parser quirk.
	if _, err := ParsePlaceholders(input); err != nil {
		return "", nil, nil, err
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
		if strings.Contains(node.Data, "{{") || strings.Contains(node.Data, "}}") {
			return errorf(domain.MailErrTemplateInvalid, "placeholders are not allowed in HTML comments")
		}
		return nil
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
		if name == "href" || name == "src" {
			if err := validateURLAttribute(attribute.Val, name, state); err != nil {
				return err
			}
			names, err := ParsePlaceholders(attribute.Val)
			if err != nil {
				return err
			}
			state.placeholders = append(state.placeholders, names...)
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

func validateURLAttribute(value, attribute string, state *htmlState) error {
	hasPlaceholder := strings.Contains(value, "{{")
	if state.mode == htmlTemplateMode {
		if attribute == "href" {
			if hasPlaceholder {
				if strings.TrimSpace(stripPlaceholders(value)) == "" {
					// A bare placeholder is validated after substitution.
					return nil
				}
				return validateStrictURL(replacePlaceholders(value, "x"), "href", nil)
			}
			return validateStrictURL(value, "href", nil)
		}
		// Images may only use cid: references. The individual content id is
		// checked against the attachment snapshot after substitution.
		if !strings.HasPrefix(strings.ToLower(value), "cid:") {
			return errorf(domain.MailErrTemplateInvalid, "img src must use cid:")
		}
		if hasPlaceholder {
			return validateStrictURL(replacePlaceholders(value, "x"), "src", nil)
		}
		return validateStrictURL(value, "src", nil)
	}
	if attribute == "href" {
		return validateStrictURL(value, "href", nil)
	}
	return validateStrictURL(value, "src", state.allowedCIDs)
}

// validateStrictURL enforces the design's URL policy: https/mailto for links,
// cid for embedded images, no relative or protocol-relative URLs, no
// whitespace/control obfuscation and no attribute-breaking characters.
func validateStrictURL(raw, attribute string, allowedCIDs map[string]bool) error {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return errorf(domain.MailErrTemplateInvalid, "URL is empty or has surrounding whitespace")
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errorf(domain.MailErrTemplateInvalid, "URL contains whitespace or control characters")
		}
		if strings.ContainsRune("\"'<>`\\", r) {
			return errorf(domain.MailErrTemplateInvalid, "URL contains an unsafe character")
		}
	}
	if strings.HasPrefix(raw, "//") {
		return errorf(domain.MailErrTemplateInvalid, "protocol-relative URLs are not allowed")
	}
	colon := strings.IndexByte(raw, ':')
	if colon <= 0 {
		return errorf(domain.MailErrTemplateInvalid, "URL must be absolute")
	}
	scheme := strings.ToLower(raw[:colon])
	for _, r := range scheme {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '+' && r != '-' && r != '.' {
			return errorf(domain.MailErrTemplateInvalid, "URL scheme is invalid")
		}
	}
	rest := raw[colon+1:]
	switch attribute {
	case "href":
		if scheme != "https" && scheme != "mailto" {
			return errorf(domain.MailErrTemplateInvalid, "URL scheme %q is not allowed", scheme)
		}
		if rest == "" {
			return errorf(domain.MailErrTemplateInvalid, "URL is missing a target")
		}
	case "src":
		if scheme != "cid" {
			return errorf(domain.MailErrTemplateInvalid, "image URL scheme %q is not allowed", scheme)
		}
		if !cidValuePattern.MatchString(rest) {
			return errorf(domain.MailErrTemplateInvalid, "cid reference is invalid")
		}
		if allowedCIDs != nil && !allowedCIDs[rest] {
			return errorf(domain.MailErrTemplateInvalid, "cid reference does not match a selected attachment")
		}
	}
	return nil
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
