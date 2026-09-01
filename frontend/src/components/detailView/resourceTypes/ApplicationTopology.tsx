import { useEffect, useMemo, useState, useRef, useCallback } from 'react';
import api from '../../../services/api';
import { useStore } from '../../../store';
import useResourceNavigation from '../../../hooks/useResourceNavigation';
import { useVisibleInterval } from '../../../hooks/useVisibleInterval';
import { ArgoDestinationMap } from '../../../services/api/resources';
import './ApplicationTopology.css';

interface TopologyNode {
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
  uid?: string;
  phase?: string;
  health?: string;
  syncStatus?: string;
  ready?: string;
  message?: string;
  exists?: boolean;
  children?: TopologyNode[];
}

interface TopologySummary {
  destServer?: string;
  destName?: string;
  destNamespace?: string;
}

interface Props {
  cluster: string;
  namespace: string;
  name: string;
  destinations: ArgoDestinationMap | null;
}

const healthClass = (h?: string): string => {
  switch ((h || '').toLowerCase()) {
    case 'healthy':
      return 'topo-healthy';
    case 'degraded':
    case 'missing':
      return 'topo-degraded';
    case 'progressing':
      return 'topo-progressing';
    case 'suspended':
      return 'topo-suspended';
    default:
      return 'topo-unknown';
  }
};

const syncClass = (s?: string): string => {
  switch ((s || '').toLowerCase()) {
    case 'synced':
      return 'topo-sync-synced';
    case 'outofsync':
      return 'topo-sync-out';
    default:
      return 'topo-sync-unknown';
  }
};

const KIND_ABBREV: Record<string, string> = {
  Deployment: 'deploy',
  StatefulSet: 'sts',
  DaemonSet: 'ds',
  ReplicaSet: 'rs',
  Pod: 'pod',
  Service: 'svc',
  ConfigMap: 'cm',
  Secret: 'sec',
  Job: 'job',
  CronJob: 'cj',
  Endpoints: 'ep',
  Ingress: 'ing',
  ServiceAccount: 'sa',
  HorizontalPodAutoscaler: 'hpa',
  PodDisruptionBudget: 'pdb',
  VirtualService: 'vs',
};

