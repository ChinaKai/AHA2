package configimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAHARedactsSecrets(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	source := `{
	  "providers":[{"id":"p1","credential":"provider-secret"}],
	  "configured_models":[{"provider_id":"p1","model_id":"model-a","backend":"codex","wire_api":"responses","context_window":200000}],
	  "codex":{"env":[{"name":"prod","AHA_PROVIDER_ID":"p1","OPENAI_BASE_URL":"https://example.test/v1","OPENAI_MODEL":"model-a","CODEX_WIRE_API":"responses","CODEX_ENV_KEY":"OPENAI_API_KEY","OPENAI_API_KEY":"api-secret"}]}
	}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := LoadAHA(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || len(result.EnvGroups) != 1 || len(result.Secrets) != 1 {
		t.Fatalf("unexpected import: %#v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "api-secret") || strings.Contains(string(data), "provider-secret") {
		t.Fatal("secret leaked through JSON result")
	}
	if !result.EnvGroups[0].SecretConfigured {
		t.Fatal("secret readiness not reported")
	}
}

func TestLoadAHAScopesCustomCredentialEnvironment(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	source := `{
	  "configured_models":[{"provider_id":"p1","model_id":"model-a","backend":"codex"}],
	  "codex":{"env":[{"name":"prod","AHA_PROVIDER_ID":"p1","OPENAI_MODEL":"model-a","CODEX_ENV_KEY":"INTERNAL_AUTH_TOKEN","OPENAI_API_KEY":"api-secret"}]}
	}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := LoadAHA(path)
	if err != nil {
		t.Fatal(err)
	}
	group := result.EnvGroups[0]
	ref := group.SecretRefs["INTERNAL_AUTH_TOKEN"]
	if ref == "" || result.Secrets[ref] != "api-secret" {
		t.Fatalf("custom credential environment was not scoped: %#v %#v", group.SecretRefs, result.Secrets)
	}
}
