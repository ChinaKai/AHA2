package store

import "errors"

var ErrActiveTurn = errors.New("task already has an active turn")

// ErrTaskNotRunnable rejects scheduling against a Task that was completed,
// cancelled, or blocked concurrently with the claim. It is checked inside the
// claim transaction so a terminal transition cannot race a new Turn into life.
var ErrTaskNotRunnable = errors.New("task is not runnable")

// ErrRoundTerminal rejects a new Turn for a Round that was already settled. It
// is checked inside the insert transaction, because a caller's optimistic
// Round read can race an interrupt or settlement that closes the Round.
var ErrRoundTerminal = errors.New("round is terminal")

// ErrTaskAgentLimitBelowCount rejects a collaboration downgrade whose new
// Max Agents is below the agent count at the moment the write commits. The
// count is checked inside the same write transaction as the update, so a
// legitimate agent creation that commits after the caller's own count check
// cannot be undercut by a smaller limit.
var ErrTaskAgentLimitBelowCount = errors.New("max_agents cannot be lower than the current agent count")

var ErrChannelRevision = errors.New("channel resource revision conflict")
var ErrChannelInboxDigest = errors.New("channel inbound event id has a different payload digest")
var ErrChannelNotRetired = errors.New("channel instance is not retired")

// ErrInvalidListenAddress rejects a listen address that is not a literal IP and
// port, which is all the server can bind and all a scheduled task can carry.
var ErrInvalidListenAddress = errors.New("listen address must be an IP address and port")
