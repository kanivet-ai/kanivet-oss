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
  const {
    currentTab,
    selectNode,
    loadListItems,
    recordNavigation,
    setFocusArea,
    loadTreeData,
    selectItem,
    loadDetails,
    updateCurrentTabState,
  } = useStore();

  return useCallback(
    async (e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();

      if (!currentTab) return;

      try {
        await loadTreeData(currentTab);

        if (target.targetNamespace && target.resource.namespaced) {
          updateCurrentTabState({ selectedNamespace: target.targetNamespace });
          await new Promise((resolve) => setTimeout(resolve, 100));
        }

        const nodeId =
          target.nodeId ||
          `${currentTab}/${target.resource.group}/${target.resource.version}/${target.resource.name}`;

        selectNode({
          id: nodeId,
          label: target.resource.name,
          type: 'resource',
          data: target.resource,
        });

        const hasItems = await loadListItems(currentTab, target.resource);

        await recordNavigation(
          'resource',
          `${target.resource.group}/${target.resource.version}/${target.resource.name}`,
          target.resource,
        );

        setFocusArea('list');

        if (hasItems && target.targetName) {
          await new Promise((resolve) => setTimeout(resolve, 200));

          const { getCurrentTabState } = useStore.getState();
          const tabState = getCurrentTabState();

          if (tabState?.listItems) {
            const targetItem = tabState.listItems.find((item: any) => {
              if (target.targetNamespace) {
                return (
                  item.name === target.targetName &&
                  item.namespace === target.targetNamespace
                );
              }
              return item.name === target.targetName;
            });

            if (targetItem) {
              selectItem(targetItem);
              await loadDetails(currentTab, target.resource, targetItem);
              console.log(
                `Successfully navigated to ${target.resource.kind}/${target.targetName}`,
              );
            } else {
              console.log(`Could not find ${target.targetName} in the list`);
            }
          }
        }
      } catch (error) {
        console.error('Failed to navigate:', error);
      }
    },
    [
      currentTab,
      target,
      selectNode,
      loadListItems,
      recordNavigation,
      setFocusArea,
      loadTreeData,
      selectItem,
      loadDetails,
      updateCurrentTabState,
    ],
  );
};
