package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func (s *Server) serveWeb(writer http.ResponseWriter, request *http.Request) {
	if s.web == nil {
		http.NotFound(writer, request)
		return
	}
	requestPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if requestPath == "." || requestPath == "" {
		requestPath = "index.html"
	}
	data, err := fs.ReadFile(s.web, requestPath)
	if err != nil && path.Ext(requestPath) == "" && !strings.HasPrefix(requestPath, "api/") {
		requestPath = "index.html"
		data, err = fs.ReadFile(s.web, requestPath)
	}
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(requestPath)); contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if requestPath == "index.html" {
		writer.Header().Set("Cache-Control", "no-store")
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(data)
}
