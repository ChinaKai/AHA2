package channel

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func syncTestFeishuGroups(t *testing.T, service *Service, claims RuntimeClaims, chats ...string) {
	t.Helper()
	ctx := context.Background()
	command, err := service.EnqueueCommand(ctx, claims.InstanceID, store.FeishuGroupRefreshCommand, domain.NewID("group_refresh"), nil)
	if err != nil {
		t.Fatal(err)
	}
	leased, err := service.ClaimCommands(ctx, claims, 1)
	if err != nil || len(leased) != 1 || leased[0].ID != command.ID {
		t.Fatalf("claim refresh command: %v, %v", leased, err)
	}
	groups := make([]domain.FeishuGroup, 0, len(chats))
	for _, chat := range chats {
		groups = append(groups, domain.FeishuGroup{ChatID: chat, DisplayName: "测试群 " + chat})
	}
	if err := service.CompleteCommand(ctx, claims, command.ID, leased[0].LeaseID, true, map[string]any{"groups": groups}, ""); err != nil {
		t.Fatal(err)
	}
}

func setTestGroupReception(t *testing.T, service *Service, instance domain.ChannelInstance, chat string, enabled bool) {
	t.Helper()
	group, err := service.SetFeishuGroupReception(context.Background(), instance.OwnerID, instance.ID, chat, enabled)
	if err != nil || group.ReceiveAllMessages != enabled {
		t.Fatalf("set %s reception=%v: %+v, %v", chat, enabled, group, err)
	}
}

func TestFeishuGroupReceptionCanBeDisabledAfterGroupLeavesBotList(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const chat = "oc_local_bound"
	const listedChat = "oc_still_listed"
	host := startFeishuMentionHost(t, service, instance, claims, chat, now)
	route, err := service.store.ActivateOwnerChannelTaskRoute(ctx, instance.ID, host.ID, host.HostTaskID, now)
	if err != nil {
		t.Fatal(err)
	}
	syncTestFeishuGroups(t, service, claims, chat, listedChat, "oc_no_local_conversation")
	setTestGroupReception(t, service, instance, chat, true)
	syncTestFeishuGroups(t, service, claims, listedChat)

	summaries, err := service.OwnerConversationSummaries(ctx, instance.OwnerID, instance.ID)
	if err != nil || len(summaries) != 2 {
		t.Fatalf("refresh did not keep the local group alongside the listed group: %+v, %v", summaries, err)
	}
	local := summaries[0]
	if local.ID != host.ID || local.BoundTaskID != route.TargetTaskID || !local.NotInBotGroupList ||
		local.Group == nil || local.Group.ChatID != chat || !local.Group.ReceiveAllMessages || local.Group.UpdatedBy != instance.OwnerID {
		t.Fatalf("unlisted group lost its binding or switch state: %+v", local)
	}
	if summaries[1].ID != listedChat || summaries[1].NotInBotGroupList {
		t.Fatalf("listed group was not preserved: %+v", summaries[1])
	}
	setTestGroupReception(t, service, instance, local.Group.ChatID, false)
	summaries, err = service.OwnerConversationSummaries(ctx, instance.OwnerID, instance.ID)
	if err != nil || len(summaries) != 1 || summaries[0].ID != listedChat {
		t.Fatalf("unlisted group still appears after turning off reception: %+v, %v", summaries, err)
	}
}

