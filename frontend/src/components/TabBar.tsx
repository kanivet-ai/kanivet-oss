import { useStore } from '../store';
import { useState, useRef, useEffect, useCallback } from 'react';
import { parseClusterName } from '../utils/clusterUtils';
import AWSIcon from './AWSIcon';
import AzureIcon from './AzureIcon';
import GCPIcon from './GCPIcon';
import { useTheme } from './ThemeProvider';
import {
  MoonIcon,
  SunIcon,
  KeyboardIcon,
  DesktopIcon,
  Pencil2Icon,
  MagnifyingGlassIcon,
  Half2Icon,
  ChevronLeftIcon,
  ChevronRightIcon,
  LayersIcon,
  GearIcon,
} from '@radix-ui/react-icons';
import KeyboardShortcutsEditor from './KeyboardShortcutsEditor';
import SSOSessionManager from './SSOSessionManager';
import ConnectionStatusIndicator from './ConnectionStatusIndicator';
import './TabBar.css';

interface TabBarProps {
  onOpenSettings?: () => void;
}

const TabBar = ({ onOpenSettings }: TabBarProps) => {
  const {
    activeTabs,
    currentTab,
    setCurrentTab,
    closeTab,
    openBottomTab,
    clusterAliases,
    reorderTabs,
    clusterErrors,
  } = useStore();
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    tabId: string;
    tabIndex: number;
  } | null>(null);
  const [hoveredTab, setHoveredTab] = useState<string | null>(null);
  const [tooltipPosition, setTooltipPosition] = useState({ x: 0, y: 0 });
  const [draggedIndex, setDraggedIndex] = useState<number | null>(null);
  const [dropIndicator, setDropIndicator] = useState<{ index: number; side: 'left' | 'right' } | null>(null);
  const [controlsCollapsed, setControlsCollapsed] = useState(() => window.innerWidth < 900);
  const [manualOverride, setManualOverride] = useState(false);
  const { theme, toggleTheme } = useTheme();
  const contextMenuRef = useRef<HTMLDivElement>(null);
  const tooltipTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const tabsContainerRef = useRef<HTMLDivElement>(null);

  const clearTooltipTimeout = useCallback(() => {
    if (tooltipTimeoutRef.current) {
      clearTimeout(tooltipTimeoutRef.current);
      tooltipTimeoutRef.current = null;
    }
  }, []);

  const clearTooltip = useCallback(() => {
    clearTooltipTimeout();
    setHoveredTab(null);
  }, [clearTooltipTimeout]);

  const activeTabIds = activeTabs.map((tab) => tab.id).join('\n');

  useEffect(() => {
    const handleResize = () => {
      if (!manualOverride) {
        setControlsCollapsed(window.innerWidth < 900);
      }
    };
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, [manualOverride]);

  const handleNewTab = () => {
    clearTooltip();
    // Use the same cluster selector modal as CMD+T for consistency
    const event = new CustomEvent('layout:openClusterSelector');
    window.dispatchEvent(event);
  };

  const handleOpenTerminal = () => {
    openBottomTab(
      'shell',
      { kind: 'Terminal', metadata: { name: 'Terminal' } },
      'local',
    );
  };

  const handleNewResource = () => {
    if (!currentTab) return;
    openBottomTab(
      'create',
      { kind: 'New', metadata: { name: 'new-resource' } },
      currentTab,
    );
  };

  const handleCloseOthers = (tabId: string) => {
    activeTabs.forEach((tab) => {
      if (tab.id !== tabId) {
        closeTab(tab.id);
      }
    });
  };

  const handleCloseToRight = (tabIndex: number) => {
    activeTabs.forEach((tab, index) => {
      if (index > tabIndex) {
        closeTab(tab.id);
      }
    });
  };

  const handleContextMenu = (
    e: React.MouseEvent,
    tabId: string,
    tabIndex: number,
  ) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, tabId, tabIndex });
  };

  const handleMouseEnter = (e: React.MouseEvent, tabId: string) => {
    if (tooltipTimeoutRef.current) {
      clearTimeout(tooltipTimeoutRef.current);
    }

    const rect = e.currentTarget.getBoundingClientRect();
    tooltipTimeoutRef.current = setTimeout(() => {
      setTooltipPosition({
        x: rect.left + rect.width / 2,
        y: rect.bottom + 8,
      });
      setHoveredTab(tabId);
    }, 500);
  };

  const handleDragStart = (e: React.DragEvent, index: number) => {
    clearTooltip();
    setDraggedIndex(index);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', index.toString());
  };

  const handleDragOver = (e: React.DragEvent, index: number) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (draggedIndex === null || index === draggedIndex) {
      setDropIndicator(null);
      return;
    }
    const rect = e.currentTarget.getBoundingClientRect();
    const midpoint = rect.left + rect.width / 2;
    const side = e.clientX < midpoint ? 'left' : 'right';
    setDropIndicator({ index, side });
  };

  const handleDragLeave = () => {
    setDropIndicator(null);
  };

  const handleDrop = (e: React.DragEvent, index: number) => {
    e.preventDefault();
    clearTooltip();
    if (draggedIndex === null || draggedIndex === index) {
      setDraggedIndex(null);
      setDropIndicator(null);
      return;
    }
    const rect = e.currentTarget.getBoundingClientRect();
    const midpoint = rect.left + rect.width / 2;
    const dropOnRight = e.clientX >= midpoint;
    let toIndex = dropOnRight ? index + 1 : index;
    if (draggedIndex < index) toIndex--;
    if (toIndex !== draggedIndex) reorderTabs(draggedIndex, toIndex);
    setDraggedIndex(null);
    setDropIndicator(null);
  };

  const handleDragEnd = () => {
    clearTooltip();
    setDraggedIndex(null);
    setDropIndicator(null);
  };

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        contextMenuRef.current &&
        !contextMenuRef.current.contains(e.target as Node)
      ) {
        setContextMenu(null);
      }
    };

    if (contextMenu) {
      document.addEventListener('mousedown', handleClickOutside);
      return () =>
        document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [contextMenu]);

  useEffect(() => {
    return () => {
      clearTooltipTimeout();
    };
  }, [clearTooltipTimeout]);

  useEffect(() => {
    clearTooltip();
  }, [activeTabIds, currentTab, clearTooltip]);

  useEffect(() => {
    const handleWindowBlur = () => clearTooltip();
    const handlePointerDown = (e: PointerEvent) => {
      if (!tabsContainerRef.current?.contains(e.target as Node)) {
        clearTooltip();
      }
    };

    window.addEventListener('blur', handleWindowBlur);
    document.addEventListener('pointerdown', handlePointerDown);
    return () => {
      window.removeEventListener('blur', handleWindowBlur);
      document.removeEventListener('pointerdown', handlePointerDown);
    };
  }, [clearTooltip]);

  return (
    <div className="tab-bar">
      <button
        className="tab-bar-logo"
        title="Kanivet"
      >
        <svg
          className="tab-bar-logo-icon"
          width="20"
          height="20"
          viewBox="0 0 512 512"
          xmlns="http://www.w3.org/2000/svg"
        >
          <rect width="512" height="512" rx="96" fill="var(--bg-tertiary)" />
          <path
            d="M168 96 L168 416 L232 416 L232 280 L340 416 L420 416 L290 256 L410 96 L330 96 L232 232 L232 96 Z"
            fill="var(--accent)"
          />
        </svg>
        <span className="tab-bar-logo-text">Kanivet</span>
      </button>
      <div className="tab-bar-divider" />
      <div className="tabs-container" ref={tabsContainerRef}>
        {activeTabs.map((tab, index) => {
          const clusterInfo = parseClusterName(tab.id, clusterAliases[tab.id]);
          const isActive = currentTab === tab.id;
          const isDragging = draggedIndex === index;
          const showLeftIndicator = dropIndicator?.index === index && dropIndicator.side === 'left';
          const showRightIndicator = dropIndicator?.index === index && dropIndicator.side === 'right';
          const isVCluster = !!clusterInfo.isVCluster;
          const tabError = clusterErrors[tab.id];
          const vcHostLabel = isVCluster && clusterInfo.vcluster
            ? parseClusterName(clusterInfo.vcluster.host).displayName
            : '';
          const vcName = clusterInfo.vcluster?.name || '';
          const vcTooltip = isVCluster && clusterInfo.vcluster
            ? `Virtual cluster ${clusterInfo.vcluster.name} in ${vcHostLabel}/${clusterInfo.vcluster.namespace}`
            : tab.id;
          return (
            <div
              key={tab.id}
              className={`tab ${isActive ? 'active' : ''} ${isDragging ? 'dragging' : ''} ${showLeftIndicator ? 'drag-over-left' : ''} ${showRightIndicator ? 'drag-over-right' : ''} ${isVCluster ? 'vcluster' : ''}`}
              data-vcluster={isVCluster ? 'true' : undefined}
              title={vcTooltip}
              onClick={() => {
                clearTooltip();
                setCurrentTab(tab.id);
              }}
              onContextMenu={(e) => handleContextMenu(e, tab.id, index)}
              onMouseEnter={(e) => handleMouseEnter(e, tab.id)}
              onMouseLeave={clearTooltip}
              draggable
              onDragStart={(e) => handleDragStart(e, index)}
              onDragOver={(e) => handleDragOver(e, index)}
              onDragLeave={handleDragLeave}
              onDrop={(e) => handleDrop(e, index)}
              onDragEnd={handleDragEnd}
            >
              {tabError && (
                <span className="tab-error-dot" title={tabError.errorMessage} />
              )}
              {isVCluster ? (
                <LayersIcon className="vcluster-icon" width={14} height={14} />
              ) : clusterInfo.provider === 'aws' ? (
                <AWSIcon className="aws-icon" size={14} />
              ) : clusterInfo.provider === 'azure' ? (
                <AzureIcon className="azure-icon" size={14} />
              ) : clusterInfo.provider === 'gcp' ? (
                <GCPIcon className="gcp-icon" size={14} />
              ) : null}
              {isVCluster ? (
                <span className="tab-name">
                  <span className="vcluster-host">{vcHostLabel}</span>
                  <span className="vcluster-sep"> › </span>
                  <span className="vcluster-name">{vcName}</span>
                </span>
              ) : (
                <span className="tab-name">{clusterInfo.displayName}</span>
              )}
              <span
                className="tab-close"
                onClick={(e) => {
                  e.stopPropagation();
                  clearTooltip();
                  closeTab(tab.id);
                }}
              >
                ×
              </span>
            </div>
          );
        })}
        <button
          className="tab-add-btn"
          onClick={handleNewTab}
          title="New Tab (⌘T)"
        >
          +
        </button>
      </div>
      <div className="tab-bar-controls">
        <ConnectionStatusIndicator />
        <button
          className="theme-toggle controls-toggle"
          onClick={() => {
            setManualOverride(true);
            setControlsCollapsed(!controlsCollapsed);
          }}
          title={controlsCollapsed ? 'Show controls' : 'Hide controls'}
          aria-label={controlsCollapsed ? 'Show controls' : 'Hide controls'}
        >
          {controlsCollapsed ? <ChevronLeftIcon width={16} height={16} /> : <ChevronRightIcon width={16} height={16} />}
        </button>
        <div className={`controls-collapsible ${controlsCollapsed ? 'collapsed' : ''}`}>
          <button
            className="theme-toggle"
            onClick={handleOpenTerminal}
            title="Open Terminal"
            aria-label="Open Terminal"
          >
            <DesktopIcon width={16} height={16} />
          </button>
          <button
            className="theme-toggle"
            onClick={handleNewResource}
            title="Create New Resource"
            aria-label="Create New Resource"
            disabled={!currentTab}
          >
            <Pencil2Icon width={16} height={16} />
          </button>
          <button
            className="theme-toggle"
            onClick={() => setShowShortcuts(true)}
            title="Keyboard shortcuts"
            aria-label="Keyboard shortcuts"
          >
            <KeyboardIcon width={16} height={16} />
          </button>
          <SSOSessionManager />
          <button
            className="theme-toggle"
            onClick={toggleTheme}
            title={
              theme === 'dark' ? 'Dark theme (click for light)' :
              theme === 'light' ? 'Light theme (click for auto)' :
              'Auto theme (click for dark)'
            }
            aria-label="Toggle theme"
          >
            {theme === 'dark' ? (
              <MoonIcon width={16} height={16} />
            ) : theme === 'light' ? (
              <SunIcon width={16} height={16} />
            ) : (
              <Half2Icon width={16} height={16} />
            )}
          </button>
          <button
            className="theme-toggle"
            onClick={() => {
              const event = new CustomEvent('layout:openCommandPalette');
              window.dispatchEvent(event);
            }}
            title="Search (⌘K)"
            aria-label="Search"
          >
            <MagnifyingGlassIcon width={16} height={16} />
          </button>
          {onOpenSettings && (
            <button
              className="theme-toggle"
              onClick={onOpenSettings}
              title="Settings"
              aria-label="Settings"
            >
              <GearIcon width={16} height={16} />
            </button>
          )}
          <div className="tab-bar-separator" />
        </div>
        <KeyboardShortcutsEditor
          isOpen={showShortcuts}
          onClose={() => setShowShortcuts(false)}
        />
      </div>
      {contextMenu && (
        <div
          ref={contextMenuRef}
          className="tab-context-menu"
          style={{
            position: 'fixed',
            left: contextMenu.x,
            top: contextMenu.y,
            zIndex: 10000,
          }}
        >
          <div
            className="context-menu-item"
            onClick={() => {
              closeTab(contextMenu.tabId);
              setContextMenu(null);
            }}
          >
            <span>Close</span>
            <span className="context-menu-shortcut">⌘W</span>
          </div>
          <div
            className="context-menu-item"
            onClick={() => {
              handleCloseOthers(contextMenu.tabId);
              setContextMenu(null);
            }}
          >
            <span>Close Others</span>
            <span className="context-menu-shortcut">⌥⌘T</span>
          </div>
          {contextMenu.tabIndex < activeTabs.length - 1 && (
            <div
              className="context-menu-item"
              onClick={() => {
                handleCloseToRight(contextMenu.tabIndex);
                setContextMenu(null);
              }}
            >
              <span>Close to the Right</span>
            </div>
          )}
          <div
            className="context-menu-item"
            onClick={() => {
              activeTabs.forEach((tab) => {
                closeTab(tab.id);
              });
              setContextMenu(null);
            }}
          >
            <span>Close All</span>
            <span className="context-menu-shortcut">⌘⇧W</span>
          </div>
        </div>
      )}
      {hoveredTab && (
        <div
          className="tab-tooltip"
          style={{
            position: 'fixed',
            left: tooltipPosition.x,
            top: tooltipPosition.y,
            transform: 'translateX(-50%)',
            zIndex: 10000,
            pointerEvents: 'none',
          }}
        >
          {hoveredTab}
        </div>
      )}
    </div>
  );
};

export default TabBar;
