package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

func decodeKnowledgeMCPText(t *testing.T, text string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("knowledge MCP text is not JSON: %v: %s", err, text)
	}
	return payload
}

// TestAgentKnowledgeMCPDomainExcludesBoundLibraries covers the P1/P2 range
// findings: a global entry is readable but not revisable, and a library bound
// to the current Project stays outside this domain even though the general
// HTTP Agent API still exposes it with its original binding semantics.
func TestAgentKnowledgeMCPDomainExcludesBoundLibraries(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-bound")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	current := seedKnowledgeForMCP(t, h, "project", task.ProjectID, "knowledge-domain-current",
		"Domain current practice", "Domain current body stays inside the owning project.")
	global := seedKnowledgeForMCP(t, h, "global", "", "knowledge-domain-global",
		"Domain global reference", "Domain global body must remain unchanged after a rejected revise.")

	now := time.Now().UTC()
	otherProject := domain.Project{ID: domain.NewID("project"), Name: "Other", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := h.database.CreateProject(h.ctx, otherProject); err != nil {
		t.Fatal(err)
	}
	foreign := seedKnowledgeForMCP(t, h, "project", otherProject.ID, "knowledge-domain-foreign",
		"Domain foreign practice", "Domain foreign body must never be listed or revised.")

	container := domain.Project{
		ID: domain.NewID("project"), Name: "Bound library", ProjectType: "knowledge",
		KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
	}
	if err := h.database.CreateProject(h.ctx, container); err != nil {
		t.Fatal(err)
	}
	library, err := h.database.KnowledgeLibrary(h.ctx, "library_"+container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.database.BindKnowledgeLibrary(h.ctx, library.ID, task.ProjectID, "project", now); err != nil {
		t.Fatal(err)
	}
	boundRoot, err := h.database.EnsureKnowledgeRoot(h.ctx, "project", container.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound := domain.KnowledgeEntry{
		ID: "knowledge-domain-bound", Scope: "project", ProjectID: container.ID,
		ParentID: boundRoot.ID, Slug: "domain-bound", Type: "practice",
		Title: "Bound library practice", Body: "Bound library body must stay outside the MCP domain.",
		Status: domain.KnowledgeVerified, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.database.CreateKnowledge(h.ctx, bound); err != nil {
		t.Fatal(err)
	}

	list := coreMCPCallTool(t, url, token, "knowledge_list", map[string]any{"limit": 100})
	if list.Status != http.StatusOK || list.IsError {
		t.Fatalf("knowledge_list status=%d payload=%#v", list.Status, list)
	}
	if !strings.Contains(list.Text, current.ID) || !strings.Contains(list.Text, global.ID) {
		t.Fatalf("knowledge_list missing own/global entries: %s", list.Text)
	}
	for _, leaked := range []string{foreign.ID, bound.ID, "Domain foreign body", "Bound library body"} {
		if strings.Contains(list.Text, leaked) {
			t.Fatalf("knowledge_list leaked %q: %s", leaked, list.Text)
		}
	}

	for name, entry := range map[string]domain.KnowledgeEntry{
		"foreign": foreign,
		"bound":   bound,
	} {
		if got := coreMCPCallTool(t, url, token, "knowledge_get", map[string]any{"id": entry.ID}); got.Status != http.StatusOK ||
			!got.IsError || !strings.Contains(got.Text, "knowledge_not_found") {
			t.Fatalf("%s knowledge_get payload=%#v", name, got)
		}
		if feedback := coreMCPCallTool(t, url, token, "knowledge_feedback", map[string]any{
			"entry_id": entry.ID, "kind": "helped",
		}); feedback.Status != http.StatusOK || !feedback.IsError ||
			!strings.Contains(feedback.Text, "knowledge_not_found") {
			t.Fatalf("%s knowledge_feedback payload=%#v", name, feedback)
		}
		if revise := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
			"mode": "revise", "scope": "project", "entry_id": entry.ID, "base_revision": 1,
			"type": "practice", "title": "Cross domain revise", "summary": "This revise must be refused.",
			"body": "This replacement body is long enough to satisfy the knowledge contract.",
		}); revise.Status != http.StatusOK || !revise.IsError ||
			!strings.Contains(revise.Text, "knowledge_not_found") {
			t.Fatalf("%s knowledge_submit revise payload=%#v", name, revise)
		}
	}

	// The P1 reproduction: a Main Turn used to infer global scope for a revise
	// with an omitted or project scope and rewrite the global body.
	globalRevise := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "revise", "entry_id": global.ID, "base_revision": global.Revision,
		"type": "practice", "title": "Global overwrite attempt", "summary": "Global is read-only here.",
		"body": "This global body must never be written by the project-scoped knowledge domain.",
	})
	if globalRevise.Status != http.StatusOK || !globalRevise.IsError ||
		!strings.Contains(globalRevise.Text, "knowledge_scope_forbidden") {
		t.Fatalf("global revise payload=%#v", globalRevise)
	}
	storedGlobal, err := h.database.Knowledge(h.ctx, global.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedGlobal.Body != global.Body || storedGlobal.Revision != global.Revision ||
		storedGlobal.Status != global.Status {
		t.Fatalf("global entry changed after rejected revise: %#v", storedGlobal)
	}
	proposals, err := h.database.ListKnowledgeProposals(h.ctx, "global", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("global revise created proposals: %#v", proposals)
	}

	// Preserve the original HTTP binding capability: the same bound library is
	// still listed and still accepts a contribution proposal over the Agent API.
	response := agentRequest(t, h.server.URL+"/api/v1/agent/knowledge", http.MethodGet, token, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("http knowledge status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	var httpList struct {
		Knowledge []domain.KnowledgeEntry `json:"knowledge"`
	}
	decodeResponse(t, response, &httpList)
	var boundVisible, boundWritable bool
	for _, item := range httpList.Knowledge {
		if item.ID == bound.ID {
			boundVisible = true
			boundWritable = item.CanProposeRevision
		}
	}
	if !boundVisible || !boundWritable {
		t.Fatalf("http knowledge lost bound library semantics: visible=%v writable=%v", boundVisible, boundWritable)
	}
	response = agentRequest(t, h.server.URL+"/api/v1/agent/knowledge/entries", http.MethodPost, token, map[string]any{
		"mode": "revise", "scope": "project", "entry_id": bound.ID, "base_revision": bound.Revision,
		"type": "practice", "title": "Bound contribution", "summary": "The legacy HTTP binding path must keep working.",
		"body": "This contribution body is long enough to satisfy the knowledge contract.",
	})
	var httpBound struct {
		Source string `json:"source"`
	}
	decodeResponse(t, response, &httpBound)
	if (response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK) || httpBound.Source != "legacy" {
		t.Fatalf("http bound revise status=%d payload=%#v", response.StatusCode, httpBound)
	}
}

// TestAgentKnowledgeMCPGlobalReviseRejectedOnVault runs the P1 reproduction
// against a real exported vault. The rejected revise must leave the file and
// index revision untouched.
func TestAgentKnowledgeMCPGlobalReviseRejectedOnVault(t *testing.T) {
	unsetKnowledgeVaultReadProjects(t)
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-vault-global")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	current := seedKnowledgeForMCP(t, h, "project", task.ProjectID, "knowledge-vault-current",
		"Vault current practice", "Vault current body stays inside the owning project.")
	global := seedKnowledgeForMCP(t, h, "global", "", "knowledge-vault-global",
		"Vault global reference", "Vault global body must never be revised through this domain.")

	vaultConfig := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Export(h.ctx, vaultConfig, h.dbPath, knowledgevault.ExportOptions{Commit: false}); err != nil {
		t.Fatal(err)
	}
	index, err := knowledgevault.OpenIndex(h.ctx, vaultConfig.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(h.ctx, vaultConfig.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(vaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.SetEnabled(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	h.service.SetKnowledgeVault(manager, true)
	h.service.SetKnowledgeVaultRuntimeWrite(true)

	before := hashVaultTree(t, vaultConfig.VaultPath)
	got := coreMCPCallTool(t, url, token, "knowledge_get", map[string]any{"id": global.ID})
	if got.Status != http.StatusOK || got.IsError || !strings.Contains(got.Text, `"source":"vault"`) {
		t.Fatalf("vault global get payload=%#v", got)
	}
	revise := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "revise", "scope": "project", "entry_id": global.ID, "base_revision": global.Revision,
		"type": "practice", "title": "Vault global overwrite", "summary": "Global is read-only here.",
		"body": "This rewrite must not reach the vault global file or its index revision.",
	})
	if revise.Status != http.StatusOK || !revise.IsError ||
		!strings.Contains(revise.Text, "knowledge_scope_forbidden") {
		t.Fatalf("vault global revise payload=%#v", revise)
	}
	if after := hashVaultTree(t, vaultConfig.VaultPath); after != before {
		t.Fatalf("vault tree changed after rejected global revise: before=%s after=%s", before, after)
	}
	got = coreMCPCallTool(t, url, token, "knowledge_get", map[string]any{"id": global.ID})
	decoded := decodeKnowledgeMCPText(t, got.Text)
	entry, _ := decoded["knowledge"].(map[string]any)
	body, _ := entry["body"].(string)
	if entry["revision"] != float64(global.Revision) || !strings.Contains(body, global.Body) {
		t.Fatalf("vault global entry changed: %#v", entry)
	}

	// The current Project entry remains revisable on the same vault.
	reviseCurrent := coreMCPCallTool(t, url, token, "knowledge_submit", map[string]any{
		"mode": "revise", "scope": "project", "entry_id": current.ID, "base_revision": current.Revision,
		"type": "practice", "title": "Vault project revise", "summary": "A project entry stays writable.",
		"body": "This revised project body is long enough to satisfy the vault write contract.",
	})
	if reviseCurrent.Status != http.StatusOK || reviseCurrent.IsError ||
		!strings.Contains(reviseCurrent.Text, `"source":"vault"`) {
		t.Fatalf("vault project revise payload=%#v", reviseCurrent)
	}
}

func unsetKnowledgeVaultReadProjects(t *testing.T) {
	t.Helper()
	previous, existed := os.LookupEnv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
	if err := os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS", previous)
		} else {
			_ = os.Unsetenv("AHA2_KNOWLEDGE_VAULT_READ_PROJECTS")
		}
	})
}

