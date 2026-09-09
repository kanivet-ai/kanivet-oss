import React, { useState, useEffect } from 'react';
import { useTheme } from './ThemeProvider';
import { themeService, MarketplaceTheme } from '../services/themeService';
import {
  SunIcon,
  MoonIcon,
  MixIcon,
  DownloadIcon,
  MagnifyingGlassIcon,
  Cross2Icon,
  CheckIcon,
  TrashIcon,
} from '@radix-ui/react-icons';
import './ThemeSettings.css';

interface ThemeSettingsProps {
  onOpenComponentLibrary?: () => void;
}

export const ThemeSettings: React.FC<ThemeSettingsProps> = ({
  onOpenComponentLibrary,
}) => {
  const {
    theme,
    setTheme,
    customThemes,
    currentCustomTheme,
    applyCustomTheme,
    removeCustomTheme,
    addCustomTheme,
  } = useTheme();
  const [showThemeBrowser, setShowThemeBrowser] = useState(false);
  const [marketplaceThemes, setMarketplaceThemes] = useState<
    MarketplaceTheme[]
  >([]);
  const [loadingThemes, setLoadingThemes] = useState(false);
  const [selectedTheme, setSelectedTheme] = useState<MarketplaceTheme | null>(
    null,
  );
  const [loadingSelectedTheme, setLoadingSelectedTheme] = useState(false);
  const [searchTerm, setSearchTerm] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [deleteConfirmTheme, setDeleteConfirmTheme] = useState<string | null>(
    null,
  );
  const itemsPerPage = 8;
  const isDev =
    process.env.NODE_ENV !== 'production' || (window as any).electron?.isDev;

  useEffect(() => {
    if (showThemeBrowser) {
      fetchThemes();
    }
  }, [showThemeBrowser]);

  const loadTheme = async (marketplaceTheme: MarketplaceTheme) => {
    const existingTheme = customThemes.find(
      (t) => t.name === marketplaceTheme.displayName,
    );
    if (existingTheme) {
      applyCustomTheme(existingTheme);
      return;
    }

    setLoadingSelectedTheme(true);
    try {
      const theme =
        await themeService.downloadAndInstallTheme(marketplaceTheme);
      await addCustomTheme(theme);
      await applyCustomTheme(theme);
    } catch (error) {
      console.error('Failed to load theme:', error);
      alert(
        `Failed to load theme: ${
          error instanceof Error ? error.message : 'Unknown error'
        }`,
      );
    } finally {
      setLoadingSelectedTheme(false);
    }
  };

  const filteredThemes = marketplaceThemes.filter(
    (t) =>
      t.displayName.toLowerCase().includes(searchTerm.toLowerCase()) ||
      t.description?.toLowerCase().includes(searchTerm.toLowerCase()),
  );

  const totalPages = Math.ceil(filteredThemes.length / itemsPerPage);
  const paginatedThemes = filteredThemes.slice(
    (currentPage - 1) * itemsPerPage,
    currentPage * itemsPerPage,
  );

  useEffect(() => {
    setCurrentPage(1);
    if (showThemeBrowser) {
      fetchThemes();
    }
  }, [searchTerm]);

  const fetchThemes = async () => {
    setLoadingThemes(true);
    try {
      const themes = await themeService.searchMarketplaceThemes(
        searchTerm || '',
        100,
      );
      setMarketplaceThemes(themes);
    } catch (error) {
      console.error('Failed to fetch themes:', error);
    } finally {
      setLoadingThemes(false);
    }
  };

  const getCurrentThemeName = () => {
    if (theme === 'custom' && currentCustomTheme) {
      return currentCustomTheme.name;
    }
    return theme === 'light' ? 'Default (Light)' : 'Default (Dark)';
  };

  const isThemeDownloaded = (themeName: string) => {
    return customThemes.some((t) => t.name === themeName);
  };

  const handleDeleteTheme = (themeId: string) => {
    setDeleteConfirmTheme(themeId);
  };

  const confirmDeleteTheme = () => {
    if (deleteConfirmTheme) {
      removeCustomTheme(deleteConfirmTheme);
      setDeleteConfirmTheme(null);
    }
  };

  const cancelDeleteTheme = () => {
    setDeleteConfirmTheme(null);
  };

  const allThemes = [
    {
      id: 'dark',
      name: 'Default (Dark)',
      type: 'built-in' as const,
      colors: {
        primary: '#1e1e1e',
        secondary: '#252526',
        text: '#cccccc',
        accent: '#007acc',
      },
    },
    {
      id: 'light',
      name: 'Default (Light)',
      type: 'built-in' as const,
      colors: {
        primary: '#ffffff',
        secondary: '#f3f3f3',
        text: '#333333',
        accent: '#0066cc',
      },
    },
    ...customThemes.map((t) => ({
      id: t.id,
      name: t.name,
      type: 'custom' as const,
      theme: t,
      colors: t.colors,
    })),
  ];

  const getColorPalette = (themeItem: any) => {
    if (themeItem.colors) {
      const colors =
        themeItem.type === 'custom' ? themeItem.colors : themeItem.colors;
      return [
        colors['editor.background'] ||
          colors['primary'] ||
          colors['bg-primary'],
        colors['editor.foreground'] || colors['text'] || colors['text-primary'],
        colors['activityBar.background'] ||
          colors['secondary'] ||
          colors['bg-secondary'],
        colors['statusBar.background'] || colors['accent'] || colors['accent'],
      ]
        .filter(Boolean)
        .slice(0, 4);
    }
    return [];
  };

  return (
    <div className="theme-settings-compact">
      <div className="theme-header">
        <h3>Theme</h3>
        <button
          className="browse-themes-btn"
          onClick={() => setShowThemeBrowser(true)}
          title="Browse themes from marketplace"
        >
          <DownloadIcon />
          <span>Browse Themes</span>
        </button>
      </div>

      <div className="current-theme-indicator">
        <span className="current-theme-label">Active:</span>
        <span className="current-theme-name">{getCurrentThemeName()}</span>
      </div>

      <div className="theme-list-compact">
        {allThemes.map((themeItem) => {
          const isActive =
            themeItem.type === 'built-in'
              ? theme === themeItem.id && theme !== 'custom'
              : theme === 'custom' && currentCustomTheme?.id === themeItem.id;

          return (
            <div
              key={themeItem.id}
              className={`theme-item-compact ${isActive ? 'active' : ''}`}
            >
              <button
                className="theme-item-button"
                onClick={() => {
                  if (themeItem.type === 'built-in') {
                    // Clear any custom theme styles when switching to built-in
                    document.documentElement.removeAttribute('style');
                    setTheme(themeItem.id as 'light' | 'dark');
                  } else if (themeItem.theme) {
                    applyCustomTheme(themeItem.theme);
                  }
                }}
              >
                <span className="theme-item-icon">
                  {themeItem.id === 'light' ? (
                    <SunIcon />
                  ) : themeItem.id === 'dark' ? (
                    <MoonIcon />
                  ) : (
                    <MixIcon />
                  )}
                </span>
                <span className="theme-item-name">{themeItem.name}</span>
                <div className="theme-color-preview">
                  {getColorPalette(themeItem).map((color, idx) => (
                    <span
                      key={idx}
                      className="color-dot"
                      style={{ backgroundColor: color }}
                    />
                  ))}
                </div>
                {isActive && <CheckIcon className="theme-item-check" />}
              </button>
              {themeItem.type === 'custom' && (
                <button
                  className="theme-item-remove"
                  onClick={(e) => {
                    e.stopPropagation();
                    handleDeleteTheme(themeItem.id);
                  }}
                  title="Remove theme"
                >
                  <TrashIcon />
                </button>
              )}
            </div>
          );
        })}
      </div>

      {isDev && onOpenComponentLibrary && (
        <div className="dev-section">
          <button
            className="component-library-btn"
            onClick={onOpenComponentLibrary}
            title="Browse component library"
          >
            Component Library
          </button>
        </div>
      )}

      {deleteConfirmTheme && (
        <div className="delete-confirm-modal">
          <div className="modal-backdrop" onClick={cancelDeleteTheme} />
          <div className="delete-confirm-dialog">
            <h3>Delete Theme</h3>
            <p>Are you sure you want to delete "{deleteConfirmTheme}"?</p>
            <p className="delete-warning">This action cannot be undone.</p>
            <div className="delete-confirm-actions">
              <button className="btn-cancel" onClick={cancelDeleteTheme}>
                Cancel
              </button>
              <button className="btn-delete" onClick={confirmDeleteTheme}>
                <TrashIcon />
                Delete
              </button>
            </div>
          </div>
        </div>
      )}

      {showThemeBrowser && (
        <div className="theme-browser-modal">
          <div
            className="modal-backdrop"
            onClick={() => setShowThemeBrowser(false)}
          />
          <div className="modal-content theme-browser-content">
            <div className="theme-browser-header">
              <h3>Browse Themes</h3>
              <button
                className="close-btn"
                onClick={() => setShowThemeBrowser(false)}
              >
                <Cross2Icon />
              </button>
            </div>
            <div className="theme-browser-search">
              <div className="search-input-wrapper">
                <MagnifyingGlassIcon className="search-icon" />
                <input
                  type="text"
                  placeholder="Search themes..."
                  value={searchTerm}
                  onChange={(e) => setSearchTerm(e.target.value)}
                  className="theme-search-input"
                />
              </div>
            </div>
            <div className="theme-browser-body">
              {loadingThemes ? (
                <div className="loading-themes">Loading themes...</div>
              ) : (
                <>
                  <div className="theme-list">
                    {paginatedThemes.map((marketplaceTheme) => {
                      const downloaded = isThemeDownloaded(
                        marketplaceTheme.displayName,
                      );
                      const existingTheme = customThemes.find(
                        (t) => t.name === marketplaceTheme.displayName,
                      );
                      const themeColors = existingTheme?.colors;

                      return (
                        <div
                          key={`${marketplaceTheme.namespace}.${marketplaceTheme.name}`}
                          className={`theme-list-item ${
                            selectedTheme?.name === marketplaceTheme.name
                              ? 'selected'
                              : ''
                          } ${downloaded ? 'downloaded' : ''}`}
                          onClick={() => setSelectedTheme(marketplaceTheme)}
                        >
                          <div className="theme-info">
                            <div className="theme-name">
                              {marketplaceTheme.displayName}
                              {selectedTheme?.name ===
                                marketplaceTheme.name && (
                                <CheckIcon className="selected-indicator" />
                              )}
                              {downloaded && (
                                <span className="downloaded-badge">
                                  <CheckIcon />
                                  Downloaded
                                </span>
                              )}
                            </div>
                            {marketplaceTheme.description && (
                              <div className="theme-description">
                                {marketplaceTheme.description}
                              </div>
                            )}
                            <div className="theme-meta">
                              <span className="theme-downloads">
                                <DownloadIcon className="meta-icon" />
                                {marketplaceTheme.downloads.toLocaleString()}
                              </span>
                              <span className="theme-author">
                                by {marketplaceTheme.namespace}
                              </span>
                              {themeColors && (
                                <div className="theme-color-preview-list">
                                  {[
                                    themeColors['editor.background'],
                                    themeColors['editor.foreground'],
                                    themeColors['activityBar.background'],
                                    themeColors['statusBar.background'],
                                  ]
                                    .filter(Boolean)
                                    .slice(0, 5)
                                    .map((color, idx) => (
                                      <span
                                        key={idx}
                                        className="color-dot-list"
                                        style={{ backgroundColor: color }}
                                      />
                                    ))}
                                </div>
                              )}
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                  {totalPages > 1 && (
                    <div className="theme-pagination">
                      <button
                        className="pagination-btn"
                        onClick={() =>
                          setCurrentPage((prev) => Math.max(1, prev - 1))
                        }
                        disabled={currentPage === 1}
                      >
                        Previous
                      </button>
                      <span className="pagination-info">
                        Page {currentPage} of {totalPages}
                      </span>
                      <button
                        className="pagination-btn"
                        onClick={() =>
                          setCurrentPage((prev) =>
                            Math.min(totalPages, prev + 1),
                          )
                        }
                        disabled={currentPage === totalPages}
                      >
                        Next
                      </button>
                    </div>
                  )}
                </>
              )}
            </div>
            {selectedTheme && (
              <div className="theme-browser-footer">
                <div className="selected-theme-info">
                  Selected: <strong>{selectedTheme.displayName}</strong>
                </div>
                <button
                  className="load-theme-btn"
                  onClick={() => loadTheme(selectedTheme)}
                  disabled={loadingSelectedTheme}
                >
                  {loadingSelectedTheme
                    ? 'Loading...'
                    : isThemeDownloaded(selectedTheme.displayName)
                      ? 'Apply Theme'
                      : 'Download & Apply'}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
