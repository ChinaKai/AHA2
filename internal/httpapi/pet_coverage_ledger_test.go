package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/petcoverage"
)

// The coverage ledgers must describe the code as it is: every registered
// operation and built-in MCP tool is listed, every Web call reaches a
// registered route, changed handlers are not left with an old verdict, and
// every claim a row makes names real tools and real tests. Completeness is
// a separate release gate (go run ./cmd/aha-pet-coverage -complete).
func TestPetCoverageLedgersMatchRegistrationsAndEvidence(t *testing.T) {
	report, err := petcoverage.Check(filepath.Join("..", ".."), petcoverage.NewCatalog(MCPToolCatalog()), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Operations == 0 || report.WebSites == 0 || report.Tools == 0 {
		t.Fatalf("empty inventory: %+v", report)
	}
}

func TestMCPToolCatalogCoversEveryBuiltInServer(t *testing.T) {
	catalog := MCPToolCatalog()
	for server, tool := range map[string]string{
		"aha2_core": "inbox_wait", "aha2_knowledge": "knowledge_guide", "aha2_workspace": "workspace_process_list",
		"aha2_browser": "browser_status", "aha2_desktop": "desktop_status", "aha2_hardware": "hardware_adb_exec",
		"aha2_mail": "mail_send", "aha2_channel": "channel_reply", "aha2_collab": "collab_batch_submit", "aha2_pet": "aha_overview",
	} {
		found := false
		for _, name := range catalog[server] {
			found = found || name == tool
		}
		if !found {
			t.Fatalf("%s catalog lacks %s: %v", server, tool, catalog[server])
		}
	}
}

// /healthz is a transport probe: unauthenticated, and it returns nothing
// beyond liveness, so it has no pet MCP counterpart.
func TestHealthProbeCarriesNoBusinessData(t *testing.T) {
	recorder := httptest.NewRecorder()
	New(Config{}).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]any
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || len(body) != 2 || body["ok"] != true || body["service"] != "aha2" {
		t.Fatalf("health probe changed: %d %s", recorder.Code, recorder.Body.String())
	}
}
