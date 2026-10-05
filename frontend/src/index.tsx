import React from 'react';
import ReactDOM from 'react-dom/client';
import './index.css';
import './styles/radix-bridge.css';
import './styles/primitives.css';
import App from './App';
// Stylesheets of lazily loaded views whose unscoped rules (.search-input,
// .search-icon, .no-results, .error-message, .terminal-container…) also style
// first-screen elements and each other. Split out, they would be appended in
// whatever order their chunks load; imported here, after everything App
// pulls in and in the order the single bundle had them, the cascade stays as
// it was.
import './components/finops/FinOpsFilters.css';
import './components/detailView/shared/DetailViewShared.css';
import './components/Terminal.css';
import './components/PodShell.css';
import './components/common/Terminal.css';
import './components/ResourceSelector.css';
import './components/CrossplaneTrace.css';
import './components/ThemeSettings.css';
import { setBackendPort } from './services/api/types';
import { setupResizeObserverErrorHandler } from './utils/resizeObserverPolyfill';
import { runLegacyChatSessionMigration } from './utils/legacyChatSessionMigration.mjs';

// Setup ResizeObserver error handler before anything else
setupResizeObserverErrorHandler();

if (typeof window !== 'undefined') {
  runLegacyChatSessionMigration(window.localStorage);
}

// Browser mode: mock electronAPI for development without Electron
if (typeof window !== 'undefined' && !(window as any).electronAPI) {
  (window as any).electronAPI = {
    writeLog: (msg: string) => console.log('[Log]', msg),
    onClearCache: () => () => {},
    island: {
      notify: async () => undefined,
    },
    backend: {
      onPortChanged: () => () => {},
    },
    security: {
      getSessionSecret: async () => null,
    },
    updater: {
      getAppVersion: async () => '0.0.0',
      checkForUpdates: async () => null,
      downloadUpdate: async () => {},
      installUpdate: () => {},
      removeAllListeners: () => {},
      onUpdateAvailable: () => () => {},
      onDownloadProgress: () => () => {},
      onUpdateDownloaded: () => () => {},
      onError: () => () => {},
      onMenuCheckForUpdates: () => () => {},
    },
  };
  console.log('[App] Running in browser mode');
}

if (typeof window !== 'undefined') {
  (window as any).electronAPI?.backend?.onPortChanged?.((port: number) =>
    setBackendPort(port),
  );
}

// Suppress ResizeObserver loop limit exceeded error
if (typeof window !== 'undefined') {
  const originalError = window.console.error;

  window.console.error = (...args) => {
    if (
      args[0]?.includes?.(
        'ResizeObserver loop completed with undelivered notifications',
      ) ||
      args[0]?.includes?.('ResizeObserver loop limit exceeded') ||
      args[0]?.message?.includes?.('ResizeObserver loop')
    ) {
      return;
    }
    originalError.apply(console, args);
  };

  // Handle window error events
  window.addEventListener(
    'error',
    (e) => {
      if (
        e.message?.includes(
          'ResizeObserver loop completed with undelivered notifications',
        ) ||
        e.message?.includes('ResizeObserver loop limit exceeded')
      ) {
        e.stopImmediatePropagation();
        e.preventDefault();
        return false;
      }
    },
    true,
  );

  window.addEventListener(
    'unhandledrejection',
    (e) => {
      if (
        e.reason?.message?.includes?.('ResizeObserver loop') ||
        e.reason?.includes?.('ResizeObserver loop')
      ) {
        e.preventDefault();
        return false;
      }
    },
    true,
  );
}

const root = ReactDOM.createRoot(
  document.getElementById('root') as HTMLElement,
);
root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
