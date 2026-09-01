import { useCallback, useRef } from 'react';
import { useStore } from '../store';
import {
  handleActionSelect as handleResourceActionSelect,
  ActionHandlerParams,
} from '../utils/resourceActions';

interface UseResourceListActionsProps {
  selectedNode: any;
  listItems: any[];
  selectedResources: Set<string>;
  setSelectedResources: (resources: Set<string>) => void;
  getResourceKey: (item: any) => string;
  setRestartingItems: React.Dispatch<React.SetStateAction<Set<string>>>;
  setScaleDialog: React.Dispatch<React.SetStateAction<{ item: any; currentReplicas: number } | null>>;
  setShowDeleteConfirm: React.Dispatch<React.SetStateAction<boolean>>;
  setShowRemoveFinalizersConfirm: React.Dispatch<React.SetStateAction<boolean>>;
  setDrainDialog: React.Dispatch<React.SetStateAction<{ item: any } | null>>;
  setTaintDialog: React.Dispatch<React.SetStateAction<{ item: any } | null>>;
  setActionMenu: React.Dispatch<React.SetStateAction<{ item: any; x: number; y: number } | null>>;
  scaleDialog: { item: any; currentReplicas: number } | null;
  setIsScaling: React.Dispatch<React.SetStateAction<boolean>>;
  taintDialog: { item: any } | null;
  setIsTainting: React.Dispatch<React.SetStateAction<boolean>>;
  drainDialog: { item: any } | null;
  drainOptions: { ignoreDaemonsets: boolean; deleteEmptyDir: boolean; gracePeriod: number };
  setIsDraining: React.Dispatch<React.SetStateAction<boolean>>;
  setDrainResults: React.Dispatch<React.SetStateAction<any>>;
}

