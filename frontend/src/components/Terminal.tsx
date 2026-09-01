import { useEffect, useRef, useState, useCallback } from 'react';
import TerminalManager from '../services/terminalManager';
import TerminalSearch, { SearchOptions } from './TerminalSearch';
import { getWsBase } from '../services/api/types';
import 'xterm/css/xterm.css';
import './Terminal.css';

interface TerminalProps {
  tabId: string;
  showSearch?: boolean;
  onSearchToggle?: () => void;
}

const Terminal = ({
  tabId,
  showSearch: showSearchProp = false,
  onSearchToggle,
}: TerminalProps) => {
  const terminalRef = useRef<HTMLDivElement>(null);
  const [isConnected, setIsConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showSearchInternal, setShowSearchInternal] = useState(false);
  const [searchResultCount, setSearchResultCount] = useState(0);
  const [currentSearchResult, setCurrentSearchResult] = useState(0);
  const [lastSearchTerm, setLastSearchTerm] = useState('');
  const [lastSearchOptions, setLastSearchOptions] = useState<
    SearchOptions | undefined
  >();
  const [sessionSecret, setSessionSecret] = useState<string | null>(null);
  const searchResultsRef = useRef<number>(0);
  const currentResultIndexRef = useRef<number>(0);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);

  // Use prop search state if provided, otherwise use internal state
  const showSearch =
    showSearchProp !== undefined ? showSearchProp : showSearchInternal;

  useEffect(() => {
    Promise.resolve(
      (window as any).electronAPI?.security?.getSessionSecret?.() || null,
    ).then(setSessionSecret);
  }, []);

  useEffect(() => {
    if (!terminalRef.current) return;

    const manager = TerminalManager.getInstance();

    // Clear search state immediately when switching tabs
    if (showSearchProp === undefined) {
      setShowSearchInternal(false);
    }
    setSearchResultCount(0);
    setCurrentSearchResult(0);
    setLastSearchTerm('');
    setLastSearchOptions(undefined);
    searchResultsRef.current = 0;
    currentResultIndexRef.current = 0;

    // Small delay to ensure container is ready
    const timeoutId = setTimeout(() => {
      const session = manager.getOrCreateSession(tabId);

      // Attach terminal to container
      manager.attachToContainer(tabId, terminalRef.current!);

      // Connect WebSocket if not already connected
      if (!session.ws || session.ws.readyState === WebSocket.CLOSED) {
        connectWebSocket();
      } else if (session.ws.readyState === WebSocket.OPEN) {
        setIsConnected(true);
        // Don't re-setup handlers if we're just re-attaching
        // The handlers are already set up and working
      } else if (session.ws.readyState === WebSocket.CONNECTING) {
        // WebSocket is CONNECTING, wait for it to open
        session.ws.onopen = () => {
          setIsConnected(true);
          setError(null);
        };
      }
    }, 10);

    async function connectWebSocket() {
      const secret =
        sessionSecret ||
        (await (window as any).electronAPI?.security?.getSessionSecret?.());
      if (!secret) {
        console.warn('[Terminal] No session secret available, will retry...');
        setError('Local session unavailable');
        setTimeout(() => connectWebSocket(), 2000);
        return;
      }
      setSessionSecret(secret);
      const wsUrl = `${getWsBase()}?session_secret=${encodeURIComponent(secret)}`;
      const ws = new WebSocket(wsUrl);
      manager.setWebSocket(tabId, ws);

      ws.onopen = () => {
        console.log('WebSocket connected');
        setIsConnected(true);
        setError(null);

        // Only create new session if we don't have one
        const currentSession = manager.getSession(tabId);
        if (!currentSession?.sessionId) {
          // Get terminal dimensions - wait a bit for fitAddon to be ready
          setTimeout(() => {
            try {
              const session = manager.getSession(tabId);
              const dimensions = session?.fitAddon?.proposeDimensions();
              if (!dimensions) {
                console.error(
                  'Could not get terminal dimensions, using defaults',
                );
                // Use default dimensions if proposeDimensions fails
                const defaultDimensions = { cols: 80, rows: 24 };
                ws.send(
                  JSON.stringify({
                    type: 'terminal',
                    payload: {
                      action: 'create',
                      cols: defaultDimensions.cols,
                      rows: defaultDimensions.rows,
                      shell: '', // Use default shell
                    },
                  }),
                );
                return;
              }

              // Create terminal session
              ws.send(
                JSON.stringify({
                  type: 'terminal',
                  payload: {
                    action: 'create',
                    cols: dimensions.cols,
                    rows: dimensions.rows,
                    shell: '', // Use default shell
                  },
                }),
              );
            } catch (error) {
              console.error('Error getting terminal dimensions:', error);
              // Use default dimensions as fallback
              const defaultDimensions = { cols: 80, rows: 24 };
              ws.send(
                JSON.stringify({
                  type: 'terminal',
                  payload: {
                    action: 'create',
                    cols: defaultDimensions.cols,
                    rows: defaultDimensions.rows,
                    shell: '', // Use default shell
                  },
                }),
              );
            }
          }, 50);
        }
      };

      ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);

          if (message.type === 'terminal') {
            const payload = message.payload || message.data;

            if (payload.type === 'created') {
              manager.setSessionId(tabId, payload.sessionId);
              const session = manager.getSession(tabId);
              session?.terminal.focus();
            } else if (payload.type === 'output' && payload.data) {
              const session = manager.getSession(tabId);
              const data = payload.encoding === 'base64'
                ? atob(payload.data)
                : payload.data;
              session?.terminal.write(data);
            } else if (payload.type === 'error') {
              const session = manager.getSession(tabId);
              session?.terminal.writeln(`\r\nError: ${payload.error}`);
              setError(payload.error);
            }
          }
        } catch (err) {
          console.error('Failed to parse WebSocket message:', err);
        }
      };

      ws.onerror = (event) => {
        console.error('WebSocket error:', event);
        setError('WebSocket connection error');
        setIsConnected(false);
      };

      ws.onclose = () => {
        console.log('WebSocket disconnected');
        setIsConnected(false);

        // Only reconnect if component is still mounted
        if (terminalRef.current && !reconnectTimeoutRef.current) {
          const session = manager.getSession(tabId);
          session?.terminal.writeln('\r\nConnection lost. Reconnecting...');
          reconnectTimeoutRef.current = setTimeout(() => {
            reconnectTimeoutRef.current = null;
            if (terminalRef.current) {
              connectWebSocket();
            }
          }, 3000);
        }
      };

      setupHandlers(ws);
    }

    function setupHandlers(ws: WebSocket) {
      // Clear any existing handlers first
      manager.clearDisposables(tabId);

      const session = manager.getSession(tabId);
      if (!session) return;

      // Setup input handler
      const inputHandler = session.terminal.onData((data: string) => {
        const currentSession = manager.getSession(tabId);
        if (ws.readyState === WebSocket.OPEN && currentSession?.sessionId) {
          ws.send(
            JSON.stringify({
              type: 'terminal',
              payload: {
                action: 'input',
                sessionId: currentSession.sessionId,
                data: data,
              },
            }),
          );
        } else {
          console.warn(
            'Cannot send input - WebSocket not ready or no session ID',
          );
        }
      });
      manager.addDisposable(tabId, inputHandler);

      // Setup resize handler
      const resizeHandler = session.terminal.onResize(
        (size: { cols: number; rows: number }) => {
          const currentSession = manager.getSession(tabId);
          if (ws.readyState === WebSocket.OPEN && currentSession?.sessionId) {
            ws.send(
              JSON.stringify({
                type: 'terminal',
                payload: {
                  action: 'resize',
                  sessionId: currentSession.sessionId,
                  cols: size.cols,
                  rows: size.rows,
                },
              }),
            );
          }
        },
      );
      manager.addDisposable(tabId, resizeHandler);
    }

    const handleResize = () => {
      manager.resizeSession(tabId);
    };

    // Observe container size changes
    const resizeObserver = new ResizeObserver(() => {
      // Check if session still exists before resizing
      const session = manager.getSession(tabId);
      if (
        session &&
        session.terminal &&
        !session.terminal.element?.style.display
      ) {
        handleResize();
      }
    });

    if (terminalRef.current) {
      resizeObserver.observe(terminalRef.current);
    }

    window.addEventListener('resize', handleResize);

    return () => {
      clearTimeout(timeoutId);
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      window.removeEventListener('resize', handleResize);
      resizeObserver.disconnect();

      // Don't close the session when unmounting - keep it alive for when we come back
      // Only close when the tab itself is closed (handled elsewhere)
    };
  }, [tabId, showSearchProp, sessionSecret]);

  // Search handlers
  const handleSearch = useCallback(
    (term: string, direction: 'next' | 'previous', options?: SearchOptions) => {
      const session = TerminalManager.getInstance().getSession(tabId);
      if (!session || !term) return;

      const searchAddon = session.searchAddon;
      const searchOptions = {
        regex: options?.regex || false,
        wholeWord: options?.wholeWord || false,
        caseSensitive: options?.caseSensitive || false,
        incremental: false,
        decorations: {
          matchBackground: '#515c6a',
          matchOverviewRuler: '#515c6a',
          activeMatchBackground: '#ff7b00',
          activeMatchColorOverviewRuler: '#ff7b00',
        },
      };

      // If it's a new search term or options changed, count all occurrences
      const optionsChanged =
        JSON.stringify(options) !== JSON.stringify(lastSearchOptions);
      if (term !== lastSearchTerm || optionsChanged) {
        setLastSearchTerm(term);
        setLastSearchOptions(options);
        searchAddon.clearDecorations();

        // Count total results by searching through the terminal buffer
        let count = 0;
        const buffer = session.terminal.buffer.active;

        // Build regex based on options
        let searchPattern = term;
        if (!options?.regex) {
          // Escape regex special characters if not using regex mode
          searchPattern = term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        }

        if (options?.wholeWord) {
          searchPattern = `\\b${searchPattern}\\b`;
        }

        const flags = options?.caseSensitive ? 'g' : 'gi';
        let searchRegex;

        try {
          searchRegex = new RegExp(searchPattern, flags);
        } catch (e) {
          // Invalid regex, don't search
          setSearchResultCount(0);
          setCurrentSearchResult(0);
          return;
        }

        // Search through all lines in the buffer
        for (let i = 0; i < buffer.length; i++) {
          const line = buffer.getLine(i);
          if (line) {
            const lineText = line.translateToString();
            const matches = lineText.match(searchRegex);
            if (matches) {
              count += matches.length;
            }
          }
        }

        searchResultsRef.current = count;
        currentResultIndexRef.current = count > 0 ? 1 : 0;
        setSearchResultCount(count);
        setCurrentSearchResult(count > 0 ? 1 : 0);

        // Find first result
        if (count > 0) {
          searchAddon.findNext(term, searchOptions);
        }
      } else {
        // Navigate through existing results
        let result;
        if (direction === 'next') {
          result = searchAddon.findNext(term, searchOptions);
          if (result && searchResultsRef.current > 0) {
            currentResultIndexRef.current++;
            if (currentResultIndexRef.current > searchResultsRef.current) {
              currentResultIndexRef.current = 1;
            }
            setCurrentSearchResult(currentResultIndexRef.current);
          }
        } else {
          result = searchAddon.findPrevious(term, searchOptions);
          if (result && searchResultsRef.current > 0) {
            currentResultIndexRef.current--;
            if (currentResultIndexRef.current <= 0) {
              currentResultIndexRef.current = searchResultsRef.current;
            }
            setCurrentSearchResult(currentResultIndexRef.current);
          }
        }
      }
    },
    [tabId, lastSearchTerm, lastSearchOptions],
  );

  const handleCloseSearch = useCallback(() => {
    if (onSearchToggle) {
      onSearchToggle();
    } else {
      setShowSearchInternal(false);
    }
    setSearchResultCount(0);
    setCurrentSearchResult(0);
    setLastSearchTerm('');
    setLastSearchOptions(undefined);
    searchResultsRef.current = 0;
    currentResultIndexRef.current = 0;

    // Clear search highlights
    const session = TerminalManager.getInstance().getSession(tabId);
    if (session) {
      session.searchAddon.clearDecorations();
    }

    // Focus terminal
    const terminalSession = TerminalManager.getInstance().getSession(tabId);
    if (terminalSession) {
      terminalSession.terminal.focus();
    }
  }, [tabId, onSearchToggle]);

  // Keyboard shortcut handler
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Only handle shortcuts if this terminal is visible/focused
      const terminalElement = terminalRef.current;
      if (!terminalElement || !terminalElement.offsetParent) return;

      // Check if the terminal container or its children have focus
      const hasFocus =
        terminalElement.contains(document.activeElement) ||
        terminalElement.parentElement?.contains(document.activeElement);

      if (!hasFocus && !showSearch) return;

      // Ctrl+F or Cmd+F to toggle search
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault();
        if (onSearchToggle) {
          // When controlled externally, always use the toggle
          onSearchToggle();
        } else {
          // When using internal state, toggle it
          if (showSearch) {
            handleCloseSearch();
          } else {
            setShowSearchInternal(true);
          }
        }
      }

      // Escape to close search when search is open
      if (e.key === 'Escape' && showSearch) {
        e.preventDefault();
        handleCloseSearch();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [showSearch, handleCloseSearch, onSearchToggle]);

  return (
    <div className="terminal-instance">
      {showSearch && (
        <TerminalSearch
          onSearch={handleSearch}
          onClose={handleCloseSearch}
          resultCount={searchResultCount}
          currentResult={currentSearchResult}
        />
      )}
      <div
        className="terminal-container"
        onClick={() => {
          // Focus terminal on click
          const session = TerminalManager.getInstance().getSession(tabId);
          if (session) {
            session.terminal.focus();
          }
        }}
      >
        {error && (
          <div
            style={{
              position: 'absolute',
              top: 0,
              right: 0,
              background: 'rgba(255, 0, 0, 0.1)',
              color: '#ff6b6b',
              padding: '4px 8px',
              fontSize: '12px',
              zIndex: 10,
              pointerEvents: 'none',
            }}
          >
            {error}
          </div>
        )}
        <div ref={terminalRef} className="terminal-wrapper" />
        {!isConnected && !error && (
          <div
            style={{
              position: 'absolute',
              top: '50%',
              left: '50%',
              transform: 'translate(-50%, -50%)',
              color: '#666',
              fontSize: '14px',
              pointerEvents: 'none',
            }}
          >
            Connecting...
          </div>
        )}
      </div>
    </div>
  );
};

export default Terminal;
