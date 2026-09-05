package main

import (
	"strings"
	"testing"
)

func TestResolveAgentAPIURL(t *testing.T) {
	tests := []struct {
		listen, configured, expected string
	}{
		{"0.0.0.0:8766", "", "http://127.0.0.1:8766"},
		{"[::]:9000", "", "http://127.0.0.1:9000"},
		{"127.0.0.1:7000", "", "http://127.0.0.1:7000"},
		{"0.0.0.0:8766", "https://aha.example.test/", "https://aha.example.test"},
	}
	for _, test := range tests {
		if actual := resolveAgentAPIURL(test.listen, test.configured); actual != test.expected {
			t.Errorf("resolveAgentAPIURL(%q, %q) = %q, want %q", test.listen, test.configured, actual, test.expected)
		}
	}
}

func TestValidateAgentAPIURLRequiresHTTPSOffLoopback(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:8766", "http://localhost:8766", "https://192.0.2.10"} {
		if err := validateAgentAPIURL(value, false); err != nil {
			t.Errorf("%s: %v", value, err)
		}
	}
	if err := validateAgentAPIURL("http://192.0.2.10:8766", false); err == nil || !strings.Contains(err.Error(), "requires https") {
		t.Fatalf("insecure remote URL error = %v", err)
	}
	if err := validateAgentAPIURL("http://192.0.2.10:8766", true); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentAPIURL("https://user:secret@example.test", false); err == nil {
		t.Fatal("URL credentials were accepted")
	}
}
