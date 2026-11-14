package knowledgevault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

var testKnowledgeSequence atomic.Int64

type testKnowledge struct {
	Scope     string
	ProjectID string
	ParentID  string
	Slug      string
	Type      string
	Title     string
	Body      string
}

func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "aha2.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, path
}

func createTestProject(t *testing.T, database *store.Store, id, name, projectType string) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.CreateProject(context.Background(), domain.Project{
		ID: id, Name: name, ProjectType: projectType, KnowledgePolicy: "enabled",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create project %s: %v", id, err)
	}
}

func createTestKnowledge(t *testing.T, database *store.Store, item testKnowledge) domain.KnowledgeEntry {
	t.Helper()
	now := time.Now().UTC()
	status := domain.KnowledgeVerified
	if item.Scope == "" {
		item.Scope = "project"
	}
	if item.Type == "" {
		item.Type = "practice"
	}
	entry := domain.KnowledgeEntry{
		ID:    fmt.Sprintf("knowledge_%s_%s_%d", item.Scope, item.Slug, testKnowledgeSequence.Add(1)),
		Scope: item.Scope, ProjectID: item.ProjectID, ParentID: item.ParentID,
		Slug: item.Slug, Type: item.Type, Title: item.Title, Body: item.Body,
		Status: status, Confidence: 0.9, Revision: 1,
		CreatedAt: now, UpdatedAt: now, LastVerifiedAt: now,
	}
	if item.Scope == "global" {
		entry.ProjectID = ""
	}
	if err := database.CreateKnowledge(context.Background(), entry); err != nil {
		t.Fatalf("create knowledge %s: %v", item.Slug, err)
	}
	return entry
}

func seedStandardVaultDatabase(t *testing.T, database *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := database.EnsureGlobalKnowledgeCategories(ctx); err != nil {
		t.Fatalf("ensure global categories: %v", err)
	}
	createTestKnowledge(t, database, testKnowledge{
		Scope: "global", ParentID: store.GlobalGeneralKnowledgeID, Slug: "shared-deploy",
		Type: "practice", Title: "共享部署经验", Body: "部署前先验证候选版本。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "global", ParentID: store.GlobalTechnicalLessonsKnowledgeID, Slug: "serial-login",
		Type: "diagnostic", Title: "串口登录等待提示", Body: "等待 login 提示后再发送凭据。参考 [[shared-deploy]]。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "global", ParentID: store.GlobalBehaviorLessonsKnowledgeID, Slug: "verify-first",
		Type: "practice", Title: "先验证再提交", Body: "提交前运行测试。",
	})

	createTestProject(t, database, "project-demo", "Demo Project", "folder")
	demoRoot, err := database.EnsureKnowledgeRoot(ctx, "project", "project-demo")
	if err != nil {
		t.Fatalf("ensure demo knowledge root: %v", err)
	}
	group := createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-demo", ParentID: demoRoot.ID,
		Slug: "navigation-group", Type: "navigation", Title: "导航组", Body: "模块入口。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-demo", ParentID: group.ID,
		Slug: "module-a", Type: "reference", Title: "模块 A", Body: "模块 A 的说明。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-demo", ParentID: group.ID,
		Slug: "module-b", Type: "decision", Title: "模块 B 决策", Body: "选择方案 B。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-demo", ParentID: demoRoot.ID,
		Slug: "inbox-one", Type: "practice", Title: "收件箱一", Body: "根下叶子一。",
	})
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-demo", ParentID: demoRoot.ID,
		Slug: "inbox-two", Type: "diagnostic", Title: "收件箱二", Body: "根下叶子二。",
	})

	createTestProject(t, database, "project-archive", "AHA1 Archive", "knowledge")
	archiveRoot, err := database.EnsureKnowledgeRoot(ctx, "project", "project-archive")
	if err != nil {
		t.Fatalf("ensure archive knowledge root: %v", err)
	}
	createTestKnowledge(t, database, testKnowledge{
		Scope: "project", ProjectID: "project-archive", ParentID: archiveRoot.ID,
		Slug: "legacy-note", Type: "general", Title: "旧版笔记", Body: "AHA1 归档内容。",
	})
}

// execTestSQL opens a second connection so tests can create states the store
// validation would reject (malicious slugs imported from older databases).
func execTestSQL(t *testing.T, databasePath, query string, args ...any) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func readExportedFile(t *testing.T, vaultPath, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vaultPath, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func exportedTreeHash(t *testing.T, vaultPath string) string {
	t.Helper()
	var combined string
	err := filepath.WalkDir(vaultPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(vaultPath, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == filepath.ToSlash(filepath.Join(stateDirectory, exportStateFile)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		combined += relative + "\x00" + fmt.Sprintf("%x", sha256.Sum256(data)) + "\n"
		return nil
	})
	if err != nil {
		t.Fatalf("hash tree: %v", err)
	}
	return combined
}
