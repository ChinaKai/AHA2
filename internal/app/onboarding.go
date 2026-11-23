package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrOnboardingDirectory   = errors.New("onboarding_directory_unavailable")
	ErrOnboardingWorkspace   = errors.New("onboarding_workspace_unavailable")
	ErrOnboardingModel       = errors.New("onboarding_model_unavailable")
	ErrOnboardingCLI         = errors.New("onboarding_cli_missing")
	ErrOnboardingPermissions = errors.New("onboarding_permissions_mismatch")
)

type OnboardingModel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Backend   string    `json:"backend"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OnboardingView struct {
	DismissedAt       *time.Time         `json:"onboarding_dismissed_at"`
	ShowAutomatically bool               `json:"show_automatically"`
	BackendCLI        map[string]bool    `json:"backend_cli"`
	Models            []OnboardingModel  `json:"models"`
	Workspaces        []domain.Workspace `json:"workspaces"`
	TaskID            string             `json:"task_id,omitempty"`
	TaskWorkspaceID   string             `json:"task_workspace_id,omitempty"`
	TaskError         string             `json:"task_error,omitempty"`
}

func (s *Service) onboardingCLIFound(backend string) bool {
	binary := s.backendBinary(backend)
	if binary == "" {
		binary = backend
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

func (s *Service) Onboarding(ctx context.Context) (OnboardingView, error) {
	dismissedAt, automatic, err := s.store.OnboardingVisibility(ctx)
	if err != nil {
		return OnboardingView{}, err
	}
	view := OnboardingView{
		DismissedAt: dismissedAt, ShowAutomatically: automatic,
		BackendCLI: map[string]bool{"codex": s.onboardingCLIFound("codex"), "claude": s.onboardingCLIFound("claude"), "dsh": s.onboardingCLIFound("dsh")},
		Models:     []OnboardingModel{}, Workspaces: []domain.Workspace{},
	}
	models, err := s.store.ListModels(ctx)
	if err != nil {
		return OnboardingView{}, err
	}
	for _, model := range models {
		if _, supported := view.BackendCLI[model.Backend]; supported && onboardingProviderModel(model) {
			view.Models = append(view.Models, OnboardingModel{ID: model.ID, Name: model.DisplayName, Backend: model.Backend, UpdatedAt: model.UpdatedAt})
		}
	}
	view.Workspaces, err = s.onboardingWorkspaces(ctx)
	if err != nil {
		return OnboardingView{}, err
	}
	task, err := s.store.OnboardingTask(ctx)
	if err != nil {
		return OnboardingView{}, err
	}
	if task.ID != "" {
		if err := s.verifyOnboardingTask(ctx, task); err != nil {
			view.TaskError = ErrOnboardingPermissions.Error()
		} else {
			view.TaskID, view.TaskWorkspaceID = task.ID, task.WorkspaceID
		}
	}
	return view, nil
}

func onboardingProviderModel(model domain.Model) bool {
	return model.Source == "" || model.Source == domain.ModelSourceProvider
}

func (s *Service) onboardingWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	workspaces, err := s.store.ListWorkspaces(ctx, "")
	if err != nil {
		return nil, err
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	localProjects := make(map[string]bool, len(projects))
	for _, project := range projects {
		localProjects[project.ID] = !domain.IsSystemManagedProject(project) && project.ProjectType != "knowledge"
	}
	result := make([]domain.Workspace, 0)
	for _, workspace := range workspaces {
		if localProjects[workspace.ProjectID] && onboardingLocalWorkspace(workspace) && onboardingCheckDirectory(workspace.RootPath) == nil {
			result = append(result, workspace)
		}
	}
	return result, nil
}

func onboardingLocalWorkspace(workspace domain.Workspace) bool {
	return !workspace.ReadOnly && !workspace.ChannelRetired && workspace.Locality == "local" && workspace.Transport == "native"
}

func onboardingCheckDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return ErrOnboardingDirectory
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOnboardingDirectory, err)
	}
	if !info.IsDir() {
		return ErrOnboardingDirectory
	}
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOnboardingDirectory, err)
	}
	return dir.Close()
}

func (s *Service) CreateOnboardingWorkspace(ctx context.Context, name string, rootPath *string) (domain.Workspace, error) {
	s.onboardingMu.Lock()
	defer s.onboardingMu.Unlock()

	workspaces, err := s.onboardingWorkspaces(ctx)
	if err != nil {
		return domain.Workspace{}, err
	}
	root := ""
	if rootPath != nil {
		root = strings.TrimSpace(*rootPath)
		if err := onboardingCheckDirectory(root); err != nil {
			return domain.Workspace{}, err
		}
		root = filepath.Clean(root)
	}
	if len(workspaces) > 0 {
		// Another tab may have finished this step while the owner was
		// submitting it. Return a workspace already suitable for the guide.
		return workspaces[0], nil
	}
	if name = strings.TrimSpace(name); name == "" {
		name = "Starter Workspace"
	}
	now := s.now().UTC()
	workspaceID := domain.NewID("workspace_starter")
	project := domain.Project{
		ID: domain.NewID("project"), Name: name, ProjectType: "folder",
		DefaultWorkspaceID: workspaceID, KnowledgePolicy: "inherit", CreatedAt: now, UpdatedAt: now,
	}
	managed := root == ""
	if managed {
		parent := filepath.Join(s.store.DataDir(), "starter-workspaces")
		root = filepath.Join(parent, workspaceID)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return domain.Workspace{}, err
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			return domain.Workspace{}, err
		}
		readme := filepath.Join(root, "README.md")
		defer func() {
			if _, err := s.store.Workspace(context.WithoutCancel(ctx), workspaceID); errors.Is(err, sql.ErrNoRows) {
				_ = os.Remove(readme)
				_ = os.Remove(root)
			}
		}()
		content := "# AHA2 Starter Workspace\n\nThis folder is managed by AHA2. Your first task can read this file.\n\nWorkspace ID: " + workspaceID + "\n"
		if err := os.WriteFile(readme, []byte(content), 0o600); err != nil {
			return domain.Workspace{}, err
		}
	}
	workspace := domain.Workspace{
		ID: workspaceID, ProjectID: project.ID, Name: name, RootPath: root,
		Locality: "local", Transport: "native", Health: "ready", AgentAPIMode: "auto", SSHPort: 22,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.createProject(ctx, project, func(ctx context.Context, project domain.Project) error {
		return s.store.CreateProjectWithWorkspace(ctx, project, workspace)
	})
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) CreateOnboardingTask(ctx context.Context, workspaceID, modelID, title, request string) (domain.Task, error) {
	s.onboardingMu.Lock()
	defer s.onboardingMu.Unlock()

	task, err := s.store.OnboardingTask(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	if task.ID == "" {
		workspace, err := s.store.Workspace(ctx, workspaceID)
		if err != nil || !onboardingLocalWorkspace(workspace) {
			return domain.Task{}, ErrOnboardingWorkspace
		}
		project, err := s.store.Project(ctx, workspace.ProjectID)
		if err != nil || domain.IsSystemManagedProject(project) || project.ProjectType == "knowledge" {
			return domain.Task{}, ErrOnboardingWorkspace
		}
		if err := onboardingCheckDirectory(workspace.RootPath); err != nil {
			return domain.Task{}, err
		}
		model, err := s.store.Model(ctx, modelID)
		if err != nil || !onboardingProviderModel(model) {
			return domain.Task{}, ErrOnboardingModel
		}
		switch model.Backend {
		case "codex", "claude", "dsh":
		default:
			return domain.Task{}, ErrOnboardingModel
		}
		if !s.onboardingCLIFound(model.Backend) {
			return domain.Task{}, ErrOnboardingCLI
		}
		autoSkills := false
		task, err = s.CreateTask(ctx, CreateTaskInput{
			ID: domain.NewID("task_starter"), ProjectID: workspace.ProjectID, WorkspaceID: workspace.ID,
			Title: title, Request: request, Backend: model.Backend, ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
			Filesystem: "read-only", Approval: "never", Isolation: "inplace", CollaborationMode: "single", MaxAgents: 1,
			StartMode: "manual", ApplyAutoSkills: &autoSkills,
		})
		if err != nil {
			return domain.Task{}, err
		}
		task, err = s.store.Task(ctx, task.ID)
		if err != nil {
			return domain.Task{}, err
		}
	}
	if err := s.verifyOnboardingTask(ctx, task); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (s *Service) verifyOnboardingTask(ctx context.Context, task domain.Task) error {
	if task.Isolation != "inplace" || task.CollaborationMode != "single" || task.MaxAgents != 1 {
		return ErrOnboardingPermissions
	}
	agent, err := s.store.TaskAgent(ctx, task.ID, "main")
	if err != nil {
		return err
	}
	snapshot, err := s.store.RuntimeSnapshot(ctx, agent.RuntimeConfigSnapshotID)
	if err != nil {
		return err
	}
	var permissions map[string]string
	if json.Unmarshal([]byte(snapshot.PermissionsJSON), &permissions) != nil ||
		permissions["filesystem"] != "read-only" || permissions["approval"] != "never" {
		return ErrOnboardingPermissions
	}
	return nil
}
