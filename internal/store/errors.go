package store

import "errors"

var ErrActiveTurn = errors.New("task already has an active turn")

var ErrChannelRevision = errors.New("channel resource revision conflict")
var ErrChannelInboxDigest = errors.New("channel inbound event id has a different payload digest")
var ErrChannelNotRetired = errors.New("channel instance is not retired")
