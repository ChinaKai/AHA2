package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestInboundReplyContextFetchesParentMessage(t *testing.T) {
	t.Parallel()
	requested := false
	client := lark.NewClient("app", "secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		body := `{"code":0,"msg":"success"}`
		switch request.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			body = `{"code":0,"msg":"success","tenant_access_token":"tenant-token","expire":7200}`
		case "/open-apis/im/v1/messages/om_parent":
			requested = true
			if request.URL.Query().Get("with_sender_name") != "true" {
				t.Errorf("with_sender_name=%q", request.URL.Query().Get("with_sender_name"))
			}
			body = `{"code":0,"msg":"success","data":{"items":[{"message_id":"om_parent","root_id":"om_root","thread_id":"omt_thread","msg_type":"text","sender":{"id":"ou_sender","sender_type":"user","sender_name":"张三"},"body":{"content":"{\"text\":\"原始需求和限制\"}"}}]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
	}}))

	result := inboundReplyContext(context.Background(), client, inboundReplyMessage("om_parent", "om_root"))
	if !requested {
		t.Fatal("parent message API was not requested")
	}
	if result["status"] != "available" || result["content"] != "原始需求和限制" || result["message_type"] != "text" ||
		result["parent_message_id"] != "om_parent" || result["root_message_id"] != "om_root" || result["thread_id"] != "omt_thread" {
		t.Fatalf("reply context=%#v", result)
	}
	sender, _ := result["sender"].(map[string]any)
	if sender["display_name"] != "张三" || sender["is_bot"] != false {
		t.Fatalf("sender=%#v", sender)
	}
}

func TestInboundReplyContextKeepsMessageWhenParentIsForbidden(t *testing.T) {
	t.Parallel()
	client := lark.NewClient("app", "secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		body := `{"code":0,"msg":"success"}`
		switch request.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			body = `{"code":0,"msg":"success","tenant_access_token":"tenant-token","expire":7200}`
		case "/open-apis/im/v1/messages/om_parent":
			body = `{"code":230027,"msg":"provider-secret-detail"}`
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
	}}))

	result := inboundReplyContext(context.Background(), client, inboundReplyMessage("om_parent", "om_root"))
	if result["status"] != "unavailable" || result["error_code"] != "reply_parent_provider_230027" {
		t.Fatalf("reply context=%#v", result)
	}
	if _, exposed := result["content"]; exposed {
		t.Fatalf("unavailable context exposed content: %#v", result)
	}
	if strings.Contains(toJSONForTest(result), "provider-secret-detail") {
		t.Fatalf("provider detail leaked: %#v", result)
	}
}

func TestInboundReplyContextSkipsMessagesWithoutParent(t *testing.T) {
	t.Parallel()
	if result := inboundReplyContext(context.Background(), lark.NewClient("app", "secret"), inboundMessageWithoutParent()); result != nil {
		t.Fatalf("reply context=%#v", result)
	}
}

func TestNormalizedReplyContentRedactsResourceKeys(t *testing.T) {
	t.Parallel()
	content, resources := normalizedReplyContent("file", `{"file_key":"file_key_secret","file_name":"report.pdf"}`)
	if strings.Contains(content, "file_key_secret") || content != "[文件附件]" {
		t.Fatalf("content=%q", content)
	}
	if len(resources) != 1 || resources[0]["type"] != "file" || resources[0]["file_name"] != "report.pdf" {
		t.Fatalf("resources=%#v", resources)
	}
}

func inboundReplyMessage(parentID, rootID string) *channeltypes.NormalizedMessage {
	message := &larkim.EventMessage{ParentId: &parentID, RootId: &rootID}
	return &channeltypes.NormalizedMessage{RawEvent: &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: message}}}
}

func inboundMessageWithoutParent() *channeltypes.NormalizedMessage {
	return &channeltypes.NormalizedMessage{RawEvent: &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{Message: &larkim.EventMessage{}}}}
}

func toJSONForTest(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
