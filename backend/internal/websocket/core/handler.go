package core

import "context"

type MessageHandler interface {
	HandleMessage(ctx context.Context, conn *Connection, msg *IncomingMessage) error
	MessageTypes() []MessageType
}

type MessageHandlerFunc func(ctx context.Context, conn *Connection, msg *IncomingMessage) error

func (f MessageHandlerFunc) HandleMessage(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
	return f(ctx, conn, msg)
}

func (f MessageHandlerFunc) MessageTypes() []MessageType {
	return nil
}

type Router interface {
	Register(msgType MessageType, handler MessageHandler) error
	Unregister(msgType MessageType) error
	Route(ctx context.Context, conn *Connection, msg *IncomingMessage) error
}

type SubscriptionManager interface {
	Subscribe(topic string, conn *Connection) (bool, error)
	Unsubscribe(topic string, conn *Connection) (bool, error)
	UnsubscribeAll(conn *Connection) error
	Broadcast(topic string, msg Message) error
	GetSubscribers(topic string) []*Connection
	HasSubscribers(topic string) bool
	GetTopics(conn *Connection) []string
}
