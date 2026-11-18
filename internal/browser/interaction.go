package browser

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Interaction limits are enforced twice: in AHA before the host call and in the
// loopback host before any Playwright call. The values are deliberately small
// so one Agent action cannot turn into an unbounded input or memory budget.
const (
	MaxActionTextBytes  = 4096
	MaxActionKeys       = 4
	MaxActionKeyLength  = 32
	MaxScrollDelta      = 4000
	MaxCoordinate       = 100000
	MaxRefLength        = 64
	MaxUploadFiles      = 5
	MaxUploadFileBytes  = 10 << 20
	MaxUploadTotalBytes = 20 << 20
	MaxFilenameBytes    = 255
	MaxDownloadBytes    = 10 << 20
	MaxCookies          = 300
	MaxCookieNameBytes  = 256
	MaxCookieValueBytes = 4096
	MaxCookiePathBytes  = 1024
	MaxCookieDomain     = 253
)

var actionKinds = map[string]bool{
	"click": true, "double_click": true, "hover": true,
	"type": true, "key": true, "scroll": true,
}

// Act runs one page interaction under the Task grant, its capability and the
// exclusive lease. Text and key payloads are passed to the host but never
// written to the audit trail or returned in the response.
func (s *Service) Act(ctx context.Context, actor, grantID, tabID string, action HostAction) (HostActionResult, error) {
	grant, tab, err := s.authorize(ctx, grantID, domain.BrowserCapabilityInteract, tabID)
	if err != nil {
		return HostActionResult{}, err
	}
	action, err = normalizeAction(action)
	if err != nil {
		return HostActionResult{}, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return HostActionResult{}, err
	}
	var result HostActionResult
	err = s.withOperationHold(ctx, grant, func(expiresAt time.Time) error {
		hostResult, callErr := s.host.Action(ctx, grant.ProfileID, session.HostSessionID,
			tab.HostTabID, action, s.operationFence(grant, expiresAt))
		if callErr != nil {
			return sanitizeHostError(callErr)
		}
		if _, _, authErr := s.authorize(ctx, grantID, domain.BrowserCapabilityInteract, tabID); authErr != nil {
			return authErr
		}
		result = hostResult
		return nil
	})
	auditData := map[string]any{
		"task_id": tab.TaskID, "tab_id": tab.ID, "kind": action.Kind,
	}
	if target := actionTarget(action); target != "" {
		auditData["target"] = target
	}
	if err != nil {
		s.auditActionResult(ctx, actor, "browser_tab.action", auditData, err)
		return HostActionResult{}, err
	}
	s.touchSession(ctx, session)
	s.auditActionResult(ctx, actor, "browser_tab.action", auditData, nil)
	return result, nil
}

