package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	OpenTab(ctx context.Context, profileID, sessionID, targetURL, fence string) (HostTab, error)
	ListTabs(ctx context.Context, profileID, sessionID string) ([]HostTab, error)
	Activate(ctx context.Context, profileID, sessionID, tabID, fence string) (HostTab, error)
	Navigate(ctx context.Context, profileID, sessionID, tabID, targetURL, fence string) (HostTab, error)
	Snapshot(ctx context.Context, profileID, sessionID, tabID string) (HostSnapshot, error)
	Frame(ctx context.Context, profileID, sessionID, tabID string) (HostFrame, error)
	CloseTab(ctx context.Context, profileID, sessionID, tabID, fence string) error
	Bookmarks(ctx context.Context, profileID, sessionID string) ([]HostBookmark, error)
	// Action runs one page-level interaction against the authorized tab. The
	// host accepts only element refs from a recent structured snapshot or
	// viewport coordinates; it never accepts a selector, script or CDP call.
	Action(ctx context.Context, profileID, sessionID, tabID string, action HostAction, fence string) (HostActionResult, error)
	// Upload hands Agent-supplied bytes to one file input or file chooser.
	// The bytes stay in memory; no caller-supplied or host path is accepted.
	Upload(ctx context.Context, profileID, sessionID, tabID string, upload HostUpload, fence string) (HostUploadResult, error)
	Downloads(ctx context.Context, profileID, sessionID string) ([]HostDownload, error)
	Download(ctx context.Context, profileID, sessionID, downloadID string) (HostDownloadFile, error)
	ClearDownloads(ctx context.Context, profileID, sessionID string) error
	Cookies(ctx context.Context, profileID, sessionID, targetURL, domain string) ([]HostCookie, bool, error)
	ImportCookies(ctx context.Context, profileID, sessionID string, cookies []HostCookie, fence string) (int, error)
	// SetFence installs the current lease fence on the host session. Mutating
	// host calls carry the same value; a stale value is rejected before any
	// page side effect.
	SetFence(ctx context.Context, profileID, sessionID, expectedFence, fence string) error
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
	// Fence is the lease token currently installed on the host session. AHA
	// installs it with SetFence; mutating host calls must carry the same value.
	Fence string `json:"fence,omitempty"`
}

type HostTab struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Active bool   `json:"active,omitempty"`
}

type HostSnapshot struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Elements is the structured, read-only list of actionable page elements.
	// It never contains input values, cookies, HTML source or a CDP handle.
	Elements []HostElement `json:"elements,omitempty"`
}

type HostBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// HostElement is one ref-addressable element from a structured snapshot. Ref is
// short-lived and invalidated by a newer snapshot or a navigation.
type HostElement struct {
	Ref     string     `json:"ref"`
	Role    string     `json:"role"`
	Name    string     `json:"name"`
	Bounds  HostBounds `json:"bounds"`
	Actions []string   `json:"actions"`
}

// HostAction is one page interaction. Kind is from the closed action set;
// Ref addresses an element from the latest snapshot, X/Y are viewport
// coordinates used only when the ActionEndpoint allows them. Text and Keys are
// input payloads and must never be persisted or audited.
type HostAction struct {
	Kind   string   `json:"kind"`
	Ref    string   `json:"ref,omitempty"`
	X      *int     `json:"x,omitempty"`
	Y      *int     `json:"y,omitempty"`
	Text   string   `json:"text,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	DeltaX int      `json:"delta_x,omitempty"`
	DeltaY int      `json:"delta_y,omitempty"`
}

// HostActionResult echoes only non-sensitive addressing metadata. Page text,
// typed input and key names are deliberately not part of the response.
type HostActionResult struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
	X    *int   `json:"x,omitempty"`
	Y    *int   `json:"y,omitempty"`
}

type HostUploadFile struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

type HostUpload struct {
	Ref   string           `json:"ref,omitempty"`
	X     *int             `json:"x,omitempty"`
	Y     *int             `json:"y,omitempty"`
	Files []HostUploadFile `json:"files"`
}

type HostUploadResult struct {
	Ref        string `json:"ref"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

// HostDownload is download metadata. The host-local file path is never part of
// the payload.
type HostDownload struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at,omitempty"`
}

