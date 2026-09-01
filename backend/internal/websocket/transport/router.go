package transport

import (
	"context"
	"sync"

	"github.com/kanivet/backend/internal/websocket/core"
)

type DefaultRouter struct {
	mu       sync.RWMutex
	handlers map[core.MessageType]core.MessageHandler
}

func NewRouter() *DefaultRouter {
	return &DefaultRouter{
		handlers: make(map[core.MessageType]core.MessageHandler),
	}
}

func (r *DefaultRouter) Register(msgType core.MessageType, handler core.MessageHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[msgType] = handler
	return nil
}

func (r *DefaultRouter) Unregister(msgType core.MessageType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.handlers, msgType)
	return nil
}

func (r *DefaultRouter) Route(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	r.mu.RLock()
	handler, exists := r.handlers[msg.Type()]
	r.mu.RUnlock()

	if !exists {
		return core.ErrHandlerNotFound
	}

	return handler.HandleMessage(ctx, conn, msg)
}
