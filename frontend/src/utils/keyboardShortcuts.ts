import { FocusArea } from '../types';

export const createTabSwitchHandlers = (
  activeTabs: any[],
  setCurrentTab: (id: string) => void,
) => {
  const handlers: Record<string, () => void> = {};
  for (let i = 1; i <= 9; i++) {
    handlers[`meta+${i}`] = () => {
      if (activeTabs.length >= i) {
        setCurrentTab(activeTabs[i - 1].id);
      }
    };
  }
  return handlers;
};

export const createNavigationHandlers = (
  focusArea: string,
  items: any[],
  selectedItem: any,
  handleItemSelect: (item: any, fromKeyboard?: boolean) => void,
) => {
  const navigateList = (direction: 'up' | 'down', lines: number = 1) => {
    if (!items.length) return;
    const currentIndex = items.findIndex((item) => item === selectedItem);
    let newIndex;
    if (direction === 'up') {
      newIndex = Math.max(0, currentIndex - lines);
    } else {
      newIndex = Math.min(items.length - 1, currentIndex + lines);
    }
    handleItemSelect(items[newIndex], true);
  };

  return {
    j: () => focusArea === 'list' && navigateList('down'),
    k: () => focusArea === 'list' && navigateList('up'),
    down: () => focusArea === 'list' && navigateList('down'),
    up: () => focusArea === 'list' && navigateList('up'),
    'ctrl+d': () => focusArea === 'list' && navigateList('down', 10),
    'ctrl+u': () => focusArea === 'list' && navigateList('up', 10),
  };
};

export const createTreeNavigationHandlers = (
  focusArea: string,
  getAllNodes: () => any[],
  focusedNodeId: string | null,
  setFocusedNodeId: (id: string) => void,
) => {
  const navigate = (direction: 'up' | 'down', lines: number = 1) => {
    if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
      return;
    const allNodes = getAllNodes();
    if (allNodes.length === 0) return;

    const currentIndex = focusedNodeId
      ? allNodes.findIndex((n) => n.id === focusedNodeId)
      : -1;
    let newIndex;

    if (direction === 'up') {
      newIndex =
        currentIndex > 0
          ? Math.max(0, currentIndex - lines)
          : allNodes.length - 1;
    } else {
      newIndex =
        currentIndex < allNodes.length - 1
          ? Math.min(allNodes.length - 1, currentIndex + lines)
          : 0;
    }

    setFocusedNodeId(allNodes[newIndex].id);
  };

  return {
    j: (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('down');
      }
    },
    k: (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('up');
      }
    },
    down: (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('down');
      }
    },
    up: (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('up');
      }
    },
    'ctrl+d': (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('down', 10);
      }
    },
    'ctrl+u': (e: any) => {
      if (
        focusArea === 'tree' &&
        document.activeElement?.id !== 'tree-search'
      ) {
        e.preventDefault();
        navigate('up', 10);
      }
    },
  };
};

export const createFocusNavigationHandlers = (
  focusArea: FocusArea,
  setFocusArea: (area: FocusArea) => void,
  hasListItems: boolean,
  detailData: any,
  isDetailsPanelCollapsed: boolean,
  hasDetailTabs: boolean = false,
) => {
  const hasDetail = detailData || hasDetailTabs;

  const getNextArea = (): FocusArea => {
    if (focusArea === 'tree')
      return hasListItems ? 'list' : hasDetail ? 'detail' : 'tree';
    if (focusArea === 'list') return hasDetail ? 'detail' : 'tree';
    if (focusArea === 'detail')
      return !isDetailsPanelCollapsed ? 'tree' : 'detail';
    return 'tree';
  };

  const getPrevArea = (): FocusArea => {
    if (focusArea === 'tree')
      return hasDetail && !isDetailsPanelCollapsed
        ? 'detail'
        : hasListItems
          ? 'list'
          : 'tree';
    if (focusArea === 'list') return 'tree';
    if (focusArea === 'detail') return hasListItems ? 'list' : 'tree';
    return 'tree';
  };

  return {
    Tab: (e: any) => {
      e.preventDefault();
      setFocusArea(getNextArea());
    },
    'Shift+Tab': (e: any) => {
      e.preventDefault();
      setFocusArea(getPrevArea());
    },
  };
};