func TestFeishuGroupReceptionMatrix(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	instance, err := service.store.UpdateChannelInstance(ctx, instance, instance.Revision)
	if err != nil {
		t.Fatal(err)
	}
	syncTestFeishuGroups(t, service, claims, "oc_bound_on", "oc_bound_off", "oc_host_on", "oc_host_off")

	for _, group := range []struct {
		chat           string
		bound, enabled bool
	}{
		{"oc_bound_on", true, true}, {"oc_bound_off", true, false},
		{"oc_host_on", false, true}, {"oc_host_off", false, false},
	} {
		t.Run(group.chat, func(t *testing.T) {
			if group.enabled {
				setTestGroupReception(t, service, instance, group.chat, true)
			}
			var targetID string
			if group.bound {
				host := startFeishuMentionHost(t, service, instance, claims, group.chat, now)
				target, err := service.application.CreateTask(ctx, app.CreateTaskInput{
					ProjectID: instance.HostProjectID, WorkspaceID: instance.HostWorkspaceID,
					Title: "Bound matrix target", Request: "matrix", Isolation: "inplace",
					Backend: "stub", ModelID: "model-stub", CollaborationMode: "single", AllowSystemProject: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := service.store.UpdateTaskStatus(ctx, target.ID, target.Status, domain.TaskWaitingUser, timeStringUTC(now), ""); err != nil {
					t.Fatal(err)
				}
				if _, err := service.store.ActivateOwnerChannelTaskRoute(ctx, instance.ID, host.ID, target.ID, now); err != nil {
					t.Fatal(err)
				}
				targetID = target.ID
			}
			for _, message := range []struct {
				name, thread, senderType string
				mentioned                bool
			}{
				{"top_plain", "", "user", false},
				{"top_bot", "", "app", false},
				{"top_mention", "", "user", true},
				{"topic_mention", "omt_a", "user", true},
				{"topic_plain", "omt_a", "user", false},
				{"topic_bot", "omt_a", "app", false},
			} {
				t.Run(message.name, func(t *testing.T) {
					select {
					case <-service.wakeInbound:
					default:
					}
					eventID := group.chat + "-" + message.name
					envelope := domain.ChannelInboundEnvelope{
						SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
						ExternalChatID: group.chat, ChatDisplayName: "测试群", ExternalSenderID: "ou_" + message.senderType,
						ExternalEventID: eventID, ExternalMessageID: "om_" + eventID, Content: "matrix " + eventID,
						MentionedBot: message.mentioned, SenderType: message.senderType, ThreadID: message.thread, OccurredAt: now,
					}
					if !message.mentioned && message.thread == "" {
						envelope.Resources = []map[string]any{{"type": "file", "file_key": "must-not-download"}}
					}
					before, err := service.store.ListTasks(ctx, instance.HostProjectID)
					if err != nil {
						t.Fatal(err)
					}
					receipt, duplicate, err := service.ReceiveInbound(ctx, claims, envelope)
					if !group.enabled && !message.mentioned {
						if !errors.Is(err, store.ErrChannelGroupNotAllowed) || receipt.ID != "" || len(service.wakeInbound) != 0 {
							t.Fatalf("disabled group accepted a non-mention: %+v, %v", receipt, err)
						}
						return
					}
					if err != nil || duplicate {
						t.Fatalf("receive: %+v, duplicate=%v, %v", receipt, duplicate, err)
					}
					if !message.mentioned && message.thread == "" {
						if receipt.State != "listening" || receipt.NormalizedPayload != nil || len(service.wakeInbound) != 0 {
							t.Fatalf("not a listening-only receipt: %+v", receipt)
						}
						if _, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID); !errors.Is(err, sql.ErrNoRows) {
							t.Fatalf("listening created an inbox receipt: %v", err)
						}
						if err := service.processInboundBatch(ctx); err != nil {
							t.Fatal(err)
						}
						if after, err := service.store.ListTasks(ctx, instance.HostProjectID); err != nil || len(after) != len(before) {
							t.Fatalf("listening created a Task: %d -> %d, %v", len(before), len(after), err)
						}
						if commands, err := service.ClaimCommands(ctx, claims, 10); err != nil || len(commands) != 0 {
							t.Fatalf("listening queued a command: %v, %v", commands, err)
						}
						if group.bound {
							if task, err := service.store.Task(ctx, targetID); err != nil || task.Status != domain.TaskWaitingUser || !task.UpdatedAt.Equal(now) {
								t.Fatalf("listening updated Task: %+v, %v", task, err)
							}
							if _, err := service.store.LatestRound(ctx, targetID); !errors.Is(err, sql.ErrNoRows) {
								t.Fatalf("listening created a round: %v", err)
							}
							if pending, err := service.store.PendingAgentInbox(ctx, targetID, "main"); err != nil || len(pending) != 0 {
								t.Fatalf("listening queued agent messages: %v, %v", pending, err)
							}
						} else if conv, err := service.store.ChannelConversation(ctx, receipt.ConversationID); err != nil || conv.HostTaskID != "" {
							t.Fatalf("listening created a group host: %+v, %v", conv, err)
						}
						return
					}
					if receipt.State != "received" || len(service.wakeInbound) == 0 {
						t.Fatalf("wake receipt: %+v", receipt)
					}
					if err := service.processInboundBatch(ctx); err != nil {
						t.Fatal(err)
					}
					stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, receipt.ID)
					if err != nil || stored.State != "processed" || stored.AHAMessageID == "" {
						t.Fatalf("wake did not reach the Task: %+v, %v", stored, err)
					}
					if !group.bound {
						conv, err := service.store.ChannelConversation(ctx, stored.ConversationID)
						if err != nil || conv.HostTaskID == "" {
							t.Fatalf("wake did not use the group host: %+v, %v", conv, err)
						}
						targetID = conv.HostTaskID
						if _, err := service.store.ActiveChannelTaskRoute(ctx, conv.ID); !errors.Is(err, sql.ErrNoRows) {
							t.Fatalf("host wake created a binding: %v", err)
						}
					}
					if _, err := service.store.LatestRound(ctx, targetID); err != nil {
						t.Fatalf("wake did not create a round on %s: %v", targetID, err)
					}
					if message.thread != "" {
						metadata, err := service.store.ChannelGroupReceipt(ctx, stored.ID)
						if err != nil || metadata.BatchID == "" || metadata.TargetTaskID != targetID || (metadata.RouteID != "") != group.bound {
							t.Fatalf("topic wake lost Task or topic metadata: %+v, %v", metadata, err)
						}
					}
					if group.enabled && message.name == "top_mention" {
						background, err := service.store.ChannelListeningContext(ctx, targetID, stored.ConversationID, "", "", now)
						for _, suffix := range []string{"top_plain", "top_bot"} {
							if err != nil || !strings.Contains(background, "matrix "+group.chat+"-"+suffix) {
								t.Fatalf("mention did not find the earlier listening background: %q, %v", background, err)
							}
						}
					}
				})
			}
		})
	}
}

