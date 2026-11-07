package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// Codex live inbox delivery goes through `codex app-server` and turn/steer.
//
// `codex queue` (0.166.0) is not a live channel: while `codex exec` is running
// it only appends a message that becomes a *new* turn after the current one
// ends (measured; see the task report). `codex exec` does not read its queue.
// app-server is the only surface that can add input to an in-flight turn:
// turn/steer takes the active turn id and appends a user message that the model
// reads at its next model call. AHA therefore runs Codex through app-server
// when live delivery is enabled, and keeps the one-shot exec path otherwise.

const (
	defaultCodexLivePoll      = 500 * time.Millisecond
	codexLiveHandshakeTimeout = 30 * time.Second
	codexLiveSteerTimeout     = 30 * time.Second
	codexLiveShutdownGrace    = 5 * time.Second
)

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type codexRPCMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexLiveClaim struct {
	requestID int64
	batch     LiveBatch
	deadline  time.Time
}

// codexAppServerSession drives one app-server process through initialize,
// thread start/resume, turn start, and the live-inbox steer loop. It owns all
// protocol writes; the runner only carries them to the process.
type codexAppServerSession struct {
	request  Request
	emit     func(Event)
	input    chan<- string
	messages <-chan codexRPCMessage
	runDone  <-chan struct{}
	ctx      context.Context
	note     func(string)
	tick     func()
	onThread func(string)
	poll     time.Duration

	nextID          int64
	pendingUnphased *Event
	threadID        string
	turnID          string
	reply           string
	providerError   string
	started         bool
	completed       bool
	claim           *codexLiveClaim
	abandoned       map[int64]bool
}

func (session *codexAppServerSession) send(method string, params any) (int64, error) {
	session.nextID++
	return session.sendMessage(map[string]any{
		"jsonrpc": "2.0", "id": session.nextID, "method": method, "params": params,
	})
}

func (session *codexAppServerSession) sendNotification(method string, params any) error {
	_, err := session.sendMessage(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return err
}

func (session *codexAppServerSession) sendMessage(payload map[string]any) (int64, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	select {
	// app-server reads newline-delimited JSON-RPC; the runner writes each
	// Input value verbatim, so the framing is the adapter's job.
	case session.input <- string(encoded) + "\n":
		id, _ := payload["id"].(int64)
		return id, nil
	case <-session.runDone:
		return 0, fmt.Errorf("codex app-server exited")
	case <-session.ctx.Done():
		return 0, context.Cause(session.ctx)
	}
}

func (session *codexAppServerSession) requestWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id, err := session.send(method, params)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case message, ok := <-session.messages:
			if !ok {
				return nil, fmt.Errorf("%s: codex app-server stream closed", method)
			}
			if message.ID != nil && *message.ID == id && message.Method == "" {
				if message.Error != nil {
					return nil, fmt.Errorf("%s: %s", method, message.Error.Message)
				}
				return message.Result, nil
			}
			if err := session.handle(message); err != nil {
				return nil, err
			}
		case <-timer.C:
			return nil, fmt.Errorf("%s: timed out after %s", method, timeout)
		case <-session.runDone:
			return nil, fmt.Errorf("%s: codex app-server exited", method)
		case <-session.ctx.Done():
			return nil, context.Cause(session.ctx)
		}
	}
}

func (session *codexAppServerSession) run() error {
	initializeParams := map[string]any{
		"clientInfo": map[string]any{"name": "aha2", "version": "1"},
	}
	if _, err := session.requestWithTimeout("initialize", initializeParams, codexLiveHandshakeTimeout); err != nil {
		return err
	}
	if err := session.sendNotification("initialized", map[string]any{}); err != nil {
		return err
	}

	threadParams := map[string]any{
		"cwd": session.request.WorkDir, "model": session.request.Model,
		"modelProvider":  codexProviderID(session.request),
		"approvalPolicy": "never", "sandbox": codexSandbox(session.request.Filesystem),
	}
	threadMethod := "thread/start"
	if session.request.ProviderSessionID != "" {
		threadMethod = "thread/resume"
		threadParams["threadId"] = session.request.ProviderSessionID
	}
	response, err := session.requestWithTimeout(threadMethod, threadParams, codexLiveHandshakeTimeout)
	if err != nil {
		return err
	}
	session.threadID = nestedString(response, "thread", "id")
	if session.threadID == "" {
		return fmt.Errorf("%s returned no thread id", threadMethod)
	}
	if session.onThread != nil {
		session.onThread(session.threadID)
	}
	session.emit(Event{Type: "agent_session", Data: map[string]any{"provider_session_id": session.threadID}})

	response, err = session.requestWithTimeout("turn/start", map[string]any{
		"threadId": session.threadID,
		"cwd":      session.request.WorkDir,
		"input":    []any{map[string]any{"type": "text", "text": session.request.Prompt}},
	}, codexLiveHandshakeTimeout)
	if err != nil {
		return err
	}
	session.turnID = nestedString(response, "turn", "id")
	if session.turnID == "" {
		return fmt.Errorf("turn/start returned no turn id")
	}
	session.started = true
	if session.poll <= 0 {
		session.poll = defaultCodexLivePoll
	}
	return session.loop()
}

