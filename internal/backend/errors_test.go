package backend

import (
	"strings"
	"testing"
)

// Authorization redaction must remove the credential without swallowing the
// diagnostics that share the line.
func TestProviderErrorTextRedactsAuthorizationAndKeepsDiagnostics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		secrets []string
	}{
		{
			name:    "bearer",
			input:   "authorization: Bearer abc.def.ghi; request_id=req_9 status=401",
			secrets: []string{"abc.def.ghi"},
		},
		{
			name:    "basic",
			input:   "Authorization: Basic dXNlcjpwYXNz; request_id=req_10",
			secrets: []string{"dXNlcjpwYXNz"},
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := providerErrorText(test.input)
			for _, secret := range test.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("credential leaked: %s", got)
				}
			}
			for _, want := range []string{"[redacted]", "request_id=req_"} {
				if !strings.Contains(got, want) {
					t.Fatalf("diagnostic %q was lost: %s", want, got)
				}
			}
		})
	}
}

func TestProviderErrorTextRedactsCommonProviderKeyPrefixes(t *testing.T) {
	t.Parallel()
	aws := "AKIAIOSFODNN7EXAMPLE"
	google := "AIza" + strings.Repeat("A", 35)
	anthropic := "sk-ant-api03-abcdefghijklmnop"
	got := providerErrorText("keys " + aws + " " + google + " " + anthropic)
	for _, secret := range []string{aws, google, anthropic} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q leaked: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("redaction marker missing: %s", got)
	}
}

// Token counts are diagnostics, not credentials: redaction must not mangle
// ordinary provider text such as "requested 1055361 tokens".
func TestProviderErrorTextKeepsTokenCounts(t *testing.T) {
	t.Parallel()
	input := "maximum context length: requested 1055361 tokens, max_tokens=8192"
	if got := providerErrorText(input); got != input {
		t.Fatalf("benign token counts were redacted: %s", got)
	}
}
