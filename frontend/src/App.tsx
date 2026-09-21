import { Theme } from '@radix-ui/themes';
import { useEffect, useState } from 'react';
import Layout from './components/Layout';
import SplashScreen from './components/SplashScreen';
import StarProjectDialog from './components/StarProjectDialog';
import { ThemeProvider, useTheme } from './components/ThemeProvider';
import api from './services/api';

const AppInner = () => {
  const { theme, resolvedTheme } = useTheme();
  const appearance = theme === 'custom' ? 'dark' : resolvedTheme;
  const [isLoading, setIsLoading] = useState(true);
  const [showStarProjectDialog, setShowStarProjectDialog] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api
      .waitForBackend(30000)
      .catch((error) => {
        console.error('[App] Backend readiness check failed:', error);
      })
      .finally(() => {
        if (cancelled) return;
        setIsLoading(false);
        setShowStarProjectDialog(true);
        const splash = document.getElementById('native-splash');
        if (splash) {
          splash.classList.add('hiding');
          setTimeout(() => splash.remove(), 300);
        }
      });
    return () => {
      cancelled = true;
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

  if (isLoading) {
    return (
      <Theme
        appearance={appearance}
        accentColor="blue"
        grayColor="gray"
        radius="medium"
        panelBackground="solid"
      >
        <SplashScreen />
      </Theme>
    );
  }

  return (
    <Theme
      appearance={appearance}
      accentColor="blue"
      grayColor="gray"
      radius="medium"
      panelBackground="solid"
    >
      <Layout />
      <StarProjectDialog
        isOpen={showStarProjectDialog}
        onClose={() => setShowStarProjectDialog(false)}
      />
    </Theme>
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
