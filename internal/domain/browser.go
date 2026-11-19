package domain

import "time"

const (
	BrowserKindEdge = "edge"

	BrowserProfileStateIdle         = "idle"
	BrowserProfileStateStarting     = "starting"
	BrowserProfileStateReady        = "ready"
	BrowserProfileStateAuthRequired = "auth_required"
	BrowserProfileStateBusy         = "busy"
	BrowserProfileStateError        = "error"

	BrowserSessionStateStarting     = "starting"
	BrowserSessionStateReady        = "ready"
	BrowserSessionStateAuthRequired = "auth_required"
	BrowserSessionStateBusy         = "busy"
	BrowserSessionStateClosed       = "closed"
	BrowserSessionStateError        = "error"

	BrowserGrantStatusActive  = "active"
	BrowserGrantStatusExpired = "expired"
	BrowserGrantStatusRevoked = "revoked"

	BrowserCapabilityOpenTab  = "open_tab"
	BrowserCapabilityNavigate = "navigate"
	BrowserCapabilitySnapshot = "snapshot"
	BrowserCapabilityInteract = "interact"
	BrowserCapabilityFiles    = "files"
	BrowserCapabilityCookies  = "cookies"
)

// BrowserCapabilities is the closed capability set. Anything else is rejected
// at grant creation instead of being silently ignored. Existing grants keep
// the capability array they were created with, so adding a capability here
// never upgrades an old grant; the Owner must re-grant it explicitly.
var BrowserCapabilities = []string{
	BrowserCapabilityOpenTab,
	BrowserCapabilityNavigate,
	BrowserCapabilitySnapshot,
	BrowserCapabilityInteract,
	BrowserCapabilityFiles,
	BrowserCapabilityCookies,
}

// BrowserDefaultCapabilities is granted when a caller names no capability. It
// stays at the pre-interaction set so neither an old caller nor a re-granted
// old lease can silently acquire interact/files/cookies.
var BrowserDefaultCapabilities = []string{
	BrowserCapabilityOpenTab,
	BrowserCapabilityNavigate,
	BrowserCapabilitySnapshot,
}

// BrowserProfile is the logical handle for a host-owned persistent browser
// profile. The real user-data directory never crosses this boundary: the host
// resolves it from the profile ID.
type BrowserProfile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	BrowserKind string    `json:"browser_kind"`
	HostID      string    `json:"host_id"`
	State       string    `json:"state"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// BrowserSession is one running browser instance for a profile. A profile has
// at most one non-closed session.
type BrowserSession struct {
	ID            string    `json:"id"`
	ProfileID     string    `json:"profile_id"`
	HostID        string    `json:"host_id"`
	HostSessionID string    `json:"-"`
	State         string    `json:"state"`
	Error         string    `json:"error,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastUsedAt    time.Time `json:"last_used_at,omitempty"`
	ClosedAt      time.Time `json:"closed_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (item BrowserSession) Active() bool {
	return item.State != BrowserSessionStateClosed
}

// BrowserTaskGrant gives one Task time-bounded use of a profile's session.
// Every operation validates the grant, its session and the target tab.
type BrowserTaskGrant struct {
	ID                    string    `json:"id"`
	TaskID                string    `json:"task_id"`
	ProfileID             string    `json:"profile_id"`
	SessionID             string    `json:"session_id"`
	Capabilities          []string  `json:"capabilities"`
	ExpiresAt             time.Time `json:"expires_at"`
	HoldID                string    `json:"-"`
	HoldOriginalExpiresAt time.Time `json:"-"`
	HoldExpiresAt         time.Time `json:"-"`
	RevokedAt             time.Time `json:"revoked_at,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Active reports whether the grant still authorizes work: it is not revoked
// and has not passed an explicit expiry. A zero ExpiresAt is the Owner's
// continuous switch: the grant stays active until it is released or its
// session closes.
func (item BrowserTaskGrant) Active(now time.Time) bool {
	if !item.RevokedAt.IsZero() {
		return false
	}
	return item.ExpiresAt.IsZero() || now.Before(item.ExpiresAt)
}

func (item BrowserTaskGrant) Status(now time.Time) string {
	if !item.RevokedAt.IsZero() {
		return BrowserGrantStatusRevoked
	}
	if !item.ExpiresAt.IsZero() && !now.Before(item.ExpiresAt) {
		return BrowserGrantStatusExpired
	}
	return BrowserGrantStatusActive
}

func (item BrowserTaskGrant) Has(capability string) bool {
	for _, value := range item.Capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

const (
	// BrowserTabOriginAgent is a page AHA opened. BrowserTabOriginOwner is a
	// page discovered in the visible browser that the Owner opened. Under the
	// profile-exclusive model origin is display metadata only; it never blocks
	// the current lease holder from operating the tab.
	BrowserTabOriginAgent = "agent"
	BrowserTabOriginOwner = "owner"
)

// BrowserTab records the visible tab's current lease holder and host identity.
// Tab authorization is by exclusive Profile lease and session, not by tab
// ownership.
type BrowserTab struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	GrantID   string `json:"grant_id"`
	TaskID    string `json:"task_id"`
	HostTabID string `json:"-"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Origin    string `json:"origin"`
	// Active and Focused are transient view fields from the host's page list;
	// they are not stored in browser_tabs. Active means the page is visible in
	// its window; Focused means its window is the foreground one, which is what
	// the live preview should follow when several windows are open.
	Active    bool      `json:"active,omitempty"`
	Focused   bool      `json:"focused,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
