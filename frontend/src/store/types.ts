import { FocusArea } from '../types';

export interface TreeNode {
  id: string;
  label: string;
  type: string;
  data?: any;
  children?: TreeNode[];
  expanded?: boolean;
  count?: number;
  icon?: string;
  hideCount?: boolean;
  disabled?: boolean;
}

export interface PinnedDetail {
  id: string;
  name: string;
  namespace?: string;
  kind: string;
  data: any;
  cluster: string;
}

export interface RolloutStatusData {
  status: string;
  message: string;
  replicas: number;
  updatedReplicas: number;
  readyReplicas: number;
  availableReplicas: number;
}

export interface DetailTab {
  id: string;
  title: string;
  resource: any;
  item: any;
  cluster: string;
  isPinned: boolean;
  location?: 'detail' | 'center';
  paneId?: string;
  isDeleted?: boolean;
}

export interface ResourceListTab {
  id: string;
  title: string;
  resource: any;
  items: any[];
  selectedItem: any;
  cluster: string;
  selectedNamespaces: string[];
  sortBy: string;
  sortOrder: 'asc' | 'desc';
  isPinned: boolean;
  paneId?: string;
}

export interface BottomTab {
  id: string;
  type: 'logs' | 'shell' | 'edit' | 'trace' | 'deployment-logs' | 'create';
  title: string;
  customTitle?: string;
  resource: any;
  cluster: string;
  selectedContainer?: string;
  location: 'bottom' | 'center';
  paneId?: string;
}

export interface TabState {
  treeData: TreeNode[];
  selectedNode: TreeNode | null;
  listItems: any[];
  selectedItem: any;
  detailData: any;
  searchQuery: string;
  searchMatches: string[];
  focusArea: FocusArea;
  isDetailsPanelCollapsed: boolean;
  expandedNodes: Set<string>;
  pinnedDetails: PinnedDetail[];
  namespaces: string[];
  selectedNamespace: string;
  selectedNamespaces?: string[];
  sortBy: string;
  sortOrder: 'asc' | 'desc';
  rolloutStatuses?: Map<string, RolloutStatusData>;
  rolloutRequests?: Map<string, any>;
  detailTabs: DetailTab[];
  activeDetailTab: string | null;
  resourceListTabs: ResourceListTab[];
  activeResourceListTab: string | null;
  activeResourceListTabByPane?: Record<string, string | null>;
  centerPaneLayout?: {
    id: string;
    type: 'resourceList' | 'split';
    direction?: 'horizontal' | 'vertical';
    children?: any[];
  };
  focusedCenterPaneId?: string | null;
  isLoadingListItems?: boolean;
  hasReceivedInitialListData?: boolean;
  loadError?: string;
  dashboardData?: any;
  helmReleases?: any[];
  helmReleasesLoading?: boolean;
  helmReleasesStreaming?: boolean;
  helmReleasesProgress?: { namespacesTotal: number; namespacesCompleted: number } | null;
  helmReleasesError?: string | null;
  helmReleasesLastFetch?: number;
  scrollPositions?: Record<string, number>;
}

export interface Tab {
  id: string;
  name: string;
  state: TabState;
}

export interface NavigationEntry {
  type: string;
  path: string;
  resource?: any;
  item?: any;
}

export interface MonitoringSettings {
  preferredProvider: 'auto' | 'prometheus' | 'mimir' | 'metrics-server' | 'custom' | 'disabled';
  autoRefreshInterval: number;
  showMetricsPanel: boolean;
  customPrometheusUrl?: string;
}

export interface ClusterError {
  cluster: string;
  errorCode: string;
  errorMessage: string;
  details?: string;
  recoverable: boolean;
  timestamp: number;
}

export interface VClusterStatus {
  cluster: string;
  state: 'connecting' | 'healthy' | 'reconnecting' | 'failed';
  detail?: string;
  generation?: number;
  updatedAt: number;
}

export interface DashboardOverviewMetric {
  title: string;
  value: number;
  trend?: number;
  color: string;
  iconKey: 'nodes' | 'pods' | 'services' | 'namespaces';
}

export interface DashboardOverviewData {
  metrics: DashboardOverviewMetric[];
  resourceUsage: { cpu: { used: number; total: number; percentage: number }; memory: { used: number; total: number; percentage: number } } | null;
  podStatus: { running: number; pending: number; failed: number; succeeded: number } | null;
  nodeStatus: { ready: number; notReady: number; schedulable: number } | null;
  events: Array<{ time: string; type: 'Normal' | 'Warning'; reason: string; message: string; object: string; namespace?: string }>;
  alerts: Array<{ severity: string; type: string; resource: string; namespace: string; reason: string; message: string; age: string }>;
  clusterInfo: { version: string; provider: string; architecture: string } | null;
  lastUpdated?: number;
}

export interface SSOSession {
  startUrl: string;
  region: string;
  expiresAt: number;
  label?: string;
}

