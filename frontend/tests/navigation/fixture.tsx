import React from 'react';
import { createRoot } from 'react-dom/client';
import { Theme } from '@radix-ui/themes';
import '@radix-ui/themes/styles.css';
import '../../src/index.css';
import TreeSidebar from '../../src/components/TreeSidebar';
import ResourceList from '../../src/components/ResourceList';
import DetailView from '../../src/components/DetailView';
import { useStore } from '../../src/store';
import { createInitialTabState } from '../../src/store/utils';
import api from '../../src/services/api';

const cluster = 'navigation-test';
const resource = {
  name: 'pods',
  kind: 'Pod',
  group: '',
  version: 'v1',
  namespaced: true,
};
const items = ['pod-a', 'pod-b'].map((name) => ({
  name,
  namespace: 'default',
  kind: 'Pod',
  apiVersion: 'v1',
  status: 'Running',
}));
const node = {
  id: 'workloads-core-v1-pods',
  label: 'Pods',
  type: 'resource' as const,
  data: resource,
};
const otherNode = {
  id: 'workloads-core-v1-services',
  label: 'Services',
  type: 'resource' as const,
  data: { ...resource, name: 'services', kind: 'Service' },
};
let finishDetails: Array<() => void> = [];
let listLoads = 0;

// Keep real rendering, shortcuts and store transitions; isolate cluster I/O.
api.subscribeToCounts = () => () => {};
api.getResourceDetails = async (
  _cluster,
  _group,
  _version,
  _kind,
  namespace,
  name,
) => {
  await new Promise<void>((resolve) => finishDetails.push(resolve));
  return {
    kind: 'Pod',
    apiVersion: 'v1',
    metadata: { name, namespace },
    spec: { containers: [] },
    status: { phase: 'Running' },
  };
};
api.getNamespaces = async () => ['default'];
useStore.setState({
  loadTreeData: async () => {},
  startRealtime: () => {},
  loadResourceEvents: async () => {},
  loadListItems: async () => {
    listLoads++;
    return true;
  },
  recordNavigation: async () => {},
  backendState: 'connected',
  websocketState: 'connected',
});

const root = createRoot(document.getElementById('root')!);
let generation = 0;
function reset({ empty = false, split = false, descending = false } = {}) {
  finishDetails = [];
  listLoads = 0;
  const rows = empty ? [] : items;
  const listTab = {
    id: 'pods-list',
    title: 'Pods',
    resource,
    items: rows,
    selectedItem: rows[0] || null,
    cluster,
    selectedNamespaces: [],
    sortBy: 'name',
    sortOrder: descending ? ('desc' as const) : ('asc' as const),
    isPinned: true,
    paneId: 'root',
  };
  useStore.setState({
    currentTab: cluster,
    tabIndexMap: new Map([[cluster, 0]]),
    activeTabs: [
      {
        id: cluster,
        name: cluster,
        state: {
          ...createInitialTabState(),
          treeData: [node, otherNode],
          selectedNode: node,
          listItems: rows,
          selectedItem: rows[0] || null,
          hasReceivedInitialListData: true,
          resourceListTabs: split
            ? [
                listTab,
                {
                  ...listTab,
                  id: 'other-list',
                  paneId: 'other',
                  selectedItem: items[1],
                },
              ]
            : [listTab],
          activeResourceListTab: listTab.id,
          activeResourceListTabByPane: {
            root: listTab.id,
            ...(split ? { other: 'other-list' } : {}),
          },
          isDetailsPanelCollapsed: true,
        },
      },
    ],
  });
  root.render(
    <Theme appearance="dark">
      <div key={generation++} style={{ display: 'flex', height: '100vh' }}>
        <TreeSidebar />
        <div style={{ flex: 1, minWidth: 0 }}>
          <ResourceList paneId="root" />
        </div>
        {split && (
          <div style={{ flex: 1, minWidth: 0 }}>
            <ResourceList paneId="other" />
          </div>
        )}
        <DetailView />
      </div>
    </Theme>,
  );
}

(window as any).navigationTest = {
  reset,
  finishDetails: () => {
    finishDetails.splice(0).forEach((resolve) => resolve());
  },
  collapseDetails: () => useStore.getState().setDetailsPanelCollapsed(true),
  snapshot: () => {
    const state = useStore.getState().getCurrentTabState()!;
    const detail = state.detailTabs.find(
      (tab) => tab.id === state.activeDetailTab,
    );
    return {
      focus: state.focusArea,
      selected: state.resourceListTabs.find(
        (tab) => tab.id === state.activeResourceListTab,
      )?.selectedItem?.name,
      detail: detail?.item?.metadata?.name || detail?.item?.name,
      collapsed: state.isDetailsPanelCollapsed,
      listLoads,
      selectedNode: state.selectedNode?.data?.name,
      treeFocused: !!document.querySelector('.tree-sidebar.focused'),
      listsFocused: document.querySelectorAll('.resource-list.focused').length,
      detailFocused: !!document.querySelector(
        '.detail-view.focused:not(.collapsed)',
      ),
    };
  },
};
reset();
