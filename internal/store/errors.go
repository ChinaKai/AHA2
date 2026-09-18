package store

import "errors"

var ErrActiveTurn = errors.New("task already has an active turn")

var ErrChannelRevision = errors.New("channel resource revision conflict")
var ErrChannelInboxDigest = errors.New("channel inbound event id has a different payload digest")
var ErrChannelNotRetired = errors.New("channel instance is not retired")

// ErrInvalidListenAddress rejects a listen address that is not a literal IP and
// port, which is all the server can bind and all a scheduled task can carry.
var ErrInvalidListenAddress = errors.New("listen address must be an IP address and port")
