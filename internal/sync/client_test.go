package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestClientInjectsCredentialAndUsesProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Errorf("authorization not injected")
		}
		if r.Header.Get("X-Device-ID") != "device-a" {
			t.Errorf("device identity not injected")
		}
		if r.URL.Path != "/v1/sync/push" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var request PushRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(PushResponse{AckedKeys: []string{request.Objects[0].IdempotencyKey}})
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, DeviceID: "device-a", Credential: func(context.Context) (string, error) { return "runtime-token", nil }}
	response, err := client.Push(context.Background(), PushRequest{Scope: "default", Objects: []domain.SyncObject{{Type: "note", ID: "1", IdempotencyKey: "key"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.AckedKeys) != 1 {
		t.Fatalf("response=%#v", response)
	}
}
