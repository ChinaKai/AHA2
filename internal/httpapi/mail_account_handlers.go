package httpapi

import (
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

// applyMailAccountPayload applies only explicitly supplied fields. On update an
// omitted or empty string leaves the existing value untouched, so a partial PUT
// such as {"status":"disabled"} cannot erase the account name.
func applyMailAccountPayload(item domain.MailAccount, payload mailAccountPayload, create bool) domain.MailAccount {
	if create || strings.TrimSpace(payload.Name) != "" {
		item.Name = strings.TrimSpace(payload.Name)
	}
	if create || strings.TrimSpace(payload.Address) != "" {
		item.Address = strings.TrimSpace(payload.Address)
	}
	if create || strings.TrimSpace(payload.DisplayName) != "" {
		item.DisplayName = strings.TrimSpace(payload.DisplayName)
	}
	if create || strings.TrimSpace(payload.Provider) != "" {
		item.Provider = strings.TrimSpace(payload.Provider)
	}
	if create || strings.TrimSpace(payload.SMTPHost) != "" {
		item.SMTPHost = strings.TrimSpace(payload.SMTPHost)
	}
	if create || payload.SMTPPort != 0 {
		item.SMTPPort = payload.SMTPPort
	}
	if create || strings.TrimSpace(payload.SMTPSecurity) != "" {
		item.SMTPSecurity = strings.TrimSpace(payload.SMTPSecurity)
	}
	if create || strings.TrimSpace(payload.SMTPUsername) != "" {
		item.SMTPUsername = strings.TrimSpace(payload.SMTPUsername)
	}
	if payload.Aliases != nil {
		aliases := make([]domain.MailAccountAlias, 0, len(payload.Aliases))
		for _, raw := range payload.Aliases {
			aliases = append(aliases, domain.MailAccountAlias{Address: strings.TrimSpace(raw)})
		}
		item.Aliases = aliases
	}
	return item
}

func validateMailAccount(item domain.MailAccount) (domain.MailAccount, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.Address = strings.TrimSpace(item.Address)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	if item.Name == "" {
		return domain.MailAccount{}, errors.New("账号名称必填")
	}
	if _, err := mailpkg.NormalizeRecipient(item.Address); err != nil {
		return domain.MailAccount{}, errors.New("发件地址格式不正确")
	}
	item.Provider = strings.TrimSpace(item.Provider)
	if item.Provider == "" {
		item.Provider = domain.MailProviderNeteaseQiye
	}
	if item.Provider != domain.MailProviderNeteaseQiye {
		return domain.MailAccount{}, errors.New("一期只支持网易企业邮箱")
	}
	item.SMTPSecurity = strings.TrimSpace(item.SMTPSecurity)
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
	rawHost := strings.TrimSpace(item.SMTPHost)
	if rawHost == "" {
		rawHost = domain.MailDefaultSMTPHost
	}
	host, ok := mailpkg.NormalizeSMTPHost(rawHost)
	if !ok {
		return domain.MailAccount{}, errors.New("SMTP 主机必须是网易企业邮箱的主用或备用主机")
	}
	item.SMTPHost = host
	item.SMTPUsername = strings.TrimSpace(item.SMTPUsername)
	if item.SMTPUsername == "" {
		item.SMTPUsername = item.Address
	}
	if _, err := mailpkg.NormalizeRecipient(item.SMTPUsername); err != nil {
		return domain.MailAccount{}, errors.New("SMTP 用户名格式不正确")
	}
	aliases := make([]domain.MailAccountAlias, 0, len(item.Aliases))
	seen := map[string]bool{}
	for _, alias := range item.Aliases {
		address := strings.TrimSpace(alias.Address)
		if address == "" {
			return domain.MailAccount{}, errors.New("别名地址不能为空")
		}
		if _, err := mailpkg.NormalizeRecipient(address); err != nil {
			return domain.MailAccount{}, errors.New("别名地址格式不正确")
		}
		if strings.EqualFold(address, item.Address) {
			return domain.MailAccount{}, errors.New("别名不能与发件地址相同")
		}
		key := strings.ToLower(address)
		if seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, domain.MailAccountAlias{Address: address})
	}
	item.Aliases = aliases
	return item, nil
}

func validateMailAccountStatusTransition(status string) (domain.MailAccountStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return "", true
	case string(domain.MailAccountDisabled):
		return domain.MailAccountDisabled, true
	case "enabled", string(domain.MailAccountUnverified):
		// Re-enabling is explicit only; no credential/host update performs it
		// implicitly.
		return domain.MailAccountUnverified, true
	default:
		return "", false
	}
}

