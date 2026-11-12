package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

type approvalResolveResult struct {
	status int
	body   map[string]any
	err    error
}

func postApprovalResolve(client *http.Client, url, csrf string) approvalResolveResult {
	body, _ := json.Marshal(map[string]any{"decision": "approve"})
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return approvalResolveResult{err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := client.Do(request)
	if err != nil {
		return approvalResolveResult{err: err}
	}
	defer response.Body.Close()
	payload := map[string]any{}
	data, _ := io.ReadAll(response.Body)
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	return approvalResolveResult{status: response.StatusCode, body: payload}
}

func waitHTTPApprovalFinal(t *testing.T, database *store.Store, id string) domain.PetApproval {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		item, err := database.PetApproval(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Status != "executing" {
			return item
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval %s did not finish executing", id)
	return domain.PetApproval{}
}

func TestHeavyPetApprovalHTTPReturnsAcceptedBeforeExecutionCompletes(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	defer appService.Shutdown(ctx)
	approval, err := appService.CreatePetApproval(ctx, app.PetApprovalInput{
		Kind: "delete_workspace", Title: "后台删除 Workspace", Summary: "验证接口快速返回",
		Risk: "high", Payload: `{"workspace_id":"missing"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A deliberately slow execution proves the endpoint does not wait for it.
	appService.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	started := time.Now()
	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", map[string]any{"decision": "approve"}, csrf)
	var payload struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	decodeResponse(t, response, &payload)
	elapsed := time.Since(started)
	if response.StatusCode != http.StatusAccepted || !payload.OK || payload.Status != "executing" {
		t.Fatalf("resolve=%d %#v, want 202 executing", response.StatusCode, payload)
	}
	if elapsed >= time.Second {
		t.Fatalf("resolve took %s, want the request accepted in under 1s", elapsed)
	}
	if final := waitHTTPApprovalFinal(t, database, approval.ID); final.Status != "approved" {
		t.Fatalf("background execution did not finish approved: %#v", final)
	}
}

func TestHeavyPetApprovalHTTPConcurrentResolveConflicts(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	defer appService.Shutdown(ctx)
	approval, err := appService.CreatePetApproval(ctx, app.PetApprovalInput{
		Kind: "delete_task", Title: "并发删除 Task", Summary: "验证只有一个点击被受理",
		Risk: "high", Payload: `{"task_id":"missing"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	appService.SetPetApprovalExecutionHook(func(ctx context.Context, item domain.PetApproval) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	url := server.URL + "/api/v1/pet/approvals/" + approval.ID + "/resolve"

	results := make([]approvalResolveResult, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index] = postApprovalResolve(client, url, csrf)
		}(index)
	}
	wait.Wait()
	accepted, conflicts := 0, 0
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("resolve request failed: %v", result.err)
		}
		switch result.status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("concurrent resolve returned %d %#v", result.status, result.body)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted=%d conflicts=%d, want one of each", accepted, conflicts)
	}
	if final := waitHTTPApprovalFinal(t, database, approval.ID); final.Status != "approved" {
		t.Fatalf("approval did not finish approved: %#v", final)
	}
}

func TestLightPetApprovalHTTPStaysSynchronous(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	defer appService.Shutdown(ctx)
	approval, err := appService.CreatePetApproval(ctx, app.PetApprovalInput{
		Kind: "owner_decision", Title: "轻审批", Summary: "保持同步处理", Risk: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", map[string]any{"decision": "approve"}, csrf)
	var payload struct {
		Approval struct {
			Status string `json:"status"`
		} `json:"approval"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Approval.Status != "approved" {
		t.Fatalf("light resolve=%d %#v, want an immediate approval", response.StatusCode, payload)
	}
}