func normalizeAction(action HostAction) (HostAction, error) {
	action.Kind = strings.TrimSpace(action.Kind)
	action.Ref = strings.TrimSpace(action.Ref)
	if !actionKinds[action.Kind] {
		return HostAction{}, badRequest("browser_action_invalid", "不支持的页面操作")
	}
	hasRef := action.Ref != ""
	hasX := action.X != nil
	hasY := action.Y != nil
	if hasX != hasY {
		return HostAction{}, badRequest("browser_action_invalid", "坐标 x/y 必须同时提供")
	}
	hasCoords := hasX && hasY
	if hasRef && hasCoords {
		return HostAction{}, badRequest("browser_action_invalid", "元素 ref 与坐标只能选一种")
	}
	if hasRef && (len(action.Ref) > MaxRefLength || !safeRef(action.Ref)) {
		return HostAction{}, badRequest("browser_action_invalid", "元素 ref 格式无效")
	}
	if hasCoords {
		if *action.X < 0 || *action.X > MaxCoordinate || *action.Y < 0 || *action.Y > MaxCoordinate {
			return HostAction{}, badRequest("browser_action_invalid", "坐标超出允许范围")
		}
	}
	switch action.Kind {
	case "click", "double_click", "hover":
		if !hasRef && !hasCoords {
			return HostAction{}, badRequest("browser_action_invalid", "请提供元素 ref 或坐标")
		}
		if action.Text != "" || len(action.Keys) > 0 || action.DeltaX != 0 || action.DeltaY != 0 {
			return HostAction{}, badRequest("browser_action_invalid", "该操作不接受输入内容")
		}
	case "type":
		if !hasRef && !hasCoords {
			return HostAction{}, badRequest("browser_action_invalid", "输入操作需要元素 ref 或坐标")
		}
		if action.Text == "" {
			return HostAction{}, badRequest("browser_action_invalid", "请输入要键入的文本")
		}
		if len(action.Text) > MaxActionTextBytes || !utf8.ValidString(action.Text) || hasControlChars(action.Text, false) {
			return HostAction{}, badRequest("browser_action_invalid", "输入文本过长或包含无效字符")
		}
		if len(action.Keys) > 0 || action.DeltaX != 0 || action.DeltaY != 0 {
			return HostAction{}, badRequest("browser_action_invalid", "该操作不接受按键或滚动参数")
		}
	case "key":
		if hasRef || hasCoords || action.Text != "" || action.DeltaX != 0 || action.DeltaY != 0 {
			return HostAction{}, badRequest("browser_action_invalid", "按键操作不接受元素、文本或滚动参数")
		}
		if len(action.Keys) == 0 || len(action.Keys) > MaxActionKeys {
			return HostAction{}, badRequest("browser_action_invalid", "按键数量必须在 1 到 %d 之间", MaxActionKeys)
		}
		for index, key := range action.Keys {
			key = strings.TrimSpace(key)
			if key == "" || len(key) > MaxActionKeyLength || hasControlChars(key, true) {
				return HostAction{}, badRequest("browser_action_invalid", "按键名称无效")
			}
			action.Keys[index] = key
		}
	case "scroll":
		if hasRef || action.Text != "" || len(action.Keys) > 0 {
			return HostAction{}, badRequest("browser_action_invalid", "滚动操作不接受元素或输入内容")
		}
		if action.DeltaX == 0 && action.DeltaY == 0 {
			return HostAction{}, badRequest("browser_action_invalid", "滚动距离不能为 0")
		}
		if absInt(action.DeltaX) > MaxScrollDelta || absInt(action.DeltaY) > MaxScrollDelta {
			return HostAction{}, badRequest("browser_action_invalid", "滚动距离不能超过 %d 像素", MaxScrollDelta)
		}
	}
	return action, nil
}

func safeRef(ref string) bool {
	for _, value := range ref {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' || value == '-' || value == '_' || value == '.' {
			continue
		}
		return false
	}
	return true
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// hasControlChars rejects NUL/CR/LF for all payloads; key names additionally
// reject whitespace because a key name is a token such as "ENTER", not text.
func hasControlChars(value string, rejectSpace bool) bool {
	for _, char := range value {
		if char == 0 || char == '\r' || char == '\n' || char == 0x7f {
			return true
		}
		if rejectSpace && unicode.IsSpace(char) {
			return true
		}
	}
	return false
}

func actionTarget(action HostAction) string {
	if action.Ref != "" {
		return action.Ref
	}
	if action.X != nil && action.Y != nil {
		return "viewport:" + strconv.Itoa(*action.X) + "," + strconv.Itoa(*action.Y)
	}
	return ""
}

