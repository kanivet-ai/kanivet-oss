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

/**
 * Terminal theme from the app's CSS tokens (see src/index.css), re-applied to
 * every live session whenever the document theme flips.
 */
const TOKEN_FALLBACKS: Record<string, string> = {
  '--content': '#1e1e20',
  '--inset': '#1c1c1e',
  '--text': '#f5f5f7',
  '--text2': 'rgba(235, 235, 245, 0.62)',
  '--text3': 'rgba(235, 235, 245, 0.34)',
  '--blue': '#0a84ff',
  '--green': '#30d158',
  '--yellow': '#ffd60a',
  '--red': '#ff453a',
  '--purple': '#bf5af2',
  '--teal': '#40c8e0',
  '--blue-rgb': '10, 132, 255',
};

function readToken(name: string): string {
  if (typeof document === 'undefined') return TOKEN_FALLBACKS[name] || '';
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || TOKEN_FALLBACKS[name] || '';
}

export function buildXtermTheme() {
  const text = readToken('--text');
  return {
    background: readToken('--content'),
    foreground: text,
    cursor: readToken('--blue'),
    cursorAccent: readToken('--content'),
    selectionBackground: `rgba(${readToken('--blue-rgb')}, 0.3)`,
    black: readToken('--inset'),
    red: readToken('--red'),
    green: readToken('--green'),
    // Pure yellow is illegible on the light surface; the bright slot keeps it.
    yellow: readToken('--orange'),
    blue: readToken('--blue'),
    magenta: readToken('--purple'),
    cyan: readToken('--teal'),
    white: text,
    brightBlack: readToken('--text3'),
    brightRed: readToken('--red'),
    brightGreen: readToken('--green'),
    brightYellow: readToken('--yellow'),
    brightBlue: readToken('--blue'),
    brightMagenta: readToken('--purple'),
    brightCyan: readToken('--teal'),
    brightWhite: text,
  };
}

export const TERMINAL_FONT_FAMILY =
  '"JetBrainsMono Nerd Font", "MesloLGS NF", "CaskaydiaMono Nerd Font", "CaskaydiaCove Nerd Font", "FiraCode Nerd Font", ui-monospace, "SF Mono", Menlo, Monaco, "Cascadia Code", monospace';

class TerminalManager {
  private sessions: Map<string, TerminalSession> = new Map();
  private static instance: TerminalManager;
  private themeObserver: MutationObserver | null = null;

  private constructor() {
    if (typeof document !== 'undefined' && typeof MutationObserver !== 'undefined') {
      this.themeObserver = new MutationObserver(() => this.applyThemeToAll());
      this.themeObserver.observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-theme', 'style'],
      });
    }
  }

  private applyThemeToAll(): void {
    const theme = buildXtermTheme();
    this.sessions.forEach((session) => {
      session.terminal.options.theme = theme;
    });
  }

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
        fontSize: 13,
        lineHeight: 1.2,
        fontFamily: TERMINAL_FONT_FAMILY,
        scrollback: 10000, // Increased scrollback for better history
        fastScrollModifier: 'shift', // Use shift for fast scrolling
        smoothScrollDuration: 0, // Disable smooth scrolling for better performance
        // Note: In xterm.js v5+, renderer is set via addons (canvas is default)
        // WebGL renderer would provide better performance for vim/scrolling
        // but requires xterm-addon-webgl package
        allowProposedApi: true, // Enable proposed APIs for better performance
        // Performance optimizations for vim and other full-screen apps
        convertEol: true,
        theme: buildXtermTheme(),
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

  clearSessionId(tabId: string): void {
    const session = this.sessions.get(tabId);
    if (session) {
      session.sessionId = null;
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