func hashVaultTree(t *testing.T, root string) string {
	t.Helper()
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest.Write([]byte(relative))
		digest.Write([]byte{0})
		digest.Write(data)
		digest.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// TestAgentKnowledgeMCPRejectsChannelBoundTurns covers the P2 gate hole: both
// task_route and conversation_host Turns carry a channel batch, so initialize
// must be refused even when the route row is no longer active.
func TestAgentKnowledgeMCPRejectsChannelBoundTurns(t *testing.T) {
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	url := knowledgeMCPURL(h)

	assertDenied := func(label, token string) {
		t.Helper()
		response := agentRequest(t, url, http.MethodPost, token, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
		})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s initialize status=%d body=%s", label, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		if status, names, _ := coreMCPToolsList(t, url, token); status != http.StatusForbidden {
			t.Fatalf("%s tools/list status=%d names=%#v", label, status, names)
		}
		if call := coreMCPCallTool(t, url, token, "knowledge_list", map[string]any{}); call.Status != http.StatusForbidden {
			t.Fatalf("%s knowledge_list status=%d payload=%#v", label, call.Status, call)
		}
	}

	// Positive control: a batch that really contains only a plain message.
	controlTask := createCoreTaskInProject(t, h, base, "")
	control := createCoreTurn(t, h.database, controlTask, "main", "batch-knowledge-plain")
	seedKnowledgeMCPInbox(t, h.dbPath, controlTask, control, "plain")
	controlToken := h.issue(controlTask.ID, "main", control.ID)
	response := agentRequest(t, url, http.MethodPost, controlToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("plain Turn initialize status=%d body=%s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	if status, names, _ := coreMCPToolsList(t, url, controlToken); status != http.StatusOK || len(names) != len(app.KnowledgeMCPToolNames(true)) {
		t.Fatalf("plain Turn tools/list status=%d names=%#v", status, names)
	}
	if call := coreMCPCallTool(t, url, controlToken, "knowledge_list", map[string]any{}); call.Status != http.StatusOK || call.IsError {
		t.Fatalf("plain Turn knowledge_list payload=%#v", call)
	}

	// A batch that mixes a plain message with a channel message is
	// channel-bound regardless of insertion order. The aggregate context
	// lookup returns sql.ErrNoRows for both orders.
	orders := []struct {
		label string
		kinds []string
	}{
		{label: "plain then channel", kinds: []string{"plain", "channel"}},
		{label: "channel then plain", kinds: []string{"channel", "plain"}},
	}
	for name, mode := range map[string]string{
		"conversation host": "conversation_host",
		"task route":        "task_route",
	} {
		for _, order := range orders {
			task := createCoreTaskInProject(t, h, base, "")
			batchID := "batch-knowledge-" + strings.ReplaceAll(mode, "_", "-") + "-" +
				strings.ReplaceAll(order.label, " ", "-")
			turn := createCoreTurn(t, h.database, task, "main", batchID)
			kinds := make([]string, 0, len(order.kinds))
			for _, kind := range order.kinds {
				if kind == "channel" {
					kinds = append(kinds, "channel:"+mode)
					continue
				}
				kinds = append(kinds, kind)
			}
			seedKnowledgeMCPInbox(t, h.dbPath, task, turn, kinds...)
			assertDenied(name+" "+order.label, h.issue(task.ID, "main", turn.ID))
		}
	}

	// Third state: a batch the store cannot classify (malformed payload) must
	// fail closed instead of looking like a plain batch.
	brokenTask := createCoreTaskInProject(t, h, base, "")
	broken := createCoreTurn(t, h.database, brokenTask, "main", "batch-knowledge-malformed")
	seedKnowledgeMCPInbox(t, h.dbPath, brokenTask, broken, "raw:not-json")
	assertDenied("unclassifiable batch", h.issue(brokenTask.ID, "main", broken.ID))
}