export function useResourceListActions({
  selectedNode,
  listItems,
  selectedResources,
  setSelectedResources,
  getResourceKey,
  setRestartingItems,
  setScaleDialog,
  setShowDeleteConfirm,
  setShowRemoveFinalizersConfirm,
  setDrainDialog,
  setTaintDialog,
  setActionMenu,
  scaleDialog,
  setIsScaling,
  taintDialog,
  setIsTainting,
  drainDialog,
  drainOptions,
  setIsDraining,
  setDrainResults,
}: UseResourceListActionsProps) {
  const {
    currentTab,
    deleteResources,
    removeFinalizers,
    forceRefreshResources,
    restartResource,
    triggerCronJob,
    bulkRestartResources,
    scaleResource,
    taintNode,
    drainNode,
    cordonNode,
    reloadListItems,
    loadDetails,
    openBottomTab,
    getCurrentTabState,
    addToast,
  } = useStore();

  const pendingOperationRef = useRef<'delete' | 'removeFinalizers' | null>(null);

  const handleDelete = useCallback(async () => {
    if (selectedResources.size === 0 || !currentTab || !selectedNode) return;
    setShowDeleteConfirm(true);
  }, [selectedResources.size, currentTab, selectedNode, setShowDeleteConfirm]);

  const handleRemoveFinalizers = useCallback(async () => {
    if (selectedResources.size === 0 || !currentTab || !selectedNode) return;
    setShowRemoveFinalizersConfirm(true);
  }, [selectedResources.size, currentTab, selectedNode, setShowRemoveFinalizersConfirm]);

  const handleForceRefresh = useCallback(async () => {
    if (selectedResources.size === 0 || !currentTab || !selectedNode) return;
    const itemsToRefresh = listItems.filter((item: any) => selectedResources.has(getResourceKey(item)));

    try {
      await forceRefreshResources(currentTab, selectedNode.data, itemsToRefresh);
      setSelectedResources(new Set());
      await reloadListItems();
    } catch (error) {
      console.error('Failed to force refresh resources:', error);
      setSelectedResources(new Set());
    }
  }, [selectedResources, currentTab, selectedNode, listItems, getResourceKey, forceRefreshResources, setSelectedResources, reloadListItems]);

  const confirmDelete = useCallback(() => {
    if (!currentTab || !selectedNode || pendingOperationRef.current) return;
    pendingOperationRef.current = 'delete';

    const itemsToDelete = listItems.filter((item: any) => selectedResources.has(getResourceKey(item)));
    const count = itemsToDelete.length;

    setSelectedResources(new Set());
    setShowDeleteConfirm(false);

    deleteResources(currentTab, selectedNode.data, itemsToDelete).then((result) => {
      pendingOperationRef.current = null;
      if (result.failed.length > 0) {
        addToast({ type: 'error', message: `Failed to delete ${result.failed.length} of ${count} resources` });
      } else {
        addToast({ type: 'success', message: `Deleted ${result.succeeded} resource${result.succeeded !== 1 ? 's' : ''}` });
      }
    }).catch(() => {
      pendingOperationRef.current = null;
      addToast({ type: 'error', message: `Failed to delete resources` });
    });
  }, [currentTab, selectedNode, listItems, selectedResources, getResourceKey, setSelectedResources, setShowDeleteConfirm, deleteResources, addToast]);

  const confirmRemoveFinalizers = useCallback(() => {
    if (!currentTab || !selectedNode || pendingOperationRef.current) return;
    pendingOperationRef.current = 'removeFinalizers';

    const itemsToProcess = listItems.filter((item: any) => selectedResources.has(getResourceKey(item)));
    const count = itemsToProcess.length;

    setSelectedResources(new Set());
    setShowRemoveFinalizersConfirm(false);

    removeFinalizers(currentTab, selectedNode.data, itemsToProcess).then((result) => {
      pendingOperationRef.current = null;
      if (result.failed.length > 0) {
        addToast({ type: 'error', message: `Failed to remove finalizers from ${result.failed.length} of ${count} resources` });
      } else {
        addToast({ type: 'success', message: `Removed finalizers from ${result.succeeded} resource${result.succeeded !== 1 ? 's' : ''}` });
      }
      reloadListItems();
    }).catch(() => {
      pendingOperationRef.current = null;
      addToast({ type: 'error', message: `Failed to remove finalizers` });
    });
  }, [currentTab, selectedNode, listItems, selectedResources, getResourceKey, setSelectedResources, setShowRemoveFinalizersConfirm, removeFinalizers, addToast, reloadListItems]);

  const handleBulkRestart = useCallback(async () => {
    if (selectedResources.size === 0 || !currentTab || !selectedNode) return;

    const itemsToRestart = listItems.filter((item: any) => selectedResources.has(getResourceKey(item)));

    setRestartingItems((prev) => {
      const newSet = new Set([...prev]);
      itemsToRestart.forEach((item: any) => newSet.add(getResourceKey(item)));
      return newSet;
    });

    try {
      await bulkRestartResources(currentTab, selectedNode.data, itemsToRestart);
      setSelectedResources(new Set());
    } catch (error) {
      console.error('Failed to restart resources:', error);
    } finally {
      setTimeout(() => {
        setRestartingItems((prev) => {
          const updated = new Set(prev);
          itemsToRestart.forEach((item: any) => updated.delete(getResourceKey(item)));
          return updated;
        });
      }, 2000);
    }
  }, [selectedResources, currentTab, selectedNode, listItems, getResourceKey, setRestartingItems, bulkRestartResources, setSelectedResources]);

  const confirmScale = useCallback(async (replicas: number) => {
    if (!currentTab || !selectedNode || !scaleDialog) return;
    setIsScaling(true);
    try {
      await scaleResource(currentTab, selectedNode.data, scaleDialog.item, replicas);
      await reloadListItems();
      setScaleDialog(null);
    } catch (error) {
      console.error('Failed to scale resource:', error);
    } finally {
      setIsScaling(false);
    }
  }, [currentTab, selectedNode, scaleDialog, scaleResource, reloadListItems, setScaleDialog, setIsScaling]);

  const confirmTaint = useCallback(async (key: string, value: string, effect: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute') => {
    if (!currentTab || !taintDialog) return;
    setIsTainting(true);
    try {
      await taintNode(currentTab, taintDialog.item.name, key, value, effect);
      await reloadListItems();
      setTaintDialog(null);
    } catch (error) {
      console.error('Failed to taint node:', error);
    } finally {
      setIsTainting(false);
    }
  }, [currentTab, taintDialog, taintNode, reloadListItems, setTaintDialog, setIsTainting]);

  const confirmDrain = useCallback(async () => {
    if (!currentTab || !drainDialog) return;
    setIsDraining(true);
    try {
      const result = await drainNode(currentTab, drainDialog.item.name, drainOptions);
      await reloadListItems();
      setDrainDialog(null);
      setDrainResults(result);
    } catch (error) {
      console.error('Failed to drain node:', error);
    } finally {
      setIsDraining(false);
    }
  }, [currentTab, drainDialog, drainOptions, drainNode, reloadListItems, setDrainDialog, setIsDraining, setDrainResults]);

  const handleBulkAction = useCallback((action: string) => {
    switch (action) {
      case 'delete':
        handleDelete();
        break;
      case 'removeFinalizers':
        handleRemoveFinalizers();
        break;
      case 'forceRefresh':
        handleForceRefresh();
        break;
      case 'restart':
        handleBulkRestart();
        break;
    }
  }, [handleDelete, handleRemoveFinalizers, handleForceRefresh, handleBulkRestart]);

  const handleActionClick = useCallback((e: React.MouseEvent, item: any) => {
    e.stopPropagation();
    if (e.type === 'contextmenu') {
      setActionMenu({ item, x: e.clientX, y: e.clientY });
    } else {
      const rect = (e.target as HTMLElement).getBoundingClientRect();
      setActionMenu({ item, x: rect.left, y: rect.bottom + 5 });
    }
  }, [setActionMenu]);

  const handleActionSelect = useCallback(async (action: string, item: any) => {
    setActionMenu(null);

    const params: ActionHandlerParams = {
      action,
      item,
      currentTab,
      selectedNode,
      loadDetails,
      openBottomTab,
      restartResource,
      triggerCronJob: async (tab: any, itm: any) => {
        await triggerCronJob(tab, itm);
      },
      cordonNode,
      reloadListItems,
      setRestartingItems,
      setScaleDialog,
      setSelectedResources,
      setShowDeleteConfirm,
      setDrainDialog,
      setTaintDialog,
      getCurrentTabState,
      getResourceKey,
    };

    await handleResourceActionSelect(params);
  }, [currentTab, selectedNode, loadDetails, openBottomTab, restartResource, triggerCronJob, cordonNode, reloadListItems, setRestartingItems, setScaleDialog, setSelectedResources, setShowDeleteConfirm, setDrainDialog, setTaintDialog, getCurrentTabState, getResourceKey, setActionMenu]);

  return {
    handleDelete,
    handleRemoveFinalizers,
    handleForceRefresh,
    handleBulkRestart,
    handleBulkAction,
    confirmDelete,
    confirmRemoveFinalizers,
    confirmScale,
    confirmTaint,
    confirmDrain,
    handleActionClick,
    handleActionSelect,
  };
}
