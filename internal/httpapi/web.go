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
	"regexp"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
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
	if requestPath == "index.html" {
		data = s.localizeIndex(data, request)
	}
	if contentType := mime.TypeByExtension(path.Ext(requestPath)); contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if requestPath == "index.html" {
		writer.Header().Set("Cache-Control", "no-cache")
	} else if request.URL.Query().Get("v") != "" || hashedAssetName(requestPath) {
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
		// The Web build now serves one large bundle instead of three dozen
		// small modules. BestSpeed leaves it around 280 KiB on the wire; the
		// default level costs CPU only on the first, cache-filling request and
		// keeps the first-screen transfer within budget.
		zipper, zipErr := gzip.NewWriterLevel(&compressed, gzip.DefaultCompression)
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

// localizeIndex aligns the first HTML paint with the owner's saved language.
// The boot card itself is language-neutral (logo plus spinner), but the html
// lang attribute and any surrounding text should not wait for the Web bundle to
// fetch general settings.
func (s *Server) localizeIndex(data []byte, request *http.Request) []byte {
	if s.store == nil {
		return data
	}
	settings, err := s.store.GeneralSettings(request.Context())
	if err != nil {
		return data
	}
	language := domain.NormalizeLanguage(settings.Language)
	if language == domain.LanguageAuto {
		if strings.Contains(strings.ToLower(request.Header.Get("Accept-Language")), "zh") {
			language = domain.LanguageSimplified
		} else {
			language = domain.LanguageEnglish
		}
	}
	if language != domain.LanguageEnglish {
		return data
	}
	return bytes.ReplaceAll(data, []byte(`<html lang="zh-CN">`), []byte(`<html lang="en-US">`))
}

// hashedAssetName reports whether the build stamped a content hash into the
// file name (app.10a0e87f92c5.js). Those files can never change at the same
// URL, so the browser may keep them forever without revalidation.
func hashedAssetName(name string) bool {
	return hashedAssetSuffix.MatchString(name)
}

var hashedAssetSuffix = regexp.MustCompile(`^[^/]+[.-][0-9a-f]{12}\.(?:js|css)$`)

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
