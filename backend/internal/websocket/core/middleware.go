package core

import (
	"context"
	"time"

	"github.com/kanivet/backend/internal/faults"
)

type Middleware interface {
	Wrap(next MessageHandlerFunc) MessageHandlerFunc
}

type MiddlewareFunc func(next MessageHandlerFunc) MessageHandlerFunc

func (f MiddlewareFunc) Wrap(next MessageHandlerFunc) MessageHandlerFunc {
	return f(next)
}

func LoggingMiddleware(logger Logger) Middleware {
	return MiddlewareFunc(func(next MessageHandlerFunc) MessageHandlerFunc {
		return func(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
			start := time.Now()
			err := next(ctx, conn, msg)
			duration := time.Since(start)

			if logger != nil {
				logger.LogMessage(conn.ID(), msg.Type(), duration, err)
			}

			return err
		}
	})
}

func RecoveryMiddleware() Middleware {
	return MiddlewareFunc(func(next MessageHandlerFunc) MessageHandlerFunc {
		return func(ctx context.Context, conn *Connection, msg *IncomingMessage) (err error) {
			defer func() {
				if r := recover(); r != nil {
					faults.CaptureExceptionWithContext(
						ErrInvalidMessage,
						map[string]any{
							"panic":        r,
							"conn_id":      conn.ID(),
							"message_type": msg.Type(),
						},
					)
					err = ErrInvalidMessage
				}
			}()
			return next(ctx, conn, msg)
		}
	})
}

type rateLimitMiddleware struct {
	limiter RateLimiter
	exempt  map[MessageType]bool
}

// RateLimitMiddleware throttles inbound messages. Exempt types bypass the
// limiter entirely: dropping subscribe/unsubscribe desyncs subscription state
// from watch refcounts with no signal to the client, so those must never be
// throttled away.
func RateLimitMiddleware(limiter RateLimiter, exempt ...MessageType) Middleware {
	m := &rateLimitMiddleware{limiter: limiter, exempt: make(map[MessageType]bool, len(exempt))}
	for _, t := range exempt {
		m.exempt[t] = true
	}
	return m
}

func (rl *rateLimitMiddleware) Wrap(next MessageHandlerFunc) MessageHandlerFunc {
	return func(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
		if rl.exempt[msg.Type()] {
			return next(ctx, conn, msg)
		}
		if rl.limiter != nil && !rl.limiter.Allow(conn.ID()) {
			return ErrRateLimitExceeded
		}
		return next(ctx, conn, msg)
	}
}

func (rl *rateLimitMiddleware) Cleanup(connID ConnectionID) {
	if rl.limiter != nil {
		rl.limiter.Reset(connID)
	}
}
