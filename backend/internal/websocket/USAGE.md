# WebSocket Usage Guide

This document shows how to use the new standardized WebSocket infrastructure.

## Frontend Integration

All WebSocket communication now goes through a single endpoint: `/api/v1/ws`

### Basic Connection
```typescript
const ws = new WebSocket('ws://localhost:8080/api/v1/ws');

ws.onmessage = (event) => {
  const message = JSON.parse(event.data);
  console.log('Received:', message);
};
```

### Pod Exec Sessions
```typescript
// Start pod exec session
ws.send(JSON.stringify({
  type: "exec",
  payload: {
    type: "exec_start",
    cluster: "prod",
    namespace: "default",
    pod: "my-pod",
    container: "main"
  }
}));

// Send input to pod
ws.send(JSON.stringify({
  type: "exec",
  payload: {
    type: "exec_input",
    cluster: "prod",
    namespace: "default",
    pod: "my-pod",
    container: "main",
    data: new TextEncoder().encode("ls -la\n")
  }
}));

// Resize terminal
ws.send(JSON.stringify({
  type: "exec",
  payload: {
    type: "exec_resize",
    cluster: "prod",
    namespace: "default",
    pod: "my-pod",
    container: "main",
    resize: { width: 80, height: 24 }
  }
}));
```

### Node Exec Sessions
```typescript
// Start node exec session
ws.send(JSON.stringify({
  type: "node_exec",
  payload: {
    type: "node_exec_start",
    cluster: "prod",
    node: "worker-1"
  }
}));

// Send input to node
ws.send(JSON.stringify({
  type: "node_exec",
  payload: {
    type: "node_exec_input",
    cluster: "prod",
    node: "worker-1",
    data: new TextEncoder().encode("ps aux\n")
  }
}));
```

### Kubernetes Resource Subscriptions
```typescript
// Subscribe to deployments
ws.send(JSON.stringify({
  type: "subscribe",
  payload: {
    cluster: "prod",
    group: "apps",
    version: "v1",
    kind: "deployments",
    namespace: "default"
  }
}));

// Subscribe to pod status updates
ws.send(JSON.stringify({
  action: "subscribe",
  topic: "pod-statuses:prod",
  resources: [
    {
      uid: "deployment-uid-123",
      namespace: "default",
      selector: {
        matchLabels: {
          app: "my-app"
        }
      }
    }
  ]
}));
```

## Message Types

### Incoming Messages (Server → Client)

#### Exec Output
```json
{
  "type": "exec_output",
  "timestamp": "2024-01-01T12:00:00Z",
  "payload": {
    "topic": "exec:prod:default:my-pod:main",
    "data": [65, 66, 67]
  }
}
```

#### Resource Events
```json
{
  "type": "event",
  "timestamp": "2024-01-01T12:00:00Z",
  "payload": {
    "channel": "items",
    "topic": "items:prod:apps:v1:deployments:default",
    "action": "MODIFIED",
    "item": {
      "name": "my-app",
      "namespace": "default",
      "replicas": 3,
      "readyReplicas": 3
    }
  }
}
```

#### Pod Status Updates
```json
{
  "type": "pod-status-update",
  "timestamp": "2024-01-01T12:00:00Z",
  "payload": {
    "uid": "deployment-uid-123",
    "podStatuses": [
      {
        "name": "my-app-abc123",
        "phase": "Running",
        "ready": true,
        "restarts": 0
      }
    ]
  }
}
```

## Error Handling

All errors are sent as standardized error messages:

```json
{
  "type": "error",
  "timestamp": "2024-01-01T12:00:00Z",
  "error": "exec session not found",
  "code": "SESSION_NOT_FOUND"
}
```

## Connection Management

The WebSocket infrastructure provides:
- Automatic ping/pong heartbeat
- Graceful connection cleanup
- Rate limiting with backpressure
- Proper error recovery
- Connection lifecycle management

All connections are automatically cleaned up when the WebSocket closes.
