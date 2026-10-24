package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestAgentLiveInboxClaimAckHTTP(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "start", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            "batch-live-http", Attempt: 1, Required: true, QueuedAt: now,
	}
	if _, _, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn); err != nil {
		t.Fatal(err)
	}
	if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: round.ID,
		TargetAgentID: "main", SourceAgentID: "owner", SourceKind: "owner_message",
		Content: "please also check the logs", Status: "pending", CreatedAt: now,
		Payload: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}

	service := app.NewService(database, nil, app.StubExecutor{})
	capabilities := agentapi.NewCapabilities()
	token, err := capabilities.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store: database, App: service, AgentCapabilities: capabilities,
	}).Handler())
	defer server.Close()

	response := agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, token, map[string]any{"limit": 10})
	var claim struct {
		Messages      []map[string]any `json:"messages"`
		InjectionText string           `json:"injection_text"`
	}
	decodeResponse(t, response, &claim)
	if response.StatusCode != http.StatusOK || len(claim.Messages) != 1 {
		t.Fatalf("claim status=%d payload=%#v", response.StatusCode, claim)
	}
	if !strings.Contains(claim.InjectionText, "[AHA live inbox]") ||
		!strings.Contains(claim.InjectionText, "please also check the logs") ||
		!strings.Contains(claim.InjectionText, "authority=owner") {
		t.Fatalf("injection text missing context: %q", claim.InjectionText)
	}
	messageID, _ := claim.Messages[0]["id"].(string)
	if messageID == "" {
		t.Fatalf("claimed message has no id: %#v", claim.Messages[0])
	}

	// A claim is exclusive: neither a second claim nor the model-driven wait
	// endpoint may deliver the same message again.
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, token, map[string]any{"limit": 10})
	var secondClaim struct {
		Messages []map[string]any `json:"messages"`
	}
	decodeResponse(t, response, &secondClaim)
	if response.StatusCode != http.StatusOK || len(secondClaim.Messages) != 0 {
		t.Fatalf("second claim status=%d payload=%#v", response.StatusCode, secondClaim)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/wait", http.MethodPost, token, map[string]any{
		"after_cursor": 0, "timeout_seconds": 1,
	})
	var wait struct {
		Messages []map[string]any `json:"messages"`
	}
	decodeResponse(t, response, &wait)
	if response.StatusCode != http.StatusOK || len(wait.Messages) != 0 {
		t.Fatalf("wait after claim status=%d payload=%#v", response.StatusCode, wait)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/ack", http.MethodPost, token, map[string]any{
		"message_ids": []string{messageID}, "outcome": "delivered",
	})
	var ack struct {
		Acknowledged int `json:"acknowledged"`
	}
	decodeResponse(t, response, &ack)
	if response.StatusCode != http.StatusOK || ack.Acknowledged != 1 {
		t.Fatalf("ack delivered status=%d payload=%#v", response.StatusCode, ack)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/ack", http.MethodPost, token, map[string]any{
		"message_ids": []string{messageID}, "outcome": "delivered",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("idempotent ack status=%d", response.StatusCode)
	}
	response.Body.Close()

	if _, err := database.EnqueueAgentRoute(ctx, domain.AgentInboxItem{
		ID: domain.NewID("inbox"), TaskID: task.ID, RoundID: round.ID,
		TargetAgentID: "main", SourceAgentID: "owner", SourceKind: "owner_message",
		Content: "failed injection", Status: "pending", CreatedAt: now.Add(time.Second),
		Payload: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, token, map[string]any{"limit": 10})
	var retryClaim struct {
		Messages []map[string]any `json:"messages"`
	}
	decodeResponse(t, response, &retryClaim)
	if response.StatusCode != http.StatusOK || len(retryClaim.Messages) != 1 {
		t.Fatalf("retry claim status=%d payload=%#v", response.StatusCode, retryClaim)
	}
	retryID, _ := retryClaim.Messages[0]["id"].(string)
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/ack", http.MethodPost, token, map[string]any{
		"message_ids": []string{retryID}, "outcome": "failed",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("nack status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, token, map[string]any{"limit": 10})
	var reClaim struct {
		Messages []map[string]any `json:"messages"`
	}
	decodeResponse(t, response, &reClaim)
	if response.StatusCode != http.StatusOK || len(reClaim.Messages) != 1 || reClaim.Messages[0]["id"] != retryID {
		t.Fatalf("rolled back message was not reclaimable: %d %#v", response.StatusCode, reClaim)
	}

	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/ack", http.MethodPost, token, map[string]any{
		"message_ids": []string{retryID}, "outcome": "unknown",
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid ack outcome status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, token, map[string]any{"limit": 51})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized claim limit status=%d", response.StatusCode)
	}
	response.Body.Close()

	otherToken, err := capabilities.Issue(task.ID, "main", "turn-other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(t, server.URL+"/api/v1/agent/turn/inbox/claim", http.MethodPost, otherToken, map[string]any{"limit": 10})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("other-turn claim status=%d", response.StatusCode)
	}
	response.Body.Close()
}
