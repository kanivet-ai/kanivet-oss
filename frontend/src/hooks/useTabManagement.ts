import { useState, useCallback } from 'react';

interface TabContextMenu {
  x: number;
  y: number;
  tab: any;
  isDetailTab: boolean;
  isBottomTab: boolean;
  isResourceListTab: boolean;
}

export const useTabManagement = (
  paneId: string | undefined,
  allCenterTabs: any[],
  closeDetailTab: (id: string) => void,
  closeResourceListTab: (id: string) => void,
  closeBottomTab: (id: string) => void,
) => {
  const [tabContextMenu, setTabContextMenu] = useState<TabContextMenu | null>(
    null,
  );

  const handleTabContextMenu = useCallback(
    (
      e: React.MouseEvent,
      tab: any,
      isDetailTab: boolean,
      isBottomTab: boolean,
      isResourceListTab: boolean,
    ) => {
      e.preventDefault();
      setTabContextMenu({
        x: e.clientX,
        y: e.clientY,
        tab,
        isDetailTab,
        isBottomTab,
        isResourceListTab,
      });
    },
    [],
  );

  const handleSplitPane = useCallback(
    (direction: 'up' | 'down' | 'left' | 'right') => {
      const splitDirection =
        direction === 'up' || direction === 'down' ? 'horizontal' : 'vertical';
      console.log('Splitting pane:', {
        direction,
        splitDirection,
        paneId: paneId || 'root',
      });
      const event = new CustomEvent('centerPane:split', {
        detail: {
          paneId: paneId || 'root',
          direction: splitDirection,
        },
      });
      window.dispatchEvent(event);
      setTabContextMenu(null);
    },
    [paneId],
  );

  const handleTabContextAction = useCallback(
    (action: string) => {
      if (!tabContextMenu) return;

      const tab = tabContextMenu.tab;
      const currentTabId = tab.id;

      switch (action) {
        case 'close':
          if (tabContextMenu.isDetailTab) {
            closeDetailTab(tab.id);
          } else if (tabContextMenu.isResourceListTab) {
            closeResourceListTab(tab.id);
          } else if (tabContextMenu.isBottomTab) {
            closeBottomTab(tab.id);
          }
          break;

        case 'closeOthers': {
          const tabsInPane = allCenterTabs.filter((t) => {
            if (tabContextMenu.isResourceListTab) {
              return 'resource' in t && t.paneId === tab.paneId;
            } else if (tabContextMenu.isDetailTab) {
              return (
                'item' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            } else if (tabContextMenu.isBottomTab) {
              return (
                'type' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            }
            return false;
          });

          tabsInPane.forEach((t) => {
            if (t.id !== currentTabId) {
              if ('item' in t) {
                closeDetailTab(t.id);
              } else if ('type' in t) {
                closeBottomTab(t.id);
              } else {
                closeResourceListTab(t.id);
              }
            }
          });
          break;
        }

        case 'closeToRight': {
          const currentTabIndex = allCenterTabs.findIndex(
            (t) => t.id === currentTabId,
          );
          const tabsInPane = allCenterTabs.filter((t, index) => {
            if (index <= currentTabIndex) return false;
            if (tabContextMenu.isResourceListTab) {
              return 'resource' in t && t.paneId === tab.paneId;
            } else if (tabContextMenu.isDetailTab) {
              return (
                'item' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            } else if (tabContextMenu.isBottomTab) {
              return (
                'type' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            }
            return false;
          });

          tabsInPane.forEach((t) => {
            if ('item' in t) {
              closeDetailTab(t.id);
            } else if ('type' in t) {
              closeBottomTab(t.id);
            } else {
              closeResourceListTab(t.id);
            }
          });
          break;
        }

        case 'closeAll': {
          const tabsInPane = allCenterTabs.filter((t) => {
            if (tabContextMenu.isResourceListTab) {
              return 'resource' in t && t.paneId === tab.paneId;
            } else if (tabContextMenu.isDetailTab) {
              return (
                'item' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            } else if (tabContextMenu.isBottomTab) {
              return (
                'type' in t &&
                t.location === 'center' &&
                t.paneId === tab.paneId
              );
            }
            return false;
          });

          tabsInPane.forEach((t) => {
            if ('item' in t) {
              closeDetailTab(t.id);
            } else if ('type' in t) {
              closeBottomTab(t.id);
            } else {
              closeResourceListTab(t.id);
            }
          });
          break;
        }
      }

      setTabContextMenu(null);
    },
    [
      tabContextMenu,
      allCenterTabs,
      closeDetailTab,
      closeResourceListTab,
      closeBottomTab,
    ],
  );

  return {
    tabContextMenu,
    setTabContextMenu,
    handleTabContextMenu,
    handleTabContextAction,
    handleSplitPane,
  };
};
