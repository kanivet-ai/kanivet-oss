package core

import (
	"time"
)

type Logger interface {
	LogMessage(connID ConnectionID, msgType MessageType, duration time.Duration, err error)
	LogConnection(connID ConnectionID, event string, metadata map[string]interface{})
}

type Metrics interface {
	ConnectionAdded()
	ConnectionRemoved()
	MessageReceived(msgType MessageType)
	MessageSent(msgType MessageType)
	MessageError(operation string, err error)
	SubscriptionAdded(topic string)
	SubscriptionRemoved(topic string)
}

type RateLimiter interface {
	Allow(connID ConnectionID) bool
	Reset(connID ConnectionID)
}

type Cleanup interface {
	Cleanup(connID ConnectionID)
}

type ConnectionCloseHandler interface {
	OnConnectionClose(conn *Connection)
}