export interface SSOSlice {
  ssoSessions: SSOSession[];
  ssoSessionsLoading: boolean;
  ssoSessionsLoaded: boolean;
  activeSsoSession: string | null;
  setSsoSessions: (sessions: SSOSession[]) => void;
  addSsoSession: (session: SSOSession) => void;
  removeSsoSession: (startUrl: string) => void;
  updateSsoSessionLabel: (startUrl: string, label: string) => void;
  setActiveSsoSession: (startUrl: string | null) => void;
  loadSsoSessions: () => Promise<void>;
  refreshSsoSession: (startUrl: string, region: string) => Promise<void>;
}

export interface StoreState extends
  ClusterSlice,
  TabSlice,
  ResourceSlice,
  RealtimeSlice,
  NavigationSlice,
  BottomTabSlice,
  DetailTabSlice,
  ResourceListTabSlice,
  HelmSlice,
  ToastSlice,
  SSOSlice,
  ConnectionSlice {
  monitoringSettings: MonitoringSettings;
  setMonitoringSettings: (settings: Partial<MonitoringSettings>) => void;
  getDefaultColumns: (resourceKind: string, isNamespaced?: boolean, printerColumns?: import('../utils/resourceListColumns').PrinterColumnCell[] | null) => string[];
  hydrateFromStorage: () => void;
}

export interface ClusterSlice {
  clusters: string[];
  clusterStatuses: Record<string, import('../types').ClusterStatus>;
  clusterAliases: Record<string, string>;
  clusterErrors: Record<string, ClusterError>;
  vclusterStatuses: Record<string, VClusterStatus>;
  clusterDashboards: Record<string, DashboardOverviewData>;
  loadClusters: () => Promise<void>;
  loadClusterAliases: () => Promise<void>;
  setClusterAlias: (cluster: string, alias: string) => Promise<void>;
  deleteClusterAlias: (cluster: string) => Promise<void>;
  loadClusterStatus: (cluster: string, force?: boolean) => Promise<void>;
  loadBatchClusterStatus: (clusters: string[], force?: boolean) => Promise<void>;
  refreshAllClusterStatuses: () => Promise<void>;
  setClusterError: (cluster: string, errorCode: string, errorMessage: string, recoverable: boolean, details?: string) => void;
  clearClusterError: (cluster: string) => void;
  setVClusterStatus: (cluster: string, state: 'connecting' | 'healthy' | 'reconnecting' | 'failed', detail?: string, generation?: number) => void;
  updateClusterDashboard: (cluster: string, partial: Partial<DashboardOverviewData>) => void;
  clearClusterDashboard: (cluster: string) => void;
}

export interface TabSlice {
  activeTabs: Tab[];
  tabIndexMap: Map<string, number>;
  currentTab: string | null;
  openTab: (cluster: string) => Promise<void>;
  closeTab: (clusterId: string) => void;
  setCurrentTab: (tabId: string | null) => void;
  reorderTabs: (fromIndex: number, toIndex: number) => void;
  getCurrentTabState: () => TabState | null;
  updateCurrentTabState: (updates: Partial<TabState>) => void;
}

export interface ResourceSlice {
  loadTreeData: (cluster: string, data?: TreeNode[]) => Promise<void>;
  expandNode: (cluster: string, nodeId: string, nodeType: string, metadata: any) => Promise<void>;
  selectNode: (node: TreeNode) => void;
  loadListItems: (cluster: string, resource: any) => Promise<boolean>;
  reloadListItems: () => Promise<void>;
  selectItem: (item: any) => void;
  loadDetails: (cluster: string, resource: any, item: any, signal?: AbortSignal) => Promise<any>;
  loadResourceEvents: (cluster: string, resource: any, details: any, signal?: AbortSignal) => Promise<void>;
  updateDetailData: (data: any) => void;
  setSearchQuery: (query: string) => void;
  performSearch: (query: string) => void;
  toggleNodeExpansion: (nodeId: string) => void;
  pinDetail: (detail: any) => void;
  unpinDetail: (detailId: string) => void;
  selectPinnedDetail: (detail: PinnedDetail) => void;
  deleteResources: (cluster: string, resource: any, items: any[]) => Promise<{ succeeded: number; failed: string[] }>;
  removeFinalizers: (cluster: string, resource: any, items: any[]) => Promise<{ succeeded: number; failed: string[] }>;
  forceRefreshResources: (cluster: string, resource: any, items: any[]) => Promise<void>;
  restartResource: (cluster: string, resource: any, item: any) => Promise<void>;
  triggerCronJob: (cluster: string, item: any) => Promise<{ jobName: string }>;
  bulkRestartResources: (cluster: string, resource: any, items: any[]) => Promise<void>;
  scaleResource: (cluster: string, resource: any, item: any, replicas: number) => Promise<void>;
  taintNode: (cluster: string, nodeName: string, key: string, value: string, effect: string) => Promise<void>;
  removeTaint: (cluster: string, nodeName: string, key: string) => Promise<void>;
  drainNode: (cluster: string, nodeName: string, options?: any) => Promise<void>;
  cordonNode: (cluster: string, nodeName: string, unschedulable: boolean) => Promise<void>;
  rolloutPollingInterval: NodeJS.Timeout | null;
  startRolloutPolling: () => void;
  stopRolloutPolling: () => void;
  addRolloutTracking: (key: string, group: string, version: string, kind: string, namespace: string, name: string) => void;
  removeRolloutTracking: (key: string) => void;
}

