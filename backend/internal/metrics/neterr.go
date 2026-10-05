package metrics

import (
	"context"
	"errors"
	"net"
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

// IsTransient reports whether a chart query failed for a reason that may pass
// on its own: the store shed load or failed (429, 5xx), its tunnel dropped
// and is being replaced, or the request timed out or lost its connection. A
// query the store refused, a store that is not there and credentials it
// rejects are not, and asking again would only delay saying so.
func IsTransient(err error) bool {
	var status *HTTPStatusError
	var refused *QueryError
	var auth *MimirAuthError
	switch {
	case err == nil:
		return false
	case errors.As(err, &status):
		return status.Retryable()
	case errors.As(err, &refused), errors.As(err, &auth), errors.Is(err, ErrNoHistorySource), errors.Is(err, context.Canceled):
		return false
	case errors.Is(err, errPortForwardDropped), errors.Is(err, context.DeadlineExceeded):
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) || isPortForwardLikelyDead(err)
}
