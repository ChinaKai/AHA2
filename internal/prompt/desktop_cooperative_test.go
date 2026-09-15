package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAgentDesktopReferenceExplainsJointAccessWithoutLegacyEscalation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "prompt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	content := templateContent(t, NewEngine(db), ctx, "resource.agent-api")
	for _, required := range []string{`controller:"shared"`, "automatically register", "consent to joint Owner/Agent",
		"does not change the controller/revision", "`controller:\"owner\"` grants do not authorize Agent access",
		"rather than silently broadening the old grant", "Actions are single-flight",
		"previous observations", "Never replay", "Only the Owner can enumerate/select/switch"} {
		if !strings.Contains(content, required) {
			t.Errorf("missing joint-control boundary: %s", required)
		}
	}
	if strings.Contains(content, "Owner takeover and stop revoke actions") {
		t.Fatal("shared reference still requires exclusive handoff")
	}
}
