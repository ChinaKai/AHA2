package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPetMCPNotificationsAreAcceptedWithoutJSON(t *testing.T) {
	server := New(Config{})
	for _, method := range []string{
		"notifications/initialized",
		"notifications/cancelled",
		"notifications/progress",
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/pet/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","method":"`+method+`"}`))
		request.RemoteAddr = "127.0.0.1:12345"
		recorder := httptest.NewRecorder()
		server.petMCP(recorder, request)
		if recorder.Code != http.StatusAccepted || recorder.Body.Len() != 0 {
			t.Fatalf("%s: status=%d body=%q", method, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPetMCPRequestLimitCoversBase64Attachment(t *testing.T) {
	t.Parallel()
	// base64 expands 3 bytes to 4; the request limit must leave room for the
	// encoded 200MiB storage cap plus JSON overhead.
	if petMCPMaxRequestBytes < maxAttachmentBytes*4/3 {
		t.Fatalf("petMCPMaxRequestBytes = %d, want at least %d", petMCPMaxRequestBytes, maxAttachmentBytes*4/3)
	}
}

func TestPetMCPPingReturnsResult(t *testing.T) {
	server := New(Config{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pet/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"ping"}`))
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	server.petMCP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"result"`) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
