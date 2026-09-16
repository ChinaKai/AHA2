package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestNormalizedInboundImagesUseAttachmentsNotResourceURLs(t *testing.T) {
	for _, scenario := range []struct {
		name, kind, raw, want string
		keys                  []string
	}{
		{"image", "image", `{"image_key":"img_v3_test"}`, "[图片附件]", []string{"img_v3_test"}},
		{"post", "post", `{"zh_cn":{"content":[[{"tag":"text","text":"查看图片"},{"tag":"img","image_key":"img_first"},{"tag":"img","image_key":"img_second"}]]}}`, "查看图片[图片附件][图片附件]", []string{"img_first", "img_second"}},
		{"markdown image", "post", `{"zh_cn":{"content_v2":[[{"tag":"md","text":"看这张 ![示意图](img_diagram)"}]]}}`, "看这张 [图片附件]", []string{"img_diagram"}},
		{"ordinary URL", "text", `{"text":"![diagram](https://example.com/image.png)"}`, "![diagram](https://example.com/image.png)", nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			content, source := normalize.ParseContent(scenario.kind, scenario.raw)
			got, resources := normalizedInboundMedia(&channeltypes.NormalizedMessage{Content: content, Resources: source})
			if strings.TrimSpace(got) != scenario.want || len(resources) != len(scenario.keys) {
				t.Fatalf("content=%q resources=%#v", got, resources)
			}
			for index, key := range scenario.keys {
				if strings.Contains(got, key) || resources[index]["file_key"] != key || resources[index]["type"] != "image" {
					t.Fatalf("resource key must remain only in attachment metadata: content=%q resources=%#v", got, resources)
				}
			}
		})
	}
}

func TestNormalizedInboundDirectPostUsesRawEventResources(t *testing.T) {
	messageType := "post"
	content := `{"title":"","content":[[{"tag":"text","text":"引用图片"},{"tag":"img","image_key":"img_direct"}]]}`
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: &messageType,
				Content:     &content,
			},
		},
	}
	message := normalize.ParseMessage(event)
	if message.Content != "[rich text message]" || len(message.Resources) != 0 {
		t.Fatalf("fixture no longer reproduces SDK direct-post gap: %#v", message)
	}
	got, resources := normalizedInboundMedia(message)
	if got != "引用图片[图片附件]" || len(resources) != 1 ||
		resources[0]["type"] != "image" || resources[0]["file_key"] != "img_direct" {
		t.Fatalf("content=%q resources=%#v", got, resources)
	}
}

func TestNormalizedInboundMentionsSeparateSenderVisibleText(t *testing.T) {
	message := &channeltypes.NormalizedMessage{
		Content: "@_user_1 请和 @_user_2 联调",
		Mentions: []channeltypes.Mention{
			{Key: "@_user_1", OpenID: "bot-open", Name: "机器人", IsBot: true},
			{Key: "@_user_2", OpenID: "app-user", Name: "APP 张三"},
		},
	}
	content, _ := normalizedInboundMedia(message)
	mentions := normalizedInboundMentions(message)
	if content != "请和 @APP 张三 联调" {
		t.Fatalf("content=%q", content)
	}
	if len(mentions) != 2 || mentions[1]["external_user_id"] != "app-user" || mentions[1]["display_name"] != "APP 张三" {
		t.Fatalf("mentions=%#v", mentions)
	}
	filtered := normalizedInboundMentions(message, "bot-open")
	if len(filtered) != 1 || filtered[0]["external_user_id"] != "app-user" {
		t.Fatalf("self bot mention was not filtered: %#v", filtered)
	}
}

func TestRestoreInboundMentionTypesUsesRawMentionedType(t *testing.T) {
	messageType := "text"
	content := `{"text":"@_user_1 @_user_2 测试"}`
	selfKey, otherKey := "@_user_1", "@_user_2"
	selfID, otherID := "self-bot-open", "other-bot-open"
	selfName, otherName := "HOME-AHA", "WORK-AHA"
	botType := "bot"
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: &messageType,
				Content:     &content,
				Mentions: []*larkim.MentionEvent{
					{Key: &selfKey, Id: &larkim.UserId{OpenId: &selfID}, MentionedType: &botType, Name: &selfName},
					{Key: &otherKey, Id: &larkim.UserId{OpenId: &otherID}, MentionedType: &botType, Name: &otherName},
				},
			},
		},
	}
	message := normalize.ParseMessage(event)
	if message == nil || message.Mentions[0].IsBot || message.Mentions[1].IsBot {
		t.Fatalf("unexpected SDK mention classification: %#v", message)
	}

	restoreInboundMentionTypes(message)
	normalizedContent, _ := normalizedInboundMedia(message)
	mentions := normalizedInboundMentions(message, selfID)
	if normalizedContent != "测试" || len(mentions) != 1 ||
		mentions[0]["external_user_id"] != otherID || mentions[0]["is_bot"] != true {
		t.Fatalf("content=%q mentions=%#v", normalizedContent, mentions)
	}
}

