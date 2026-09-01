import { Terminal as XTerm } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { SearchAddon } from 'xterm-addon-search';
import { IDisposable } from 'xterm';

interface TerminalSession {
  terminal: XTerm;
  fitAddon: FitAddon;
  searchAddon: SearchAddon;
  ws: WebSocket | null;
  sessionId: string | null;
  container: HTMLElement | null;
  isAttached: boolean;
  disposables: IDisposable[];
}

class TerminalManager {
  private sessions: Map<string, TerminalSession> = new Map();
  private static instance: TerminalManager;

  private constructor() {}

  static getInstance(): TerminalManager {
    if (!TerminalManager.instance) {
      TerminalManager.instance = new TerminalManager();
    }
    return TerminalManager.instance;
  }

  getOrCreateSession(tabId: string): TerminalSession {
    let session = this.sessions.get(tabId);

    if (!session) {
      // Create new terminal
      const terminal = new XTerm({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        scrollback: 10000, // Increased scrollback for better history
        fastScrollModifier: 'shift', // Use shift for fast scrolling
        smoothScrollDuration: 0, // Disable smooth scrolling for better performance
        // Note: In xterm.js v5+, renderer is set via addons (canvas is default)
        // WebGL renderer would provide better performance for vim/scrolling
        // but requires xterm-addon-webgl package
        allowProposedApi: true, // Enable proposed APIs for better performance
        // Performance optimizations for vim and other full-screen apps
        convertEol: true,
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
          cursor: '#d4d4d4',
          black: '#000000',
          red: '#cd3131',
          green: '#0dbc79',
          yellow: '#e5e510',
          blue: '#2472c8',
          magenta: '#bc3fbc',
          cyan: '#11a8cd',
          white: '#e5e5e5',
          brightBlack: '#666666',
          brightRed: '#f14c4c',
          brightGreen: '#23d18b',
          brightYellow: '#f5f543',
          brightBlue: '#3b8eea',
          brightMagenta: '#d670d6',
          brightCyan: '#29b8db',
          brightWhite: '#e5e5e5',
        },
      });

      const fitAddon = new FitAddon();
      const searchAddon = new SearchAddon();

      // Load addons before creating session
      terminal.loadAddon(fitAddon);
      terminal.loadAddon(searchAddon);

      session = {
        terminal,
        fitAddon,
        searchAddon,
        ws: null,
        sessionId: null,
        container: null,
        isAttached: false,
        disposables: [],
      };

      this.sessions.set(tabId, session);
    }

    return session;
  }

  attachToContainer(tabId: string, container: HTMLElement): void {
    const session = this.sessions.get(tabId);
    if (!session) return;

    if (session.container === container && session.isAttached) {
      session.terminal.focus();
      return;
    }

    // Store container reference
    session.container = container;

    // Delay terminal attachment to ensure container is ready
    requestAnimationFrame(() => {
      if (!container.offsetParent) {
        setTimeout(() => this.attachToContainer(tabId, container), 50);
        return;
      }

      try {
        if (!session.isAttached) {
          session.terminal.open(container);
          session.isAttached = true;
        } else {
          // Re-attach to new container by moving the existing terminal element
          // Get the terminal element from its current container
          const terminalElement = session.terminal.element;
          if (terminalElement && terminalElement.parentNode) {
            // Remove from current container
            terminalElement.parentNode.removeChild(terminalElement);
          }
          // Append to new container
          if (terminalElement) {
            container.appendChild(terminalElement);
          }
        }

        requestAnimationFrame(() => {
          try {
            if (session.fitAddon && session.terminal.element && container.offsetParent) {
              session.fitAddon.fit();
              session.terminal.focus();
            }
          } catch (e) {
            console.warn('Failed to fit terminal:', e);
          }
        });
      } catch (e) {
        console.error('Failed to open terminal:', e);
        session.isAttached = false;
      }
    });
  }

  setWebSocket(tabId: string, ws: WebSocket): void {
    const session = this.sessions.get(tabId);
    if (session) {
      // Close old WebSocket if exists
      if (session.ws && session.ws.readyState !== WebSocket.CLOSED) {
        session.ws.close();
      }
      session.ws = ws;
    }
  }

  setSessionId(tabId: string, sessionId: string): void {
    const session = this.sessions.get(tabId);
    if (session) {
      session.sessionId = sessionId;
    }
  }

  getSession(tabId: string): TerminalSession | undefined {
    return this.sessions.get(tabId);
  }

  clearDisposables(tabId: string): void {
    const session = this.sessions.get(tabId);
    if (session) {
      // Dispose all existing disposables
      session.disposables.forEach((d) => d.dispose());
      session.disposables = [];
    }
  }

  addDisposable(tabId: string, disposable: IDisposable): void {
    const session = this.sessions.get(tabId);
    if (session) {
      session.disposables.push(disposable);
    }
  }

  closeSession(tabId: string): void {
    const session = this.sessions.get(tabId);
    if (session) {
      // First remove from map to prevent any new operations
      this.sessions.delete(tabId);

      // Dispose all event handlers
      this.clearDisposables(tabId);

      try {
        // Clear the terminal element to prevent viewport errors
        if (session.terminal.element && session.terminal.element.parentNode) {
          session.terminal.element.style.display = 'none';
        }

        // Dispose addons first
        if (session.fitAddon) {
          session.fitAddon.dispose();
        }
        if (session.searchAddon) {
          session.searchAddon.dispose();
        }

        // Close WebSocket if open
        if (session.ws && session.ws.readyState === WebSocket.OPEN) {
          if (session.sessionId) {
            try {
              session.ws.send(
                JSON.stringify({
                  type: 'terminal',
                  payload: {
                    action: 'close',
                    sessionId: session.sessionId,
                  },
                }),
              );
            } catch (e) {
              console.error('Error sending close message:', e);
            }
          }
          session.ws.close();
        }

        // Finally dispose terminal
        session.terminal.dispose();
      } catch (e) {
        console.error('Error during terminal cleanup:', e);
      }
    }
  }

  resizeSession(tabId: string): void {
    const session = this.sessions.get(tabId);
    if (
      session &&
      session.container &&
      session.fitAddon &&
      session.terminal.element
    ) {
      try {
        // Check if terminal is not being disposed
        if (
          !session.terminal.element.style.display ||
          session.terminal.element.style.display !== 'none'
        ) {
          session.fitAddon.fit();
        }
      } catch (e) {
        // Silently ignore resize errors during disposal
        const error = e as Error;
        if (!error.message?.includes('dimensions')) {
          console.warn('Failed to resize terminal:', error);
        }
      }
    }
  }
}

export default TerminalManager;