const ApplicationTopology = ({ cluster, namespace, name, destinations }: Props) => {
  const [topology, setTopology] = useState<TopologyNode[] | null>(null);
  const [summary, setSummary] = useState<TopologySummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [navigatingTo, setNavigatingTo] = useState<string | null>(null);
  const [syncingNode, setSyncingNode] = useState<string | null>(null);
  const [menuOpen, setMenuOpen] = useState<string | null>(null);
  const { openTab } = useStore();
  const { navigateToResource } = useResourceNavigation(cluster);

  useEffect(() => {
    if (!menuOpen) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest('.topo-card-menu') && !target.closest('.topo-card-menu-btn')) {
        setMenuOpen(null);
      }
    };
    window.addEventListener('mousedown', onDown);
    return () => window.removeEventListener('mousedown', onDown);
  }, [menuOpen]);

  const activeKeyRef = useRef('');
  useEffect(() => {
    const key = `${cluster}|${namespace}|${name}`;
    activeKeyRef.current = key;
    setTopology(null);
    setSummary(null);
    setError(null);
    setLoading(true);
    setCollapsed(new Set());
    setNavigatingTo(null);
    setSyncingNode(null);
    setMenuOpen(null);
  }, [cluster, namespace, name]);

  const load = useCallback(
    async (silent = false) => {
      const key = `${cluster}|${namespace}|${name}`;
      if (!silent) setLoading(true);
      try {
        const res = await api.getArgoApplicationTopology(cluster, namespace, name);
        if (activeKeyRef.current !== key) return;
        setTopology(res.topology || []);
        setSummary(res.summary || null);
        setError(null);
      } catch (e: any) {
        if (activeKeyRef.current !== key) return;
        setError(e?.message || 'Failed to load topology');
      } finally {
        if (!silent && activeKeyRef.current === key) setLoading(false);
      }
    },
    [cluster, namespace, name],
  );

  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => { load(); }, [load]);
  useVisibleInterval(() => load(true), 15000, { targetRef: rootRef });

  const nodeId = (n: TopologyNode) =>
    `${n.kind}-${n.namespace || ''}-${n.name}`;

  const toggleCollapse = (id: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const resolveTargetCluster = useCallback(async (): Promise<string> => {
    if (!summary || !destinations) return cluster;
    if (summary.destName && destinations.byName[summary.destName]?.kind === 'vcluster') {
      const ref = destinations.byName[summary.destName].vcluster;
      if (ref) {
        const conn = await api.connectVCluster(cluster, ref.namespace, ref.name);
        return conn.id;
      }
    }
    if (summary.destServer && destinations.byServer[summary.destServer]?.kind === 'vcluster') {
      const ref = destinations.byServer[summary.destServer].vcluster;
      if (ref) {
        const conn = await api.connectVCluster(cluster, ref.namespace, ref.name);
        return conn.id;
      }
    }
    return cluster;
  }, [cluster, summary, destinations]);

  const syncResource = useCallback(
    async (n: TopologyNode, prune = false) => {
      const key = nodeId(n);
      setSyncingNode(key);
      setMenuOpen(null);
      try {
        await api.syncArgoApplication(cluster, namespace, name, {
          prune,
          resources: [{ group: n.group, kind: n.kind, namespace: n.namespace, name: n.name }],
        });
        window.dispatchEvent(new CustomEvent('toast:success', { detail: { message: `Sync triggered for ${n.kind}/${n.name}` } }));
        load(true);
      } catch (e: any) {
        const message = e?.response?.data?.error || e?.message || 'Sync failed';
        window.dispatchEvent(new CustomEvent('toast:error', { detail: { message } }));
      } finally {
        setSyncingNode(null);
      }
    },
    [cluster, namespace, name, load],
  );

  const navigateTo = useCallback(
    async (n: TopologyNode) => {
      const key = nodeId(n);
      setNavigatingTo(key);
      try {
        const targetCluster = await resolveTargetCluster();
        if (targetCluster !== cluster) {
          await openTab(targetCluster);
          await new Promise((r) => setTimeout(r, 200));
        }
        const apiVersion = n.group ? `${n.group}/${n.version}` : n.version;
        await navigateToResource({
          cluster: targetCluster,
          apiVersion,
          kind: n.kind,
          name: n.name,
          namespace: n.namespace,
        });
      } catch (e: any) {
        const message = e?.response?.data?.error || e?.message || 'Failed to navigate';
        window.dispatchEvent(new CustomEvent('toast:error', { detail: { message } }));
      } finally {
        setNavigatingTo(null);
      }
    },
    [resolveTargetCluster, cluster, openTab, navigateToResource],
  );

  const allNodes = useMemo(() => {
    let total = 0;
    const walk = (nodes: TopologyNode[]) => {
      for (const n of nodes) {
        total++;
        if (n.children) walk(n.children);
      }
    };
    if (topology) walk(topology);
    return total;
  }, [topology]);

  if (loading && !topology) {
    return <div className="topo-empty">Building topology…</div>;
  }
  if (error) {
    return (
      <div className="topo-empty topo-error">
        {error}
        <button className="topo-retry" onClick={() => load()}>Retry</button>
      </div>
    );
  }
  if (!topology || topology.length === 0) {
    return <div className="topo-empty">No managed resources reported.</div>;
  }

  const renderNode = (n: TopologyNode, depth: number): JSX.Element => {
    const id = nodeId(n);
    const hasChildren = !!(n.children && n.children.length > 0);
    const isCollapsed = collapsed.has(id);
    const abbrev = KIND_ABBREV[n.kind] || n.kind.slice(0, 3).toLowerCase();
    const phaseLabel = n.health || n.phase || '';
    const phaseClass = healthClass(phaseLabel);
    const syncClassName = syncClass(n.syncStatus);
    const isBusy = navigatingTo === id;

    return (
      <div key={id} className="topo-row">
        <div className={`topo-card ${phaseClass}`} title={n.message || ''}>
          <div className={`topo-card-ring ${phaseClass}`} />
          <div className="topo-card-body">
            <div className="topo-card-header">
              <span className="topo-card-kind">{abbrev}</span>
              {n.syncStatus && <span className={`topo-card-sync ${syncClassName}`}>{n.syncStatus}</span>}
            </div>
            <button
              type="button"
              className="topo-card-name"
              onClick={() => navigateTo(n)}
              disabled={isBusy}
              title={n.name}
            >
              {isBusy ? 'Connecting…' : n.name}
            </button>
            <div className="topo-card-meta">
              {n.namespace && <span className="topo-card-ns">{n.namespace}</span>}
              {n.ready && <span className="topo-card-ready">{n.ready}</span>}
              {!n.ready && phaseLabel && <span className="topo-card-phase">{phaseLabel}</span>}
            </div>
          </div>
          <div className="topo-card-actions">
            <button
              className="topo-card-menu-btn"
              onClick={(e) => {
                e.stopPropagation();
                setMenuOpen(menuOpen === id ? null : id);
              }}
              title="Resource actions"
              disabled={syncingNode === id}
            >
              {syncingNode === id ? <span className="topo-card-spinner" /> : '⋯'}
            </button>
            {menuOpen === id && (
              <div className="topo-card-menu">
                <button onClick={() => syncResource(n, false)}>Sync this resource</button>
                <button onClick={() => syncResource(n, true)}>Sync + Prune this resource</button>
                <button onClick={() => { setMenuOpen(null); navigateTo(n); }}>Open in cluster view</button>
              </div>
            )}
            {hasChildren && (
              <button
                className="topo-card-toggle"
                onClick={() => toggleCollapse(id)}
                title={isCollapsed ? 'Expand' : 'Collapse'}
              >
                {isCollapsed ? '▸' : '▾'}
              </button>
            )}
          </div>
        </div>

        {hasChildren && !isCollapsed && (
          <div className="topo-children">
            <div className="topo-connector" />
            <div className="topo-children-list">
              {n.children!.map((child) => renderNode(child, depth + 1))}
            </div>
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="topo-root" ref={rootRef}>
      <div className="topo-summary">
        <span className="topo-count">{allNodes} resources</span>
        <button className="topo-refresh" onClick={() => load(true)} title="Refresh">
          Refresh
        </button>
      </div>
      <div className="topo-graph">
        {topology.map((n) => renderNode(n, 0))}
      </div>
    </div>
  );
};

export default ApplicationTopology;
