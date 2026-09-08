package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkchannel "github.com/larksuite/oapi-sdk-go/v3/channel"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type bootstrap struct {
	Schema         string `json:"schema"`
	Protocol       string `json:"protocol"`
	RuntimeBaseURL string `json:"runtime_base_url"`
	PluginID       string `json:"plugin_id"`
	ProviderKey    string `json:"provider_key"`
	InstanceID     string `json:"instance_id"`
	Capability     string `json:"capability"`
	AppID          string `json:"app_id"`
	AppSecret      string `json:"app_secret"`
	TenantBrand    string `json:"tenant_brand"`
	Registration   bool   `json:"registration"`
}

type runtimeClient struct {
	baseURL, instanceID, token string
	http                       *http.Client
}

type command struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	IdempotencyKey string         `json:"idempotency_key"`
	Payload        map[string]any `json:"payload"`
	LeaseID        string         `json:"lease_id"`
}

type delivery struct {
	ID               string            `json:"id"`
	IdempotencyKey   string            `json:"idempotency_key"`
	SemanticPayload  map[string]any    `json:"semantic_payload"`
	Target           map[string]string `json:"target"`
	LeaseID          string            `json:"lease_id"`
	Attempts         int               `json:"attempts"`
	FirstAttemptAt   time.Time         `json:"first_attempt_at"`
	OutcomeCertainty string            `json:"outcome_certainty"`
}

type secretMessage struct {
	Schema        string `json:"schema"`
	Type          string `json:"type"`
	InstanceID    string `json:"instance_id"`
	OnboardingID  string `json:"onboarding_id"`
	CommandID     string `json:"command_id"`
	LeaseID       string `json:"lease_id"`
	URL           string `json:"url,omitempty"`
	ExpireIn      int    `json:"expire_in,omitempty"`
	AppID         string `json:"app_id,omitempty"`
	AppSecret     string `json:"app_secret,omitempty"`
	ScannerOpenID string `json:"scanner_open_id,omitempty"`
	TenantBrand   string `json:"tenant_brand,omitempty"`
}

var secretOutput sync.Mutex

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--channel-runtime" {
		fmt.Fprintln(os.Stderr, "this binary is managed by AHA2")
		os.Exit(2)
	}
	var boot bootstrap
	if err := json.NewDecoder(os.Stdin).Decode(&boot); err != nil || boot.Schema != "channel-secret/v1" || boot.Protocol != "channel-runtime/v1" || boot.RuntimeBaseURL == "" || boot.InstanceID == "" || boot.Capability == "" {
		fmt.Fprintln(os.Stderr, "invalid AHA2 channel bootstrap")
		os.Exit(2)
	}
	client := &runtimeClient{baseURL: strings.TrimRight(boot.RuntimeBaseURL, "/"), instanceID: boot.InstanceID, token: boot.Capability, http: &http.Client{Timeout: 35 * time.Second}}
	ctx := context.Background()
	if err := client.handshake(ctx, boot); err != nil {
		fmt.Fprintln(os.Stderr, "AHA2 channel handshake failed")
		os.Exit(1)
	}
	if boot.Registration || boot.AppID == "" || boot.AppSecret == "" {
		if err := registrationLoop(ctx, client); err != nil {
			fmt.Fprintln(os.Stderr, "Feishu registration failed")
			os.Exit(1)
		}
		return
	}
	if err := runChannel(ctx, client, boot); err != nil {
		_ = client.health(context.Background(), "degraded", "channel_runtime_stopped")
		fmt.Fprintln(os.Stderr, "Feishu channel runtime stopped")
		os.Exit(1)
	}
}

func (c *runtimeClient) request(ctx context.Context, method, path string, body, output any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AHA-Channel-Protocol", "1")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("runtime status %d", response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func (c *runtimeClient) handshake(ctx context.Context, boot bootstrap) error {
	return c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/handshake", map[string]any{"schema_version": 1, "instance_id": c.instanceID, "plugin_id": boot.PluginID, "supported_protocols": []string{"channel-runtime/v1"}, "boot_id": fmt.Sprint(time.Now().UnixNano())}, &map[string]any{})
}