func TestDeliveryMentionsUseProviderMarkup(t *testing.T) {
	msgType, content := applyDeliveryMentions("text", `{"text":"请测试"}`, map[string]string{
		"mention_user_ids": `["user-one","user-two"]`,
		"mention_names":    `["张三","李四"]`,
	})
	var payload map[string]string
	if msgType != "text" || json.Unmarshal([]byte(content), &payload) != nil ||
		!strings.Contains(payload["text"], `<at user_id="user-one">张三</at>`) ||
		!strings.Contains(payload["text"], `<at user_id="user-two">李四</at>`) {
		t.Fatalf("content=%s", content)
	}
}

func TestDeliveryMentionsUseTextForBotOutreach(t *testing.T) {
	msgType, content := applyDeliveryMentions("text", `{"text":"请处理"}`, map[string]string{
		"mention_user_ids": `["ou-bot"]`,
		"mention_names":    `["目标机器人"]`,
	})
	var payload map[string]string
	if msgType != "text" || json.Unmarshal([]byte(content), &payload) != nil ||
		!strings.Contains(payload["text"], `<at user_id="ou-bot">目标机器人</at>`) ||
		!strings.Contains(payload["text"], "请处理") {
		t.Fatalf("text content type=%s content=%s", msgType, content)
	}
}

func mediaTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestMediaDownloadBodyHasHardSizeLimit(t *testing.T) {
	body := &limitedMediaBody{ReadCloser: io.NopCloser(strings.NewReader("123456")), remaining: 5}
	if content, err := io.ReadAll(body); !errors.Is(err, errMediaSize) || string(content) != "12345" {
		t.Fatalf("limited content=%q err=%v", content, err)
	}
	client := mediaHTTPClient{client: mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: maxMediaImageBytes + 1, Body: io.NopCloser(strings.NewReader("x"))}, nil
	}}}
	request, _ := http.NewRequest(http.MethodGet, "https://open.feishu.cn/open-apis/im/v1/messages/msg/resources/key?type=image", nil)
	if _, err := client.Do(request); !errors.Is(err, errMediaSize) {
		t.Fatalf("oversized Content-Length accepted: %v", err)
	}
}

func TestMediaRenderUsesProviderNativeMessageTypes(t *testing.T) {
	for _, kind := range []string{"image", "file"} {
		msgType, content := renderDelivery(map[string]any{"kind": "provider_media", "resource_type": kind, "resource_key": "resource"})
		var payload map[string]string
		if json.Unmarshal([]byte(content), &payload) != nil || msgType != kind {
			t.Fatalf("render=%s %s", msgType, content)
		}
		field := "file_key"
		if kind == "image" {
			field = "image_key"
		}
		if payload[field] != "resource" {
			t.Fatalf("payload=%#v", payload)
		}
	}
}

func TestMediaMentionsDoNotConvertNativeAttachmentsToRichText(t *testing.T) {
	target := map[string]string{
		"mention_user_ids": `["group-user"]`,
		"mention_names":    `["群成员"]`,
	}
	for _, kind := range []string{"image", "file"} {
		msgType, content := renderDelivery(map[string]any{"kind": "provider_media", "resource_type": kind, "resource_key": "resource"})
		actualType, actualContent := applyDeliveryMentions(msgType, content, target)
		if actualType != kind || actualContent != content {
			t.Fatalf("%s attachment converted to %s: %s", kind, actualType, actualContent)
		}
	}
}

