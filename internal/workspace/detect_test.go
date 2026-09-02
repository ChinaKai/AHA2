package workspace

import (
	"runtime"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSameWorkspaceRootRejectsNestedDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		item := domain.Workspace{RootPath: `E:\repo\nested`, Locality: "local", Transport: "native"}
		if sameWorkspaceRoot(item, `E:/repo`) {
			t.Fatal("nested directory was treated as repository root")
		}
		if !sameWorkspaceRoot(domain.Workspace{RootPath: `E:\repo`, Locality: "local"}, `E:/repo`) {
			t.Fatal("equivalent Windows roots did not match")
		}
		return
	}
	item := domain.Workspace{RootPath: "/repo/nested", Locality: "local", Transport: "native"}
	if sameWorkspaceRoot(item, "/repo") {
		t.Fatal("nested directory was treated as repository root")
	}
}

func TestSameWorkspaceRootRemote(t *testing.T) {
	t.Parallel()
	item := domain.Workspace{RootPath: "/srv/project", Locality: "remote", Transport: "ssh"}
	if !sameWorkspaceRoot(item, "/srv/project/") {
		t.Fatal("equivalent remote roots did not match")
	}
	if sameWorkspaceRoot(item, "/srv") {
		t.Fatal("remote parent repository was accepted")
	}
}