type HostDownloadFile struct {
	Download HostDownload
	Data     []byte
}

// HostCookie is a browser cookie. Value is sensitive: it may be returned to the
// authorized Task, but it must never enter an audit row, error message or log.
type HostCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	HTTPOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"same_site,omitempty"`
}

// HostBookmark is one read-only Edge bookmark. The host resolves the real
// profile file; the path is never part of the response.
type HostBookmark struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Folder string `json:"folder,omitempty"`
}

// HostFrame is a single read-only preview image of an authorized tab. It is
// transient: AHA never writes it to disk or into an audit row.
type HostFrame struct {
	ContentType string
	Data        []byte
}

const (
	maxHostFrameBytes    = 4 << 20
	maxHostDownloadBytes = 10 << 20
)

// HTTPHost talks to the loopback browser host over authenticated HTTP. The
// token is sent only in the Authorization header and is never included in an
// error message.
type HTTPHost struct {
	baseURL string
	token   string
	client  *http.Client
}

// DefaultHostTimeout is deliberately longer than every host-side operation:
// page navigation uses a 30s deadline inside the host, so a client timeout that
// fires first would report failure while the host still commits the navigation.
const DefaultHostTimeout = 60 * time.Second

func NewHTTPHost(baseURL, token string) *HTTPHost {
	return NewHTTPHostWithTimeout(baseURL, token, DefaultHostTimeout)
}

// NewHTTPHostWithTimeout is used by tests to exercise slow-host behavior without
// waiting a full minute.
func NewHTTPHostWithTimeout(baseURL, token string, timeout time.Duration) *HTTPHost {
	if timeout <= 0 {
		timeout = DefaultHostTimeout
	}
	return &HTTPHost{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		client:  &http.Client{Timeout: timeout},
	}
}

// Host operations have a closed set of machine codes. AHA never forwards the
// host's diagnostic text: Playwright errors can contain the user-data directory
// or Edge executable path, which must not cross into AHA storage or the Web UI.
func hostErrorMessage(code string) string {
	switch code {
	case "browser_host_unavailable":
		return "浏览器宿主不可达，请确认 host 进程已启动且仅监听本机"
	case "browser_host_timeout":
		return "浏览器宿主操作超时，请刷新状态后再试"
	case "browser_host_unauthorized":
		return "浏览器宿主拒绝了 AHA 的本地凭据，请核对 host token"
	case "browser_host_node_missing":
		return "未找到可用的 Node.js（安装包内置运行时缺失或已损坏）；可设置 AHA2_NODE_PATH 覆盖"
	case "browser_host_script_missing":
		return "安装目录缺少 browser-host/host.mjs，请重新安装 AHA2"
	case "browser_host_starting":
		return "浏览器宿主正在启动，请稍候"
	case "browser_host_not_ready":
		return "浏览器宿主尚未就绪"
	case "browser_host_start_failed":
		return "浏览器宿主自动启动失败（端口被占用或依赖缺失），请查看 AHA2 日志"
	case "browser_frame_failed":
		return "读取页面画面失败"
	case "browser_edge_unavailable":
		return "未找到 Microsoft Edge；POC 不会换用 Chrome/Chromium"
	case "browser_profile_locked":
		return "该 Profile 的用户数据目录已被另一个浏览器进程占用"
	case "browser_launch_failed":
		return "浏览器启动失败，请查看浏览器宿主本地日志"
	case "browser_navigation_failed":
		return "页面打开失败或超时"
	case "browser_snapshot_failed":
		return "读取页面失败"
	case "browser_tab_list_failed":
		return "读取当前页面列表失败"
	case "browser_activation_failed":
		return "切换浏览器页面失败"
	case "browser_bookmarks_failed":
		return "读取收藏夹失败"
	case "browser_fence_stale":
		return "浏览器控制权已变化，请重新获取独占控制"
	case "browser_fence_expired":
		return "浏览器独占控制已过期，请重新获取"
	case "browser_fence_invalid":
		return "浏览器操作租约格式无效，操作未执行"
	case "browser_lease_hold_failed":
		return "浏览器授权续期失败，操作未开始，请重试"
	case "browser_fence_timeout":
		return "切换浏览器控制权超时，旧控制仍保留"
	case "browser_operation_busy":
		return "浏览器正在执行其他操作，请稍后重试"
	case "browser_session_not_found":
		return "浏览器会话不存在或已关闭"
	case "browser_session_changed":
		return "浏览器会话已换代，请重新附加"
	case "browser_tab_not_found":
		return "浏览器标签页不存在或已关闭"
	case "browser_element_not_found":
		return "页面元素不存在或不属于当前页面，请重新获取页面快照"
	case "browser_element_stale":
		return "页面元素引用已过期，请重新获取页面快照后再操作"
	case "browser_element_not_actionable":
		return "该页面元素不支持此操作"
	case "browser_action_invalid":
		return "页面操作参数无效"
	case "browser_action_failed":
		return "页面操作执行失败"
	case "browser_upload_invalid":
		return "上传文件参数无效"
	case "browser_upload_failed":
		return "文件上传失败"
	case "browser_download_not_found":
		return "下载文件不存在或已过期"
	case "browser_download_failed":
		return "读取下载文件失败"
	case "browser_cookie_invalid":
		return "Cookie 参数无效"
	case "browser_cookie_export_failed":
		return "读取 Cookie 失败"
	case "browser_cookie_import_failed":
		return "写入 Cookie 失败"
	default:
		return "浏览器宿主操作失败"
	}
}

// hostCodes are the errors whose text comes from the host process. Their AHA
// text is always the static message above, never the host's diagnostic string.
var hostCodes = map[string]bool{
	"browser_host_unavailable": true, "browser_host_timeout": true,
	"browser_host_unauthorized": true, "browser_host_bad_response": true,
	"browser_host_node_missing": true, "browser_host_script_missing": true,
	"browser_host_starting": true, "browser_host_not_ready": true,
	"browser_host_start_failed": true, "browser_frame_failed": true,
	"browser_edge_unavailable": true, "browser_profile_locked": true,
	"browser_profile_unavailable": true, "browser_profile_invalid": true,
	"browser_launch_failed": true, "browser_navigation_failed": true,
	"browser_snapshot_failed": true, "browser_tab_list_failed": true,
	"browser_activation_failed": true, "browser_bookmarks_failed": true,
	"browser_fence_stale":       true,
	"browser_fence_invalid":     true,
	"browser_lease_hold_failed": true,
	"browser_session_not_found": true,
	"browser_session_changed":   true, "browser_tab_not_found": true,
	"browser_element_not_found": true, "browser_element_stale": true,
	"browser_element_not_actionable": true, "browser_action_invalid": true,
	"browser_action_failed": true, "browser_upload_invalid": true,
	"browser_upload_failed": true, "browser_download_not_found": true,
	"browser_download_failed": true, "browser_cookie_invalid": true,
	"browser_cookie_export_failed": true, "browser_cookie_import_failed": true,
}

// sanitizeHostError replaces a host-originated error's text with the stable AHA
// message. Service return values therefore stay safe even if a host or fake
// adapter sends a diagnostic containing local paths.
func sanitizeHostError(err error) error {
	var typed *Error
	if errors.As(err, &typed) && hostCodes[typed.Code] {
		return &Error{Code: typed.Code, Message: hostErrorMessage(typed.Code), Status: typed.Status}
	}
	return err
}

// PublicMessage is the only textual part of a typed browser error that may be
// persisted or shown outside AHA. Host-originated errors and unknown errors get
// a stable message; service validation errors keep their owner-facing text.
func PublicMessage(err error) string {
	var typed *Error
	if !errors.As(err, &typed) {
		return "浏览器操作失败"
	}
	message := strings.TrimSpace(typed.Message)
	if hostCodes[typed.Code] {
		message = hostErrorMessage(typed.Code)
	}
	if message == "" {
		message = "浏览器操作失败"
	}
	if len([]rune(message)) > 240 {
		message = string([]rune(message)[:240])
	}
	return message
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
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return &Error{Code: "browser_host_timeout", Status: http.StatusGatewayTimeout,
				Message: hostErrorMessage("browser_host_timeout")}
		}
		return &Error{Code: "browser_host_unavailable", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_unavailable")}
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
		status := http.StatusBadGateway
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			status = http.StatusServiceUnavailable
			code = "browser_host_unauthorized"
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusConflict:
			status = http.StatusConflict
		case http.StatusServiceUnavailable:
			status = http.StatusServiceUnavailable
		case http.StatusGatewayTimeout:
			status = http.StatusGatewayTimeout
		}
		return &Error{Code: code, Message: hostErrorMessage(code), Status: status}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return newError(http.StatusBadGateway, "browser_host_bad_response", "浏览器宿主返回了无法解析的响应")
	}
	return nil
}

func isTimeout(err error) bool {
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) && netErr.Timeout()
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

func (h *HTTPHost) OpenTab(ctx context.Context, profileID, sessionID, targetURL, fence string) (HostTab, error) {
	var result struct {
		Tab HostTab `json:"tab"`
	}
	err := h.call(ctx, http.MethodPost, profilePath(profileID)+"/tabs",
		map[string]string{"session_id": sessionID, "url": targetURL, "fence": fence}, &result)
	return result.Tab, err
}

func (h *HTTPHost) SetFence(ctx context.Context, profileID, sessionID, expectedFence, fence string) error {
	return h.call(ctx, http.MethodPost, profilePath(profileID)+"/fence",
		map[string]string{"session_id": sessionID, "expected_fence": expectedFence, "fence": fence}, nil)
}

// ListTabs returns the ordinary web pages currently open in the session's
// visible browser. It is the only way AHA can discover a page the Owner opened
// by hand; the host never exposes a CDP endpoint or a profile path.
func (h *HTTPHost) ListTabs(ctx context.Context, profileID, sessionID string) ([]HostTab, error) {
	var result struct {
		Tabs []HostTab `json:"tabs"`
	}
	path := profilePath(profileID) + "/tabs?session_id=" + url.QueryEscape(sessionID)
	err := h.call(ctx, http.MethodGet, path, nil, &result)
	return result.Tabs, err
}

func (h *HTTPHost) Activate(ctx context.Context, profileID, sessionID, tabID, fence string) (HostTab, error) {
	var result struct {
		Tab HostTab `json:"tab"`
	}
	err := h.call(ctx, http.MethodPost,
		profilePath(profileID)+"/tabs/"+url.PathEscape(tabID)+"/activate",
		map[string]string{"session_id": sessionID, "fence": fence}, &result)
	return result.Tab, err
}

// Bookmarks reads the Edge bookmark file for the Profile through the host. The
// response contains titles, URLs and folder names only; no local path.
func (h *HTTPHost) Bookmarks(ctx context.Context, profileID, sessionID string) ([]HostBookmark, error) {
	var result struct {
		Bookmarks []HostBookmark `json:"bookmarks"`
	}
	path := profilePath(profileID) + "/bookmarks?session_id=" + url.QueryEscape(sessionID)
	err := h.call(ctx, http.MethodGet, path, nil, &result)
	return result.Bookmarks, err
}

func (h *HTTPHost) Navigate(ctx context.Context, profileID, sessionID, tabID, targetURL, fence string) (HostTab, error) {
	var result struct {
		Tab HostTab `json:"tab"`
	}
	err := h.call(ctx, http.MethodPost,
		profilePath(profileID)+"/tabs/"+url.PathEscape(tabID)+"/navigate",
		map[string]string{"session_id": sessionID, "url": targetURL, "fence": fence}, &result)
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

func (h *HTTPHost) CloseTab(ctx context.Context, profileID, sessionID, tabID, fence string) error {
	path := profilePath(profileID) + "/tabs/" + url.PathEscape(tabID) +
		"?session_id=" + url.QueryEscape(sessionID) + "&fence=" + url.QueryEscape(fence)
	return h.call(ctx, http.MethodDelete, path, nil, nil)
}

type hostActionWire struct {
	SessionID string   `json:"session_id"`
	Fence     string   `json:"fence"`
	Kind      string   `json:"kind"`
	Ref       string   `json:"ref,omitempty"`
	X         *int     `json:"x,omitempty"`
	Y         *int     `json:"y,omitempty"`
	Text      string   `json:"text,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	DeltaX    int      `json:"delta_x,omitempty"`
	DeltaY    int      `json:"delta_y,omitempty"`
}

