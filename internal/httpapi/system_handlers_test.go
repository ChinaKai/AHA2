package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSystemInfoIncludesVersionAndStartedAt(t *testing.T) {
	started := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	server := New(Config{Version: "v0.5.1-test", StartedAt: started})
	recorder := httptest.NewRecorder()
	server.systemInfo(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/system", nil))
	var payload struct {
		System struct {
			Version   string    `json:"version"`
			StartedAt time.Time `json:"started_at"`
		} `json:"system"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || payload.System.Version != "v0.5.1-test" || !payload.System.StartedAt.Equal(started) {
		t.Fatalf("status=%d system=%#v", recorder.Code, payload.System)
	}
}
