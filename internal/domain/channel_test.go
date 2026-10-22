package domain

import "testing"

func TestChannelBotDialogueMaxTurnsDefaultsTo99(t *testing.T) {
	t.Parallel()
	if got := ChannelBotDialogueMaxTurns(nil); got != 99 {
		t.Fatalf("default max turns=%d", got)
	}
	for _, value := range []any{99, int64(99), float64(99)} {
		if got := ChannelBotDialogueMaxTurns(map[string]any{"bot_dialogue_max_turns": value}); got != 99 {
			t.Fatalf("max turns from %T=%d", value, got)
		}
	}
	for _, value := range []any{0, 100, "invalid"} {
		if got := ChannelBotDialogueMaxTurns(map[string]any{"bot_dialogue_max_turns": value}); got != DefaultChannelBotDialogueMaxTurns {
			t.Fatalf("invalid max turns %#v=%d", value, got)
		}
	}
}
