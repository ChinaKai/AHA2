package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
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
		writer.Header().Set("Cache-Control", "no-cache")
	} else if request.URL.Query().Get("v") != "" {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	responseData := data
	compressible := compressibleWebAsset(requestPath)
	if compressible {
		writer.Header().Set("Vary", "Accept-Encoding")
	}
	if acceptsGzip(request) && compressible {
		var compressed bytes.Buffer
		zipper, zipErr := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		if zipErr == nil {
			_, zipErr = zipper.Write(data)
			if closeErr := zipper.Close(); zipErr == nil {
				zipErr = closeErr
			}
		}
		if zipErr == nil {
			responseData = compressed.Bytes()
			writer.Header().Set("Content-Encoding", "gzip")
		}
	}
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(responseData))
	writer.Header().Set("ETag", etag)
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(responseData)
}

func acceptsGzip(request *http.Request) bool {
	for _, encoding := range strings.Split(request.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(strings.SplitN(encoding, ";", 2)[0]) == "gzip" {
			return true
		}
	}
	return false
}

func compressibleWebAsset(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".css", ".js", ".json", ".svg", ".txt":
		return true
	default:
		return false
	}
}