func (h *HTTPHost) Action(ctx context.Context, profileID, sessionID, tabID string, action HostAction, fence string) (HostActionResult, error) {
	var result struct {
		Action HostActionResult `json:"action"`
	}
	body := hostActionWire{
		SessionID: sessionID, Fence: fence, Kind: action.Kind, Ref: action.Ref,
		X: action.X, Y: action.Y, Text: action.Text, Keys: action.Keys,
		DeltaX: action.DeltaX, DeltaY: action.DeltaY,
	}
	err := h.call(ctx, http.MethodPost,
		profilePath(profileID)+"/tabs/"+url.PathEscape(tabID)+"/actions", body, &result)
	return result.Action, err
}

func (h *HTTPHost) Upload(ctx context.Context, profileID, sessionID, tabID string, upload HostUpload, fence string) (HostUploadResult, error) {
	var result struct {
		Upload HostUploadResult `json:"upload"`
	}
	body := struct {
		SessionID string           `json:"session_id"`
		Fence     string           `json:"fence"`
		Ref       string           `json:"ref,omitempty"`
		X         *int             `json:"x,omitempty"`
		Y         *int             `json:"y,omitempty"`
		Files     []HostUploadFile `json:"files"`
	}{SessionID: sessionID, Fence: fence, Ref: upload.Ref, X: upload.X, Y: upload.Y, Files: upload.Files}
	err := h.call(ctx, http.MethodPost,
		profilePath(profileID)+"/tabs/"+url.PathEscape(tabID)+"/uploads", body, &result)
	return result.Upload, err
}

