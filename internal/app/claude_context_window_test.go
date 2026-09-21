package app

import "testing"

func TestClaudeContextWindowUsesKnownModels(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		wireModel     string
		resolvedModel string
		want          int64
	}{
		// Sonnet 5, Opus 4.8 and Opus 4.7+ carry a native 1M window on the
		// Anthropic API, so the recommended option is not the 200K tier.
		{wireModel: "default", resolvedModel: "claude-sonnet-5", want: 1_000_000},
		{wireModel: "opus", resolvedModel: "claude-opus-4-8", want: 1_000_000},
		{wireModel: "opus-4-6", resolvedModel: "claude-opus-4-6", want: 1_000_000},
		// Haiku is the small/fast tier and keeps the 200K window.
		{wireModel: "haiku", resolvedModel: "claude-haiku-4-5-20251001", want: claudeDefaultContextWindow},
		// An alias must agree with the model it resolves to.
		{wireModel: "sonnet", resolvedModel: "claude-sonnet-5", want: 1_000_000},
	} {
		if got := ClaudeContextWindow(testCase.wireModel, testCase.resolvedModel, false); got != testCase.want {
			t.Fatalf("ClaudeContextWindow(%q, %q) = %d, want %d",
				testCase.wireModel, testCase.resolvedModel, got, testCase.want)
		}
	}
}

// A model the table does not cover must keep an unknown window. Guessing one
// would print a plausible percentage against a number nobody verified, which is
// worse than admitting the window is unknown.
func TestClaudeContextWindowLeavesUnknownModelsUnset(t *testing.T) {
	t.Parallel()
	if got := ClaudeContextWindow("some-new-model", "claude-something-unreleased", false); got != 0 {
		t.Fatalf("unknown model window = %d, want 0", got)
	}
}

// The CLI marks its 1M-token variant with a [1m] suffix.
func TestClaudeContextWindowHonoursOneMillionSuffix(t *testing.T) {
	t.Parallel()
	if got := ClaudeContextWindow("sonnet[1m]", "", false); got != 1_000_000 {
		t.Fatalf("1m-suffix window = %d, want 1000000", got)
	}
}

// A run through a custom endpoint is budgeted at the fallback window unless the
// model name carries the "[1m]" marker, so reporting the model's capable window
// would show context the run cannot reach. Display must match the run.
func TestClaudeContextWindowFollowsGatewayBudget(t *testing.T) {
	t.Parallel()
	// Without the marker, a natively-1M model is budgeted at the fallback.
	if got := ClaudeContextWindow("sonnet", "claude-sonnet-5", true); got != claudeDefaultContextWindow {
		t.Fatalf("gateway window = %d, want %d", got, claudeDefaultContextWindow)
	}
	// The operator adding the marker is exactly how the run reaches 1M, so the
	// display has to follow it.
	if got := ClaudeContextWindow("sonnet[1m]", "claude-sonnet-5", true); got != 1_000_000 {
		t.Fatalf("gateway 1m-marked window = %d, want 1000000", got)
	}
	// Direct to Anthropic the suffix is unnecessary; the model is natively 1M.
	if got := ClaudeContextWindow("sonnet", "claude-sonnet-5", false); got != 1_000_000 {
		t.Fatalf("direct window = %d, want 1000000", got)
	}
}
