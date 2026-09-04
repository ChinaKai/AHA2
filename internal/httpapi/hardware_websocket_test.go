package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestHardwareTerminalWebSocketStreamsRawBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	hardwareManager := hardware.NewManager(database)
	defer hardwareManager.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService,
		Secrets: &fakeSecretStore{}, Hardware: hardwareManager,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "board", "mode": "network", "access": "read_write",
			"network": map[string]any{"host": host, "port": port, "protocol": "raw"},
		}},
	}, csrf)
	response.Body.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/hardware/board/connect?transport=network", nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("connect status=%d", response.StatusCode)
	}
	response.Body.Close()
	remote := <-accepted
	defer remote.Close()
	if _, err := remote.Write([]byte("\x1b[31mold\x1b[0m\r\n")); err != nil {
		t.Fatal(err)
	}
	var afterSequence int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page, pageErr := database.HardwareIOPage(ctx, task.ID, "board", "network", 0, 20)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		for _, item := range page.Items {
			if item.Direction == "rx" && strings.Contains(item.Data, "old") {
				afterSequence = item.Sequence
				break
			}
		}
		if afterSequence > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if afterSequence == 0 {
		t.Fatal("historical output was not recorded")
	}
	baseURL, _ := url.Parse(server.URL)
	header := http.Header{}
	for _, cookie := range jar.Cookies(baseURL) {
		header.Add("Cookie", cookie.String())
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		fmt.Sprintf("/api/v1/tasks/%s/hardware/board/terminal/ws?transport=network&after=%d", task.ID, afterSequence)
	dialContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(dialContext, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	messageType, readyData, err := connection.Read(dialContext)
	if err != nil || messageType != websocket.MessageText {
		t.Fatalf("ready: %v %v", messageType, err)
	}
	var ready map[string]any
	if err := json.Unmarshal(readyData, &ready); err != nil || ready["type"] != "ready" {
		t.Fatalf("ready payload: %s %v", readyData, err)
	}
	go remote.Write([]byte("\x1b[32mready\x1b[0m\r\n"))
	messageType, output, err := connection.Read(dialContext)
	if err != nil || messageType != websocket.MessageBinary || string(output) != "\x1b[32mready\x1b[0m\r\n" {
		t.Fatalf("output: %q %v %v", output, messageType, err)
	}
	read := make(chan string, 1)
	go func() {
		buffer := make([]byte, 16)
		count, _ := remote.Read(buffer)
		read <- string(buffer[:count])
	}()
	if err := connection.Write(dialContext, websocket.MessageBinary, []byte("\x03")); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-read:
		if value != "\x03" {
			t.Fatalf("raw input=%q", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("raw input timed out")
	}
}
