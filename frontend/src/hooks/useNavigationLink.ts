import { useCallback } from 'react';
import { useStore } from '../store';

export interface NavigationTarget {
  resource: {
    name: string;
    group: string;
    version: string;
    kind: string;
    namespaced: boolean;
  };
  targetName?: string;
  targetNamespace?: string;
  nodeId?: string;
}

export const useNavigationLink = (target: NavigationTarget) => {
  return useCallback(async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const state = useStore.getState();
    if (!state.currentTab) return;
    const item = target.targetName ? { name: target.targetName, namespace: target.targetNamespace } : null;
    const entry = { type: 'resource', path: target.nodeId || '', resource: target.resource, item };
    try {
      await state.restoreNavigationState(entry);
      await state.recordNavigation(entry.type, entry.path, entry.resource, item);
    } catch (error) {
      console.error('Failed to navigate:', error);
    }
  }, [target]);
};
