package httpapi

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/knowledgevault"
)

// knowledgeVaultFiles lists the real vault directory tree. It deliberately does
// not depend on the index: a file that was added or edited externally is
// visible immediately, and clicking it reads the file directly.
func (s *Server) knowledgeVaultFiles(writer http.ResponseWriter, request *http.Request) {
	relative := strings.TrimSpace(request.URL.Query().Get("path"))
	var result knowledgevault.VaultFileList
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		result, readErr = reader.Files(relative)
		return readErr
	})
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
	case errors.Is(err, knowledgevault.ErrReaderNotReady):
		writeError(writer, http.StatusNotFound, "knowledge_vault_unavailable")
		return
	case errors.Is(err, knowledgevault.ErrDocumentNotFound):
		writeError(writer, http.StatusNotFound, "knowledge_vault_path_not_found")
		return
	case err != nil:
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "knowledge_vault_files_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "path": result.Path, "count": result.Count, "entries": result.Entries,
	})
}

func (s *Server) knowledgeVaultFile(writer http.ResponseWriter, request *http.Request) {
	relative := strings.TrimSpace(request.URL.Query().Get("path"))
	var file knowledgevault.VaultFile
	err := s.withKnowledgeVaultReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		file, readErr = reader.File(relative)
		return readErr
	})
	switch {
	case errors.Is(err, knowledgevault.ErrDisabled):
		writeKnowledgeVaultDisabled(writer)
		return
	case errors.Is(err, knowledgevault.ErrReaderNotReady):
		writeError(writer, http.StatusNotFound, "knowledge_vault_unavailable")
		return
	case errors.Is(err, knowledgevault.ErrDocumentNotFound):
		writeError(writer, http.StatusNotFound, "knowledge_vault_file_not_found")
		return
	case errors.Is(err, knowledgevault.ErrBinaryFile):
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]any{
			"ok": false, "error": "binary_file", "download": true,
		})
		return
	case errors.Is(err, knowledgevault.ErrFileTooLarge):
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{
			"ok": false, "error": "file_too_large", "download": true,
		})
		return
	case err != nil:
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "knowledge_vault_file_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "file": file})
}

// knowledgeVaultFileDownload serves one regular file as an attachment. Skills
// and binaries never execute inline; the browser only downloads the bytes.
func (s *Server) knowledgeVaultFileDownload(writer http.ResponseWriter, request *http.Request) {
	relative := strings.TrimSpace(request.URL.Query().Get("path"))
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var absolute string
	var info os.FileInfo
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		var readErr error
		absolute, info, readErr = reader.FilePath(relative)
		return readErr
	})
	if errors.Is(err, knowledgevault.ErrDisabled) {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	if err != nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_file_not_found")
		return
	}
	file, err := os.Open(absolute)
	if err != nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_file_not_found")
		return
	}
	defer file.Close()
	writer.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filepath.Base(absolute), `"`, "_")+`"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
}

// knowledgeVaultOpenInfo tells the Web whether the "open in file manager"
// action is available. It is loopback-only by design: a remote or reverse-
// proxied owner still gets "copy path", but never a local process launch.
func (s *Server) knowledgeVaultOpenInfo(writer http.ResponseWriter, request *http.Request) {
	loopback := requestIsLoopback(request)
	absolute := ""
	if s.knowledgeVault != nil {
		_ = s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
			vaultPath := reader.VaultPath()
			absolute = vaultPath
			return nil
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "open_available": loopback, "vault_path": absolute,
	})
}

func (s *Server) revealKnowledgeVaultPath(writer http.ResponseWriter, request *http.Request) {
	if !requestIsLoopback(request) {
		writeError(writer, http.StatusForbidden, "file_manager_open_loopback_only")
		return
	}
	if s.knowledgeVault == nil {
		writeKnowledgeVaultDisabled(writer)
		return
	}
	var absolute string
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		var copyErr error
		absolute, copyErr = reader.CopyPathFor(strings.TrimSpace(request.URL.Query().Get("path")))
		return copyErr
	})
	if err != nil {
		writeError(writer, http.StatusNotFound, "knowledge_vault_path_not_found")
		return
	}
	reveal := s.revealPath
	if reveal == nil {
		reveal = defaultRevealPath
	}
	if err := reveal(absolute); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{
			"ok": false, "error": "file_manager_open_failed", "message": err.Error(),
		})
		return
	}
	if s.store != nil {
		s.audit(request, "knowledge_vault.reveal", "knowledge_vault", absolute, nil)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "path": absolute})
}

// requestIsLoopback reports whether the browser request came straight from
// this machine. Any forwarding header means a proxy is in front, so the action
// is hidden even when the proxy itself connects from 127.0.0.1.
func requestIsLoopback(request *http.Request) bool {
	if request == nil {
		return false
	}
	if strings.TrimSpace(request.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(request.Header.Get("X-Real-IP")) != "" ||
		strings.TrimSpace(request.Header.Get("Forwarded")) != "" {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(request.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(request.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultRevealPath(target string) error {
	switch {
	case runtime.GOOS == "windows":
		return exec.Command("explorer", "/select,", target).Start()
	case runtime.GOOS == "darwin":
		return exec.Command("open", "-R", target).Start()
	default:
		directory := target
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			directory = filepath.Dir(target)
		}
		return exec.Command("xdg-open", directory).Start()
	}
}