func (s *Service) auditActionResult(ctx context.Context, actor, action string, data map[string]any, err error) {
	if err != nil {
		if code := Code(err); code != "" {
			data["result"] = code
		} else {
			data["result"] = "error"
		}
	} else {
		data["result"] = "ok"
	}
	s.audit(ctx, actor, action, "browser_tab", stringValue(data["tab_id"]), data)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (s *Service) Upload(ctx context.Context, actor, grantID, tabID, ref string, x, y *int, files []UploadFile) (HostUploadResult, error) {
	// Authorize before touching the payload: an unauthorized caller must not be
	// able to make AHA validate and base64-encode a multi-megabyte body.
	grant, tab, err := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, tabID)
	if err != nil {
		return HostUploadResult{}, err
	}
	ref = strings.TrimSpace(ref)
	hasRef := ref != ""
	hasX := x != nil
	hasY := y != nil
	if hasX != hasY {
		return HostUploadResult{}, badRequest("browser_upload_invalid", "坐标 x/y 必须同时提供")
	}
	hasCoords := hasX && hasY
	if hasRef == hasCoords {
		return HostUploadResult{}, badRequest("browser_upload_invalid", "请提供文件输入元素 ref 或坐标")
	}
	if hasRef && (len(ref) > MaxRefLength || !safeRef(ref)) {
		return HostUploadResult{}, badRequest("browser_upload_invalid", "文件输入元素 ref 格式无效")
	}
	if hasCoords && (*x < 0 || *x > MaxCoordinate || *y < 0 || *y > MaxCoordinate) {
		return HostUploadResult{}, badRequest("browser_upload_invalid", "坐标超出允许范围")
	}
	if len(files) == 0 || len(files) > MaxUploadFiles {
		return HostUploadResult{}, badRequest("browser_upload_invalid", "文件数量必须在 1 到 %d 之间", MaxUploadFiles)
	}
	hostFiles := make([]HostUploadFile, 0, len(files))
	names := make([]string, 0, len(files))
	var total int64
	for _, file := range files {
		name, err := validUploadFilename(file.Filename)
		if err != nil {
			return HostUploadResult{}, err
		}
		if len(file.Data) > MaxUploadFileBytes {
			return HostUploadResult{}, badRequest("browser_upload_invalid", "单个文件不能超过 %d 字节", MaxUploadFileBytes)
		}
		total += int64(len(file.Data))
		if total > MaxUploadTotalBytes {
			return HostUploadResult{}, badRequest("browser_upload_invalid", "文件总大小不能超过 %d 字节", MaxUploadTotalBytes)
		}
		names = append(names, name)
		hostFiles = append(hostFiles, HostUploadFile{
			Filename: name, ContentBase64: base64.StdEncoding.EncodeToString(file.Data),
		})
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return HostUploadResult{}, err
	}
	var result HostUploadResult
	err = s.withOperationHold(ctx, grant, func(expiresAt time.Time) error {
		hostResult, callErr := s.host.Upload(ctx, grant.ProfileID, session.HostSessionID, tab.HostTabID,
			HostUpload{Ref: ref, X: x, Y: y, Files: hostFiles}, s.operationFence(grant, expiresAt))
		if callErr != nil {
			return sanitizeHostError(callErr)
		}
		if _, _, authErr := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, tabID); authErr != nil {
			return authErr
		}
		result = hostResult
		return nil
	})
	auditData := map[string]any{
		"task_id": tab.TaskID, "tab_id": tab.ID, "ref": ref,
		"file_count": len(hostFiles), "total_bytes": total, "filenames": names,
	}
	if err != nil {
		s.auditActionResult(ctx, actor, "browser_tab.upload", auditData, err)
		return HostUploadResult{}, err
	}
	s.touchSession(ctx, session)
	s.auditActionResult(ctx, actor, "browser_tab.upload", auditData, nil)
	return result, nil
}

// UploadFile is one Agent-supplied upload. Data comes from the API body; the
// host hands it to Playwright as an in-memory FilePayload and never accepts a
// host path.
type UploadFile struct {
	Filename string
	Data     []byte
}

func validUploadFilename(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", badRequest("browser_upload_invalid", "文件名不能为空")
	}
	if len(name) > MaxFilenameBytes {
		return "", badRequest("browser_upload_invalid", "文件名过长")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") || hasControlChars(name, false) {
		return "", badRequest("browser_upload_invalid", "文件名不能包含路径或控制字符")
	}
	return name, nil
}

func (s *Service) Downloads(ctx context.Context, actor, grantID string) ([]HostDownload, error) {
	grant, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, "")
	if err != nil {
		return nil, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return nil, err
	}
	downloads, err := s.host.Downloads(ctx, grant.ProfileID, session.HostSessionID)
	if err != nil {
		return nil, sanitizeHostError(err)
	}
	if _, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, ""); err != nil {
		return nil, err
	}
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_download.list", "browser_task_grant", grant.ID, map[string]any{
		"task_id": grant.TaskID, "count": len(downloads), "result": "ok",
	})
	return downloads, nil
}