func (c *runtimeClient) claimCommands(ctx context.Context) ([]command, error) {
	var result struct {
		Commands []command `json:"commands"`
	}
	err := c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/instances/"+c.instanceID+"/commands:claim", map[string]any{"schema_version": 1, "limit": 10}, &result)
	return result.Commands, err
}

func (c *runtimeClient) progress(ctx context.Context, command command, progress map[string]any) error {
	return c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/commands/"+command.ID+"/progress", map[string]any{"schema_version": 1, "lease_id": command.LeaseID, "progress": progress}, &map[string]any{})
}

func (c *runtimeClient) complete(ctx context.Context, command command, success bool, result map[string]any, code string) error {
	return c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/commands/"+command.ID+"/complete", map[string]any{"schema_version": 1, "lease_id": command.LeaseID, "success": success, "result": result, "error_code": code}, &map[string]any{})
}

func registrationLoop(ctx context.Context, client *runtimeClient) error {
	for {
		commands, err := client.claimCommands(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, item := range commands {
			if item.Kind != "register_app" {
				_ = client.complete(ctx, item, false, map[string]any{}, "unsupported_command")
				continue
			}
			return registerApp(ctx, client, item)
		}
		time.Sleep(time.Second)
	}
}

func registerApp(ctx context.Context, client *runtimeClient, item command) error {
	preset := false
	onboardingID := stringValue(item.Payload, "onboarding_id")
	createOnly := boolValue(item.Payload, "create_only")
	result, err := registration.RegisterApp(ctx, &registration.Options{
		CreateOnly: createOnly,
		AppID:      stringValue(item.Payload, "app_id"),
		AppPreset:  &registration.AppPreset{Name: stringValue(item.Payload, "app_name"), Desc: "AHA2 channel assistant"},
		Addons: &registration.AppAddons{
			Preset:    &preset,
			Scopes:    registration.AppAddonsScopes{Tenant: []string{"im:message.p2p_msg:readonly", "im:message.group_at_msg:readonly", "im:message:send_as_bot", "cardkit:card:write"}},
			Events:    registration.AppAddonsEvents{Items: registration.AppAddonsEventItems{Tenant: []string{"im.message.receive_v1"}}},
			Callbacks: registration.AppAddonsCallbacks{Items: []string{"card.action.trigger"}},
		},
		OnQRCode: func(info *registration.QRCodeInfo) {
			emitSecret(secretMessage{Schema: "channel-secret/v1", Type: "verification_url", InstanceID: client.instanceID, OnboardingID: onboardingID, CommandID: item.ID, LeaseID: item.LeaseID, URL: info.URL, ExpireIn: info.ExpireIn})
		},
		OnStatusChange: func(info *registration.StatusChangeInfo) {
			_ = client.progress(context.Background(), item, map[string]any{"status": info.Status, "interval": info.Interval})
		},
	})
	if err != nil {
		_ = client.complete(context.Background(), item, false, map[string]any{}, registrationErrorCode(err))
		return err
	}
	if result.UserInfo == nil || result.UserInfo.OpenID == "" {
		err := errors.New("registration result omitted scanner identity")
		_ = client.complete(context.Background(), item, false, map[string]any{}, "scanner_identity_missing")
		return err
	}
	emitSecret(secretMessage{Schema: "channel-secret/v1", Type: "registration_result", InstanceID: client.instanceID, OnboardingID: onboardingID, CommandID: item.ID, LeaseID: item.LeaseID, AppID: result.ClientID, AppSecret: result.ClientSecret, ScannerOpenID: result.UserInfo.OpenID, TenantBrand: result.UserInfo.TenantBrand})
	return nil
}

func registrationErrorCode(err error) string {
	var registerErr *registration.RegisterAppError
	if errors.As(err, &registerErr) && registerErr.Code != "" {
		return registerErr.Code
	}
	return "registration_failed"
}

func emitSecret(message secretMessage) {
	secretOutput.Lock()
	defer secretOutput.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(message)
}

func runChannel(ctx context.Context, runtime *runtimeClient, boot bootstrap) error {
	client, wsClient := newFeishuClients(boot)
	channel := larkchannel.NewChannel(client, wsClient)
	channel.OnReady(func() { _ = runtime.health(context.Background(), "ready", "") })
	channel.OnReconnecting(func() { _ = runtime.health(context.Background(), "degraded", "feishu_reconnecting") })
	channel.OnMessage(func(eventCtx context.Context, message *channeltypes.NormalizedMessage) error {
		resources := make([]map[string]any, 0, len(message.Resources))
		for _, resource := range message.Resources {
			resources = append(resources, map[string]any{"type": resource.Type, "file_key": resource.FileKey, "file_name": resource.FileName})
		}
		return runtime.inbound(eventCtx, map[string]any{"schema_version": 1, "request_id": message.EventID, "instance_id": runtime.instanceID, "external_event_id": message.EventID, "event_type": "message", "occurred_at": time.UnixMilli(message.CreateTimeMs).UTC(), "chat_type": message.ChatType, "external_chat_id": message.ChatID, "external_sender_id": message.UserID, "external_message_id": message.MessageID, "content": message.Content, "mentioned_bot": message.MentionedBot, "resources": resources})
	})
	channel.OnCardAction(func(eventCtx context.Context, action *channeltypes.CardActionEvent) error {
		messageID, chatID := action.MessageID, action.ChatID
		if messageID == "" {
			messageID = action.Context.OpenMessageID
		}
		if chatID == "" {
			chatID = action.Context.OpenChatID
		}
		value := map[string]any{"provider_message_id": messageID}
		for key, item := range action.Action.Value {
			if key == "action_id" || key == "decision" {
				value[key] = item
			}
		}
		return runtime.inbound(eventCtx, map[string]any{"schema_version": 1, "request_id": action.EventID, "instance_id": runtime.instanceID, "external_event_id": action.EventID, "event_type": "card_action", "occurred_at": time.Now().UTC(), "chat_type": "p2p", "external_chat_id": chatID, "external_sender_id": action.Operator.OpenID, "external_message_id": messageID, "card_action": value})
	})
	go runtime.deliveryLoop(ctx, client)
	go runtime.commandLoop(ctx)
	return channel.Start(ctx)
}

func newFeishuClients(boot bootstrap) (*lark.Client, *larkws.Client) {
	clientOptions := []lark.ClientOptionFunc{}
	// ws.Client does not create an EventDispatcher by default. A Channel can
	// report the socket as ready without one, but every inbound event then
	// panics inside ws.Client before reaching the registered Channel handlers.
	wsOptions := []larkws.ClientOption{larkws.WithEventHandler(larkevent.NewEventDispatcher("", ""))}
	if strings.EqualFold(boot.TenantBrand, "lark") {
		clientOptions = append(clientOptions, lark.WithOpenBaseUrl(lark.LarkBaseUrl), lark.WithOAuthBaseUrl(lark.OAuthBaseUrlLark))
		wsOptions = append(wsOptions, larkws.WithDomain(lark.LarkBaseUrl))
	}
	client := lark.NewClient(boot.AppID, boot.AppSecret, clientOptions...)
	wsClient := larkws.NewClient(boot.AppID, boot.AppSecret, wsOptions...)
	return client, wsClient
}

func (c *runtimeClient) health(ctx context.Context, status, code string) error {
	return c.request(ctx, http.MethodPut, "/api/channel-runtime/v1/instances/"+c.instanceID+"/health", map[string]any{"schema_version": 1, "status": status, "error_code": code}, &map[string]any{})
}

func (c *runtimeClient) inbound(ctx context.Context, payload map[string]any) error {
	return c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/instances/"+c.instanceID+"/inbound-events", payload, &map[string]any{})
}

func (c *runtimeClient) commandLoop(ctx context.Context) {
	for ctx.Err() == nil {
		commands, err := c.claimCommands(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, item := range commands {
			switch item.Kind {
			case "verify_installation":
				_ = c.complete(ctx, item, true, map[string]any{"status": "ready"}, "")
			case "stop_runtime":
				_ = c.complete(ctx, item, true, map[string]any{"status": "stopping"}, "")
				return
			default:
				_ = c.complete(ctx, item, false, map[string]any{}, "unsupported_command")
			}
		}
		time.Sleep(time.Second)
	}
}

func (c *runtimeClient) deliveryLoop(ctx context.Context, client *lark.Client) {
	for ctx.Err() == nil {
		var result struct {
			Deliveries []delivery `json:"deliveries"`
		}
		err := c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/instances/"+c.instanceID+"/deliveries:claim", map[string]any{"schema_version": 1, "limit": 20}, &result)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, item := range result.Deliveries {
			c.deliver(ctx, client, item)
		}
		if len(result.Deliveries) == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (c *runtimeClient) deliver(ctx context.Context, client *lark.Client, item delivery) {
	messageID, requestID, err := sendDelivery(ctx, client, item)
	if err == nil {
		_ = c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/deliveries/"+item.ID+"/ack", map[string]any{"schema_version": 1, "lease_id": item.LeaseID, "provider_message_id": messageID, "provider_request_id": requestID}, &map[string]any{})
		return
	}
	permanent, certainty, retry := item.Attempts >= 12, "confirmed_failure", deliveryBackoffMS(item.IdempotencyKey, item.Attempts)
	if isAmbiguous(err) {
		certainty = "unknown"
		permanent = !item.FirstAttemptAt.IsZero() && time.Since(item.FirstAttemptAt) >= 55*time.Minute
	}
	_ = c.request(ctx, http.MethodPost, "/api/channel-runtime/v1/deliveries/"+item.ID+"/nack", map[string]any{"schema_version": 1, "lease_id": item.LeaseID, "error_code": "feishu_send_failed", "outcome_certainty": certainty, "retry_after_ms": retry, "permanent": permanent}, &map[string]any{})
}

func deliveryBackoffMS(key string, attempt int) int64 {
	steps := []int64{1000, 5000, 30000, 120000, 600000, 3600000}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(steps) {
		index = len(steps) - 1
	}
	limit := steps[index]
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", key, attempt)))
	seed := int64(digest[0])<<8 | int64(digest[1])
	return 1 + seed%limit
}

func sendDelivery(ctx context.Context, client *lark.Client, item delivery) (string, string, error) {
	chatID := item.Target["chat_id"]
	if chatID == "" {
		return "", "", errors.New("missing target chat")
	}
	msgType, content := renderDelivery(item.SemanticPayload)
	if updateID := item.Target["update_message_id"]; updateID != "" {
		request := larkim.NewPatchMessageReqBuilder().MessageId(updateID).Body(larkim.NewPatchMessageReqBodyBuilder().Content(content).Build()).Build()
		response, err := client.Im.V1.Message.Patch(ctx, request)
		if err != nil {
			return "", "", err
		}
		if !response.Success() {
			return "", response.RequestId(), fmt.Errorf("feishu card update rejected: %d", response.Code)
		}
		return updateID, response.RequestId(), nil
	}
	uuid := deterministicUUID(item.IdempotencyKey)
	if replyID := item.Target["reply_message_id"]; replyID != "" {
		request := larkim.NewReplyMessageReqBuilder().MessageId(replyID).Body(larkim.NewReplyMessageReqBodyBuilder().MsgType(msgType).Content(content).Uuid(uuid).Build()).Build()
		response, err := client.Im.V1.Message.Reply(ctx, request)
		if err != nil {
			return "", "", err
		}
		if !response.Success() || response.Data == nil || response.Data.MessageId == nil {
			return "", response.RequestId(), fmt.Errorf("feishu reply rejected: %d", response.Code)
		}
		return *response.Data.MessageId, response.RequestId(), nil
	}
	request := larkim.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(chatID).MsgType(msgType).Content(content).Uuid(uuid).Build()).Build()
	response, err := client.Im.V1.Message.Create(ctx, request)
	if err != nil {
		return "", "", err
	}
	if !response.Success() || response.Data == nil || response.Data.MessageId == nil {
		return "", response.RequestId(), fmt.Errorf("feishu create rejected: %d", response.Code)
	}
	return *response.Data.MessageId, response.RequestId(), nil
}

func renderDelivery(payload map[string]any) (string, string) {
	if stringValue(payload, "kind") == "confirmation" {
		actionID := stringValue(payload, "action_id")
		preview, _ := json.MarshalIndent(payload["preview"], "", "  ")
		card := map[string]any{"schema": "2.0", "header": map[string]any{"title": map[string]any{"tag": "plain_text", "content": "请确认 AHA 操作"}}, "body": map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": "```\n" + string(preview) + "\n```"}, map[string]any{"tag": "column_set", "columns": []any{buttonColumn("确认", "primary", actionID, "confirm"), buttonColumn("取消", "default", actionID, "cancel")}}}}}
		raw, _ := json.Marshal(card)
		return "interactive", string(raw)
	}
	if stringValue(payload, "kind") == "action_result" {
		status := stringValue(payload, "status")
		template := "green"
		if status != "succeeded" {
			template = "grey"
		}
		result, _ := json.MarshalIndent(payload["result"], "", "  ")
		card := map[string]any{"schema": "2.0", "header": map[string]any{"template": template, "title": map[string]any{"tag": "plain_text", "content": "AHA 操作"}}, "body": map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": status + "\n```\n" + string(result) + "\n```"}}}}
		raw, _ := json.Marshal(card)
		return "interactive", string(raw)
	}
	if stringValue(payload, "kind") == "handoff" {
		card := map[string]any{"schema": "2.0", "header": map[string]any{"template": "orange", "title": map[string]any{"tag": "plain_text", "content": "群聊转单待处理"}}, "body": map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": "**" + stringValue(payload, "summary") + "**\n\n" + stringValue(payload, "details") + "\n\n请在私聊中选择整理为待办、创建 Task 或忽略；写操作仍需一次性确认。"}}}}
		raw, _ := json.Marshal(card)
		return "interactive", string(raw)
	}
	text := stringValue(payload, "text")
	if text == "" {
		text = stringValue(payload, "error")
	}
	if text == "" {
		raw, _ := json.Marshal(payload)
		text = string(raw)
	}
	raw, _ := json.Marshal(map[string]string{"text": text})
	return "text", string(raw)
}

func buttonColumn(label, style, actionID, decision string) map[string]any {
	return map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{map[string]any{"tag": "button", "text": map[string]any{"tag": "plain_text", "content": label}, "type": style, "behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{"action_id": actionID, "decision": decision}}}}}}
}

func deterministicUUID(value string) string {
	digest := sha256.Sum256([]byte(value))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(digest[:16])
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
}

func stringValue(values map[string]any, key string) string {
	return strings.TrimSpace(fmt.Sprint(values[key]))
}

func boolValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func isAmbiguous(err error) bool {
	var urlError interface{ Timeout() bool }
	return errors.As(err, &urlError) || strings.Contains(strings.ToLower(err.Error()), "connection") || strings.Contains(strings.ToLower(err.Error()), "timeout")
}
