//go:build unix

package app

import "syscall"

const viewerNonblock = syscall.O_NONBLOCK