func (s *Service) Download(ctx context.Context, actor, grantID, downloadID string) (HostDownloadFile, error) {
	downloadID = strings.TrimSpace(downloadID)
	if !validDownloadID(downloadID) {
		return HostDownloadFile{}, badRequest("browser_download_not_found", "下载文件不存在或已过期")
	}
	grant, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, "")
	if err != nil {
		return HostDownloadFile{}, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return HostDownloadFile{}, err
	}
	file, err := s.host.Download(ctx, grant.ProfileID, session.HostSessionID, downloadID)
	if err != nil {
		return HostDownloadFile{}, sanitizeHostError(err)
	}
	if len(file.Data) > MaxDownloadBytes {
		return HostDownloadFile{}, newError(http.StatusBadGateway, "browser_download_failed", "下载文件超过大小上限")
	}
	if _, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityFiles, ""); err != nil {
		return HostDownloadFile{}, err
	}
	filename := safeDownloadFilename(file.Download.Filename)
	// The HTTP layer uses this value for Content-Disposition; sanitize it here
	// so a host-side suggested name can never inject a path or header break.
	file.Download.Filename = filename
	file.Download.Size = int64(len(file.Data))
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_download.fetch", "browser_task_grant", grant.ID, map[string]any{
		"task_id": grant.TaskID, "download_id": downloadID, "filename": filename,
		"size": len(file.Data), "result": "ok",
	})
	return file, nil
}

func validDownloadID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func safeDownloadFilename(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.Map(func(char rune) rune {
		if char == '/' || char == '\\' || char == 0 || char == '\r' || char == '\n' || char == 0x7f {
			return -1
		}
		return char
	}, name)
	if name == "" || name == "." || name == ".." {
		return "download.bin"
	}
	for len(name) > MaxFilenameBytes {
		name = name[:len(name)-1]
	}
	return name
}

func (s *Service) ExportCookies(ctx context.Context, actor, grantID, targetURL, domainName string) ([]HostCookie, bool, error) {
	targetURL = strings.TrimSpace(targetURL)
	domainName = strings.TrimSpace(domainName)
	if (targetURL == "") == (domainName == "") {
		return nil, false, badRequest("browser_cookie_invalid", "请提供 url 或 domain 中的一个")
	}
	if targetURL != "" {
		normalized, err := validateTargetURL(targetURL)
		if err != nil {
			return nil, false, badRequest("browser_cookie_invalid", "Cookie 导出 URL 无效")
		}
		targetURL = normalized
	}
	if domainName != "" {
		normalized, err := normalizeCookieDomain(domainName)
		if err != nil {
			return nil, false, err
		}
		domainName = normalized
	}
	grant, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityCookies, "")
	if err != nil {
		return nil, false, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return nil, false, err
	}
	cookies, truncated, err := s.host.Cookies(ctx, grant.ProfileID, session.HostSessionID, targetURL, domainName)
	if err != nil {
		return nil, false, sanitizeHostError(err)
	}
	if _, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityCookies, ""); err != nil {
		return nil, false, err
	}
	if len(cookies) > MaxCookies {
		cookies = cookies[:MaxCookies]
		truncated = true
	}
	scope := domainName
	if targetURL != "" {
		if parsed, parseErr := url.Parse(targetURL); parseErr == nil {
			scope = parsed.Hostname()
		}
	}
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_cookie.export", "browser_task_grant", grant.ID, map[string]any{
		"task_id": grant.TaskID, "domain": scope, "count": len(cookies),
		"truncated": truncated, "result": "ok",
	})
	return cookies, truncated, nil
}

