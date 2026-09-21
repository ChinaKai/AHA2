package app

import "strings"

// Claude's CLI does not report a context window. Unlike Codex, whose
// `token_count` events carry `model_context_window`, a Claude transcript records
// only token counts, so the value cannot be measured from a run -- it has to be
// declared.
//
// The values come from Claude Code's documented model configuration. Note the
// reference project (src/aha_cli/services/context_pressure.py) still records 200K
// for "claude/default"; that predates Sonnet 5, so it is not followed here.
const claudeDefaultContextWindow = 200_000

// claudeContextWindows is keyed by both forms a caller may hold: the wire value
// the operator picks ("default", "opus") and the concrete model id the CLI
// resolves it to ("claude-sonnet-5"). Keying on the resolved id is what keeps
// equivalent choices consistent -- "default" and "sonnet" are the same model and
// must not disagree about its window.
//
// These come from Claude Code's documented model configuration, not from a
// measurement on this host. On the Anthropic API, Sonnet 5 and Opus 4.7 and later
// carry a native 1M window, which is why they need no "[1m]" marker; Haiku 4.5 is
// the 200K tier.
var claudeContextWindows = map[string]int64{
	// Native 1M-window models on the Anthropic API.
	"claude-sonnet-5":   1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"opus-4-7":          1_000_000,
	"claude-opus-4-6":   1_000_000,
	"opus-4-6":          1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"sonnet-4-6":        1_000_000,
	// The small/fast tier keeps the 200K window.
	"claude-haiku-4-5-20251001": claudeDefaultContextWindow,
	// Short names the CLI accepts, for callers that have no resolved id. They
	// mirror what those aliases resolve to, so an alias and its model agree.
	"default": 1_000_000,
	"sonnet":  1_000_000,
	"opus":    1_000_000,
	"fable":   1_000_000,
	"haiku":   claudeDefaultContextWindow,
}

// ClaudeContextWindow resolves the context window for a Claude model, or 0 when
// the model is not one this table knows.
//
// It answers "what window will the run actually use", not "what is the model
// capable of". Those differ: on a third-party gateway Claude Code budgets an
// otherwise-1M model at the fallback window unless the model name carries the
// "[1m]" marker, and this table has no way to observe that from outside. Pass
// the marker-relevant context through wireModel so the answer matches the run.
//
// It is used for display only. The execution path turns a model's ContextWindow
// into CLAUDE_CODE_MAX_CONTEXT_TOKENS, so writing this value onto the model would
// change when Claude compacts -- a display concern must not reach that.
func ClaudeContextWindow(wireModel, resolvedModel string, routedThroughGateway bool) int64 {
	// "[1m]" is the CLI's own marker for the 1M-token variant, and it describes
	// the option the operator picked. It therefore decides before everything
	// else: the resolved id it shares with the base model would otherwise win
	// and report the smaller window.
	marked := strings.Contains(strings.ToLower(wireModel), "[1m]")
	if marked {
		return 1_000_000
	}
	// Through a gateway the CLI budgets the model at the fallback window unless
	// the name carries the marker, so reporting the model's capable window here
	// would promise context the run cannot use. A marker the operator added is
	// honoured above; without one the honest answer is the fallback.
	if routedThroughGateway {
		return claudeDefaultContextWindow
	}
	// The resolved id is the more specific name, so it decides next and keeps
	// short aliases of the same model in agreement.
	for _, name := range []string{resolvedModel, wireModel} {
		if window, ok := claudeContextWindows[strings.ToLower(strings.TrimSpace(name))]; ok {
			return window
		}
	}
	return 0
}
