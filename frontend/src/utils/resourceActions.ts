interface ResourceActionConfig {
  hasActions: boolean;
  actions: string[];
}

export interface ActionHandlerParams {
  action: string;
  item: any;
  currentTab: any;
  selectedNode: any;
  loadDetails: (
    tab: any,
    node: any,
    item: any,
    signal?: AbortSignal,
  ) => Promise<any>;
  openBottomTab: (
    type: 'logs' | 'shell' | 'edit' | 'trace' | 'deployment-logs',
    data: any,
    tab: any,
  ) => void;
  restartResource: (tab: any, node: any, item: any) => Promise<void>;
  triggerCronJob: (tab: any, item: any) => Promise<void>;
  cordonNode: (tab: any, name: string, cordon: boolean) => Promise<void>;
  reloadListItems: () => Promise<void>;
  setRestartingItems: (fn: (prev: Set<string>) => Set<string>) => void;
  setScaleDialog: (dialog: { item: any; currentReplicas: number }) => void;
  setSelectedResources: (resources: Set<string>) => void;
  setShowDeleteConfirm: (show: boolean) => void;
  setDrainDialog: (dialog: { item: any }) => void;
  setTaintDialog: (dialog: { item: any }) => void;
  getCurrentTabState: () => any;
  getResourceKey: (item: any) => string;
}

export const workloadControllerKinds = ['deployment', 'statefulset', 'daemonset', 'replicaset', 'job'];

const resourceActionConfigs: Record<string, ResourceActionConfig> = {
  deployment: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Scale', 'Edit', 'Delete'],
  },
  deployments: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Scale', 'Edit', 'Delete'],
  },
  statefulset: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Scale', 'Edit', 'Delete'],
  },
  statefulsets: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Scale', 'Edit', 'Delete'],
  },
  daemonset: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Edit', 'Delete'],
  },
  daemonsets: {
    hasActions: true,
    actions: ['Logs', 'Restart', 'Edit', 'Delete'],
  },
  replicaset: {
    hasActions: true,
    actions: ['Logs', 'Scale', 'Edit', 'Delete'],
  },
  replicasets: {
    hasActions: true,
    actions: ['Logs', 'Scale', 'Edit', 'Delete'],
  },
  pod: {
    hasActions: true,
    actions: ['Logs', 'Shell', 'Edit', 'Delete'],
  },
  pods: {
    hasActions: true,
    actions: ['Logs', 'Shell', 'Edit', 'Delete'],
  },
  node: {
    hasActions: true,
    actions: ['Drain', 'Cordon', 'Uncordon', 'Taint', 'Edit'],
  },
  nodes: {
    hasActions: true,
    actions: ['Drain', 'Cordon', 'Uncordon', 'Taint', 'Edit'],
  },
  job: {
    hasActions: true,
    actions: ['Logs', 'Edit', 'Delete'],
  },
  jobs: {
    hasActions: true,
    actions: ['Logs', 'Edit', 'Delete'],
  },
  cronjob: {
    hasActions: true,
    actions: ['Trigger', 'Edit', 'Delete'],
  },
  cronjobs: {
    hasActions: true,
    actions: ['Trigger', 'Edit', 'Delete'],
  },
  service: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  services: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  configmap: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  configmaps: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  secret: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  secrets: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
  default: {
    hasActions: true,
    actions: ['Edit', 'Delete'],
  },
};

export const hasActions = (item: any, selectedNode?: any): boolean => {
  const kind = (item.kind || selectedNode?.data?.kind || '')?.toLowerCase();
  if (!kind) return false;

  const config = resourceActionConfigs[kind] || resourceActionConfigs.default;
  return config.hasActions;
};

export const getAvailableActions = (
  item: any,
  selectedNode?: any,
): string[] => {
  const kind = (item.kind || selectedNode?.data?.kind || '')?.toLowerCase();
  if (!kind) return [];

  const config = resourceActionConfigs[kind] || resourceActionConfigs.default;
  return config.actions;
};

export const handleActionSelect = async (
  params: ActionHandlerParams,
): Promise<void> => {
  const {
    action,
    item,
    currentTab,
    selectedNode,
    loadDetails,
    openBottomTab,
    restartResource,
    triggerCronJob,
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
  } = params;

  switch (action.toLowerCase()) {
    case 'restart':
      if (currentTab && selectedNode) {
        const itemKey = getResourceKey(item);
        setRestartingItems((prev) => new Set([...prev, itemKey]));
        try {
          await restartResource(currentTab, selectedNode.data, item);
        } catch (error: any) {
          console.error('Failed to restart resource:', error);
        } finally {
          setTimeout(() => {
            setRestartingItems((prev) => {
              const newSet = new Set(prev);
              newSet.delete(itemKey);
              return newSet;
            });
          }, 2000);
        }
      }
      break;
    case 'trigger':
      if (currentTab) {
        const itemKey = getResourceKey(item);
        setRestartingItems((prev) => new Set([...prev, itemKey]));
        try {
          await triggerCronJob(currentTab, item);
          await reloadListItems();
        } catch (error: any) {
          console.error('Failed to trigger cronjob:', error);
        } finally {
          setTimeout(() => {
            setRestartingItems((prev) => {
              const newSet = new Set(prev);
              newSet.delete(itemKey);
              return newSet;
            });
          }, 2000);
        }
      }
      break;
    case 'scale':
      const currentReplicas = item.replicas || item.spec?.replicas || 1;
      setScaleDialog({ item, currentReplicas });
      break;
    case 'delete':
      setSelectedResources(new Set([getResourceKey(item)]));
      setShowDeleteConfirm(true);
      break;
    case 'logs':
      if (selectedNode && currentTab) {
        const rawKind = item.kind || selectedNode?.data?.kind || '';
        const kind = rawKind.toLowerCase().replace(/s$/, '');
        const tabType = workloadControllerKinds.includes(kind)
          ? 'deployment-logs'
          : 'logs';
        openBottomTab(
          tabType,
          {
            kind: rawKind,
            metadata: {
              name: item.metadata?.name || item.name,
              namespace: item.metadata?.namespace || item.namespace,
            },
            spec: item.spec,
          },
          currentTab,
        );
        loadDetails(currentTab, selectedNode.data, item).catch(() => {});
      }
      break;
    case 'exec':
    case 'shell':
    case 'edit':
      if (selectedNode && currentTab) {
        const details = (await loadDetails(currentTab, selectedNode.data, item)) || getCurrentTabState()?.detailData;
        if (details) openBottomTab(action.toLowerCase() === 'edit' ? 'edit' : 'shell', details, currentTab);
      }
      break;
    case 'drain':
      setDrainDialog({ item });
      break;
    case 'cordon':
      if (currentTab) {
        try {
          await cordonNode(currentTab, item.name, true);
          await reloadListItems();
        } catch (error) {
          console.error('Failed to cordon node:', error);
        }
      }
      break;
    case 'uncordon':
      if (currentTab) {
        try {
          await cordonNode(currentTab, item.name, false);
          await reloadListItems();
        } catch (error) {
          console.error('Failed to uncordon node:', error);
        }
      }
      break;
    case 'taint':
      setTaintDialog({ item });
      break;
  }
};
