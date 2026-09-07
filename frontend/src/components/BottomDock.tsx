import { useState, useEffect } from 'react';
import * as Tabs from '@radix-ui/react-tabs';
import {
  Pencil2Icon,
  ReaderIcon,
  Cross2Icon,
  LinkBreak2Icon,
} from '@radix-ui/react-icons';
import ScrollContainer from './ScrollContainer';
import PodLogs from './PodLogs';
import DeploymentLogs from './DeploymentLogs';
import PodShell from './PodShell';
import NodeShell from './NodeShell';
import YamlEditor from './YamlEditor';
import CrossplaneTrace from './CrossplaneTrace';
import TerminalContainer from './TerminalContainer';
import { useStore, type BottomTab } from '../store';
import { useShallow } from 'zustand/react/shallow';
import useResizableHeight from '../hooks/useResizableHeight';
import './BottomDock.css';

const BottomDock = () => {
  const {
    bottomTabs,
    activeBottomTab,
    closeBottomTab,
    setActiveBottomTab,
    moveBottomTab,
  } = useStore(useShallow((s) => ({ bottomTabs: s.bottomTabs, activeBottomTab: s.activeBottomTab, closeBottomTab: s.closeBottomTab, setActiveBottomTab: s.setActiveBottomTab, moveBottomTab: s.moveBottomTab })));
  const [maxHeight, setMaxHeight] = useState(window.innerHeight * 0.8);
  const [showDropZone, setShowDropZone] = useState(false);
  const [isDraggingBottomTab, setIsDraggingBottomTab] = useState(false);

  // Only show tabs that are in the bottom location
  const visibleBottomTabs = bottomTabs.filter(
    (tab) => tab.location === 'bottom',
  );

  useEffect(() => {
    const handleResize = () => {
      setMaxHeight(window.innerHeight * 0.8);
    };
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  // Reset drag states when component unmounts or when tabs change
  useEffect(() => {
    return () => {
      setShowDropZone(false);
      setIsDraggingBottomTab(false);
    };
  }, [visibleBottomTabs.length]);

  // Show drop zone when dragging a bottom tab from center
  useEffect(() => {
    const handleDragStart = (e: DragEvent) => {
      // Check if we're dragging a bottom tab from center
      const target = e.target as HTMLElement;
      if (!target || typeof target.closest !== 'function') return;

      const isFromCenter = target.closest('.resource-list');
      const isBottomTabWrapper = target.closest('.bottom-tab-in-center');

      if (isFromCenter && isBottomTabWrapper) {
        setIsDraggingBottomTab(true);
      } else {
        setIsDraggingBottomTab(false);
        setShowDropZone(false);
      }
    };

    const handleDragOver = (e: DragEvent) => {
      if (isDraggingBottomTab) {
        // Check if we're near the bottom of the window
        const windowHeight = window.innerHeight;
        const mouseY = e.clientY;

        if (mouseY > windowHeight - 150) {
          setShowDropZone(true);
        } else {
          setShowDropZone(false);
        }
      }
    };

    const handleDragEnd = () => {
      setIsDraggingBottomTab(false);
      setShowDropZone(false);
    };

    const handleDrop = () => {
      setIsDraggingBottomTab(false);
      setShowDropZone(false);
    };

    document.addEventListener('dragstart', handleDragStart);
    document.addEventListener('dragover', handleDragOver);
    document.addEventListener('dragend', handleDragEnd);
    document.addEventListener('drop', handleDrop);

    return () => {
      document.removeEventListener('dragstart', handleDragStart);
      document.removeEventListener('dragover', handleDragOver);
      document.removeEventListener('dragend', handleDragEnd);
      document.removeEventListener('drop', handleDrop);
      setShowDropZone(false); // Clean up on unmount
    };
  }, [isDraggingBottomTab]);

  const { height, startResize } = useResizableHeight({
    minHeight: 200,
    maxHeight,
    defaultHeight: 400,
  });

  // Only render if we have tabs or we're actively dragging
  if (visibleBottomTabs.length === 0 && !isDraggingBottomTab) {
    return null;
  }

  return (
    <>
      {showDropZone && isDraggingBottomTab && (
        <div
          className="bottom-dock-drop-zone"
          onDragOver={(e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';
            e.currentTarget.classList.add('drag-over');
          }}
          onDragLeave={(e) => {
            e.currentTarget.classList.remove('drag-over');
          }}
          onDrop={(e) => {
            e.preventDefault();
            e.currentTarget.classList.remove('drag-over');
            const tabType = e.dataTransfer.getData('tab-type');
            const tabId = e.dataTransfer.getData('tab-id');
            const sourcePane = e.dataTransfer.getData('source-pane');

            if (tabType === 'bottom' && sourcePane === 'center' && tabId) {
              moveBottomTab(tabId, 'bottom');
              setActiveBottomTab(tabId);
            }
            setShowDropZone(false);
            setIsDraggingBottomTab(false);
          }}
        >
          <div className="drop-zone-content">
            <span className="drop-zone-text">
              Drop here to return to bottom dock
            </span>
          </div>
        </div>
      )}
      {visibleBottomTabs.length > 0 && (
        <div
          className="bottom-dock"
          style={{ height: `${height}px` }}
          onDragOver={(e) => {
            const tabType = e.dataTransfer.types.includes('tab-type')
              ? 'tab'
              : null;
            if (tabType) {
              e.preventDefault();
              e.dataTransfer.dropEffect = 'move';
              // Add visual feedback
              if (!e.currentTarget.classList.contains('drag-over')) {
                e.currentTarget.classList.add('drag-over');
              }
            }
          }}
          onDragLeave={(e) => {
            // Only remove if we're actually leaving the element, not entering a child
            const rect = e.currentTarget.getBoundingClientRect();
            const x = e.clientX;
            const y = e.clientY;

            if (
              x < rect.left ||
              x >= rect.right ||
              y < rect.top ||
              y >= rect.bottom
            ) {
              e.currentTarget.classList.remove('drag-over');
            }
          }}
          onDrop={(e) => {
            e.preventDefault();
            e.currentTarget.classList.remove('drag-over');
            const tabType = e.dataTransfer.getData('tab-type');
            const tabId = e.dataTransfer.getData('tab-id');
            const sourcePane = e.dataTransfer.getData('source-pane');

            if (tabType === 'bottom' && sourcePane === 'center' && tabId) {
              moveBottomTab(tabId, 'bottom');
              setActiveBottomTab(tabId);
            }
          }}
        >
          <div
            className="bottom-dock-resize-handle"
            onMouseDown={startResize}
            role="separator"
            aria-orientation="horizontal"
            aria-label="Resize bottom panel"
            tabIndex={0}
          />
          <Tabs.Root
            value={activeBottomTab || ''}
            onValueChange={setActiveBottomTab}
            style={{ display: 'flex', flexDirection: 'column', height: '100%' }}
          >
            <div className="bottom-dock-header">
              <Tabs.List className="bottom-dock-tabs">
                {visibleBottomTabs.map((tab: BottomTab, index) => (
                  <div
                    key={tab.id}
                    className="bottom-dock-tab-wrapper"
                    draggable
                    onDragStart={(e) => {
                      e.dataTransfer.effectAllowed = 'move';
                      e.dataTransfer.setData('tab-index', index.toString());
                      e.dataTransfer.setData('tab-id', tab.id);
                      e.dataTransfer.setData('tab-type', 'bottom');
                      e.dataTransfer.setData('source-pane', 'bottom');
                      e.dataTransfer.setData('bottom-tab-type', tab.type);
                      // Add a visual indicator that the tab is being dragged
                      e.currentTarget.classList.add('dragging');
                    }}
                    onDragEnd={(e) => {
                      e.currentTarget.classList.remove('dragging');
                      // Clean up any drag-over states when drag ends
                      document.querySelectorAll('.drag-over').forEach((el) => {
                        el.classList.remove('drag-over');
                      });
                      // Also clean up the parent bottom dock
                      const bottomDock =
                        e.currentTarget.closest('.bottom-dock');
                      if (bottomDock) {
                        bottomDock.classList.remove('drag-over');
                      }
                    }}
                  >
                    <Tabs.Trigger value={tab.id} className="bottom-dock-tab">
                      <span className="tab-icon">
                        {(tab.type === 'logs' ||
                          tab.type === 'deployment-logs') && <ReaderIcon />}
                        {tab.type === 'shell' && (
                          <span
                            style={{
                              fontFamily: 'var(--font-mono)',
                              fontWeight: 'bold',
                            }}
                          >
                            $
                          </span>
                        )}
                        {(tab.type === 'edit' || tab.type === 'create') && <Pencil2Icon />}
                        {tab.type === 'trace' && <LinkBreak2Icon />}
                      </span>
                      <span className="tab-title">
                        {tab.customTitle || tab.title}
                      </span>
                    </Tabs.Trigger>
                    <button
                      className="tab-close"
                      onClick={(e) => {
                        e.stopPropagation();
                        closeBottomTab(tab.id);
                      }}
                    >
                      <Cross2Icon />
                    </button>
                  </div>
                ))}
              </Tabs.List>
              <button
                className="dock-close-all"
                onClick={() =>
                  visibleBottomTabs.forEach((tab) => closeBottomTab(tab.id))
                }
                title="Close all tabs"
              >
                <Cross2Icon />
              </button>
            </div>

            <div className="bottom-dock-contents">
              {visibleBottomTabs.map((tab: BottomTab) => (
                <div
                  key={tab.id}
                  className={`bottom-dock-content bottom-dock-content-${tab.type}`}
                  style={{
                    display: activeBottomTab === tab.id ? 'flex' : 'none',
                  }}
                >
                  {tab.type === 'logs' && (
                    <PodLogs
                      cluster={tab.cluster}
                      namespace={tab.resource.metadata?.namespace || 'default'}
                      name={tab.resource.metadata?.name || ''}
                      containers={tab.resource.spec?.containers}
                      initContainers={tab.resource.spec?.initContainers}
                    />
                  )}
                  {tab.type === 'deployment-logs' && (
                    <DeploymentLogs
                      cluster={tab.cluster}
                      namespace={
                        tab.resource.metadata?.namespace || 'default'
                      }
                      name={tab.resource.metadata?.name || ''}
                      resourceType={tab.resource.kind}
                    />
                  )}
                  {tab.type === 'shell' && tab.resource.kind === 'Pod' && (
                    <PodShell
                      key={`${tab.id}-${
                        tab.resource.selectedContainer ||
                        tab.selectedContainer ||
                        'default'
                      }`}
                      cluster={tab.cluster}
                      namespace={tab.resource.metadata?.namespace || 'default'}
                      podName={tab.resource.metadata?.name || ''}
                      containers={tab.resource.spec?.containers}
                      initContainers={tab.resource.spec?.initContainers}
                      containerStatuses={tab.resource.status?.containerStatuses}
                      initContainerStatuses={
                        tab.resource.status?.initContainerStatuses
                      }
                      tabId={tab.id}
                      initialContainer={
                        tab.resource.selectedContainer || tab.selectedContainer
                      }
                    />
                  )}
                  {tab.type === 'shell' && tab.resource.kind === 'Node' && (
                    <NodeShell
                      cluster={tab.cluster}
                      nodeName={tab.resource.metadata?.name || ''}
                    />
                  )}
                  {tab.type === 'shell' && tab.resource.kind === 'Terminal' && (
                    <TerminalContainer initialTabId={tab.id} />
                  )}
                  {(tab.type === 'edit' || tab.type === 'create') && (
                    <YamlEditor 
                      resource={tab.resource} 
                      cluster={tab.cluster}
                      mode={tab.type === 'create' ? 'create' : 'edit'}
                    />
                  )}
                  {tab.type === 'trace' &&
                    (() => {
                      const apiVersion = tab.resource.apiVersion || '';
                      const parts = apiVersion.split('/');
                      const group = parts.length > 1 ? parts[0] : '';
                      const version = parts.length > 1 ? parts[1] : apiVersion;

                      return (
                        <ScrollContainer
                          className="bottom-dock-scroll"
                          viewportClassName="bottom-dock-viewport"
                        >
                          <CrossplaneTrace
                            cluster={tab.cluster}
                            group={group}
                            version={version}
                            kind={tab.resource.kind || ''}
                            namespace={tab.resource.metadata?.namespace}
                            name={tab.resource.metadata?.name || ''}
                          />
                        </ScrollContainer>
                      );
                    })()}
                </div>
              ))}
            </div>
          </Tabs.Root>
        </div>
      )}
    </>
  );
};

export default BottomDock;
