# WebSocket Infrastructure

This package provides a production-ready, scalable WebSocket infrastructure following Google engineering standards and principal engineering best practices.

## Architecture

The WebSocket infrastructure is organized into clean, focused layers:

### Core (`/core`)
- **Connection**: Manages individual WebSocket connections with proper lifecycle, ping/pong, timeouts, backpressure
- **Hub**: Central manager for all connections with configurable limits and graceful shutdown
- **Message**: Type-safe message handling with interfaces and JSON marshaling
- **Interfaces**: Clean interfaces for logging, metrics, and rate limiting
- **Middleware**: Pluggable middleware system for cross-cutting concerns

### Transport (`/transport`)
- **Server**: HTTP to WebSocket upgrade handling with compression support
- **Router**: Message routing to appropriate handlers based on message type
- **SubscriptionManager**: Topic-based subscription management

### Middleware (`/middleware`)
- **Metrics**: Connection and message metrics with atomic counters
- **RateLimiter**: Multiple rate limiting strategies (token bucket, sliding window)

## Key Features Implemented

### ✅ Production Features
1. **Read Limits**: `conn.SetReadLimit()` configured per connection
2. **Ping/Pong**: Automatic heartbeat with configurable intervals
3. **Connection Timeouts**: Proper timeout handling for idle connections
4. **Compression**: WebSocket compression enabled by default
5. **Backpressure**: Bounded channels with overflow handling
6. **Rate Limiting**: Token bucket and sliding window algorithms
7. **Graceful Shutdown**: Proper cleanup of all connections and resources
8. **Error Recovery**: Panic recovery middleware
9. **Observability**: Comprehensive metrics and logging

### ✅ Architecture Excellence
1. **Separation of Concerns**: Infrastructure completely isolated from business logic
2. **Pluggable Design**: Easy to extend with new handlers and middleware
3. **Type Safety**: Strong typing throughout with clear interfaces
4. **Topic Decoupling**: Neutral topics package shared across components
5. **Resource Management**: Proper connection lifecycle and cleanup
6. **Scalability**: Efficient subscription management and message routing
7. **Clean Dependencies**: Gorilla WebSocket contained only in core infrastructure (3 files)
8. **No Abstraction Overhead**: Direct use of websocket library without unnecessary adapter layers
9. **Domain Separation**: Business logic handlers moved to domain-specific packages (/exec, /api, etc.)

## Usage

```go
// Pure infrastructure setup
wsServer := websocket.NewServer()

// Register business logic handlers
wsServer.RegisterHandler("exec", exec.NewPodExecHandler(k8sClient))
wsServer.RegisterHandler("subscribe", k8s.NewSubscribeHandler(k8sWatcher))

// Serve WebSocket connections
http.Handle("/ws", wsServer)

// Access the hub for broadcasting
hub := wsServer.Hub()
hub.Broadcast("events:cluster1", eventMessage)

// Graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
wsServer.Shutdown(ctx)
```

## Message Types

### Resource Subscriptions
```json
{
  "type": "subscribe",
  "payload": {
    "cluster": "prod",
    "group": "apps",
    "version": "v1",
    "kind": "deployments",
    "namespace": "default"
  }
}
```

### Terminal Exec
```json
{
  "type": "exec",
  "payload": {
    "type": "exec_start",
    "cluster": "prod",
    "namespace": "default",
    "pod": "my-pod",
    "container": "main"
  }
}
```

## Configuration

All configuration is centralized in `HubConfig` and `ConnectionConfig`:

```go
config := &core.HubConfig{
    ConnectionTimeout: 60 * time.Second,
    HeartbeatInterval: 30 * time.Second,
    MaxConnections:    10000,
    MaxMessageSize:    1024 * 1024,
    EnableCompression: true,
    SendChannelSize:   256,
}
```
