package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/coder/websocket"
)

func TestStoredLoginEchoCannotReachHistoryHTTPMCPOrWebSocket(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, true)
	result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
	if result.Status != 200 || result.IsError {
		t.Fatal("fake connection failed")
	}
	peer := f.connection(t)
	var writes sync.Mutex
	write := func(data []byte) {
		writes.Lock()
		defer writes.Unlock()
		_ = peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = peer.Write(data)
	}
	var sends atomic.Int32
	go func() {
		reader := bufio.NewReader(peer)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == f.secret {
				sends.Add(1)
				cut := len(f.secret) / 2
				write([]byte("echo:" + f.secret[:cut]))
				write([]byte(f.secret[cut:] + "\n$ \n"))
			}
		}
	}()
	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "timeout_seconds": 3, "retries": 3})
	if result.Status != 200 || result.IsError {
		t.Fatal("legitimate login failed")
	}
	write([]byte("password:"))
	deadline := time.Now().Add(5 * time.Second)
	for f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "authenticated" {
		if time.Now().After(deadline) {
			t.Fatal("fake login did not authenticate")
		}
		time.Sleep(time.Millisecond)
	}
	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "wakeup": true, "retries": 3})
	if result.Status != 200 || result.IsError {
		t.Fatal("authenticated idempotent login failed")
	}
	write([]byte("password:\nAFTER-AUTH-MARKER\n"))
	var joined string
	for {
		page, err := f.h.database.HardwareIOPage(f.h.ctx, f.task.ID, "board", "network", 0, 2000)
		if err != nil {
			t.Fatal(err)
		}
		var data strings.Builder
		for _, event := range page.Items {
			data.WriteString(event.Data)
		}
		joined = data.String()
		if strings.Contains(joined, f.secret) {
			t.Fatal("saved credential persisted in history")
		}
		if strings.Contains(joined, "AFTER-AUTH-MARKER") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake terminal marker not recorded")
		}
		time.Sleep(time.Millisecond)
	}
	if sends.Load() != 1 || !strings.Contains(joined, "••••") {
		t.Fatal("credential replay was sent or echo was not masked")
	}
	root := filepath.Join(f.h.database.DataDir(), "hardware-io")
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(f.secret)) {
			t.Fatal("credential exists in persisted terminal file")
		}
		return nil
	}); err != nil {
		t.Fatal("could not inspect isolated terminal persistence")
	}
	response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware/board/terminal?transport=network", http.MethodGet, f.token, nil)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || bytes.Contains(data, []byte(f.secret)) {
		t.Fatal("Agent HTTP terminal exposed credential")
	}
	result = f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network"})
	if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
		t.Fatal("MCP terminal exposed credential")
	}
	jar, _ := cookiejar.New(nil)
	owner := &http.Client{Jar: jar}
	_ = registerOwner(t, owner, f.server.URL)
	parsed, _ := url.Parse(f.server.URL)
	header := http.Header{}
	for _, cookie := range jar.Cookies(parsed) {
		header.Add("Cookie", cookie.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/tasks/"+f.task.ID+"/hardware/board/terminal/ws?transport=network", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal("owner terminal WebSocket fixture failed")
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	write([]byte("LIVE-ECHO:" + f.secret[:len(f.secret)/2]))
	write([]byte(f.secret[len(f.secret)/2:] + "\nWS-LIVE-SAFE-MARKER\n"))
	var stream bytes.Buffer
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			t.Fatal("terminal replay/live marker did not arrive")
		}
		if bytes.Contains(data, []byte(f.secret)) {
			t.Fatal("WebSocket frame exposed credential")
		}
		stream.Write(data)
		if bytes.Contains(stream.Bytes(), []byte("WS-LIVE-SAFE-MARKER")) {
			break
		}
	}
	if bytes.Contains(stream.Bytes(), []byte(f.secret)) {
		t.Fatal("credential spans WebSocket frames")
	}
	result = f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network"})
	if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
		t.Fatal("live echo reached retained MCP history")
	}

}

func TestLoginAndSendFailuresHaveFixedDistinctStateCodes(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	for _, operation := range []string{"hardware_login", "hardware_send"} {
		args := map[string]any{"hardware_id": "board"}
		if operation == "hardware_send" {
			args["data"] = "fixture"
		}
		result := f.mcp(t, operation, args)
		if !result.IsError || !strings.Contains(result.Text, "hardware_not_connected") {
			t.Fatal("unconnected operation lost its fixed classification")
		}
	}
	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.IsError {
		t.Fatal("fake connection failed")
	}
	_ = f.connection(t)
	result := f.mcp(t, "hardware_send", map[string]any{"hardware_id": "board", "data": "!invalid!", "encoding": "base64"})
	if !result.IsError || !strings.Contains(result.Text, "hardware_encoding_invalid") {
		t.Fatal("invalid input was confused with connection state")
	}
	for _, args := range []map[string]any{{"hardware_id": "board", "timeout_seconds": 1}, {"hardware_id": "board", "retries": 5}} {
		result := f.mcp(t, "hardware_login", args)
		if !result.IsError {
			t.Fatal("schema accepted login options outside manager semantics")
		}
	}
	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "timeout_seconds": 3, "retries": 3})
	if result.IsError {
		t.Fatal("valid bounded login options rejected")
	}
	result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "cancel": true})
	if result.IsError || f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "cancelled" {
		t.Fatal("explicit login cancellation did not disarm")
	}
}

