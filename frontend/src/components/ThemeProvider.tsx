import React, {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import { applyTheme, convertVSCodeTheme } from '../utils/themeAdapter';
import { themeService, Theme } from '../services/themeService';

type ThemeMode = 'light' | 'dark' | 'auto' | 'custom';

const THEME_CACHE_KEY = 'kanivet-theme-cache';

function getSystemTheme(): 'light' | 'dark' {
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function getCachedTheme(): ThemeMode {
  try {
    const cached = localStorage.getItem(THEME_CACHE_KEY);
    if (cached && ['light', 'dark', 'auto', 'custom'].includes(cached)) {
      return cached as ThemeMode;
    }
  } catch {}
  return 'dark';
}

function cacheTheme(mode: ThemeMode) {
  try {
    localStorage.setItem(THEME_CACHE_KEY, mode);
  } catch {}
}

interface ThemeContextValue {
  theme: ThemeMode;
  resolvedTheme: 'light' | 'dark';
  setTheme: (mode: ThemeMode) => void;
  toggleTheme: () => void;
  customThemes: Theme[];
  currentCustomTheme?: Theme;
  addCustomTheme: (theme: Theme) => void;
  removeCustomTheme: (id: string) => void;
  applyCustomTheme: (theme: Theme) => void;
  loading: boolean;
}

const ThemeContext = createContext<ThemeContextValue | undefined>(undefined);

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [theme, setThemeState] = useState<ThemeMode>(getCachedTheme);
  const [systemTheme, setSystemTheme] = useState<'light' | 'dark'>(getSystemTheme);
  const [customThemes, setCustomThemes] = useState<Theme[]>([]);
  const [currentCustomTheme, setCurrentCustomTheme] = useState<Theme | undefined>(undefined);
  const [loading, setLoading] = useState(true);

  const resolvedTheme = theme === 'auto' ? systemTheme : theme === 'custom' ? 'dark' : theme;

  useEffect(() => {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    const handleChange = (e: MediaQueryListEvent) => {
      setSystemTheme(e.matches ? 'dark' : 'light');
    };
    mediaQuery.addEventListener('change', handleChange);
    return () => mediaQuery.removeEventListener('change', handleChange);
  }, []);

  useEffect(() => {
    const initializeThemes = async () => {
      setLoading(true);
      const [themesResult, settingsResult] = await Promise.allSettled([
        themeService.listThemes(),
        themeService.getSettings(),
      ]);

      const themes = themesResult.status === 'fulfilled' ? themesResult.value : [];
      if (themes.length) {
        setCustomThemes(themes.filter((t) => t.type === 'custom'));
      }

      const persistedMode =
        settingsResult.status === 'fulfilled' ? (settingsResult.value.currentMode as ThemeMode | undefined) : undefined;
      const mode: ThemeMode = persistedMode ?? getCachedTheme();
      setThemeState(mode);
      cacheTheme(mode);

      const settings = settingsResult.status === 'fulfilled' ? settingsResult.value : undefined;
      if (mode === 'custom' && settings?.currentCustomTheme) {
        const currentTheme = themes.find((t) => t.id === settings.currentCustomTheme);
        setCurrentCustomTheme(currentTheme);
        if (currentTheme) applyTheme(convertVSCodeTheme(currentTheme));
      } else {
        const effectiveTheme = mode === 'auto' ? getSystemTheme() : mode;
        document.documentElement.setAttribute('data-theme', effectiveTheme);
        document.documentElement.style.colorScheme = effectiveTheme;
      }
      setLoading(false);
    };

    initializeThemes();
  }, []);

  useEffect(() => {
    if (theme === 'custom' && currentCustomTheme) {
      const kanivetTheme = convertVSCodeTheme(currentCustomTheme);
      applyTheme(kanivetTheme);
    } else {
      const effectiveTheme = theme === 'auto' ? systemTheme : theme;
      document.documentElement.setAttribute('data-theme', effectiveTheme);
      document.documentElement.style.colorScheme = effectiveTheme;
    }
  }, [theme, currentCustomTheme, systemTheme]);

  const setTheme = async (mode: ThemeMode) => {
    setThemeState(mode);
    cacheTheme(mode);
    if (mode !== 'custom' && mode !== 'auto') {
      await themeService.applyTheme(mode);
    } else if (mode === 'auto') {
      await themeService.applyTheme(getSystemTheme());
    }
  };

  const toggleTheme = async () => {
    const newMode: ThemeMode = theme === 'dark' ? 'light' : theme === 'light' ? 'auto' : 'dark';
    setThemeState(newMode);
    cacheTheme(newMode);
    if (newMode === 'auto') {
      await themeService.applyTheme(getSystemTheme());
    } else {
      await themeService.applyTheme(newMode);
    }
  };

  const addCustomTheme = async (theme: Theme) => {
    setCustomThemes((prev) => {
      const filtered = prev.filter((t) => t.id !== theme.id);
      return [...filtered, theme];
    });
  };

  const removeCustomTheme = async (id: string) => {
    try {
      await themeService.deleteTheme(id);
      setCustomThemes((prev) => prev.filter((t) => t.id !== id));
      if (currentCustomTheme?.id === id) {
        setCurrentCustomTheme(undefined);
        await setTheme('dark');
      }
    } catch (error) {
      console.error('Failed to remove theme:', error);
    }
  };

  const applyCustomTheme = async (theme: Theme) => {
    try {
      await themeService.applyTheme(theme.id);
      setCurrentCustomTheme(theme);
      setTheme('custom');
      const kanivetTheme = convertVSCodeTheme(theme);
      applyTheme(kanivetTheme);
    } catch (error) {
      console.error('Failed to apply theme:', error);
    }
  };

  const value = useMemo(
    () => ({
      theme,
      resolvedTheme,
      setTheme,
      toggleTheme,
      customThemes,
      currentCustomTheme,
      addCustomTheme,
      removeCustomTheme,
      applyCustomTheme,
      loading,
    }),
    [theme, resolvedTheme, customThemes, currentCustomTheme, loading],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
};

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider');
  return ctx;
}
