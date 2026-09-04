package codexaccount

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type AccountStore interface {
	UpsertCodexAccount(context.Context, domain.CodexAccount) error
	CodexAccount(context.Context, string) (domain.CodexAccount, error)
	ListCodexAccounts(context.Context) ([]domain.CodexAccount, error)
	DeleteCodexAccount(context.Context, string) error
	CodexAccountInUse(context.Context, string) (bool, error)
	ProxySettings(context.Context) (domain.ProxySettings, error)
}

type SecretStore interface {
	PutMany(map[string]string) error
	DeleteMany([]string) error
	Get(string) (string, bool)
}

type Login struct {
	ID           string               `json:"id"`
	Status       string               `json:"status"`
	AuthURL      string               `json:"auth_url"`
	Account      *domain.CodexAccount `json:"account,omitempty"`
	Error        string               `json:"error,omitempty"`
	StartedAt    time.Time            `json:"started_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	ProxyEnabled bool                 `json:"proxy_enabled"`
	state        string
	codeVerifier string
	expiresAt    time.Time
}

type Manager struct {
	ctx           context.Context
	store         AccountStore
	secrets       SecretStore
	httpClient    *http.Client
	authURL       string
	tokenURL      string
	usageURL      string
	modelsURL     string
	clientVersion string

	mu       sync.Mutex
	logins   map[string]*Login
	accounts map[string]*sync.Mutex
}

const (
	oauthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthRedirectURI = "http://localhost:1455/auth/callback"
	oauthLoginTTL    = 10 * time.Minute
)

func New(ctx context.Context, store AccountStore, secrets SecretStore, _ string, codexBinary string) *Manager {
	return &Manager{
		ctx: ctx, store: store, secrets: secrets,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		authURL:       "https://auth.openai.com/oauth/authorize",
		tokenURL:      "https://auth.openai.com/oauth/token",
		usageURL:      "https://chatgpt.com/backend-api/wham/usage",
		modelsURL:     "https://chatgpt.com/backend-api/codex/models",
		clientVersion: detectCodexClientVersion(codexBinary),
		logins:        map[string]*Login{}, accounts: map[string]*sync.Mutex{},
	}
}

func (m *Manager) List(ctx context.Context) ([]domain.CodexAccount, error) {
	return m.store.ListCodexAccounts(ctx)
}

func (m *Manager) ImportLocal(ctx context.Context) (domain.CodexAccount, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return domain.CodexAccount{}, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return domain.CodexAccount{}, fmt.Errorf("读取本机 Codex 登录失败: %w", err)
	}
	return m.Import(ctx, data, "")
}

func (m *Manager) Import(ctx context.Context, data []byte, label string) (domain.CodexAccount, error) {
	return m.importWithProxy(ctx, data, label, nil)
}

func (m *Manager) importWithProxy(ctx context.Context, data []byte, label string, proxyEnabled *bool) (domain.CodexAccount, error) {
	metadata, err := parseAuth(data)
	if err != nil {
		return domain.CodexAccount{}, err
	}
	items, err := m.store.ListCodexAccounts(ctx)
	if err != nil {
		return domain.CodexAccount{}, err
	}
	id := ""
	var existing *domain.CodexAccount
	for _, item := range items {
		if metadata.AccountID != "" && item.AccountID == metadata.AccountID {
			id = item.ID
			existing = &item
			break
		}
		if metadata.Email != "" && strings.EqualFold(item.Email, metadata.Email) {
			id = item.ID
			existing = &item
			break
		}
	}
	if id == "" {
		sum := sha256.Sum256([]byte(metadata.Email + "\x00" + metadata.AccountID + "\x00" + string(data)))
		id = fmt.Sprintf("codex_account_%x", sum[:10])
	}
	now := time.Now().UTC()
	account := domain.CodexAccount{
		ID: id, Label: strings.TrimSpace(label), Email: metadata.Email, AccountID: metadata.AccountID,
		PlanType: metadata.PlanType, Status: "ready", CredentialRef: credentialRef(id),
		CredentialConfigured: true, UpdatedAt: now, LastUsedAt: now,
	}
	if existing != nil {
		account.ProxyEnabled = existing.ProxyEnabled
	}
	if proxyEnabled != nil {
		account.ProxyEnabled = *proxyEnabled
	}
	if account.Label == "" {
		account.Label = account.Email
	}
	if account.Label == "" {
		account.Label = "Codex " + id[len(id)-8:]
	}
	for _, item := range items {
		if item.ID == id {
			account.CreatedAt = item.CreatedAt
			break
		}
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	normalized, _ := json.Marshal(metadata.Raw)
	if err := m.secrets.PutMany(map[string]string{account.CredentialRef: string(normalized)}); err != nil {
		return domain.CodexAccount{}, fmt.Errorf("保存 Codex 凭据失败: %w", err)
	}
	if err := m.store.UpsertCodexAccount(ctx, account); err != nil {
		_ = m.secrets.DeleteMany([]string{account.CredentialRef})
		return domain.CodexAccount{}, err
	}
	return account, nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	inUse, err := m.store.CodexAccountInUse(ctx, id)
	if err != nil {
		return err
	}
	if inUse {
		return fmt.Errorf("该账号已被模型或历史运行配置使用")
	}
	account, err := m.store.CodexAccount(ctx, id)
	if err != nil {
		return err
	}
	if err := m.store.DeleteCodexAccount(ctx, id); err != nil {
		return err
	}
	return m.secrets.DeleteMany([]string{account.CredentialRef})
}

func (m *Manager) StartOAuthLogin(useProxy ...bool) (Login, error) {
	state, err := randomURLToken(32)
	if err != nil {
		return Login{}, fmt.Errorf("生成 OAuth state 失败: %w", err)
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return Login{}, fmt.Errorf("生成 PKCE verifier 失败: %w", err)
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":              {"code"},
		"client_id":                  {oauthClientID},
		"redirect_uri":               {oauthRedirectURI},
		"scope":                      {"openid profile email offline_access api.connectors.read api.connectors.invoke"},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(challengeBytes[:])},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {state},
		"originator":                 {"codex_cli_rs"},
	}
	now := time.Now().UTC()
	login := &Login{
		ID: domain.NewID("codex_login"), Status: "awaiting_callback",
		AuthURL: m.authURL + "?" + query.Encode(), StartedAt: now, UpdatedAt: now,
		state: state, codeVerifier: verifier, expiresAt: now.Add(oauthLoginTTL),
	}
	if len(useProxy) > 0 {
		login.ProxyEnabled = useProxy[0]
	}
	m.mu.Lock()
	m.logins[login.ID] = login
	m.mu.Unlock()
	return cloneLogin(login), nil
}

func (m *Manager) SubmitCallback(ctx context.Context, id, callbackURL, label string) (Login, error) {
	parsed, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil || parsed.Scheme != "http" || parsed.Host != "localhost:1455" || parsed.Path != "/auth/callback" {
		return Login{}, fmt.Errorf("Callback URL 必须是完整的 %s?... 地址", oauthRedirectURI)
	}

	m.mu.Lock()
	login := m.logins[id]
	if login == nil {
		m.mu.Unlock()
		return Login{}, fmt.Errorf("登录任务不存在")
	}
	if login.Status != "awaiting_callback" {
		m.mu.Unlock()
		return Login{}, fmt.Errorf("登录任务当前状态不允许提交 Callback: %s", login.Status)
	}
	if time.Now().After(login.expiresAt) {
		login.Status = "expired"
		login.UpdatedAt = time.Now().UTC()
		m.mu.Unlock()
		return Login{}, fmt.Errorf("登录链接已过期，请重新生成")
	}
	query := parsed.Query()
	if oauthError := strings.TrimSpace(query.Get("error")); oauthError != "" {
		login.Status = "failed"
		login.Error = oauthError
		login.UpdatedAt = time.Now().UTC()
		m.mu.Unlock()
		return Login{}, fmt.Errorf("OpenAI 登录失败: %s", oauthError)
	}
	if query.Get("state") != login.state {
		m.mu.Unlock()
		return Login{}, fmt.Errorf("Callback state 校验失败")
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		m.mu.Unlock()
		return Login{}, fmt.Errorf("Callback URL 缺少授权 code")
	}
	verifier := login.codeVerifier
	proxyEnabled := login.ProxyEnabled
	login.Status = "exchanging"
	login.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()

	authJSON, err := m.exchangeCode(ctx, code, verifier, proxyEnabled)
	if err != nil {
		m.updateLogin(id, func(item *Login) {
			item.Status = "failed"
			item.Error = err.Error()
		})
		return Login{}, err
	}
	account, err := m.importWithProxy(ctx, authJSON, label, &proxyEnabled)
	if err != nil {
		m.updateLogin(id, func(item *Login) {
			item.Status = "failed"
			item.Error = err.Error()
		})
		return Login{}, err
	}
	m.updateLogin(id, func(item *Login) {
		item.Status = "succeeded"
		item.Account = &account
		item.state = ""
		item.codeVerifier = ""
	})
	return m.Login(id)
}

func (m *Manager) Login(id string) (Login, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.logins[id]
	if item == nil {
		return Login{}, fmt.Errorf("登录任务不存在")
	}
	return cloneLogin(item), nil
}

func (m *Manager) CancelLogin(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.logins[id]
	if item == nil {
		return fmt.Errorf("登录任务不存在")
	}
	if item.Status == "awaiting_callback" {
		item.Status = "cancelled"
		item.state = ""
		item.codeVerifier = ""
		item.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (m *Manager) exchangeCode(ctx context.Context, code, verifier string, useProxy bool) ([]byte, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthRedirectURI},
		"client_id":     {oauthClientID},
		"code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 OAuth Token 请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "codex_cli_rs")
	client := m.httpClient
	if useProxy {
		settings, settingsErr := m.store.ProxySettings(ctx)
		if settingsErr != nil {
			return nil, fmt.Errorf("读取代理设置失败: %w", settingsErr)
		}
		client, settingsErr = proxyconfig.Client(m.httpClient, settings)
		if settingsErr != nil {
			return nil, fmt.Errorf("代理设置无效: %w", settingsErr)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 OAuth Token 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := oauthErrorMessage(io.LimitReader(response.Body, 1<<20))
		if message != "" {
			return nil, fmt.Errorf("OAuth Token 请求失败: HTTP %d: %s", response.StatusCode, message)
		}
		return nil, fmt.Errorf("OAuth Token 请求失败: HTTP %d", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil {
		return nil, fmt.Errorf("解析 OAuth Token 响应失败")
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return nil, fmt.Errorf("OAuth Token 响应缺少 Access Token 或 Refresh Token")
	}
	claims := jwtClaims(token.IDToken)
	if len(claims) == 0 {
		claims = jwtClaims(token.AccessToken)
	}
	authClaims, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID := stringClaim(authClaims, "chatgpt_account_id", "account_id")
	auth := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token": token.IDToken, "access_token": token.AccessToken,
			"refresh_token": token.RefreshToken, "account_id": accountID,
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
	}
	return json.Marshal(auth)
}

func oauthErrorMessage(reader io.Reader) string {
	var payload struct {
		Error            any    `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return ""
	}
	message := strings.TrimSpace(payload.ErrorDescription)
	switch value := payload.Error.(type) {
	case string:
		if message == "" {
			message = value
		}
	case map[string]any:
		parts := make([]string, 0, 3)
		for _, key := range []string{"code", "type", "message"} {
			if item, ok := value[key].(string); ok && strings.TrimSpace(item) != "" {
				parts = append(parts, strings.TrimSpace(item))
			}
		}
		if len(parts) > 0 {
			message = strings.Join(parts, ": ")
		}
	}
	message = strings.Map(func(value rune) rune {
		if value < 0x20 || value == 0x7f {
			return ' '
		}
		return value
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func randomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (m *Manager) LockAccount(id string) func() {
	m.mu.Lock()
	lock := m.accounts[id]
	if lock == nil {
		lock = &sync.Mutex{}
		m.accounts[id] = lock
	}
	m.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (m *Manager) PrepareProfile(
	ctx context.Context,
	accountID string,
	item domain.Workspace,
	workDir string,
	sessionID string,
) (string, error) {
	account, err := m.store.CodexAccount(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("Codex 账号不存在: %w", err)
	}
	authJSON, ok := m.secrets.Get(account.CredentialRef)
	if !ok || strings.TrimSpace(authJSON) == "" {
		return "", fmt.Errorf("Codex 账号凭据未配置")
	}
	profileDir := runtimeProfileDir(item, workDir, sessionID)
	config := "cli_auth_credentials_store = \"file\"\n"
	if item.Transport == "native" {
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(profileDir, "auth.json"), []byte(authJSON), 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte(config), 0o600); err != nil {
			return "", err
		}
		return profileDir, nil
	}
	runner := workspace.RunnerFor(item)
	for name, content := range map[string]string{"auth.json": authJSON, "config.toml": config} {
		target := path.Join(profileDir, name)
		result, runErr := runner.Run(ctx, workspace.Command{
			Executable: "sh",
			Args:       []string{"-c", `umask 077; mkdir -p "$1"; cat > "$2"`, "aha-codex-auth", profileDir, target},
			Stdin:      content, Timeout: 30 * time.Second,
		}, nil)
		if runErr != nil {
			return "", runErr
		}
		if result.ExitCode != 0 {
			return "", fmt.Errorf("写入 Codex 账号失败: %s", strings.TrimSpace(result.Stderr))
		}
	}
	return profileDir, nil
}

func (m *Manager) SyncProfile(ctx context.Context, accountID string, item domain.Workspace, profileDir string) {
	var data []byte
	if item.Transport == "native" {
		data, _ = os.ReadFile(filepath.Join(profileDir, "auth.json"))
	} else {
		result, err := workspace.RunnerFor(item).Run(ctx, workspace.Command{
			Executable: "sh", Args: []string{"-c", `cat "$1"`, "aha-codex-auth-read", path.Join(profileDir, "auth.json")},
			Timeout: 20 * time.Second,
		}, nil)
		if err == nil && result.ExitCode == 0 {
			data = []byte(result.Stdout)
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	account, err := m.store.CodexAccount(ctx, accountID)
	if err != nil {
		return
	}
	metadata, err := parseAuth(data)
	if err != nil || account.AccountID != "" && metadata.AccountID != "" && account.AccountID != metadata.AccountID {
		return
	}
	normalized, _ := json.Marshal(metadata.Raw)
	_ = m.secrets.PutMany(map[string]string{account.CredentialRef: string(normalized)})
	account.LastUsedAt = time.Now().UTC()
	account.UpdatedAt = account.LastUsedAt
	_ = m.store.UpsertCodexAccount(ctx, account)
}

type parsedAuth struct {
	Raw       map[string]any
	Email     string
	AccountID string
	PlanType  string
}

func parseAuth(data []byte) (parsedAuth, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return parsedAuth{}, fmt.Errorf("解析 Codex auth.json 失败: %w", err)
	}
	tokens, _ := raw["tokens"].(map[string]any)
	accessToken, _ := tokens["access_token"].(string)
	idToken, _ := tokens["id_token"].(string)
	personalToken, _ := raw["personal_access_token"].(string)
	if strings.TrimSpace(accessToken) == "" && strings.TrimSpace(personalToken) == "" {
		return parsedAuth{}, fmt.Errorf("auth.json 中没有官方 Codex Access Token")
	}
	claims := jwtClaims(idToken)
	if len(claims) == 0 {
		claims = jwtClaims(accessToken)
	}
	authClaims, _ := claims["https://api.openai.com/auth"].(map[string]any)
	email := stringClaim(claims, "email")
	accountID := stringClaim(authClaims, "chatgpt_account_id", "account_id")
	if accountID == "" {
		accountID, _ = tokens["account_id"].(string)
	}
	return parsedAuth{
		Raw: raw, Email: email, AccountID: accountID,
		PlanType: stringClaim(authClaims, "chatgpt_plan_type"),
	}, nil
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func stringClaim(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func credentialRef(id string) string {
	return "codex-account/" + id + "/auth"
}

func runtimeProfileDir(item domain.Workspace, workDir, sessionID string) string {
	if item.Transport == "native" {
		return filepath.Join(workDir, ".aha2-context", "runtime", "codex-auth", sessionID)
	}
	return path.Join(strings.ReplaceAll(workDir, `\`, "/"), ".aha2-context", "runtime", "codex-auth", sessionID)
}

func (m *Manager) updateLogin(id string, update func(*Login)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.logins[id]; item != nil {
		update(item)
		item.UpdatedAt = time.Now().UTC()
	}
}

func cloneLogin(item *Login) Login {
	copy := *item
	copy.state = ""
	copy.codeVerifier = ""
	copy.expiresAt = time.Time{}
	if item.Account != nil {
		account := *item.Account
		copy.Account = &account
	}
	return copy
}
