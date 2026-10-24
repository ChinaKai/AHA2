package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestSafeProbeDetailPrefersStderrAndKeepsExitCode(t *testing.T) {
	item := domain.Workspace{}
	detail := safeProbeDetail(item, Result{ExitCode: 1, Stderr: "error: --profile <name> is required\n"}, nil)
	if detail != "exit code 1: error: --profile <name> is required" {
		t.Fatalf("unexpected detail: %q", detail)
	}
}

func TestSafeProbeDetailFallsBackToStdout(t *testing.T) {
	item := domain.Workspace{}
	detail := safeProbeDetail(item, Result{ExitCode: 1, Stdout: "unknown option: --version\n"}, nil)
	if detail != "exit code 1: unknown option: --version" {
		t.Fatalf("unexpected detail: %q", detail)
	}
}

func TestSafeProbeDetailFallsBackToErrorAndExitCode(t *testing.T) {
	item := domain.Workspace{}
	if detail := safeProbeDetail(item, Result{ExitCode: 127}, errors.New("exec: \"dsh\": executable file not found in $PATH")); detail != "exit code 127: exec: \"dsh\": executable file not found in $PATH" {
		t.Fatalf("unexpected detail: %q", detail)
	}
	if detail := safeProbeDetail(item, Result{ExitCode: 1}, nil); detail != "exit code 1" {
		t.Fatalf("unexpected detail: %q", detail)
	}
}

func TestSafeProbeDetailMasksCredentialsAndTruncates(t *testing.T) {
	item := domain.Workspace{}
	detail := safeProbeDetail(item, Result{ExitCode: 1, Stderr: "failed with sk-ant-abcdefghijklmnop token"}, nil)
	if strings.Contains(detail, "sk-ant-abcdefghijklmnop") || !strings.Contains(detail, "[已隐藏]") {
		t.Fatalf("credential not masked: %q", detail)
	}

	long := safeProbeDetail(item, Result{ExitCode: 1, Stderr: strings.Repeat("x", 400)}, nil)
	if runes := []rune(long); len(runes) > 240 {
		t.Fatalf("detail not truncated: %d runes", len(runes))
	}
}
