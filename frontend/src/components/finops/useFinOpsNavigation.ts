import { useCallback } from 'react';
import { useStore } from '../../store';
import api from '../../services/api';
import { workloadResource } from './finopsView';

const FINOPS_RESOURCE = {
  name: 'finops-dashboard',
  group: '',
  version: 'v1',
  kind: 'FinOpsDashboard',
  namespaced: false,
};

const NODE_RESOURCE = { name: 'nodes', group: '', version: 'v1', kind: 'Node', namespaced: false };

function toastError(message: string) {
  window.dispatchEvent(new CustomEvent('toast:error', { detail: { message } }));
}

/** Navigation from cost rows to the resources behind them, and between host and vcluster costs. */
export function useFinOpsNavigation(cluster: string) {
  const openDetail = useCallback((target: string, resource: any, name: string, namespace?: string) => {
    const { openDetailTab, loadDetails } = useStore.getState();
    const item = {
      name,
      namespace,
      kind: resource.kind,
      apiVersion: resource.group ? `${resource.group}/${resource.version}` : resource.version,
      uid: `${namespace || 'cluster'}-${name}`,
    };
    openDetailTab(resource, item, target, false);
    loadDetails(target, resource, item).catch(() => {});
  }, []);

  const openWorkload = useCallback((kind: string, namespace: string, name: string) => {
    const resource = workloadResource(kind);
    if (resource) openDetail(cluster, resource, name, namespace);
  }, [cluster, openDetail]);

  const openNode = useCallback((name: string, onCluster = cluster) => {
    openDetail(onCluster, NODE_RESOURCE, name);
  }, [cluster, openDetail]);

  const openCostsFor = useCallback(async (target: string) => {
    const { openTab, openResourceListTab } = useStore.getState();
    await openTab(target);
    await openResourceListTab(FINOPS_RESOURCE, target, true);
  }, []);

  /** Connects to a vcluster running on this cluster and opens its costs. */
  const openVClusterCosts = useCallback(async (namespace: string, name: string) => {
    try {
      const conn = await api.connectVCluster(cluster, namespace, name);
      await openCostsFor(conn.id);
    } catch (err: any) {
      toastError(err?.response?.data?.error || err?.message || `Failed to connect to vcluster ${name}`);
    }
  }, [cluster, openCostsFor]);

  const openHostCosts = useCallback(async (host: string) => {
    try {
      await openCostsFor(host);
    } catch (err: any) {
      toastError(err?.message || 'Failed to open host cluster');
    }
  }, [openCostsFor]);

  return { openWorkload, openNode, openVClusterCosts, openHostCosts };
}
