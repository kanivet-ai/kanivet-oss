//go:build !windows

package metrics

import "syscall"

const (
	errnoConnRefused = syscall.ECONNREFUSED
	errnoConnReset   = syscall.ECONNRESET
	errnoConnAborted = syscall.ECONNABORTED
)