func (s *Service) ImportCookies(ctx context.Context, actor, grantID string, input []HostCookie) (int, error) {
	if len(input) == 0 || len(input) > MaxCookies {
		return 0, badRequest("browser_cookie_invalid", "Cookie 数量必须在 1 到 %d 之间", MaxCookies)
	}
	cookies := make([]HostCookie, 0, len(input))
	domains := map[string]bool{}
	for _, cookie := range input {
		normalized, err := normalizeCookie(cookie, s.now())
		if err != nil {
			return 0, err
		}
		cookies = append(cookies, normalized)
		domains[strings.TrimPrefix(normalized.Domain, ".")] = true
	}
	grant, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityCookies, "")
	if err != nil {
		return 0, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return 0, err
	}
	var count int
	err = s.withOperationHold(ctx, grant, func(expiresAt time.Time) error {
		imported, callErr := s.host.ImportCookies(ctx, grant.ProfileID, session.HostSessionID,
			cookies, s.operationFence(grant, expiresAt))
		if callErr != nil {
			return sanitizeHostError(callErr)
		}
		if _, _, authErr := s.authorize(ctx, grantID, domain.BrowserCapabilityCookies, ""); authErr != nil {
			return authErr
		}
		count = imported
		return nil
	})
	auditData := map[string]any{
		"task_id": grant.TaskID, "count": len(cookies), "domains": sortedKeys(domains),
	}
	if err != nil {
		if code := Code(err); code != "" {
			auditData["result"] = code
		} else {
			auditData["result"] = "error"
		}
		s.audit(ctx, actor, "browser_cookie.import", "browser_task_grant", grant.ID, auditData)
		return 0, err
	}
	s.touchSession(ctx, session)
	auditData["result"] = "ok"
	s.audit(ctx, actor, "browser_cookie.import", "browser_task_grant", grant.ID, auditData)
	return count, nil
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeCookieDomain(raw string) (string, error) {
	domain := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "."))
	if domain == "" || len(domain) > MaxCookieDomain {
		return "", badRequest("browser_cookie_invalid", "Cookie domain 无效")
	}
	if strings.ContainsAny(domain, " /\\:@\t\r\n") || hasControlChars(domain, true) {
		return "", badRequest("browser_cookie_invalid", "Cookie domain 不能包含路径、端口或空白")
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", badRequest("browser_cookie_invalid", "Cookie domain 无效")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", badRequest("browser_cookie_invalid", "Cookie domain 无效")
		}
		for _, char := range label {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
				char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return "", badRequest("browser_cookie_invalid", "Cookie domain 无效")
		}
	}
	return strings.ToLower(domain), nil
}

func normalizeCookie(cookie HostCookie, now time.Time) (HostCookie, error) {
	if cookie.Name == "" || len(cookie.Name) > MaxCookieNameBytes || !validCookieName(cookie.Name) {
		return HostCookie{}, badRequest("browser_cookie_invalid", "Cookie name 无效")
	}
	if len(cookie.Value) > MaxCookieValueBytes || !validCookieValue(cookie.Value) {
		return HostCookie{}, badRequest("browser_cookie_invalid", "Cookie value 过长或包含无效字符")
	}
	domainName, err := normalizeCookieDomain(cookie.Domain)
	if err != nil {
		return HostCookie{}, err
	}
	cookie.Domain = domainName
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if len(cookie.Path) > MaxCookiePathBytes || !strings.HasPrefix(cookie.Path, "/") || hasControlChars(cookie.Path, false) {
		return HostCookie{}, badRequest("browser_cookie_invalid", "Cookie path 无效")
	}
	if cookie.Expires != -1 {
		if cookie.Expires <= now.Unix() || cookie.Expires > now.Add(5*365*24*time.Hour).Unix() {
			return HostCookie{}, badRequest("browser_cookie_invalid", "Cookie 过期时间无效")
		}
	}
	switch strings.ToLower(strings.TrimSpace(cookie.SameSite)) {
	case "":
		cookie.SameSite = ""
	case "strict":
		cookie.SameSite = "Strict"
	case "lax":
		cookie.SameSite = "Lax"
	case "none":
		if !cookie.Secure {
			return HostCookie{}, badRequest("browser_cookie_invalid", "SameSite=None 需要 Secure")
		}
		cookie.SameSite = "None"
	default:
		return HostCookie{}, badRequest("browser_cookie_invalid", "Cookie sameSite 无效")
	}
	return cookie, nil
}

func validCookieName(name string) bool {
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		}
		return false
	}
	return true
}

func validCookieValue(value string) bool {
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~',
			'(', ')', '[', ']', '{', '}', '<', '>', ':', '/', '?', '=', '@', '"':
			continue
		}
		return false
	}
	return true
}
