package app

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/ChinaKai/AHA2/internal/workspace"
)

// Remote runners provide process transport. The fixed worker uses directory
// file descriptors and argv-based Git execution. Input travels on stdin.
//
//go:embed task_workspace_viewer_remote.py
var viewerRemoteWorker string

type remoteViewerFS struct {
	root   string
	runner workspace.Runner
}

type viewerRemoteRequest struct {
	Root            string   `json:"root"`
	Op              string   `json:"op"`
	Path            string   `json:"path,omitempty"`
	Args            []string `json:"args,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	ReadLineEndings bool     `json:"read_line_endings,omitempty"`
}

type viewerRemoteResponse struct {
	Error     string     `json:"error"`
	Marker    bool       `json:"marker"`
	Tree      viewerTree `json:"tree"`
	Data      []byte     `json:"data"`
	Exit      int        `json:"exit"`
	Truncated bool       `json:"truncated"`
}

func (source remoteViewerFS) request(ctx context.Context, req viewerRemoteRequest) (viewerRemoteResponse, error) {
	req.Root = source.root
	input, err := json.Marshal(req)
	if err != nil {
		return viewerRemoteResponse{}, err
	}
	limit := max(req.Limit, viewerFileLimit)*2 + 4096
	var frame strings.Builder
	result, err := source.runner.Run(ctx, workspace.Command{
		Executable: "python3", Args: []string{"-I", "-c", viewerRemoteWorker},
		Stdin: string(input), OutputLimit: 4096,
	}, func(line string) {
		if frame.Len() <= limit {
			frame.WriteString(line)
		}
	})
	if err != nil || result.ExitCode != 0 || frame.Len() > limit {
		return viewerRemoteResponse{}, ErrViewerTransport
	}
	decoded, err := base64.StdEncoding.DecodeString(frame.String())
	if err != nil {
		return viewerRemoteResponse{}, ErrViewerTransport
	}
	var response viewerRemoteResponse
	if err := json.Unmarshal(decoded, &response); err != nil {
		return viewerRemoteResponse{}, ErrViewerTransport
	}
	switch response.Error {
	case "", "large":
		return response, nil
	case "root":
		return response, ErrViewerUnavailable
	case "missing":
		return response, ErrViewerMissing
	case "path":
		return response, ErrViewerPath
	case "git":
		return response, ErrViewerGit
	default:
		return response, ErrViewerTransport
	}
}

func (source remoteViewerFS) available(ctx context.Context) error {
	_, err := source.request(ctx, viewerRemoteRequest{Op: "root"})
	return err
}

func (source remoteViewerFS) gitMarker(ctx context.Context) (bool, error) {
	result, err := source.request(ctx, viewerRemoteRequest{Op: "marker"})
	return result.Marker, err
}

func (source remoteViewerFS) tree(ctx context.Context, rel string) (viewerTree, error) {
	result, err := source.request(ctx, viewerRemoteRequest{Op: "tree", Path: rel, Limit: 4 << 20})
	return result.Tree, err
}

func (source remoteViewerFS) file(ctx context.Context, rel string) (viewerContent, error) {
	result, err := source.request(ctx, viewerRemoteRequest{Op: "file", Path: rel, Limit: viewerFileLimit})
	if result.Error == "large" {
		return viewerContent{Placeholder: "too_large"}, nil
	}
	return viewerText(result.Data), err
}

func (source remoteViewerFS) git(ctx context.Context, limit int, readLineEndings bool, args ...string) (viewerGitOutput, error) {
	result, err := source.request(ctx, viewerRemoteRequest{Op: "git", Args: args, Limit: limit, ReadLineEndings: readLineEndings})
	return viewerGitOutput{data: result.Data, exit: result.Exit, truncated: result.Truncated}, err
}
