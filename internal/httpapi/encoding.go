package httpapi

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// maxRepairableBody bounds how much of a request body the encoding guard will
// buffer. JSON request bodies are far smaller; anything larger is streamed
// through unchanged.
const maxRepairableBody = 8 << 20

// repairTextEncoding accepts JSON bodies that a Windows client encoded with
// the native ANSI code page (GBK/GB18030) instead of UTF-8. Go's JSON decoder
// otherwise replaces those bytes with U+FFFD, silently corrupting Chinese text
// such as Task goals. Valid UTF-8 and binary payloads are never modified.
func (s *Server) repairTextEncoding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Body == nil || request.ContentLength == 0 || !repairableContentType(request.Header.Get("Content-Type")) {
			next.ServeHTTP(writer, request)
			return
		}
		original := request.Body
		body, err := io.ReadAll(io.LimitReader(original, maxRepairableBody+1))
		if err != nil {
			// Give the handler the bytes that did arrive; it will report the
			// decode failure instead of losing the request entirely.
			_ = original.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
			request.ContentLength = int64(len(body))
			next.ServeHTTP(writer, request)
			return
		}
		if len(body) > maxRepairableBody {
			request.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), original), original}
			next.ServeHTTP(writer, request)
			return
		}
		_ = original.Close()
		if utf8.Valid(body) {
			restoreRequestBody(request, body)
			next.ServeHTTP(writer, request)
			return
		}
		converted, ok := decodeGB18030Text(body)
		if !ok {
			restoreRequestBody(request, body)
			next.ServeHTTP(writer, request)
			return
		}
		s.logger.Warn("repaired non-UTF-8 request body", "method", request.Method, "path", request.URL.Path)
		restoreRequestBody(request, converted)
		next.ServeHTTP(writer, request)
	})
}

func restoreRequestBody(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	if request.Header.Get("Content-Length") != "" {
		request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
}

func repairableContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "", "application/json", "application/x-www-form-urlencoded", "text/plain":
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasPrefix(mediaType, "text/")
}

// decodeGB18030Text only accepts a conversion that produces valid UTF-8 and
// at least one Han character. That keeps a Latin-1 body or arbitrary binary
// data from being reinterpreted as Chinese.
func decodeGB18030Text(body []byte) ([]byte, bool) {
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(body)
	if err != nil || !utf8.Valid(decoded) {
		return nil, false
	}
	hasHan := false
	for _, value := range string(decoded) {
		if unicode.Is(unicode.Han, value) {
			hasHan = true
			break
		}
	}
	if !hasHan {
		return nil, false
	}
	return decoded, true
}
