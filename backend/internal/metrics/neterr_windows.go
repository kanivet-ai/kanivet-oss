//go:build windows

package metrics

import "syscall"

// WSAECONNREFUSED, WSAECONNRESET and WSAECONNABORTED.
const (
	errnoConnRefused = syscall.Errno(10061)
	errnoConnReset   = syscall.Errno(10054)
	errnoConnAborted = syscall.Errno(10053)
)
