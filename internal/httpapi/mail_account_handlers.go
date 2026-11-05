package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

type mailAccountPayload struct {
	Name              string   `json:"name"`
	Address           string   `json:"address"`
	DisplayName       string   `json:"display_name"`
	Provider          string   `json:"provider"`
	SMTPHost          string   `json:"smtp_host"`
	SMTPPort          int      `json:"smtp_port"`
	SMTPSecurity      string   `json:"smtp_security"`
	SMTPUsername      string   `json:"smtp_username"`
	AuthorizationCode string   `json:"authorization_code"`
	Aliases           []string `json:"aliases"`
	Status            string   `json:"status"`
}

func (s *Server) listMailAccounts(writer http.ResponseWriter, request *http.Request) {
	accounts, err := s.store.MailAccounts(request.Context(), 100)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "accounts": accounts})
}

func validateMailAccountPayload(payload mailAccountPayload, item domain.MailAccount) (domain.MailAccount, error) {
	item.Name = strings.TrimSpace(payload.Name)
	item.Address = strings.TrimSpace(payload.Address)
	item.DisplayName = strings.TrimSpace(payload.DisplayName)
	item.Provider = strings.TrimSpace(payload.Provider)
	item.SMTPHost = strings.TrimSpace(payload.SMTPHost)
	item.SMTPSecurity = strings.TrimSpace(payload.SMTPSecurity)
	item.SMTPUsername = strings.TrimSpace(payload.SMTPUsername)
	if _, err := mailpkg.NormalizeRecipient(item.Address); err != nil {
		return domain.MailAccount{}, errors.New("发件地址格式不正确")
	}
	if item.Provider == "" {
		item.Provider = domain.MailProviderNeteaseQiye
	}
	if item.Provider != domain.MailProviderNeteaseQiye {
		return domain.MailAccount{}, errors.New("一期只支持网易企业邮箱")
	}
	if item.SMTPSecurity == "" {
		item.SMTPSecurity = domain.MailSMTPSecuritySSL
	}
	if item.SMTPSecurity != domain.MailSMTPSecuritySSL {
		return domain.MailAccount{}, errors.New("只允许隐式 TLS 连接")
	}
	if item.SMTPPort == 0 {
		item.SMTPPort = domain.MailDefaultSMTPPort
	}
	if item.SMTPPort != domain.MailDefaultSMTPPort {
		return domain.MailAccount{}, errors.New("网易企业邮箱仅允许 465 端口")
	}
	if item.SMTPHost == "" {
		item.SMTPHost = domain.MailDefaultSMTPHost
	}
	if item.SMTPUsername == "" {
		item.SMTPUsername = item.Address
	}
	aliases := make([]domain.MailAccountAlias, 0, len(payload.Aliases))
	for _, raw := range payload.Aliases {
		address, err := mailpkg.NormalizeRecipient(strings.TrimSpace(raw))
		if err != nil {
			return domain.MailAccount{}, errors.New("别名地址格式不正确")
		}
		aliases = append(aliases, domain.MailAccountAlias{Address: address})
	}
	item.Aliases = aliases
	return item, nil
}

func (s *Server) createMailAccount(writer http.ResponseWriter, request *http.Request) {
	var payload mailAccountPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	item := domain.MailAccount{
		ID:      domain.NewID("mail_account"),
		Status:  domain.MailAccountUnverified,
		Aliases: nil,
	}
	item, err := validateMailAccountPayload(payload, item)
	if err != nil {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, err.Error())
		return
	}
	code := strings.TrimSpace(payload.AuthorizationCode)
	if code != "" {
		item.CredentialRef = domain.MailAccountCredentialRef(item.ID)
	}
	if code != "" {
		if err := s.putMailCredential(item.ID, code); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "保存授权码失败")
			return
		}
	}
	created, err := s.store.CreateMailAccount(request.Context(), item)
	if err != nil {
		if code != "" {
			s.deleteMailCredential(item.ID)
		}
		if errors.Is(err, store.ErrMailAddressInUse) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "发件地址或别名已被其他账号使用")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "邮箱账号参数不正确")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "account": created})
}

func (s *Server) mailAccountDetail(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.MailAccount(request.Context(), request.PathValue("id"))
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "account": item})
}

