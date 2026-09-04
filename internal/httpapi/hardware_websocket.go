package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type hardwareWSMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type hardwareWSRead struct {
	messageType websocket.MessageType
	data        []byte
	err         error
}

func (s *Server) hardwareTerminalWebSocket(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	status := s.hardware.Status(task.ID, group.ID, transport, readOnly)
	if !status.Connected {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "hardware_not_connected", "message": "请先连接硬件终端",
		})
		return
	}
	live, unsubscribe, err := s.hardware.Subscribe(task.ID, group.ID, transport)
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "hardware_subscribe_failed", "message": err.Error(),
		})
		return
	}
	defer unsubscribe()
	afterSequence := queryInt(request, "after", 0)
	if afterSequence < 0 {
		afterSequence = 0
	}
	page, err := s.store.HardwareIOPage(request.Context(), task.ID, group.ID, transport, afterSequence, 1000)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_io_failed")
		return
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		InsecureSkipVerify: s.allowCrossOrigin,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	connection.SetReadLimit(64 * 1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := writeHardwareWSJSON(ctx, connection, map[string]any{
		"type": "ready", "status": status, "latest_sequence": page.LatestSequence,
	}); err != nil {
		return
	}
	for _, item := range page.Items {
		if item.Direction != "rx" || item.Data == "" {
			continue
		}
		if err := writeHardwareWS(ctx, connection, websocket.MessageBinary, []byte(item.Data)); err != nil {
			return
		}
	}
	latestSequence := max(afterSequence, page.LatestSequence)
	incoming := make(chan hardwareWSRead, 1)
	go func() {
		for {
			messageType, data, readErr := connection.Read(ctx)
			select {
			case incoming <- hardwareWSRead{messageType: messageType, data: data, err: readErr}:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-live:
			switch event.Type {
			case "output":
				if event.Sequence > 0 && event.Sequence <= latestSequence {
					continue
				}
				if event.Sequence > latestSequence {
					latestSequence = event.Sequence
				}
				if err := writeHardwareWS(ctx, connection, websocket.MessageBinary, event.Data); err != nil {
					return
				}
			case "status":
				if err := writeHardwareWSJSON(ctx, connection, map[string]any{"type": "status", "status": event.Status}); err != nil {
					return
				}
				if !event.Status.Connected {
					return
				}
			}
		case message := <-incoming:
			if message.err != nil {
				code := websocket.CloseStatus(message.err)
				if code == websocket.StatusNormalClosure || code == websocket.StatusGoingAway {
					return
				}
				return
			}
			if message.messageType == websocket.MessageBinary {
				if err := s.hardware.SendRaw(task.ID, group.ID, transport, message.data, "websocket"); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
				continue
			}
			var control hardwareWSMessage
			if err := json.Unmarshal(message.data, &control); err != nil {
				_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": "invalid control message"})
				continue
			}
			switch control.Type {
			case "input":
				if err := s.hardware.SendRaw(task.ID, group.ID, transport, []byte(control.Data), "websocket"); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
			case "resize":
				if err := s.hardware.Resize(task.ID, group.ID, transport, control.Cols, control.Rows); err != nil {
					_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": err.Error()})
				}
			case "close":
				return
			default:
				_ = writeHardwareWSJSON(ctx, connection, map[string]any{"type": "error", "message": "unknown control message"})
			}
		}
	}
}

func writeHardwareWSJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeHardwareWS(ctx, connection, websocket.MessageText, data)
}

func writeHardwareWS(ctx context.Context, connection *websocket.Conn, messageType websocket.MessageType, data []byte) error {
	writeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := connection.Write(writeContext, messageType, data)
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return err
}
