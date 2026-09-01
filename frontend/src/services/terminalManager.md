# Terminal Manager

This service manages terminal sessions across tab switches, ensuring that terminal sessions persist when switching between tabs.

## Features

1. **Session Persistence**: Terminal sessions are kept alive when switching tabs, preserving:
   - Terminal output and history
   - Current working directory
   - Running processes (like vim)
   - WebSocket connections

2. **Performance Optimizations**:
   - Disabled smooth scrolling for better performance
   - Increased scrollback buffer to 10,000 lines
   - Canvas renderer optimized for terminal applications
   - Fast scroll with shift modifier
   - EOL conversion for better compatibility
   - Proper TERM environment variable (xterm-256color)

3. **Singleton Pattern**: Ensures only one manager instance exists across the application

## Usage

The Terminal component automatically uses this manager to:

- Create or retrieve existing sessions
- Attach/detach terminals from DOM containers
- Manage WebSocket connections
- Clean up sessions when tabs are closed

## Future Improvements

1. **WebGL Renderer**: Installing `xterm-addon-webgl` would provide even better performance for vim and scrolling
2. **Session Restoration**: Could persist sessions to localStorage for recovery after page reload
3. **Multiple Terminal Support**: Could support multiple terminals per cluster tab