func (h *HTTPHost) Downloads(ctx context.Context, profileID, sessionID string) ([]HostDownload, error) {
	var result struct {
		Downloads []HostDownload `json:"downloads"`
	}
	path := profilePath(profileID) + "/downloads?session_id=" + url.QueryEscape(sessionID)
	err := h.call(ctx, http.MethodGet, path, nil, &result)
	return result.Downloads, err
}

// Download fetches one captured download as raw bytes. The host-local file path
// never crosses this boundary; only the bounded file body does.
func (h *HTTPHost) Download(ctx context.Context, profileID, sessionID, downloadID string) (HostDownloadFile, error) {
	path := profilePath(profileID) + "/downloads/" + url.PathEscape(downloadID) + "?session_id=" + url.QueryEscape(sessionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		return HostDownloadFile{}, newError(http.StatusServiceUnavailable, "browser_host_unavailable", "浏览器宿主地址无效")
	}
	request.Header.Set("Accept", "application/octet-stream")
	if h.token != "" {
		request.Header.Set("Authorization", "Bearer "+h.token)
	}
	response, err := h.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return HostDownloadFile{}, &Error{Code: "browser_host_timeout", Status: http.StatusGatewayTimeout,
				Message: hostErrorMessage("browser_host_timeout")}
		}
		return HostDownloadFile{}, &Error{Code: "browser_host_unavailable", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_unavailable")}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxHostDownloadBytes+1))
	if err != nil {
		return HostDownloadFile{}, newError(http.StatusBadGateway, "browser_host_bad_response", "读取浏览器宿主响应失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := "browser_download_failed"
		var failure hostErrorBody
		if json.Unmarshal(payload, &failure) == nil && strings.TrimSpace(failure.Error.Code) != "" {
			code = strings.TrimSpace(failure.Error.Code)
		}
		status := http.StatusBadGateway
		if response.StatusCode == http.StatusNotFound {
			code = "browser_download_not_found"
			status = http.StatusNotFound
		}
		return HostDownloadFile{}, &Error{Code: code, Message: hostErrorMessage(code), Status: status}
	}
	if len(payload) > maxHostDownloadBytes {
		return HostDownloadFile{}, newError(http.StatusBadGateway, "browser_download_failed", "下载文件超过大小上限")
	}
	filename := ""
	if raw := strings.TrimSpace(response.Header.Get("X-AHA2-Download-Filename")); raw != "" {
		if decoded, decodeErr := url.PathUnescape(raw); decodeErr == nil {
			filename = decoded
		}
	}
	size := int64(len(payload))
	return HostDownloadFile{
		Download: HostDownload{ID: downloadID, Filename: filename, Size: size},
		Data:     payload,
	}, nil
}

func (h *HTTPHost) ClearDownloads(ctx context.Context, profileID, sessionID string) error {
	path := profilePath(profileID) + "/downloads?session_id=" + url.QueryEscape(sessionID)
	return h.call(ctx, http.MethodDelete, path, nil, nil)
}

func (h *HTTPHost) Cookies(ctx context.Context, profileID, sessionID, targetURL, domain string) ([]HostCookie, bool, error) {
	query := url.Values{}
	query.Set("session_id", sessionID)
	if targetURL != "" {
		query.Set("url", targetURL)
	}
	if domain != "" {
		query.Set("domain", domain)
	}
	var result struct {
		Cookies   []HostCookie `json:"cookies"`
		Truncated bool         `json:"truncated"`
	}
	err := h.call(ctx, http.MethodGet, profilePath(profileID)+"/cookies?"+query.Encode(), nil, &result)
	return result.Cookies, result.Truncated, err
}

func (h *HTTPHost) ImportCookies(ctx context.Context, profileID, sessionID string, cookies []HostCookie, fence string) (int, error) {
	var result struct {
		Count int `json:"count"`
	}
	body := struct {
		SessionID string       `json:"session_id"`
		Fence     string       `json:"fence"`
		Cookies   []HostCookie `json:"cookies"`
	}{SessionID: sessionID, Fence: fence, Cookies: cookies}
	err := h.call(ctx, http.MethodPost, profilePath(profileID)+"/cookies", body, &result)
	return result.Count, err
}

// Shutdown asks the host to close every browser session and exit. The manager
// uses it before falling back to killing the child process tree.
func (h *HTTPHost) Shutdown(ctx context.Context) error {
	return h.call(ctx, http.MethodPost, "/v1/shutdown", nil, nil)
}

// Frame fetches one JPEG/PNG preview of an authorized tab. Unlike the JSON
// calls it reads the raw body, but it still maps host failures to the same
// static, path-free error codes.
func (h *HTTPHost) Frame(ctx context.Context, profileID, sessionID, tabID string) (HostFrame, error) {
	path := profilePath(profileID) + "/tabs/" + url.PathEscape(tabID) + "/frame?session_id=" + url.QueryEscape(sessionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		return HostFrame{}, newError(http.StatusServiceUnavailable, "browser_host_unavailable", "浏览器宿主地址无效")
	}
	request.Header.Set("Accept", "image/jpeg, image/png")
	if h.token != "" {
		request.Header.Set("Authorization", "Bearer "+h.token)
	}
	response, err := h.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return HostFrame{}, &Error{Code: "browser_host_timeout", Status: http.StatusGatewayTimeout,
				Message: hostErrorMessage("browser_host_timeout")}
		}
		return HostFrame{}, &Error{Code: "browser_host_unavailable", Status: http.StatusServiceUnavailable,
			Message: hostErrorMessage("browser_host_unavailable")}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxHostFrameBytes+1))
	if err != nil {
		return HostFrame{}, newError(http.StatusBadGateway, "browser_host_bad_response", "读取浏览器宿主响应失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure hostErrorBody
		_ = json.Unmarshal(payload, &failure)
		code := strings.TrimSpace(failure.Error.Code)
		if code == "" {
			code = "browser_frame_failed"
		}
		status := http.StatusBadGateway
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			status = http.StatusServiceUnavailable
			code = "browser_host_unauthorized"
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusConflict:
			status = http.StatusConflict
		case http.StatusServiceUnavailable:
			status = http.StatusServiceUnavailable
		case http.StatusGatewayTimeout:
			status = http.StatusGatewayTimeout
		}
		return HostFrame{}, &Error{Code: code, Message: hostErrorMessage(code), Status: status}
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "image/jpeg" && contentType != "image/png" {
		return HostFrame{}, newError(http.StatusBadGateway, "browser_frame_failed", "浏览器宿主返回了非图片画面")
	}
	if len(payload) == 0 || len(payload) > maxHostFrameBytes {
		return HostFrame{}, newError(http.StatusBadGateway, "browser_frame_failed", "浏览器宿主返回的画面大小无效")
	}
	return HostFrame{ContentType: contentType, Data: payload}, nil
}

func isHostUnavailable(err error) bool {
	var typed *Error
	return errors.As(err, &typed) && (typed.Code == "browser_host_unavailable" || typed.Code == "browser_host_unauthorized")
}