func (session *codexAppServerSession) loop() error {
	ticker := time.NewTicker(session.poll)
	defer ticker.Stop()
	for {
		select {
		case message, ok := <-session.messages:
			if !ok {
				return fmt.Errorf("codex app-server stream closed before the turn completed")
			}
			if err := session.handle(message); err != nil {
				return err
			}
			if session.completed {
				session.settleQueuedSteerResponses()
				return nil
			}
		case <-ticker.C:
			if session.tick != nil {
				session.tick()
			}
			session.expireClaim()
			session.maybeSteer()
		case <-session.runDone:
			// The final notification may be queued behind the exit signal.
			for {
				select {
				case message := <-session.messages:
					if err := session.handle(message); err != nil {
						return err
					}
					if session.completed {
						session.settleQueuedSteerResponses()
						return nil
					}
				default:
					return fmt.Errorf("codex app-server exited before the turn completed")
				}
			}
		case <-session.ctx.Done():
			return context.Cause(session.ctx)
		}
	}
}

// settleQueuedSteerResponses applies steer responses that were already queued
// when turn/completed arrived. Without it, an accepted steer whose response
// trails the completion would be rolled back and delivered again next Turn.
func (session *codexAppServerSession) settleQueuedSteerResponses() {
	for {
		select {
		case message := <-session.messages:
			if message.Method == "" && message.ID != nil {
				_ = session.handleResponse(message)
			}
		default:
			return
		}
	}
}

func (session *codexAppServerSession) handle(message codexRPCMessage) error {
	if message.Method != "" {
		if message.ID != nil {
			// app-server may ask the client to approve something. Approval is
			// disabled for AHA Turns, so answer a request we do not implement
			// instead of leaving the server blocked.
			_, _ = session.sendMessage(map[string]any{
				"jsonrpc": "2.0", "id": *message.ID,
				"error": map[string]any{"code": -32601, "message": "AHA has no handler for this app-server request"},
			})
			return nil
		}
		return session.handleNotification(message)
	}
	if message.ID == nil {
		return nil
	}
	return session.handleResponse(message)
}

func (session *codexAppServerSession) handleResponse(message codexRPCMessage) error {
	if session.claim != nil && *message.ID == session.claim.requestID {
		claim := session.claim
		session.claim = nil
		delivered := message.Error == nil
		session.ack(claim.batch, delivered)
		return nil
	}
	// A timed-out steer may still answer. Ignore it so a late success cannot
	// mark a message delivered after the claim was rolled back.
	delete(session.abandoned, *message.ID)
	return nil
}

