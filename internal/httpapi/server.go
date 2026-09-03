package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const sessionCookieName = "aha2_session"

type Config struct {
	Store            *store.Store
	Auth             *auth.Service
	App              *app.Service
	Web              fs.FS
	Logger           *slog.Logger
	SecureCookie     bool
	AllowCrossOrigin bool
	DetectWorkspace  func(context.Context, domain.Workspace) (domain.Workspace, error)
	Secrets          SecretStore
}

type Server struct {
	store            *store.Store
	auth             *auth.Service
	app              *app.Service
	web              fs.FS
	logger           *slog.Logger
	secureCookie     bool
	allowCrossOrigin bool
	detectWorkspace  func(context.Context, domain.Workspace) (domain.Workspace, error)
	secrets          SecretStore
	authLimiter      *authLimiter
}

func New(config Config) *Server {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store: config.Store, auth: config.Auth, app: config.App, web: config.Web,
		logger: logger, secureCookie: config.SecureCookie, allowCrossOrigin: config.AllowCrossOrigin,
		detectWorkspace: config.DetectWorkspace,
		secrets:         config.Secrets,
		authLimiter:     newAuthLimiter(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/v1/auth/register", s.authRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.authLogin)
	mux.Handle("POST /api/v1/auth/logout", s.withAuth(http.HandlerFunc(s.authLogout)))

	mux.Handle("GET /api/v1/system", s.withAuth(http.HandlerFunc(s.systemInfo)))
	mux.Handle("GET /api/v1/projects", s.withAuth(http.HandlerFunc(s.listProjects)))
	mux.Handle("POST /api/v1/projects", s.withAuth(http.HandlerFunc(s.createProject)))
	mux.Handle("PUT /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.updateProject)))
	mux.Handle("DELETE /api/v1/projects/{id}", s.withAuth(http.HandlerFunc(s.deleteProject)))
	mux.Handle("GET /api/v1/workspaces", s.withAuth(http.HandlerFunc(s.listWorkspaces)))
	mux.Handle("POST /api/v1/workspaces", s.withAuth(http.HandlerFunc(s.createWorkspace)))
	mux.Handle("PUT /api/v1/workspaces/{id}", s.withAuth(http.HandlerFunc(s.updateWorkspace)))
	mux.Handle("DELETE /api/v1/workspaces/{id}", s.withAuth(http.HandlerFunc(s.deleteWorkspace)))
	mux.Handle("POST /api/v1/workspaces/{id}/detect", s.withAuth(http.HandlerFunc(s.detectWorkspaceHandler)))
	mux.Handle("GET /api/v1/providers", s.withAuth(http.HandlerFunc(s.listProviders)))
	mux.Handle("POST /api/v1/providers", s.withAuth(http.HandlerFunc(s.createProvider)))
	mux.Handle("PUT /api/v1/providers/{id}", s.withAuth(http.HandlerFunc(s.updateProvider)))
	mux.Handle("DELETE /api/v1/providers/{id}", s.withAuth(http.HandlerFunc(s.deleteProvider)))
	mux.Handle("GET /api/v1/models", s.withAuth(http.HandlerFunc(s.listModels)))
	mux.Handle("POST /api/v1/models", s.withAuth(http.HandlerFunc(s.createModel)))
	mux.Handle("PUT /api/v1/models/{id}", s.withAuth(http.HandlerFunc(s.updateModel)))
	mux.Handle("DELETE /api/v1/models/{id}", s.withAuth(http.HandlerFunc(s.deleteModel)))
	mux.Handle("GET /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.listEnvGroups)))
	mux.Handle("POST /api/v1/env-groups", s.withAuth(http.HandlerFunc(s.createEnvGroup)))
	mux.Handle("POST /api/v1/providers/detect-models", s.withAuth(http.HandlerFunc(s.detectModelsHandler)))
	mux.Handle("POST /api/v1/providers/add-models", s.withAuth(http.HandlerFunc(s.addModelsHandler)))
	mux.Handle("GET /api/v1/prompts/templates", s.withAuth(http.HandlerFunc(s.promptTemplates)))
	mux.Handle("PUT /api/v1/prompts/templates/{id}", s.withAuth(http.HandlerFunc(s.updatePromptTemplate)))
	mux.Handle("POST /api/v1/prompts/templates/{id}/reset", s.withAuth(http.HandlerFunc(s.resetPromptTemplate)))

	mux.Handle("GET /api/v1/tasks", s.withAuth(http.HandlerFunc(s.listTasks)))
	mux.Handle("POST /api/v1/tasks", s.withAuth(http.HandlerFunc(s.createTask)))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.deleteTask)))
	mux.Handle("GET /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.taskDetail)))
	mux.Handle("GET /api/v1/tasks/{id}/conversation", s.withAuth(http.HandlerFunc(s.taskConversation)))
	mux.Handle("GET /api/v1/tasks/{id}/context", s.withAuth(http.HandlerFunc(s.taskContext)))
	mux.Handle("POST /api/v1/tasks/{id}/messages", s.withAuth(http.HandlerFunc(s.submitMessage)))
	mux.Handle("GET /api/v1/tasks/{id}/agents", s.withAuth(http.HandlerFunc(s.taskAgents)))
	mux.Handle("PATCH /api/v1/tasks/{id}/collaboration", s.withAuth(http.HandlerFunc(s.updateTaskCollaboration)))
	mux.Handle("GET /api/v1/tasks/{id}/agents/{agent}/conversation", s.withAuth(http.HandlerFunc(s.agentConversation)))
	mux.Handle("GET /api/v1/tasks/{id}/agents/{agent}/context", s.withAuth(http.HandlerFunc(s.agentContext)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/messages", s.withAuth(http.HandlerFunc(s.submitAgentMessage)))
	mux.Handle("PATCH /api/v1/tasks/{id}/agents/{agent}", s.withAuth(http.HandlerFunc(s.updateAgentConfig)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/session/compact", s.withAuth(http.HandlerFunc(s.compactAgentSession)))
	mux.Handle("POST /api/v1/tasks/{id}/agents/{agent}/session/reset", s.withAuth(http.HandlerFunc(s.resetAgentSession)))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.withAuth(http.HandlerFunc(s.updateTaskTitle)))
	mux.Handle("POST /api/v1/tasks/{id}/complete", s.withAuth(http.HandlerFunc(s.completeTask)))
	mux.Handle("POST /api/v1/tasks/{id}/reopen", s.withAuth(http.HandlerFunc(s.reopenTask)))
	mux.Handle("POST /api/v1/turns/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptTurn)))
	mux.Handle("POST /api/v1/rounds/{id}/interrupt", s.withAuth(http.HandlerFunc(s.interruptRound)))
	mux.Handle("GET /api/v1/tasks/{id}/events", s.withAuth(http.HandlerFunc(s.taskEvents)))
	mux.Handle("GET /api/v1/events", s.withAuth(http.HandlerFunc(s.allEvents)))

	mux.Handle("GET /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.listKnowledge)))
	mux.Handle("POST /api/v1/knowledge", s.withAuth(http.HandlerFunc(s.createKnowledge)))
	mux.Handle("POST /api/v1/knowledge/{id}/verify", s.withAuth(http.HandlerFunc(s.verifyKnowledge)))

	mux.HandleFunc("/", s.serveWeb)
	return s.securityHeaders(s.requestLog(mux))
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'")
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		next.ServeHTTP(writer, request)
		if !strings.HasPrefix(request.URL.Path, "/assets/") {
			s.logger.Debug("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(start))
		}
	})
}