// Retained records written before stream sanitization (and stored credentials
// typed as single keystrokes) are read through every history surface with
// cursors and limits that split the credential. The fixture text contains no
// lowercase hex, so any such byte in returned terminal data is a leak.
func TestRetainedCredentialFragmentsStayMaskedAcrossPagedHistorySurfaces(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, true)
	leaked := func(value string) bool { return strings.ContainsAny(value, "0123456789abcdef") }
	appendEvent := func(direction, data string) int64 {
		sequence, err := f.h.database.AppendHardwareIO(f.h.ctx, domain.HardwareIOEvent{ID: domain.NewID("hardware_io"), TaskID: f.task.ID, HardwareID: "board", Transport: "network", Direction: direction, Data: data, Encoding: "text", Source: "runtime", CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return sequence
	}
	// after=0 means "newest page", so paging starts at an earlier event.
	start := appendEvent("system", "BOOT")
	first := appendEvent("rx", "PRE:"+f.secret[:16])
	appendEvent("rx", f.secret[16:32])
	appendEvent("rx", f.secret[32:]+"\nPOST\n")
	for index := range f.secret {
		appendEvent("tx", f.secret[index:index+1])
	}
	type item struct {
		Sequence  int64  `json:"sequence"`
		Direction string `json:"direction"`
		Data      string `json:"data"`
	}
	jar, _ := cookiejar.New(nil)
	owner := &http.Client{Jar: jar}
	_ = registerOwner(t, owner, f.server.URL)
	readers := map[string]func(after int64) ([]item, bool){
		"agent_http": func(after int64) ([]item, bool) {
			response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware/board/terminal?transport=network&limit=1&after="+strconv.FormatInt(after, 10), http.MethodGet, f.token, nil)
			defer response.Body.Close()
			var body struct {
				Stream struct {
					Items   []item `json:"items"`
					HasMore bool   `json:"has_more"`
				} `json:"stream"`
			}
			if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&body) != nil {
				t.Fatal("Agent HTTP history read failed")
			}
			return body.Stream.Items, body.Stream.HasMore
		},
		"owner_http": func(after int64) ([]item, bool) {
			response, err := owner.Get(f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware/board/terminal?transport=network&limit=1&after=" + strconv.FormatInt(after, 10))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body struct {
				Stream struct {
					Items   []item `json:"items"`
					HasMore bool   `json:"has_more"`
				} `json:"stream"`
			}
			if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&body) != nil {
				t.Fatal("owner HTTP history read failed")
			}
			return body.Stream.Items, body.Stream.HasMore
		},
		"mcp": func(after int64) ([]item, bool) {
			result := f.mcp(t, "hardware_terminal_read", map[string]any{"hardware_id": "board", "transport": "network", "after": after, "limit": 1})
			var body struct {
				Items     []item `json:"items"`
				Truncated bool   `json:"truncated"`
			}
			if result.Status != 200 || result.IsError || json.Unmarshal([]byte(result.Text), &body) != nil {
				t.Fatal("MCP history read failed")
			}
			return body.Items, body.Truncated
		},
	}
	for name, read := range readers {
		var rx, tx strings.Builder
		after, pages := int64(0), 0
		for after = start; ; pages++ {
			if pages > 80 {
				t.Fatal(name + " paging did not finish")
			}
			items, more := read(after)
			for _, event := range items {
				if event.Direction == "rx" {
					rx.WriteString(event.Data)
				} else {
					tx.WriteString(event.Data)
				}
				after = event.Sequence
			}
			if !more || len(items) == 0 {
				break
			}
		}
		if leaked(rx.String()) || leaked(tx.String()) {
			t.Fatalf("%s paged history revealed part of a retained credential (pages=%d)", name, pages)
		}
		if !strings.HasPrefix(rx.String(), "PRE:") || !strings.Contains(rx.String(), "\nPOST\n") || !strings.Contains(tx.String(), "••••") {
			t.Fatalf("%s paged history lost ordinary output (pages=%d rx=%q tx=%q)", name, pages, rx.String(), tx.String())
		}
	}
	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.Status != 200 || result.IsError {
		t.Fatal("fake connection failed")
	}
	_ = f.connection(t)
	parsed, _ := url.Parse(f.server.URL)
	header := http.Header{}
	for _, cookie := range jar.Cookies(parsed) {
		header.Add("Cookie", cookie.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Replay starts after the first fragment, so it opens mid-credential.
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/tasks/"+f.task.ID+"/hardware/board/terminal/ws?transport=network&after="+strconv.FormatInt(first, 10), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal("owner terminal WebSocket fixture failed")
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	var replay bytes.Buffer
	for !bytes.Contains(replay.Bytes(), []byte("\nPOST\n")) {
		kind, data, err := connection.Read(ctx)
		if err != nil {
			t.Fatal("WebSocket replay did not arrive")
		}
		if kind == websocket.MessageBinary {
			replay.Write(data)
		}
	}
	if leaked(replay.String()) {
		t.Fatal("WebSocket replay opened with a retained credential tail")
	}
}
