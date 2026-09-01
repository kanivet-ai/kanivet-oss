import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal, IDisposable } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { WebLinksAddon } from 'xterm-addon-web-links';
import { useTheme } from '../ThemeProvider';
import 'xterm/css/xterm.css';
import './Terminal.css';

interface TerminalProps {
  wsUrl: string;
  connectMessage?: string;
  headerInfo?: string;
  onConnectionChange?: (connected: boolean) => void;
}

interface CleanupDisposables {
  onDataDisposable?: IDisposable;
  onResizeDisposable?: IDisposable;
}

const TerminalComponent: React.FC<TerminalProps> = ({
  wsUrl,
  connectMessage = 'Connecting...',
  headerInfo = '',
  onConnectionChange,
}) => {
  const { theme } = useTheme();
  const terminalRef = useRef<HTMLDivElement>(null);
  const terminalInstance = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleResize = useCallback(() => {
    try {
      const term = terminalInstance.current;
      const fitAddon = fitAddonRef.current;
      const container = terminalRef.current;

      if (!term || !fitAddon || !container) return;

      // Check terminal is ready using public API
      if (!term.element || !term.element.offsetParent) return;
      if (!term.buffer || !term.buffer.active) return;

      // Additional safety check for dimensions
      try {
        // This will throw if terminal internals aren't ready
        const testCols = term.cols;
        const testRows = term.rows;
        if (!testCols || !testRows) return;
      } catch {
        return;
      }

      if (container.clientWidth > 0 && container.clientHeight > 0) {
        fitAddon.fit();

        // Double-check cols/rows are valid
        const cols = term.cols;
        const rows = term.rows;
        if (cols > 0 && rows > 0) {
          const ws = wsRef.current;
          if (ws?.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'resize', cols, rows }));
          }
        }
      }
    } catch (error) {
      // Silently ignore - terminal may not be ready yet
      return;
    }
  }, []);

  useEffect(() => {
    if (!terminalRef.current) return;

    let term: Terminal | null = null;
    let fitAddon: FitAddon | null = null;
    let webLinksAddon: WebLinksAddon | null = null;

    const terminalTheme =
      theme === 'light'
        ? {
            background: '#ffffff',
            foreground: '#383a42',
            cursor: '#383a42',
            black: '#383a42',
            red: '#e45649',
            green: '#50a14f',
            yellow: '#c18401',
            blue: '#0184bc',
            magenta: '#a626a4',
            cyan: '#0997b3',
            white: '#fafafa',
            brightBlack: '#4f525d',
            brightRed: '#e45649',
            brightGreen: '#50a14f',
            brightYellow: '#c18401',
            brightBlue: '#0184bc',
            brightMagenta: '#a626a4',
            brightCyan: '#0997b3',
            brightWhite: '#fafafa',
          }
        : {
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
          };

    try {
      term = new Terminal({
        fontSize: 14,
        fontFamily:
          'JetBrains Mono, SF Mono, Cascadia Code, Fira Code, Monaco, Menlo, monospace',
        theme: terminalTheme,
        cursorBlink: true,
        scrollback: 10000,
      });

      fitAddon = new FitAddon();
      webLinksAddon = new WebLinksAddon();
    } catch (e) {
      console.error('Failed to create terminal:', e);
      return;
    }

    if (!term || !fitAddon || !webLinksAddon) return;

    const resizeObserver = new ResizeObserver((entries) => {
      // Use requestAnimationFrame to debounce resize events
      window.requestAnimationFrame(() => {
        if (!entries.length) return;
        try {
          handleResize();
        } catch (e) {
          // Silently ignore resize errors
        }
      });
    });
    if (terminalRef.current) {
      resizeObserver.observe(terminalRef.current);
    }

    window.addEventListener('resize', handleResize);

    let rafId: number | null = null;
    let disposed = false;
    let terminalOpened = false;
    let wsConnectTimeout: NodeJS.Timeout | null = null;

    const openWhenReady = () => {
      if (disposed) return;
      const containerEl = terminalRef.current;
      if (
        containerEl &&
        containerEl.clientWidth > 0 &&
        containerEl.clientHeight > 0
      ) {
        if (!terminalOpened) {
          try {
            term.open(containerEl);
            terminalOpened = true;

            // Store references after successful open
            terminalInstance.current = term;
            fitAddonRef.current = fitAddon;

            // Load addons AFTER a delay to ensure terminal is ready (per SO 74672618)
            setTimeout(() => {
              if (disposed) return;

              try {
                term.loadAddon(fitAddon);
                term.loadAddon(webLinksAddon);

                // Initial fit after addon is loaded
                setTimeout(() => {
                  if (!disposed && fitAddon) {
                    try {
                      fitAddon.fit();
                    } catch (e) {
                      console.warn('Initial fit failed:', e);
                    }
                  }
                }, 50);
              } catch (e) {
                console.warn('Failed to load addons:', e);
              }
            }, 100);

            // Wait for terminal to be fully ready
            const initializeTerminal = () => {
              if (disposed) return;

              let retries = 0;
              const maxRetries = 20;

              const checkReady = () => {
                if (disposed) return;

                try {
                  // Check if terminal is fully initialized using public API
                  const isReady =
                    term.buffer &&
                    term.buffer.active &&
                    term.element &&
                    term.cols > 0 &&
                    term.rows > 0;

                  if (isReady) {
                    // Also verify we can access buffer without errors
                    try {
                      // Access these to ensure they don't throw
                      void term.buffer.active;
                      void term.element?.offsetParent;
                    } catch {
                      throw new Error('Terminal not ready');
                    }

                    // Terminal is ready, perform resize after ensuring fit addon is loaded
                    setTimeout(() => {
                      if (!disposed) {
                        handleResize();
                      }
                    }, 200); // Increased delay to ensure addon is loaded
                  } else if (retries < maxRetries) {
                    // Not ready yet, retry
                    retries++;
                    setTimeout(checkReady, 50);
                  }
                } catch (e) {
                  // Terminal not ready, retry if we haven't exceeded max retries
                  if (retries < maxRetries) {
                    retries++;
                    setTimeout(checkReady, 50);
                  }
                }
              };

              // Start checking after next frame
              requestAnimationFrame(checkReady);
            };

            initializeTerminal();
          } catch (e) {
            console.warn('Terminal open failed, retrying...', e);
            terminalOpened = false;
            rafId = requestAnimationFrame(openWhenReady);
            return;
          }
        }

        wsConnectTimeout = setTimeout(() => {
          if (disposed) return;

          const ws = new WebSocket(wsUrl);
          wsRef.current = ws;
          ws.binaryType = 'arraybuffer';

          ws.onopen = () => {
            if (disposed) return;
            setConnected(true);
            setError(null);
            onConnectionChange?.(true);
            if (terminalOpened && !disposed) {
              term.writeln(`${connectMessage}\r\n`);
            }
            setTimeout(() => handleResize(), 100);
          };

          ws.onmessage = (event) => {
            if (disposed || !terminalOpened) return;
            if (event.data instanceof ArrayBuffer) {
              const decoder = new TextDecoder();
              const text = decoder.decode(new Uint8Array(event.data));
              term.write(text);
            } else {
              const text = event.data as string;
              if (text.startsWith('Error') || text.startsWith('Exec error')) {
                term.writeln(`\r\n\x1b[31m${text}\x1b[0m\r\n`);
                setError(text);
              } else {
                term.write(text);
              }
            }
          };

          ws.onerror = () => {
            if (disposed) return;
            if (terminalOpened && !disposed) {
              term.writeln('\r\n\x1b[31mConnection error\x1b[0m\r\n');
            }
            setError('WebSocket connection failed');
            setConnected(false);
            onConnectionChange?.(false);
          };

          ws.onclose = () => {
            if (disposed) return;
            if (terminalOpened && !disposed) {
              term.writeln('\r\n\x1b[33mConnection closed\x1b[0m\r\n');
            }
            setConnected(false);
            onConnectionChange?.(false);
          };

          const onDataDisposable = term.onData((data) => {
            if (ws.readyState === WebSocket.OPEN) {
              // Send as text message, not binary - backend expects text
              ws.send(data);
            }
          });

          const onResizeDisposable = term.onResize((size) => {
            try {
              if (ws.readyState === WebSocket.OPEN) {
                ws.send(
                  JSON.stringify({
                    type: 'resize',
                    cols: size.cols,
                    rows: size.rows,
                  }),
                );
              }
            } catch (e) {
              console.warn('Terminal onResize handler failed:', e);
            }
          });

          cleanupDisposables.onDataDisposable = onDataDisposable;
          cleanupDisposables.onResizeDisposable = onResizeDisposable;
        }, 50);
      } else {
        rafId = requestAnimationFrame(openWhenReady);
      }
    };

    const cleanupDisposables: CleanupDisposables = {};
    rafId = requestAnimationFrame(openWhenReady);

    return () => {
      disposed = true;

      if (wsConnectTimeout) {
        clearTimeout(wsConnectTimeout);
        wsConnectTimeout = null;
      }

      if (rafId) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }

      window.removeEventListener('resize', handleResize);
      resizeObserver.disconnect();

      cleanupDisposables.onDataDisposable?.dispose();
      cleanupDisposables.onResizeDisposable?.dispose();

      const ws = wsRef.current;
      if (ws) {
        wsRef.current = null;
        if (ws.readyState === WebSocket.OPEN) {
          ws.close();
        } else if (ws.readyState === WebSocket.CONNECTING) {
          ws.onopen = () => ws.close();
          ws.onerror = null;
          ws.onmessage = null;
          ws.onclose = null;
        }
      }

      setTimeout(() => {
        if (terminalOpened && term) {
          try {
            if (terminalInstance.current === term) {
              terminalInstance.current = null;
              fitAddonRef.current = null;
            }
            if (term.element) {
              term.dispose();
            }
          } catch (error) {
            console.warn(
              'Error disposing terminal (gracefully handled):',
              error,
            );
          }
        }
      }, 50);
    };
  }, [wsUrl, connectMessage, onConnectionChange, handleResize, theme]);

  const reconnect = () => {
    window.location.reload();
  };

  return (
    <div className="terminal-container-wrapper">
      <div className="terminal-header">
        <div className="terminal-info">
          <span
            className={`connection-status ${
              connected ? 'connected' : 'disconnected'
            }`}
          >
            {connected ? '● Connected' : '○ Disconnected'}
          </span>
          {headerInfo && (
            <span className="terminal-header-info">{headerInfo}</span>
          )}
        </div>
        <div className="terminal-actions">
          {!connected && (
            <button onClick={reconnect} className="reconnect-btn">
              Reconnect
            </button>
          )}
        </div>
      </div>
      {error && <div className="terminal-error">{error}</div>}
      <div ref={terminalRef} className="terminal-container" />
    </div>
  );
};

export default TerminalComponent;
