package httpapi

import "testing"

func TestResolveModelBackend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		requested string
		wire      string
		want      string
		wantError bool
	}{
		{wire: "responses", want: "codex"},
		{wire: "chat", want: "codex"},
		{wire: "anthropic_messages", want: "claude"},
		{requested: "dsh", wire: "responses", want: "dsh"},
		{requested: "dsh", wire: "chat", want: "dsh"},
		{requested: "dsh", wire: "anthropic_messages", want: "dsh"},
		{requested: "codex", wire: "anthropic_messages", wantError: true},
		{requested: "claude", wire: "responses", wantError: true},
		{requested: "unknown", wire: "responses", wantError: true},
	}
	for _, test := range tests {
		got, err := resolveModelBackend(test.requested, test.wire)
		if test.wantError {
			if err == nil {
				t.Fatalf("resolveModelBackend(%q, %q) unexpectedly succeeded with %q", test.requested, test.wire, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("resolveModelBackend(%q, %q) = %q, %v; want %q", test.requested, test.wire, got, err, test.want)
		}
	}
}