// seedKnowledgeMCPInbox inserts one agent_inbox row per kind into the Turn's
// batch, preserving order. A kind is "plain", "channel:<route mode>", or
// "raw:<payload>" for a deliberately malformed row.
func seedKnowledgeMCPInbox(t *testing.T, dbPath string, task domain.Task, turn domain.Turn, kinds ...string) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, kind := range kinds {
		payload := `{}`
		sourceAgent, sourceKind := "owner", "owner_message"
		switch {
		case strings.HasPrefix(kind, "channel:"):
			mode := strings.TrimPrefix(kind, "channel:")
			encoded, err := json.Marshal(map[string]any{
				"channel_context": map[string]any{
					"schema":          "aha2.channel.v1",
					"instance_id":     "instance-mcp",
					"provider":        "test",
					"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
					"conversation_id": "conversation-mcp",
					"route":           map[string]any{"mode": mode},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			payload = string(encoded)
			sourceAgent, sourceKind = "channel", "channel_message"
		case strings.HasPrefix(kind, "raw:"):
			payload = strings.TrimPrefix(kind, "raw:")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := raw.ExecContext(context.Background(), `
			INSERT INTO agent_inbox(
				id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
				content,payload_json,status,batch_id,created_at,claimed_at,processed_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("inbox"), task.ID, turn.RoundID, "main", sourceAgent, sourceKind,
			"", "", "hello", payload, "claimed", turn.InboxBatchID, now, now, "",
		); err != nil {
			t.Fatal(err)
		}
	}
}

// buildKnowledgeMCPVault creates a real vault with the supplied files, rebuilds
// its index and returns an enabled manager.
func buildKnowledgeMCPVault(t *testing.T, files map[string]string) (*knowledgevault.Manager, knowledgevault.Config) {
	t.Helper()
	ctx := context.Background()
	config := knowledgevault.DefaultConfig(t.TempDir())
	if _, err := knowledgevault.Bootstrap(config, knowledgevault.InitOptions{VaultName: "MCP"}); err != nil {
		t.Fatal(err)
	}
	for relative, content := range files {
		target := filepath.Join(config.VaultPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index, err := knowledgevault.OpenIndex(ctx, config.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := knowledgevault.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, config
}

// TestAgentKnowledgeMCPSkillMetadataIsOpaque covers the P2 vault-key leak: a
// non-empty skill list must never return the package key, package files or the
// internal source path, and skill_get resolves only the derived identifier.
func TestAgentKnowledgeMCPSkillMetadataIsOpaque(t *testing.T) {
	unsetKnowledgeVaultReadProjects(t)
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-skills")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	manager, _ := buildKnowledgeMCPVault(t, map[string]string{
		"projects/mcp-skills/index.md":                            "---\nid: knowledge_root_project_mcp_skills\ntitle: MCP Skills\nscope: project\nproject_id: " + task.ProjectID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nProject entry.\n",
		"projects/mcp-skills/skills/review-demo/SKILL.md":         "---\nname: Review Demo\ndescription: Project review skill\n---\n\n# Review Demo Instructions\n\nUse the review checklist.\n",
		"projects/mcp-skills/skills/review-demo/scripts/check.sh": "#!/bin/sh\necho review\n",
		"global/skills/review-demo/SKILL.md":                      "---\nname: Global Review Demo\ndescription: Global review skill\n---\n\n# Global Review Instructions\n",
		"global/skills/review-demo/scripts/check.sh":              "#!/bin/sh\necho global\n",
	})
	h.service.SetKnowledgeVault(manager, true)
	projectKey := "vault-skill:project:" + task.ProjectID + ":review-demo"
	globalKey := "vault-skill:global:review-demo"
	if _, err := h.service.UpdateTaskSkills(h.ctx, task.ID, []string{projectKey, globalKey}); err != nil {
		t.Fatal(err)
	}

	list := coreMCPCallTool(t, url, token, "skill_list", map[string]any{})
	if list.Status != http.StatusOK || list.IsError {
		t.Fatalf("skill_list payload=%#v", list)
	}
	for _, leaked := range []string{"vault-skill:", "SourcePath", "source_path", "PackageFiles", "package_files", "scripts/"} {
		if strings.Contains(list.Text, leaked) {
			t.Fatalf("skill_list leaked %q: %s", leaked, list.Text)
		}
	}
	view := decodeKnowledgeMCPText(t, list.Text)
	skills, _ := view["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("skill_list skills=%#v", view["skills"])
	}
	ids := make([]string, 0, len(skills))
	for _, raw := range skills {
		skill, _ := raw.(map[string]any)
		id, _ := skill["id"].(string)
		if id == "" || !strings.HasPrefix(id, "skill-") || strings.Contains(id, "review-demo") {
			t.Fatalf("skill id is not opaque: %#v", skill)
		}
		ids = append(ids, id)
	}

	again := coreMCPCallTool(t, url, token, "skill_list", map[string]any{})
	againView := decodeKnowledgeMCPText(t, again.Text)
	againSkills, _ := againView["skills"].([]any)
	for index, raw := range againSkills {
		skill, _ := raw.(map[string]any)
		if skill["id"] != ids[index] {
			t.Fatalf("skill id is not stable: %#v vs %#v", ids, againSkills)
		}
	}

	got := coreMCPCallTool(t, url, token, "skill_get", map[string]any{"skill_id": ids[0]})
	if got.Status != http.StatusOK || got.IsError || !strings.Contains(got.Text, "Instructions") {
		t.Fatalf("skill_get payload=%#v", got)
	}
	for _, leaked := range []string{"vault-skill:", "SourcePath", "source_path", "scripts/"} {
		if strings.Contains(got.Text, leaked) {
			t.Fatalf("skill_get leaked %q: %s", leaked, got.Text)
		}
	}
	for _, rawKey := range []string{projectKey, globalKey} {
		if call := coreMCPCallTool(t, url, token, "skill_get", map[string]any{"skill_id": rawKey}); call.Status != http.StatusOK ||
			!call.IsError || !strings.Contains(call.Text, "skill_not_found") {
			t.Fatalf("raw vault key resolved a skill: %#v", call)
		}
	}
}

// TestAgentKnowledgeMCPGuideHidesInternalPaths covers the P2 path leak: a guide
// read failure must return a fixed message and keep the host path in the log.
func TestAgentKnowledgeMCPGuideHidesInternalPaths(t *testing.T) {
	unsetKnowledgeVaultReadProjects(t)
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-guide")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	manager, config := buildKnowledgeMCPVault(t, map[string]string{})
	guidePath := filepath.Join(config.VaultPath, knowledgevault.GuideRelativePath)
	if err := os.WriteFile(guidePath, []byte("# Guide\n\nReadable guide.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.service.SetKnowledgeVault(manager, true)

	control := coreMCPCallTool(t, url, token, "knowledge_guide", map[string]any{})
	if control.Status != http.StatusOK || control.IsError || !strings.Contains(control.Text, `"available":true`) {
		t.Fatalf("readable guide payload=%#v", control)
	}

	if err := os.Chmod(guidePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(guidePath, 0o644) })
	if _, err := os.ReadFile(guidePath); err == nil {
		t.Skip("running with privileges that ignore file permissions")
	}
	denied := coreMCPCallTool(t, url, token, "knowledge_guide", map[string]any{})
	if denied.Status != http.StatusOK || !denied.IsError || !strings.Contains(denied.Text, "guide_unavailable") {
		t.Fatalf("permission denied guide payload=%#v", denied)
	}
	for _, leaked := range []string{config.VaultPath, "/srv/", "/home/", guidePath} {
		if strings.Contains(denied.Text, leaked) {
			t.Fatalf("guide error leaked %q: %s", leaked, denied.Text)
		}
	}
}

func TestAgentKnowledgeMCPProjectsRealReviewState(t *testing.T) {
	h := newCoreMCPHarness(t)
	task := createHardwareAPITask(t, h.database)
	turn := createCoreTurn(t, h.database, task, "main", "batch-knowledge-review")
	token := h.issue(task.ID, "main", turn.ID)
	url := knowledgeMCPURL(h)

	now := time.Now().UTC()
	if _, err := h.database.UpdateKnowledgeReviewSettings(h.ctx, domain.KnowledgeReviewSettings{
		AutoApprove: true, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	createArgs := func(title string) map[string]any {
		return map[string]any{
			"mode": "create", "scope": "project", "type": "practice",
			"title": title, "summary": "A reusable review-state summary for this project.",
			"body":         "A review-state body that is long enough to satisfy the knowledge contract.",
			"search_query": title,
		}
	}

	auto := coreMCPCallTool(t, url, token, "knowledge_submit", createArgs("Auto approved practice"))
	if auto.Status != http.StatusOK || auto.IsError {
		t.Fatalf("auto review create payload=%#v", auto)
	}
	autoView := decodeKnowledgeMCPText(t, auto.Text)
	if autoView["proposal"] != nil || autoView["review_mode"] != "auto" {
		t.Fatalf("auto review projection=%#v", autoView)
	}
	autoEntry, _ := autoView["entry"].(map[string]any)
	if autoEntry["status"] != "verified" {
		t.Fatalf("auto review entry=%#v", autoEntry)
	}
	autoProposals, _ := autoView["proposals"].([]any)
	if len(autoProposals) != 1 {
		t.Fatalf("auto review proposals=%#v", autoView["proposals"])
	}
	autoProposal, _ := autoProposals[0].(map[string]any)
	autoStored, err := h.database.KnowledgeProposal(h.ctx, autoProposal["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if autoStored.Status != domain.KnowledgeProposalApproved || autoProposal["status"] != string(autoStored.Status) {
		t.Fatalf("auto review proposal response=%#v db=%#v", autoProposal, autoStored)
	}
	published, err := h.database.Knowledge(h.ctx, autoEntry["id"].(string))
	if err != nil || published.Status != domain.KnowledgeVerified {
		t.Fatalf("auto review published entry=%#v err=%v", published, err)
	}

	if _, err := h.database.UpdateKnowledgeReviewSettings(h.ctx, domain.KnowledgeReviewSettings{
		AutoApprove: false, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	manual := coreMCPCallTool(t, url, token, "knowledge_submit", createArgs("Manual pending practice"))
	if manual.Status != http.StatusOK || manual.IsError {
		t.Fatalf("manual review create payload=%#v", manual)
	}
	manualView := decodeKnowledgeMCPText(t, manual.Text)
	if manualView["proposal"] != true || manualView["review_mode"] != "manual" {
		t.Fatalf("manual review projection=%#v", manualView)
	}
	manualEntry, _ := manualView["entry"].(map[string]any)
	if manualEntry["status"] != string(domain.KnowledgeCandidate) {
		t.Fatalf("manual review entry=%#v", manualEntry)
	}
	manualProposals, _ := manualView["proposals"].([]any)
	if len(manualProposals) != 1 {
		t.Fatalf("manual review proposals=%#v", manualView["proposals"])
	}
	manualProposal, _ := manualProposals[0].(map[string]any)
	manualStored, err := h.database.KnowledgeProposal(h.ctx, manualProposal["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if manualStored.Status != domain.KnowledgeProposalPending || manualProposal["status"] != string(manualStored.Status) {
		t.Fatalf("manual review proposal response=%#v db=%#v", manualProposal, manualStored)
	}
}
