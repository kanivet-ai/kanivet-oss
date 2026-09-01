export interface CacheEntry {
  data: any;
  timestamp: number;
}

export interface NavigationEntry {
  type: string;
  path: string;
  resource?: any;
  item?: any;
}

export interface ClusterGroup {
  id: number;
  name: string;
  description: string;
}

export interface ClusterInfo {
  name: string;
  kubeconfig: string;
}

export interface PortForward {
  id: string;
  cluster: string;
  namespace: string;
  podName: string;
  localPort: number;
  remotePort: number;
  active: boolean;
  createdAt: string;
}

let backendPort = (() => {
  if (typeof window === 'undefined') return 53727;
  const port = Number(new URLSearchParams(window.location.search).get('backendPort') || (window as any).__KANIVET_BACKEND_PORT || 53727);
  return Number.isFinite(port) && port > 0 ? port : 53727;
})();

export const setBackendPort = (port: number) => {
  if (Number.isFinite(port) && port > 0) {
    backendPort = port;
    if (typeof window !== 'undefined') (window as any).__KANIVET_BACKEND_PORT = port;
  }
};

export const getBackendPort = () => backendPort;
export const getBackendOrigin = () => `http://127.0.0.1:${getBackendPort()}`;
export const getApiBase = () => `${getBackendOrigin()}/api/v1`;
export const getWsBase = () => `ws://127.0.0.1:${getBackendPort()}/api/v1/ws`;