func TestOutreachImageSendsMentionTextAndImageInOnePost(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	reads, records, uploads, sends := 0, 0, 0, 0
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/deliveries/delivery/attachment"):
			reads++
			if request.URL.Query().Get("attachment_id") != "image-attachment" ||
				request.Header.Get("X-AHA-Lease-ID") != "lease" {
				t.Errorf("invalid attachment request %s headers=%#v", request.URL.String(), request.Header)
			}
			writer.Header().Set("Content-Type", "image/png")
			writer.Write(png)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/deliveries/delivery/media"):
			records++
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload["attachment_id"] != "image-attachment" || payload["lease_id"] != "lease" {
				t.Errorf("invalid media record %#v", payload)
			}
			writer.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected runtime request %s %s", request.Method, request.URL.String())
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer runtimeServer.Close()
	client := lark.NewClient("outreach-image", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/auth/") {
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"sdk-test","expire":3600}`), nil
		}
		if strings.HasSuffix(request.URL.Path, "/images") {
			uploads++
			return mediaTestResponse(200, `{"code":0,"data":{"image_key":"uploaded-image"}}`), nil
		}
		if strings.Contains(request.URL.Path, "/messages") {
			sends++
			var envelope map[string]any
			_ = json.NewDecoder(request.Body).Decode(&envelope)
			if envelope["msg_type"] != "post" || envelope["uuid"] != deterministicUUID("outreach-key") {
				t.Errorf("invalid send envelope %#v", envelope)
			}
			rawContent, _ := envelope["content"].(string)
			var post struct {
				ZhCN struct {
					Content [][]map[string]string `json:"content"`
				} `json:"zh_cn"`
			}
			if json.Unmarshal([]byte(rawContent), &post) != nil || len(post.ZhCN.Content) != 2 ||
				post.ZhCN.Content[0][0]["tag"] != "at" ||
				post.ZhCN.Content[0][0]["user_id"] != "work-bot-open" ||
				post.ZhCN.Content[0][1]["text"] != " 请原样回复图片" ||
				post.ZhCN.Content[1][0]["tag"] != "img" ||
				post.ZhCN.Content[1][0]["image_key"] != "uploaded-image" {
				t.Errorf("invalid post content %s", rawContent)
			}
			return mediaTestResponse(200, `{"code":0,"data":{"message_id":"sent-post"}}`), nil
		}
		t.Errorf("unexpected provider request %s", request.URL.String())
		return mediaTestResponse(400, `{"code":1}`), nil
	}}))
	runtime := &runtimeClient{baseURL: runtimeServer.URL, token: "runtime-capability", http: runtimeServer.Client()}
	item := delivery{
		ID: "delivery", IdempotencyKey: "outreach-key", LeaseID: "lease",
		Target: map[string]string{
			"chat_id": "chat", "mention_user_ids": `["work-bot-open"]`,
			"mention_names": `["WORK-AHA"]`,
		},
		SemanticPayload: map[string]any{
			"kind": "agent_outreach", "task_id": "task", "text": "请原样回复图片",
			"image_attachments": []map[string]any{{
				"attachment_id": "image-attachment", "task_id": "task",
				"name": "image.png", "media_type": "image/png",
			}},
		},
	}
	messageID, _, err := runtime.sendOutreachImageDelivery(context.Background(), client, item)
	if err != nil || messageID != "sent-post" || reads != 1 || records != 2 || uploads != 1 || sends != 1 {
		t.Fatalf("message=%q read/record/upload/send=%d/%d/%d/%d err=%v", messageID, reads, records, uploads, sends, err)
	}
}

func TestMediaDownloadTransfersBinaryWithCommandLease(t *testing.T) {
	for _, kind := range []string{"file", "image"} {
		t.Run(kind, func(t *testing.T) {
			content := []byte("report content")
			name := "report.txt"
			if kind == "image" {
				content = []byte("\x89PNG\r\n\x1a\nfixture")
				name = "image.png"
			}
			uploaded := false
			runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer runtime-capability" {
					t.Error("missing runtime capability")
				}
				if strings.HasSuffix(request.URL.Path, "/progress") {
					writer.Write([]byte("{\"ok\":true}"))
					return
				}
				if !strings.HasSuffix(request.URL.Path, "/attachment") {
					t.Errorf("unexpected runtime operation %s", request.URL.Path)
					writer.WriteHeader(404)
					return
				}
				if request.Header.Get("X-AHA-Lease-ID") != "lease" {
					t.Error("missing upload lease")
				}
				file, header, err := request.FormFile("file")
				if err != nil {
					t.Error(err)
					writer.WriteHeader(400)
					return
				}
				defer file.Close()
				received, _ := io.ReadAll(file)
				if !bytes.Equal(received, content) || header.Filename != name {
					t.Errorf("wrong transferred file %q", header.Filename)
				}
				uploaded = true
				writer.WriteHeader(http.StatusCreated)
				writer.Write([]byte("{\"ok\":true,\"attachment_id\":\"attachment\"}"))
			}))
			defer runtimeServer.Close()
			client := lark.NewClient("media-download-app", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/auth/") {
					return mediaTestResponse(200, "{\"code\":0,\"tenant_access_token\":\"sdk-test\",\"expire\":3600}"), nil
				}
				if !strings.Contains(request.URL.Path, "/messages/message/resources/resource") || request.URL.Query().Get("type") != kind {
					t.Errorf("wrong source resource path %s", request.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(bytes.NewReader(content))}, nil
			}}))
			runtime := &runtimeClient{baseURL: runtimeServer.URL, token: "runtime-capability", http: runtimeServer.Client()}
			err := runtime.downloadResource(context.Background(), client, command{ID: "command", LeaseID: "lease", Payload: map[string]any{"message_id": "message", "resource_key": "resource", "resource_type": kind, "file_name": name}})
			if err != nil || !uploaded {
				t.Fatalf("uploaded=%v err=%v", uploaded, err)
			}
		})
	}
}

func TestMediaOutboundRetriesReusePersistedResource(t *testing.T) {
	for _, kind := range []string{"image", "file"} {
		t.Run(kind, func(t *testing.T) {
			content := []byte("document")
			if kind == "image" {
				content = []byte("\x89PNG\r\n\x1a\nfixture")
			}
			resourceKey := ""
			reads, uploads, sends := 0, 0, 0
			runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/attachment") {
					reads++
					if request.Header.Get("X-AHA-Lease-ID") != "lease" {
						t.Error("missing read lease")
					}
					writer.Write(content)
					return
				}
				if strings.HasSuffix(request.URL.Path, "/media") {
					var payload map[string]any
					_ = json.NewDecoder(request.Body).Decode(&payload)
					if key, _ := payload["resource_key"].(string); key != "" {
						resourceKey = key
					}
					writer.Write([]byte("{\"ok\":true}"))
					return
				}
				writer.WriteHeader(404)
			}))
			defer runtimeServer.Close()
			client := lark.NewClient("media-send-"+kind, "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/auth/") {
					return mediaTestResponse(200, "{\"code\":0,\"tenant_access_token\":\"sdk-test\",\"expire\":3600}"), nil
				}
				if strings.HasSuffix(request.URL.Path, "/images") || strings.HasSuffix(request.URL.Path, "/files") {
					uploads++
					if !strings.Contains(request.Header.Get("Content-Type"), "multipart/form-data") {
						t.Error("provider upload is not multipart")
					}
					field := "file_key"
					if kind == "image" {
						field = "image_key"
					}
					raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]string{field: "uploaded-resource"}})
					return mediaTestResponse(200, string(raw)), nil
				}
				if strings.Contains(request.URL.Path, "/messages") {
					sends++
					var payload map[string]any
					_ = json.NewDecoder(request.Body).Decode(&payload)
					if payload["msg_type"] != kind || payload["uuid"] != deterministicUUID("delivery-key") {
						t.Errorf("wrong send envelope %#v", payload)
					}
					if resourceKey == "" {
						t.Error("message sent before resource key persisted")
					}
					if sends == 1 {
						return nil, errors.New("connection lost")
					}
					return mediaTestResponse(200, "{\"code\":0,\"data\":{\"message_id\":\"sent-message\"}}"), nil
				}
				t.Errorf("unexpected provider path %s", request.URL.Path)
				return mediaTestResponse(400, "{\"code\":1}"), nil
			}}))
			runtime := &runtimeClient{baseURL: runtimeServer.URL, token: "runtime-capability", http: runtimeServer.Client()}
			item := delivery{ID: "delivery", IdempotencyKey: "delivery-key", LeaseID: "lease", Target: map[string]string{"chat_id": "chat"}, SemanticPayload: map[string]any{"kind": "attachment", "name": "report.txt"}}
			if _, _, err := runtime.sendAttachmentDelivery(context.Background(), client, item); err == nil {
				t.Fatal("expected ambiguous first send")
			}
			item.SemanticPayload["provider_resource_key"], item.SemanticPayload["provider_resource_type"] = resourceKey, kind
			message, _, err := runtime.sendAttachmentDelivery(context.Background(), client, item)
			if err != nil || message != "sent-message" || reads != 1 || uploads != 1 || sends != 2 {
				t.Fatalf("message=%s read/upload/send=%d/%d/%d err=%v", message, reads, uploads, sends, err)
			}
		})
	}
}