type codexAppServerParams struct {
	ThreadID string         `json:"threadId"`
	TurnID   string         `json:"turnId"`
	Item     map[string]any `json:"item"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
	TokenUsage struct {
		Total map[string]any `json:"total"`
	} `json:"tokenUsage"`
	Message string `json:"message"`
}

func (session *codexAppServerSession) handleNotification(message codexRPCMessage) error {
	var params codexAppServerParams
	if len(message.Params) > 0 {
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return nil
		}
	}
	switch message.Method {
	case "item/started", "item/completed":
		if params.TurnID != "" && session.turnID != "" && params.TurnID != session.turnID {
			return nil
		}
		return session.handleItem(message.Method, params.Item)
	case "thread/tokenUsage/updated":
		if params.TurnID != "" && session.turnID != "" && params.TurnID != session.turnID {
			return nil
		}
		if usage := codexLiveUsage(params.TokenUsage.Total); len(usage) > 0 {
			session.flushPending(false)
			session.emit(Event{Type: "agent_usage", Data: map[string]any{"usage": usage}})
		}
		return nil
	case "turn/completed":
		if params.Turn.ID != "" && params.Turn.ID != session.turnID {
			return nil
		}
		session.flushPending(true)
		session.completed = true
		if strings.EqualFold(params.Turn.Status, "failed") || strings.EqualFold(params.Turn.Status, "interrupted") {
			message := "codex turn did not complete"
			if params.Turn.Error != nil && strings.TrimSpace(params.Turn.Error.Message) != "" {
				message = strings.TrimSpace(params.Turn.Error.Message)
			} else if session.providerError != "" {
				message = session.providerError
			}
			return errors.New(message)
		}
		return nil
	case "error":
		message := strings.TrimSpace(params.Message)
		if message == "" {
			message = "codex app-server reported an error"
		}
		session.providerError = message
		session.emit(Event{Type: "agent_error", Data: map[string]any{"message": message}})
		return nil
	default:
		return nil
	}
}

func (session *codexAppServerSession) handleItem(method string, item map[string]any) error {
	itemType, _ := item["type"].(string)
	if itemType == "" {
		return nil
	}
	switch itemType {
	case "agentMessage":
		if method != "item/completed" {
			return nil
		}
		text, _ := item["text"].(string)
		session.reply = text
		event := Event{Type: "agent_message", Data: map[string]any{"text": text}}
		phase, _ := item["phase"].(string)
		if phase != "" {
			event.Data["phase"] = phase
			event.Data["intermediate"] = phase == "commentary"
			event.Data["final"] = phase == "final_answer"
			session.flushPending(false)
		} else {
			session.flushPending(false)
			session.pendingUnphased = &event
			return nil
		}
		session.emit(event)
	case "reasoning":
		if method != "item/completed" {
			return nil
		}
		text := codexReasoningText(item)
		if text == "" {
			return nil
		}
		session.flushPending(false)
		if session.note != nil {
			session.note(text)
		}
		session.emit(Event{Type: "agent_reasoning", Data: map[string]any{"text": text, "reasoning": true}})
	case "commandExecution":
		session.flushPending(false)
		data := map[string]any{
			"tool_call_id": item["id"], "command": item["command"], "status": item["status"], "exit_code": item["exitCode"],
		}
		if method == "item/completed" {
			output, _ := item["aggregatedOutput"].(string)
			data["output_tail"] = tail(output, 2000)
			session.emit(Event{Type: "agent_command_finished", Data: data})
		} else {
			session.emit(Event{Type: "agent_command_started", Data: data})
		}
	}
	return nil
}

func (session *codexAppServerSession) flushPending(final bool) {
	if session.pendingUnphased == nil {
		return
	}
	session.pendingUnphased.Data["intermediate"] = !final
	session.pendingUnphased.Data["final"] = final
	session.emit(*session.pendingUnphased)
	session.pendingUnphased = nil
}

func (session *codexAppServerSession) maybeSteer() {
	if session.claim != nil || session.request.LiveInbox == nil || session.turnID == "" {
		return
	}
	batch, err := session.request.LiveInbox.Claim(session.ctx)
	if err != nil || len(batch.IDs) == 0 {
		return
	}
	if strings.TrimSpace(batch.Text) == "" {
		session.ack(batch, false)
		return
	}
	id, err := session.send("turn/steer", map[string]any{
		"threadId": session.threadID, "expectedTurnId": session.turnID,
		"input": []any{map[string]any{"type": "text", "text": batch.Text}},
	})
	if err != nil {
		session.ack(batch, false)
		return
	}
	session.claim = &codexLiveClaim{requestID: id, batch: batch, deadline: time.Now().Add(codexLiveSteerTimeout)}
}

func (session *codexAppServerSession) expireClaim() {
	if session.claim == nil || time.Now().Before(session.claim.deadline) {
		return
	}
	claim := session.claim
	session.claim = nil
	if session.abandoned == nil {
		session.abandoned = map[int64]bool{}
	}
	session.abandoned[claim.requestID] = true
	session.ack(claim.batch, false)
}

// rollbackClaim returns a steer the process never acknowledged. The message
// stays pending and the next Turn delivers it; the alternative is losing it
// when the process is interrupted between claim and acknowledgement.
func (session *codexAppServerSession) rollbackClaim() {
	if session.claim == nil {
		return
	}
	claim := session.claim
	session.claim = nil
	session.ack(claim.batch, false)
}

func (session *codexAppServerSession) ack(batch LiveBatch, delivered bool) {
	if session.request.LiveInbox == nil || len(batch.IDs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(session.ctx), 10*time.Second)
	defer cancel()
	_ = session.request.LiveInbox.Ack(ctx, batch, delivered)
}

func nestedString(raw json.RawMessage, path ...string) string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[key]
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func codexLiveUsage(total map[string]any) map[string]any {
	if len(total) == 0 {
		return nil
	}
	usage := map[string]any{}
	for source, target := range map[string]string{
		"inputTokens": "input_tokens", "cachedInputTokens": "cached_input_tokens",
		"outputTokens": "output_tokens", "reasoningOutputTokens": "reasoning_output_tokens",
		"totalTokens": "total_tokens",
	} {
		if value, ok := total[source]; ok {
			usage[target] = value
		}
	}
	return usage
}

// executeLiveInbox runs one Turn through app-server with live inbox steering.
// started reports whether turn/start succeeded; when it did not, no message was
// claimed and the caller may safely fall back to the one-shot exec path.
func (adapter Codex) executeLiveInbox(
	ctx context.Context, request Request, emit func(Event), binary string, timeout time.Duration,
) (result Result, err error, started bool) {
	idleWarning, idleTimeout, heartbeat := backendWatchdogDurations(adapter.IdleWarning, adapter.IdleTimeout, adapter.Heartbeat)
	catalogPath := adapter.ensureModelCatalog(ctx, request)
	environment := filterEnvironment(request.Environment)
	runContext, cancelRun := context.WithCancelCause(ctx)
	rollout := &codexRollout{
		home:   strings.TrimSpace(request.Environment["CODEX_HOME"]),
		thread: strings.TrimSpace(request.ProviderSessionID),
		runner: request.Runner,
	}
	rollout.prime(runContext)
	var emitMu sync.Mutex
	send := func(event Event) {
		if event.Type == "" || emit == nil {
			return
		}
		emitMu.Lock()
		emit(event)
		emitMu.Unlock()
	}
	activity := make(chan string, 1)
	monitorDone := make(chan struct{})
	go monitorBackendActivity(runContext, idleWarning, idleTimeout, heartbeat, activity, send, cancelRun, monitorDone)

	input := make(chan string)
	messages := make(chan codexRPCMessage, 256)
	runDone := make(chan struct{})
	runnerResult := make(chan workspace.Result, 1)
	runnerErr := make(chan error, 1)
	go func() {
		defer close(runDone)
		value, runFailure := request.Runner.Run(runContext, workspace.Command{
			Executable: binary, Args: codexAppServerArguments(request, catalogPath),
			Dir: request.WorkDir, Env: environment, Input: input,
			Timeout: timeout, KillTree: true, ReverseForward: request.ReverseForward,
		}, func(line string) {
			select {
			case activity <- "app_server":
			default:
			}
			var message codexRPCMessage
			if json.Unmarshal([]byte(line), &message) != nil {
				return
			}
			select {
			case messages <- message:
			case <-runContext.Done():
			case <-runDone:
			}
		})
		runnerResult <- value
		runnerErr <- runFailure
	}()

	session := &codexAppServerSession{
		request: request, emit: send, input: input, messages: messages,
		runDone: runDone, ctx: runContext, poll: adapter.LivePoll,
		note:     func(text string) { rollout.note(text) },
		onThread: func(thread string) { rollout.thread = thread },
		tick: func() {
			for _, event := range rollout.drain(runContext, false) {
				send(event)
			}
		},
	}
	sessionErr := session.run()
	started = session.started
	// Flush reasoning the app-server stream did not carry (raw reasoning,
	// empty summary) the same way the exec path does at process end.
	for _, event := range rollout.drain(runContext, true) {
		send(event)
	}
	close(input)

	select {
	case <-runDone:
	case <-time.After(codexLiveShutdownGrace):
		cancelRun(errors.New("codex app-server did not exit after the turn"))
		<-runDone
	}
	runResult := <-runnerResult
	runFailure := <-runnerErr
	cause := context.Cause(runContext)
	cancelRun(context.Canceled)
	<-monitorDone
	session.rollbackClaim()

	if errors.Is(cause, ErrBackendIdleTimeout) {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, ErrBackendIdleTimeout, started
	}
	if errors.Is(cause, context.Canceled) && ctx.Err() != nil {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, context.Canceled, started
	}
	if errors.Is(runFailure, workspace.ErrInputNotStreamed) {
		return Result{}, runFailure, false
	}
	if sessionErr != nil {
		if session.providerError != "" && !strings.Contains(sessionErr.Error(), session.providerError) {
			return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("%v: %s", sessionErr, session.providerError), started
		}
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, sessionErr, started
	}
	if runFailure != nil {
		return Result{Reply: session.reply, ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, runFailure, started
	}
	if strings.TrimSpace(session.reply) == "" {
		return Result{ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID}, fmt.Errorf("codex returned no agent message"), started
	}
	return Result{
		Reply: strings.TrimSpace(session.reply), ExitCode: runResult.ExitCode, ProviderSessionID: session.threadID,
	}, nil, started
}

// codexLiveInputEnabled is the effective live-inbox switch: the deployment
// force, the per-Task opt-in, and an actual inbox all have to be present.
func (adapter Codex) liveInputEnabled(request Request) bool {
	if request.LiveInbox == nil {
		return false
	}
	return adapter.LiveInput || request.StreamInput
}
