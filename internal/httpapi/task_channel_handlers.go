package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (s *Server) taskChannelRoutes(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	session, _ := sessionFromContext(request.Context())
	destinations, route, err := s.channels.TaskChannelRoutes(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_routes_unavailable", "message": err.Error()})
		return
	}
	var activeRoute any
	contacts := []domain.ChannelContact{}
	members := []domain.ChannelGroupMember{}
	if route.ID != "" {
		activeRoute = route
		contacts, err = s.channels.OwnerTaskChannelContacts(request.Context(), session.OwnerID, request.PathValue("id"))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_contacts_unavailable", "message": err.Error()})
			return
		}
		members, err = s.channels.OwnerTaskChannelMembers(request.Context(), session.OwnerID, request.PathValue("id"))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_members_unavailable", "message": err.Error()})
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "destinations": destinations, "route": activeRoute, "contacts": contacts, "members": members})
}

func (s *Server) bindTaskChannelRoute(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	var payload struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := decodeJSON(request, &payload); err != nil || strings.TrimSpace(payload.ConversationID) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_task_channel_route")
		return
	}
	session, _ := sessionFromContext(request.Context())
	route, err := s.channels.BindTaskChannelRoute(request.Context(), session.OwnerID, request.PathValue("id"), payload.ConversationID)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_route_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.channel.bind", "task", route.TargetTaskID, map[string]any{"route_id": route.ID, "instance_id": route.InstanceID, "conversation_id": route.ConversationID})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "route": route})
}

func (s *Server) unbindTaskChannelRoute(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	session, _ := sessionFromContext(request.Context())
	taskID, routeID := request.PathValue("id"), request.PathValue("route")
	if err := s.channels.UnbindTaskChannelRoute(request.Context(), session.OwnerID, taskID, routeID); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_unbind_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.channel.unbind", "task", taskID, map[string]any{"route_id": routeID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "route": domain.ChannelTaskRoute{ID: routeID, TargetTaskID: taskID, State: "exited"}})
}

func (s *Server) refreshTaskChannelMembers(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	session, _ := sessionFromContext(request.Context())
	command, err := s.channels.RefreshOwnerTaskChannelMembers(request.Context(), session.OwnerID, request.PathValue("id"))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_member_refresh_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.channel.members.refresh", "task", request.PathValue("id"), map[string]any{"command_id": command.ID})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current, lookupErr := s.store.ChannelCommand(request.Context(), command.InstanceID, command.ID)
		if lookupErr == nil && current.State == "completed" {
			members, membersErr := s.channels.OwnerTaskChannelMembers(request.Context(), session.OwnerID, request.PathValue("id"))
			if membersErr != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_members_unavailable", "message": membersErr.Error()})
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{
				"ok": true, "completed": true,
				"command": map[string]any{"id": current.ID, "state": current.State},
				"members": members,
			})
			return
		}
		if lookupErr == nil && (current.State == "failed" || current.State == "dead_letter" || current.State == "cancelled") {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_member_refresh_failed", "message": current.LastErrorCode})
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"ok": true, "completed": false,
		"command": map[string]any{"id": command.ID, "state": command.State},
	})
}

func (s *Server) updateTaskChannelContacts(writer http.ResponseWriter, request *http.Request) {
	if s.channels == nil {
		writeError(writer, http.StatusServiceUnavailable, "channels_unavailable")
		return
	}
	var payload struct {
		Contacts []struct {
			IdentityLinkID    string `json:"identity_link_id"`
			CollaborationRole string `json:"collaboration_role"`
		} `json:"contacts"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_task_channel_contacts")
		return
	}
	contacts := make([]store.TaskChannelContactInput, 0, len(payload.Contacts))
	for _, item := range payload.Contacts {
		contacts = append(contacts, store.TaskChannelContactInput{
			IdentityLinkID:    strings.TrimSpace(item.IdentityLinkID),
			CollaborationRole: strings.TrimSpace(item.CollaborationRole),
		})
	}
	session, _ := sessionFromContext(request.Context())
	updated, err := s.channels.SetOwnerTaskChannelContacts(request.Context(), session.OwnerID, request.PathValue("id"), contacts)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "task_channel_contacts_update_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.channel.contacts.update", "task", request.PathValue("id"), map[string]any{"contacts": len(updated)})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "contacts": updated})
}
