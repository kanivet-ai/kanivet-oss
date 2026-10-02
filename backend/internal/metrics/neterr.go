package metrics

import (
	"errors"
	"syscall"
)

// Windows reports socket failures as WSA error codes, which never match the
// Unix syscall constants, so each check also tests the platform's own code
// (see neterr_windows.go).

// IsConnRefused reports whether nothing was listening at the other end.
func IsConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, errnoConnRefused)
}

// IsConnReset reports whether the other end dropped an open connection.
func IsConnReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, errnoConnReset) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, errnoConnAborted)
}
