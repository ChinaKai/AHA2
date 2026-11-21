package hardware

import (
	"regexp"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var loginANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// stopLoginLocked invalidates every queued credential write and timer. Sent
// markers remain as facts about the completed attempt; only a new explicit
// eligible attempt resets them. Callers hold session.mu.
func (session *terminalSession) stopLoginLocked(status, message string) {
	session.loginGeneration++
	session.loginArmed = false
	session.loginImplicit = false
	session.loginBuffer = ""
	clear(session.promptDecoder.pending)
	session.promptDecoder.pending = nil
	if session.loginDone != nil {
		close(session.loginDone)
		session.loginDone = nil
	}
	session.loginStatus = status
	session.loginError = message
}

func (session *terminalSession) armLoginLocked(options LoginOptions, key string, attempt int, implicit bool) {
	session.stopLoginLocked("waiting", "")
	session.loginOptions = options
	session.loginOwner = key
	session.loginAttempts = attempt
	session.usernameSent = false
	session.passwordSent = false
	session.loginArmed = true
	session.loginImplicit = implicit
	session.loginDone = make(chan struct{})
}

func validLoginOptions(options LoginOptions) bool {
	return (options.TimeoutSeconds == 0 || (options.TimeoutSeconds >= 3 && options.TimeoutSeconds <= 300)) && (options.Retries >= 0 && options.Retries <= 3) && (options.LineEnding == "" || options.LineEnding == "lf" || options.LineEnding == "cr" || options.LineEnding == "crlf")
}

// retry count is a connection-local explicit-attempt limit. A replayed prompt
// never resends a credential. Failure/timeout/cancel disarms; a further explicit
// login needs remaining budget (1 by default, at most 3) or a new connection.
func (manager *Manager) Login(taskID, hardwareID, transport string, options LoginOptions) (domain.HardwareTerminalStatus, error) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return domain.HardwareTerminalStatus{}, ErrNotConnected
	}
	if !validLoginOptions(options) {
		return domain.HardwareTerminalStatus{}, ErrLoginOptionsInvalid
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	session.mu.Lock()
	attachment, ok := session.attachments[key]
	if !ok || session.closing || session.status != "running" {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrNotConnected
	}
	if attachment.readOnly {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrReadOnly
	}
	if options.Cancel {
		if session.loginArmed {
			session.stopLoginLocked("cancelled", "")
		}
		status := session.statusForLocked(taskID, hardwareID, attachment.readOnly)
		session.mu.Unlock()
		return status, nil
	}
	if session.loginStatus == "authenticated" || (session.loginArmed && (!session.loginImplicit || session.usernameSent || session.passwordSent)) {
		status := session.statusForLocked(taskID, hardwareID, attachment.readOnly)
		session.mu.Unlock()
		return status, nil
	}
	if session.username == "" && session.password == "" {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrLoginCredentialsNotConfigured
	}
	normalized := normalizeLoginOptions(options)
	attempt := session.loginAttempts + 1
	if session.loginImplicit {
		attempt = session.loginAttempts
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > normalized.Retries {
		session.mu.Unlock()
		return domain.HardwareTerminalStatus{}, ErrLoginRetryLimit
	}
	session.armLoginLocked(normalized, key, attempt, false)
	generation, done := session.loginGeneration, session.loginDone
	session.mu.Unlock()
	if normalized.Wakeup {
		if err := session.writeLoginLocked(generation, key, loginLineEnding(normalized.LineEnding), "<wakeup>"); err != nil {
			return domain.HardwareTerminalStatus{}, err
		}
	}
	go session.loginTimeout(generation, normalized.TimeoutSeconds, done)
	return session.statusFor(taskID, hardwareID, attachment.readOnly), nil
}

func (session *terminalSession) maybeAutoLogin(chunk []byte) {
	session.mu.Lock()
	if !session.loginArmed || session.closing {
		session.mu.Unlock()
		return
	}
	text, _ := session.promptDecoder.Decode(chunk)
	session.loginBuffer = appendLoginBuffer(session.loginBuffer, text)
	lower := strings.ToLower(loginANSI.ReplaceAllString(session.loginBuffer, ""))
	options := session.loginOptions
	// Credential and success prompts must end the buffered output, so text
	// that merely mentions a prompt never triggers a credential write. A
	// failure only disarms, so after a credential was sent (the buffer then
	// holds only later output) it counts anywhere, including when a fresh
	// login prompt arrives in the same read.
	sent := session.usernameSent || session.passwordSent
	if matchesLoginPrompt(lower, options.FailurePrompts) || (sent && containsLoginPrompt(lower, options.FailurePrompts)) {
		session.stopLoginLocked("failed", "hardware reported a login failure")
		session.mu.Unlock()
		return
	}
	if matchesLoginPrompt(lower, options.SuccessPrompts) {
		session.stopLoginLocked("authenticated", "")
		session.mu.Unlock()
		return
	}
	sendUsername := session.username != "" && !session.usernameSent && matchesLoginPrompt(lower, options.UsernamePrompts)
	sendPassword := session.password != "" && !session.passwordSent && matchesLoginPrompt(lower, options.PasswordPrompts)
	if sendUsername {
		session.usernameSent = true
		session.loginStatus = "username_sent"
	}
	if sendPassword {
		session.passwordSent = true
		session.loginStatus = "credentials_sent"
	}
	if sendUsername || sendPassword {
		session.loginBuffer = ""
	}
	generation, key := session.loginGeneration, session.loginOwner
	username, password := session.username, session.password
	session.mu.Unlock()
	ending := loginLineEnding(options.LineEnding)
	if sendUsername {
		_ = session.writeLogin(generation, key, username+ending, username+displayLineEnding(options.LineEnding))
	}
	if sendPassword {
		_ = session.writeLogin(generation, key, password+ending, "<password>"+displayLineEnding(options.LineEnding))
	}
}

func (session *terminalSession) writeLogin(generation int, key, value, display string) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return session.writeLoginLocked(generation, key, value, display)
}

func (session *terminalSession) writeLoginLocked(generation int, key, value, display string) error {
	session.mu.RLock()
	attachment, present := session.attachments[key]
	allowed := session.loginArmed && !session.closing && session.loginGeneration == generation && session.loginOwner == key && present && !attachment.readOnly
	session.mu.RUnlock()
	if !allowed {
		return nil
	}
	payload := []byte(value)
	if session.codec != nil {
		payload = session.codec.Encode(payload)
	}
	if err := session.writeRawLocked(payload); err != nil {
		return err
	}
	session.record("tx", display, "auto-login", nil, nil)
	return nil
}

func (session *terminalSession) loginTimeout(generation, seconds int, done <-chan struct{}) {
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.loginArmed && session.loginGeneration == generation {
		session.stopLoginLocked("timeout", "hardware login timed out")
	}
}
