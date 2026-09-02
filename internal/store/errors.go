package store

import "errors"

var ErrActiveTurn = errors.New("task already has an active turn")
