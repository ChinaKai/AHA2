package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Host is the only channel through which AHA operates the browser. The host
// owns the real user-data directories, the single-instance lock and the
// visible window; AHA never receives a CDP endpoint or profile path.
type Host interface {
	Health(ctx context.Context) (Health, error)
	Open(ctx context.Context, profileID, browserKind string) (HostSession, error)
	Session(ctx context.Context, profileID string) (HostSession, error)
	Close(ctx context.Context, profileID string) error
	OpenTab(ctx context.Context, profileID, sessionID, targetURL string) (HostTab, error)
	Navigate(ctx context.Context, profileID, sessionID, tabID, targetURL string) (HostTab, error)
	Snapshot(ctx context.Context, profileID, sessionID, tabID string) (HostSnapshot, error)
	CloseTab(ctx context.Context, profileID, sessionID, tabID string) error
}

type Health struct {
	HostID        string `json:"host_id"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	EdgeAvailable bool   `json:"edge_available"`
}

type HostSession struct {
	ID        string `json:"id"`
	ProfileID string `json:"profile_id"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	Reused    bool   `json:"reused,omitempty"`
}

type HostTab struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type HostSnapshot struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// HTTPHost talks to the loopback browser host over authenticated HTTP. The
// token is sent only in the Authorization header and is never included in an
// error message.
type HTTPHost struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHTTPHost(baseURL, token string) *HTTPHost {
	return &HTTPHost{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		client:  &http.Client{Timeout: 25 * time.Second},
	}
}

type hostErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (h *HTTPHost) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return newError(http.StatusInternalServerError, "browser_host_request_invalid", "无法编码浏览器宿主请求")
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return newError(http.StatusServiceUnavailable, "browser_host_unavailable", "浏览器宿主地址无效")
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if h.token != "" {
		request.Header.Set("Authorization", "Bearer "+h.token)
	}
	response, err := h.client.Do(request)
	if err != nil {
		return newError(http.StatusServiceUnavailable, "browser_host_unavailable",
			"浏览器宿主不可达，请确认 host 进程已启动且仅监听本机")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return newError(http.StatusBadGateway, "browser_host_bad_response", "读取浏览器宿主响应失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure hostErrorBody
		_ = json.Unmarshal(payload, &failure)
		code := strings.TrimSpace(failure.Error.Code)
		if code == "" {
			code = "browser_host_error"
		}
		message := strings.TrimSpace(failure.Error.Message)
		if message == "" {
			message = fmt.Sprintf("浏览器宿主返回状态 %d", response.StatusCode)
		}
		status := http.StatusBadGateway
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			status = http.StatusServiceUnavailable
			code = "browser_host_unauthorized"
			message = "浏览器宿主拒绝了 AHA 的本地凭据，请核对 host token"
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusConflict:
			status = http.StatusConflict
		case http.StatusServiceUnavailable:
			status = http.StatusServiceUnavailable
		}
		return &Error{Code: code, Message: message, Status: status}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return newError(http.StatusBadGateway, "browser_host_bad_response", "浏览器宿主返回了无法解析的响应")
	}
	return nil
}

func profilePath(profileID string) string {
	return "/v1/profiles/" + url.PathEscape(profileID)
}

func (h *HTTPHost) Health(ctx context.Context) (Health, error) {
	var result Health
	err := h.call(ctx, http.MethodGet, "/v1/health", nil, &result)
	return result, err
}

func (h *HTTPHost) Open(ctx context.Context, profileID, browserKind string) (HostSession, error) {
	var result struct {
		Session HostSession `json:"session"`
	}
	err := h.call(ctx, http.MethodPost, profilePath(profileID)+"/session",
		map[string]string{"browser_kind": browserKind}, &result)
	return result.Session, err
}

func (h *HTTPHost) Session(ctx context.Context, profileID string) (HostSession, error) {
	var result struct {
		Session HostSession `json:"session"`
	}
	err := h.call(ctx, http.MethodGet, profilePath(profileID)+"/session", nil, &result)
	return result.Session, err
}

func (h *HTTPHost) Close(ctx context.Context, profileID string) error {
	return h.call(ctx, http.MethodDelete, profilePath(profileID)+"/session", nil, nil)
}

func (h *HTTPHost) OpenTab(ctx context.Context, profileID, sessionID, targetURL string) (HostTab, error) {
	var result struct {
		Tab HostTab `json:"tab"`
	}
	err := h.call(ctx, http.MethodPost, profilePath(profileID)+"/tabs",
		map[string]string{"session_id": sessionID, "url": targetURL}, &result)
	return result.Tab, err
}

func (h *HTTPHost) Navigate(ctx context.Context, profileID, sessionID, tabID, targetURL string) (HostTab, error) {
	var result struct {
		Tab HostTab `json:"tab"`
	}
	err := h.call(ctx, http.MethodPost,
		profilePath(profileID)+"/tabs/"+url.PathEscape(tabID)+"/navigate",
		map[string]string{"session_id": sessionID, "url": targetURL}, &result)
	return result.Tab, err
}

func (h *HTTPHost) Snapshot(ctx context.Context, profileID, sessionID, tabID string) (HostSnapshot, error) {
	var result struct {
		Snapshot HostSnapshot `json:"snapshot"`
	}
	path := profilePath(profileID) + "/tabs/" + url.PathEscape(tabID) + "/snapshot?session_id=" + url.QueryEscape(sessionID)
	err := h.call(ctx, http.MethodGet, path, nil, &result)
	return result.Snapshot, err
}

func (h *HTTPHost) CloseTab(ctx context.Context, profileID, sessionID, tabID string) error {
	path := profilePath(profileID) + "/tabs/" + url.PathEscape(tabID) + "?session_id=" + url.QueryEscape(sessionID)
	return h.call(ctx, http.MethodDelete, path, nil, nil)
}

func isHostUnavailable(err error) bool {
	var typed *Error
	return errors.As(err, &typed) && (typed.Code == "browser_host_unavailable" || typed.Code == "browser_host_unauthorized")
}