func TestUnboundFeishuTopicStartsHostAndListeningNeverContinuesIt(t *testing.T) {
	service, instance, claims := mediaTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	const chat = "oc_cold_topic"
	syncTestFeishuGroups(t, service, claims, chat)
	setTestGroupReception(t, service, instance, chat, true)
	instance.Config["channel_permission_ids"] = []string{domain.FeishuFullGroupPermission}
	if _, err := service.store.UpdateChannelInstance(ctx, instance, instance.Revision); err != nil {
		t.Fatal(err)
	}
	send := func(id, thread, root, body string) domain.ChannelInboxReceipt {
		t.Helper()
		receipt, duplicate, err := service.ReceiveInbound(ctx, claims, domain.ChannelInboundEnvelope{
			SchemaVersion: 1, InstanceID: instance.ID, EventType: "message", ChatType: "group",
			ExternalChatID: chat, ChatDisplayName: "话题测试", ExternalSenderID: "ou_user",
			ExternalEventID: id, ExternalMessageID: id, Content: body, SenderType: "user",
			ThreadID: thread, RootID: root, OccurredAt: now,
		})
		if err != nil || duplicate {
			t.Fatalf("receive %s: %+v, duplicate=%v, %v", id, receipt, duplicate, err)
		}
		return receipt
	}
	root := send("om_root", "", "", "ROOT_BEFORE_HOST")
	if root.State != "listening" {
		t.Fatalf("first top-level message state: %s", root.State)
	}
	wake := send("om_thread", "omt_cold", "om_root", "A topic direct wake")
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	conv, err := service.store.ChannelConversation(ctx, root.ConversationID)
	if err != nil || conv.HostTaskID == "" {
		t.Fatalf("first topic wake did not create a host: %+v, %v", conv, err)
	}
	group, err := service.store.ChannelGroupReceipt(ctx, wake.ID)
	if err != nil || group.RouteID != "" || group.TargetTaskID != conv.HostTaskID {
		t.Fatalf("first topic wake did not retain host and topic routing: %+v, %v", group, err)
	}
	var host domain.Task
	deadline := time.Now().Add(4 * time.Second)
	for {
		turns, err := service.store.ListTurns(ctx, conv.HostTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) == 1 && turns[0].Status.Terminal() {
			if turns[0].Status != domain.TurnSucceeded || !strings.Contains(turns[0].PromptSnapshot, "ROOT_BEFORE_HOST") {
				t.Fatalf("first host Turn missed the topic root background: %s %s", turns[0].Status, turns[0].Error)
			}
			host, err = service.store.Task(ctx, conv.HostTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if host.Status == domain.TaskWaitingUser {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("first host Turn did not finish")
		}
		time.Sleep(15 * time.Millisecond)
	}
	now = now.Add(time.Second)
	if err := service.store.UpdateTaskStatus(ctx, host.ID, host.Status, domain.TaskCompleted, timeStringUTC(now), ""); err != nil {
		t.Fatal(err)
	}
	before, err := service.store.LatestRound(ctx, host.ID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	quiet := send("om_after_completion", "", "", "AFTER_COMPLETION")
	if err := service.processInboundBatch(ctx); err != nil {
		t.Fatal(err)
	}
	tasks, err := service.store.ListTasks(ctx, instance.HostProjectID)
	if err != nil || len(tasks) != 1 || tasks[0].Status != domain.TaskCompleted || !tasks[0].UpdatedAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("listening changed a completed host or created a continuation: %+v, %v", tasks, err)
	}
	if after, err := service.store.LatestRound(ctx, host.ID); err != nil || after.ID != before.ID {
		t.Fatalf("listening created a new round: %+v, %v", after, err)
	}
	summaries, err := service.OwnerConversationSummaries(ctx, instance.OwnerID, instance.ID)
	if err != nil || len(summaries) != 1 || summaries[0].InboundCount != 3 || summaries[0].LastMentionInbound != nil ||
		summaries[0].LastNonMentionInbound == nil || summaries[0].LastNonMentionInbound.State != quiet.State || quiet.State != "listening" {
		t.Fatalf("conversation summary lost unbound listening: %+v, %v", summaries, err)
	}

	t.Run("non_mention_topic_after_host_completion", func(t *testing.T) {
		topic := send("om_topic_after_completion", "omt_cold", "om_root", "Topic after host completion")
		metadata, err := service.store.ChannelGroupReceipt(ctx, topic.ID)
		if err != nil || topic.State != "received" || metadata.BatchID == "" || metadata.RouteID != "" || metadata.TargetTaskID != host.ID {
			t.Fatalf("topic receipt did not target the unbound host: state=%q metadata=%+v, %v", topic.State, metadata, err)
		}
		if err := service.processInboundBatch(ctx); err != nil {
			t.Fatal(err)
		}
		stored, err := service.store.ChannelInboxReceipt(ctx, instance.ID, topic.ID)
		if err != nil || stored.State != "failed" || stored.Outcome != "group_task_route_required" || stored.AHAMessageID != "" {
			t.Errorf("topic receipt: state=%q outcome=%q message=%q, want failed/group_task_route_required with no message, err=%v",
				stored.State, stored.Outcome, stored.AHAMessageID, err)
		}
		tasks, err := service.store.ListTasks(ctx, instance.HostProjectID)
		if err != nil || len(tasks) != 1 || tasks[0].ID != host.ID || tasks[0].Status != domain.TaskCompleted {
			t.Errorf("expected only the completed host, got %d Tasks, err=%v", len(tasks), err)
		}
	})
}
