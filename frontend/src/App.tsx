import { useEffect, useState } from 'react';
import Layout from './components/Layout';
import StarProjectDialog from './components/StarProjectDialog';
import { ThemeProvider } from './components/ThemeProvider';
import api from './services/api';
import { whenIdle } from './utils/idle';
import { isStarProjectPromptDismissed } from './utils/starProjectPromptPreference';

const AppInner = () => {
  const [isLoading, setIsLoading] = useState(true);
  const [showStarProjectDialog, setShowStarProjectDialog] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let cancelStarPrompt: (() => void) | undefined;
    api
      .waitForBackend(30000)
      .catch((error) => {
        console.error('[App] Backend readiness check failed:', error);
      })
      .finally(() => {
        if (cancelled) return;
        setIsLoading(false);
        // The prompt's focus trap and scroll lock force a style and layout
        // pass: open it after the first screen has painted, not in its task.
        if (!isStarProjectPromptDismissed()) {
          cancelStarPrompt = whenIdle(
            () => setShowStarProjectDialog(true),
            3000,
          );
        }
        clearInterval((window as any).__kanivetSplashTimer);
        const splash = document.getElementById('native-splash');
        if (splash) {
          splash.classList.add('hiding');
          setTimeout(() => splash.remove(), 300);
        }
      });
    return () => {
      cancelled = true;
      cancelStarPrompt?.();
    };
  }, []);

  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (electronAPI?.onClearCache) {
      const unsubscribe = electronAPI.onClearCache(() => {
        api.clearCache();
      });
      return () => {
        if (typeof unsubscribe === 'function') {
          unsubscribe();
        }
      };
    }
  }, []);

  // The splash in index.html covers the window until the backend answers.
  if (isLoading) return null;

  return (
    <div className="app-root">
      <Layout />
      <StarProjectDialog
        isOpen={showStarProjectDialog}
        onClose={() => setShowStarProjectDialog(false)}
      />
    </div>
  );
};

function App() {
  return (
    <ThemeProvider>
      <AppInner />
    </ThemeProvider>
  );
}

export default App;
