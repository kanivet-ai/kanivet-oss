package core

import "errors"

var (
	ErrConnectionClosed     = errors.New("connection is closed")
	ErrInvalidMessage       = errors.New("invalid message format")
	ErrHandlerNotFound      = errors.New("handler not found")
	ErrRateLimitExceeded    = errors.New("rate limit exceeded")
	ErrMessageTooLarge      = errors.New("message too large")
	ErrConnectionNotFound   = errors.New("connection not found")
	ErrSubscriptionNotFound = errors.New("subscription not found")
)
