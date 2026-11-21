package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
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

// The static scan must agree with the muxes the server really builds: every
// scanned httpapi and mail-download operation is routed to exactly its
// pattern. Conditional groups are switched on so all of them are registered.
func TestPetCoverageScanMatchesLiveMuxes(t *testing.T) {
	routes, stats, err := petcoverage.ScanRoutes(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != stats.Registering+len(stats.NonRoute) {
		t.Fatalf("unreconciled Handle calls: %+v", stats)
	}
	server := New(Config{})
	server.localOwnerIngest = true
	server.knowledgeVault = &knowledgevault.Manager{}
	main, err := petcoverage.VerifyMux(server.routeMux(), routes, "httpapi")
	if err != nil {
		t.Fatal(err)
	}
	download, ok := server.MailDownloadHandler().(*http.ServeMux)
	if !ok {
		t.Fatal("mail download handler is not a ServeMux")
	}
	mail, err := petcoverage.VerifyMux(download, routes, "mail-download")
	if err != nil {
		t.Fatal(err)
	}
	if main < 400 || mail != 2 {
		t.Fatalf("verified %d main and %d mail-download operations", main, mail)
	}
}

// r2 M1 on the real repository: the channel-runtime routes sit behind the
// two-argument withChannelCapability(scope, next); each must resolve to its
// own handler and fingerprint under the channel_runtime boundary.
func TestChannelRuntimeRoutesResolvePastTheirTwoArgumentWrapper(t *testing.T) {
	routes, _, err := petcoverage.ScanRoutes(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	handlers, fingerprints := map[string]bool{}, map[string]bool{}
	count := 0
	for _, route := range routes {
		if route.Server != "httpapi" || !strings.Contains(route.Operation, " /api/channel-runtime/") {
			continue
		}
		count++
		if route.Handler == "withChannelCapability" || route.Boundary != "channel_runtime" || !strings.HasPrefix(route.Wrappers, "withChannelCapability(") {
			t.Fatalf("%s resolved to %+v", route.Operation, route)
		}
		handlers[route.Handler], fingerprints[route.Fingerprint] = true, true
	}
	if count != 12 || len(handlers) != 12 || len(fingerprints) != 12 {
		t.Fatalf("channel-runtime routes=%d distinct handlers=%d fingerprints=%d", count, len(handlers), len(fingerprints))
	}
}