func (s *Server) updateMailAccount(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.MailAccount(request.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	var payload mailAccountPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	item := existing
	if strings.TrimSpace(payload.Name) != "" || payload.Name == "" {
		item.Name = strings.TrimSpace(payload.Name)
	}
	item = domain.MailAccount{
		ID: item.ID, Name: item.Name, Address: item.Address, DisplayName: item.DisplayName,
		Provider: item.Provider, SMTPHost: item.SMTPHost, SMTPPort: item.SMTPPort,
		SMTPSecurity: item.SMTPSecurity, SMTPUsername: item.SMTPUsername,
		Status: item.Status, LastTestAt: item.LastTestAt,
		LastTestErrorCode: item.LastTestErrorCode, IMAPHost: item.IMAPHost,
		IMAPPort: item.IMAPPort, IMAPEnabled: item.IMAPEnabled,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Aliases: item.Aliases,
	}
	if payload.Aliases != nil {
		item.Aliases = nil
	} else {
		converted := make([]string, 0, len(existing.Aliases))
		for _, alias := range existing.Aliases {
			converted = append(converted, alias.Address)
		}
		payload.Aliases = converted
	}
	if strings.TrimSpace(payload.Address) == "" {
		payload.Address = item.Address
	}
	if strings.TrimSpace(payload.DisplayName) == "" {
		payload.DisplayName = item.DisplayName
	}
	if strings.TrimSpace(payload.Provider) == "" {
		payload.Provider = item.Provider
	}
	if strings.TrimSpace(payload.SMTPHost) == "" {
		payload.SMTPHost = item.SMTPHost
	}
	if payload.SMTPPort == 0 {
		payload.SMTPPort = item.SMTPPort
	}
	if strings.TrimSpace(payload.SMTPSecurity) == "" {
		payload.SMTPSecurity = item.SMTPSecurity
	}
	if strings.TrimSpace(payload.SMTPUsername) == "" {
		payload.SMTPUsername = item.SMTPUsername
	}
	if payload.Name == "" {
		payload.Name = item.Name
	}
	item, err = validateMailAccountPayload(payload, item)
	if err != nil {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, err.Error())
		return
	}
	// Status transitions are limited to disable/enable; ok/error are only set
	// by a connection test.
	if status := strings.TrimSpace(payload.Status); status != "" {
		switch domain.MailAccountStatus(status) {
		case domain.MailAccountDisabled, domain.MailAccountUnverified:
			item.Status = domain.MailAccountStatus(status)
		default:
			writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "状态只能由连接测试或停用/启用操作修改")
			return
		}
	}
	updated, err := s.store.UpdateMailAccount(request.Context(), item)
	if err != nil {
		if errors.Is(err, store.ErrMailAddressInUse) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "发件地址或别名已被其他账号使用")
			return
		}
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrInvalidState, "邮箱账号参数不正确")
		return
	}
	if code := strings.TrimSpace(payload.AuthorizationCode); code != "" {
		if err := s.putMailCredential(id, code); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "保存授权码失败")
			return
		}
		if err := s.store.SetMailAccountCredential(request.Context(), id, time.Now().UTC()); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "更新授权码状态失败")
			return
		}
		// Rotation requires a re-test, but must not silently re-enable a
		// disabled account.
		if existing.Status == domain.MailAccountDisabled && strings.TrimSpace(payload.Status) == "" {
			if err := s.store.SetMailAccountStatus(request.Context(), id, domain.MailAccountDisabled, time.Now().UTC()); err != nil {
				writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "更新账号状态失败")
				return
			}
		}
		if updated, err = s.store.MailAccount(request.Context(), id); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "account": updated})
}

func (s *Server) deleteMailAccount(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := s.store.DeleteMailAccount(request.Context(), id); err != nil {
		if errors.Is(err, store.ErrMailAccountInUse) {
			writeMailError(writer, http.StatusConflict, domain.MailErrInvalidState, "账号已有历史邮件或绑定，只能停用")
			return
		}
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "删除邮箱账号失败")
		return
	}
	s.deleteMailCredential(id)
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) testMailAccount(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	id := request.PathValue("id")
	account, err := s.store.MailAccount(ctx, id)
	if err != nil {
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	if account.Status == domain.MailAccountDisabled {
		writeMailError(writer, http.StatusConflict, domain.MailErrAccountDisabled, "账号已停用，请先启用")
		return
	}
	ref, err := s.store.MailAccountCredentialRef(ctx, id)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取凭据引用失败")
		return
	}
	credential := ""
	if ref != "" && s.secrets != nil {
		credential, _ = s.secrets.Get(ref)
	}
	if strings.TrimSpace(credential) == "" {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrCredentialMissing, "请先配置客户端授权码")
		return
	}
	result := s.mailTester.Test(ctx, account, credential)
	status := domain.MailAccountOK
	errorCode := ""
	if !result.OK {
		status = domain.MailAccountError
		errorCode = string(result.ErrorCode)
	}
	if err := s.store.SetMailAccountTestResult(ctx, id, status, errorCode, time.Now().UTC()); err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "记录连接测试结果失败")
		return
	}
	updated, err := s.store.MailAccount(ctx, id)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": result.OK, "result": result, "account": updated,
	})
}

// ensure context import stays used when the file evolves.
var _ context.Context
