package app

import (
	"database/sql"
	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/domain"
	"testing"
	"time"
)

func TestPetChannelBatchExcludesEverySharedDomainAfterRealInboxClaim(t *testing.T) {
	f := newPetTaskFixture(t)
	task := f.createPetDraft(t, "channel scope fixture")
	assistant, err := f.database.EnsureOwnerAssistant(f.ctx, f.createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.UpdateOwnerPetPermissions(f.ctx, assistant.ID, true, true, false, false, false, true, false, false, f.createdAt); err != nil {
		t.Fatal(err)
	}

	f.service.SetDesktopMCPAvailability(func(string) bool { return true })
	seedHardwareMCPGroup(t, f.ctx, f.database, task)
	seedBrowserMCPGrant(t, f.ctx, f.database, task, "pet-channel-fixture", "active")
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner", Content: "fixture channel input", CreatedAt: time.Now()}
	round, inbox, _, err := f.database.EnqueueOwnerMessage(f.ctx, message, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	payload := `{"channel_context":{"schema":"aha2.channel.v1","instance_id":"fixture","endpoint":"group_digital_human","conversation_id":"fixture","route":{"mode":"task_route"}}}`
	if _, err := raw.Exec(`UPDATE agent_inbox SET payload_json=?,source_agent_id='channel',source_kind='channel_message' WHERE id=?`, payload, inbox.ID); err != nil {
		t.Fatal(err)
	}
	items, err := f.database.PendingAgentInbox(f.ctx, task.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	turn := domain.Turn{ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main", InputMessageID: message.ID, RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID, Status: domain.TurnRunning, InboxBatchID: domain.NewID("inbox_batch"), QueuedAt: time.Now()}
	turn, claimed, err := f.database.ClaimInboxAndCreateTurn(f.ctx, turn, items, func([]domain.AgentInboxItem) (domain.Turn, error) { return turn, nil })
	if err != nil || turn.InboxBatchID == "" || len(claimed) != 1 {
		t.Fatalf("real Inbox claim failed: %v", err)
	}
	stored, err := f.database.Turn(f.ctx, turn.ID)
	if err != nil || stored.InboxBatchID != turn.InboxBatchID {
		t.Fatal("Inbox batch identity was lost")
	}
	servers, _ := f.service.mcpRuntimeForTurn(f.ctx, task, "main", "http://127.0.0.1:8766", stored.InboxBatchID)
	for _, name := range []string{knowledgeMCPName, workspaceMCPName, browserMCPName, hardwareMCPName, desktopMCPName, collabMCPName} {
		if hasMCPServer(servers, name) {
			t.Fatalf("pet channel Turn received %s", name)
		}
	}
	claims := agentapi.Claims{TaskID: task.ID, AgentID: "main", TurnID: turn.ID}
	call, err := f.service.AgentCallContext(f.ctx, claims, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.service.AgentKnowledgeMCPAllowed(f.ctx, call) {
		t.Fatal("pet channel Turn can call knowledge domain")
	}
	for _, domainName := range []string{"workspace", "browser", "hardware", "desktop"} {
		for _, write := range []bool{false, true} {
			if _, err := f.service.AgentDomainCallContext(f.ctx, claims, domainName, write); err == nil {
				t.Fatalf("pet channel Turn can call %s write=%v", domainName, write)
			}
		}
	}
}
