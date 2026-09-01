# Terminal Module

This module provides a production-grade terminal implementation for the kanivet application, following clean architecture principles and Google's engineering standards.

## Architecture

The terminal module is organized into the following layers:

### Domain Layer (`domain/`)
- **terminal.go**: Core domain models and interfaces
  - `Session`: Represents a terminal session with metadata
  - `Terminal`: Interface for terminal operations
  - `SessionManager`: Interface for managing terminal sessions
  - `TerminalFactory`: Factory pattern for creating terminal instances

### Infrastructure Layer (`infrastructure/`)
- **pty_terminal.go**: PTY (pseudo-terminal) implementation
  - Uses the `github.com/creack/pty` library for cross-platform terminal support
  - Implements the `Terminal` interface with proper process management
  - Handles terminal resizing and I/O operations

### Service Layer (`service/`)
- **terminal_service.go**: Business logic for terminal management
  - Session lifecycle management (create, get, close, list)
  - Security: Enforces session limits and timeouts
  - Automatic cleanup of stale sessions
  - Monitoring of terminal processes

### WebSocket Layer (`websocket/`)
- **handler.go**: WebSocket message handling
  - Implements real-time bidirectional communication
  - Handles terminal actions: create, input, resize, close
  - Stream management for terminal output
  - Connection monitoring and cleanup

## Security Considerations

1. **Session Limits**: Maximum 10 concurrent sessions per server
2. **Session Timeout**: 30-minute inactivity timeout
3. **Process Isolation**: Each terminal runs in its own process
4. **Resource Cleanup**: Automatic cleanup of terminated processes
5. **Input Validation**: All WebSocket messages are validated

## Usage

The terminal module is integrated into the main application through:

1. Terminal factory registration
2. WebSocket handler registration
3. Background cleanup worker

```go
// In main.go
terminalFactory := infrastructure.NewPTYTerminalFactory()
terminalService := service.NewTerminalService(terminalFactory)
terminalHandler := terminalws.NewTerminalHandler(terminalService)

wsServer.RegisterHandler("terminal", terminalHandler)
go terminalService.StartCleanupWorker(context.Background())
```

## WebSocket Protocol

### Create Session
```json
{
  "type": "terminal",
  "payload": {
    "action": "create",
    "cols": 80,
    "rows": 24,
    "shell": ""
  }
}
```

### Send Input
```json
{
  "type": "terminal",
  "payload": {
    "action": "input",
    "sessionId": "uuid",
    "data": "ls -la\n"
  }
}
```

### Resize Terminal
```json
{
  "type": "terminal",
  "payload": {
    "action": "resize",
    "sessionId": "uuid",
    "cols": 120,
    "rows": 40
  }
}
```

### Close Session
```json
{
  "type": "terminal",
  "payload": {
    "action": "close",
    "sessionId": "uuid"
  }
}
```

## Future Enhancements

1. **Audit Logging**: Log all terminal commands for security auditing
2. **Shell Configuration**: Allow custom shell configurations
3. **File Transfer**: Support file upload/download through terminal
4. **Session Recording**: Record terminal sessions for playback
5. **Multi-tab Support**: Handle multiple terminal tabs per user