export interface RealtimeSlice {
  startRealtime: (forceRefresh?: boolean) => void;
  _startRealtimeInternal: (forceRefresh?: boolean) => void;
  stopRealtime: () => void;
}

export interface NavigationSlice {
  recordNavigation: (type: string, path: string, resource?: any, item?: any) => Promise<void>;
  navigateBack: () => Promise<void>;
  navigateForward: () => Promise<void>;
  restoreNavigationState: (entry: NavigationEntry) => Promise<void>;
  setFocusArea: (area: FocusArea) => void;
  toggleDetailsPanel: () => void;
  setDetailsPanelCollapsed: (collapsed: boolean) => void;
}

export interface BottomTabSlice {
  bottomTabs: BottomTab[];
  activeBottomTab: string | null;
  openBottomTab: (type: 'logs' | 'shell' | 'edit' | 'trace' | 'deployment-logs' | 'create', resource: any, cluster: string) => void;
  closeBottomTab: (tabId: string) => void;
  setActiveBottomTab: (tabId: string) => void;
  updateBottomTabContainer: (tabId: string, container: string) => void;
  renameBottomTab: (tabId: string, customTitle: string) => void;
  moveBottomTab: (tabId: string, location: 'bottom' | 'center', targetPaneId?: string) => void;
}

export interface DetailTabSlice {
  openDetailTab: (resource: any, item: any, cluster: string, isPinned?: boolean) => void;
  closeDetailTab: (tabId: string) => void;
  setActiveDetailTab: (tabId: string) => void;
  pinDetailTab: (tabId: string) => void;
  reorderDetailTabs: (fromIndex: number, toIndex: number) => void;
  moveDetailTab: (tabId: string, location: 'detail' | 'center', targetPaneId?: string) => void;
}

export interface ResourceListTabSlice {
  openResourceListTab: (resource: any, cluster: string, isPinned?: boolean, paneId?: string) => Promise<void>;
  closeResourceListTab: (tabId: string) => void;
  setActiveResourceListTab: (tabId: string) => void;
  setActiveResourceListTabForPane: (paneId: string, tabId: string | null) => void;
  updateResourceListTab: (tabId: string, updates: Partial<ResourceListTab>) => void;
  pinResourceListTab: (tabId: string) => void;
  reorderResourceListTabs: (fromIndex: number, toIndex: number) => void;
  moveResourceListTabToPane: (tabId: string, targetPaneId: string) => void;
}

export interface HelmSlice {
  getHelmReleases: (cluster: string) => any[];
  updateHelmReleases: (cluster: string, releases: any[]) => void;
  addHelmReleases: (cluster: string, newReleases: any[]) => void;
  removeHelmRelease: (cluster: string, namespace: string, name: string) => void;
  setHelmReleasesLoading: (cluster: string, loading: boolean) => void;
  setHelmReleasesStreaming: (cluster: string, streaming: boolean) => void;
  setHelmReleasesProgress: (cluster: string, progress: { namespacesTotal: number; namespacesCompleted: number } | null) => void;
  setHelmReleasesError: (cluster: string, error: string | null) => void;
  clearHelmReleases: (cluster: string) => void;
}

export interface Toast {
  id: string;
  type: 'success' | 'error' | 'info';
  message: string;
  duration?: number;
}

export interface ToastSlice {
  toasts: Toast[];
  addToast: (toast: Omit<Toast, 'id'>) => void;
  removeToast: (id: string) => void;
}

export type ConnectionState = 'connected' | 'connecting' | 'disconnected' | 'reconnecting';
export type ClusterConnectionState = 'connected' | 'connecting' | 'disconnected' | 'error';

export interface ClusterConnection {
  state: ClusterConnectionState;
  lastConnected?: number;
  lastError?: string;
  reconnectAttempts: number;
}

export interface ConnectionSlice {
  backendState: ConnectionState;
  websocketState: ConnectionState;
  clusterConnections: Record<string, ClusterConnection>;
  lastBackendPing?: number;
  reconnectCountdown?: number;
  setBackendState: (state: ConnectionState) => void;
  setWebsocketState: (state: ConnectionState) => void;
  setClusterConnection: (cluster: string, state: ClusterConnectionState, error?: string) => void;
  clearClusterConnection: (cluster: string) => void;
  setLastBackendPing: (timestamp: number) => void;
  setReconnectCountdown: (seconds?: number) => void;
  isFullyConnected: () => boolean;
  getClusterConnectionState: (cluster: string) => ClusterConnectionState;
  getOverallState: () => ConnectionState;
}