func (s *Server) createMailAccount(writer http.ResponseWriter, request *http.Request) {
	var payload mailAccountPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeMailError(writer, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	item := applyMailAccountPayload(domain.MailAccount{
		ID: domain.NewID("mail_account"), Status: domain.MailAccountUnverified,
	}, payload, true)
	item, err := validateMailAccount(item)
	if err != nil {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, err.Error())
		return
	}
	code := strings.TrimSpace(payload.AuthorizationCode)
	if code != "" {
		item.CredentialRef = domain.MailAccountCredentialRef(item.ID)
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
			writeMailError(writer, http.StatusConflict, domain.MailErrAccountInvalid, "发件地址或别名已被其他账号使用")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, "邮箱账号参数不正确")
		return
	}
	s.audit(request, "mail.account.create", "mail_account", created.ID, map[string]any{
		"status": string(created.Status), "credential_configured": created.CredentialConfigured,
	})
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
	ctx := request.Context()
	id := request.PathValue("id")
	existing, err := s.store.MailAccount(ctx, id)
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
	statusTransition, statusOK := validateMailAccountStatusTransition(payload.Status)
	if !statusOK {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, "状态只能由连接测试或停用/启用操作修改")
		return
	}
	item := applyMailAccountPayload(existing, payload, false)
	item, err = validateMailAccount(item)
	if err != nil {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, err.Error())
		return
	}
	hostChanged := !strings.EqualFold(existing.SMTPHost, item.SMTPHost) ||
		!strings.EqualFold(existing.SMTPUsername, item.SMTPUsername)
	updated, err := s.store.UpdateMailAccount(ctx, item)
	if err != nil {
		if errors.Is(err, store.ErrMailAddressInUse) {
			writeMailError(writer, http.StatusConflict, domain.MailErrAccountInvalid, "发件地址或别名已被其他账号使用")
			return
		}
		if isNoRows(err) {
			writeMailError(writer, http.StatusNotFound, domain.MailErrAccountNotFound, "邮箱账号不存在")
			return
		}
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, "邮箱账号参数不正确")
		return
	}
	rotated := strings.TrimSpace(payload.AuthorizationCode) != ""
	if rotated {
		if err := s.putMailCredential(id, strings.TrimSpace(payload.AuthorizationCode)); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "保存授权码失败")
			return
		}
		if err := s.store.SetMailAccountCredential(ctx, id, time.Now().UTC()); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "更新授权码状态失败")
			return
		}
	} else if hostChanged {
		// A different SMTP endpoint must never inherit "ok" from the old host.
		if err := s.store.ResetMailAccountVerification(ctx, id, time.Now().UTC()); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "重置账号验证状态失败")
			return
		}
	}
	if statusTransition != "" {
		if err := s.store.SetMailAccountStatus(ctx, id, statusTransition, time.Now().UTC()); err != nil {
			writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "更新账号状态失败")
			return
		}
	}
	updated, err = s.store.MailAccount(ctx, id)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	s.audit(request, "mail.account.update", "mail_account", id, map[string]any{
		"status": string(updated.Status), "credential_rotated": rotated, "host_changed": hostChanged,
	})
	if rotated {
		s.audit(request, "mail.account.credential_rotate", "mail_account", id, map[string]any{
			"status": string(updated.Status),
		})
	}
	if statusTransition == domain.MailAccountDisabled {
		s.audit(request, "mail.account.disable", "mail_account", id, nil)
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
	s.audit(request, "mail.account.delete", "mail_account", id, nil)
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
	if _, ok := mailpkg.NormalizeSMTPHost(account.SMTPHost); !ok {
		writeMailError(writer, http.StatusUnprocessableEntity, domain.MailErrAccountInvalid, "SMTP 主机不在允许列表")
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
	expected := store.MailAccountTestExpectation{
		Revision: account.Revision, SMTPHost: account.SMTPHost,
		SMTPUsername: account.SMTPUsername, CredentialRef: ref,
	}
	result := s.mailTester.Test(ctx, account, credential)
	status := domain.MailAccountOK
	errorCode := ""
	if !result.OK {
		status = domain.MailAccountError
		errorCode = string(result.ErrorCode)
	}
	applied, err := s.store.SetMailAccountTestResult(ctx, id, expected, status, errorCode, time.Now().UTC())
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "记录连接测试结果失败")
		return
	}
	updated, err := s.store.MailAccount(ctx, id)
	if err != nil {
		writeMailError(writer, http.StatusInternalServerError, "mail_internal_error", "读取邮箱账号失败")
		return
	}
	s.audit(request, "mail.account.test", "mail_account", id, map[string]any{
		"ok": result.OK && applied, "applied": applied, "error_code": string(result.ErrorCode),
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": result.OK && applied, "result": result, "applied": applied, "account": updated,
	})
}
