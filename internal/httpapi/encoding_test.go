package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestRepairTextEncodingConvertsGBKJSONBody(t *testing.T) {
	const goal = "修复权限面板并回到群聊"
	payload, err := simplifiedchinese.GBK.NewEncoder().String(`{"current_goal":"` + goal + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var decoded string
	handler := server.repairTextEncoding(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			CurrentGoal string `json:"current_goal"`
		}
		if err := decodeJSON(request, &body); err != nil {
			t.Errorf("decode repaired body: %v", err)
			return
		}
		decoded = body.CurrentGoal
	}))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/turn/memory", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if decoded != goal {
		t.Fatalf("decoded goal=%q want %q", decoded, goal)
	}
}

func TestRepairTextEncodingLeavesUTF8AndBinaryAlone(t *testing.T) {
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	readBody := func(body []byte, contentType string) string {
		handler := server.repairTextEncoding(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			got, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			writer.Write(got)
		}))
		request := httptest.NewRequest(http.MethodPost, "/api/v1/example", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Body.String()
	}

	utf8Body := `{"current_goal":"已经正常"}`
	if got := readBody([]byte(utf8Body), "application/json"); got != utf8Body {
		t.Fatalf("valid UTF-8 body changed: %q", got)
	}
	binary := []byte{0xff, 0x00, 0xfe, 0x01}
	if got := readBody(binary, "application/octet-stream"); got != string(binary) {
		t.Fatalf("binary body changed: %v", []byte(got))
	}
	latin1 := []byte(`{"note":"caf` + "\xe9" + `"}`)
	if got := readBody(latin1, "application/json"); got != string(latin1) {
		t.Fatalf("non-Chinese invalid body changed: %v", []byte(got))
	}
}
