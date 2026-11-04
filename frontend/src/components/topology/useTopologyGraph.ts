import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getNamespaceTopology, getTopologyNeighbors, type TopologyRef } from '../../services/api/topology';
import { failureMessage } from '../../utils/errorMessage';
import {
  emptyKnown,
  knownFrom,
  mergeGraph,
  nodeIdFor,
  visibleFromFocus,
  type KnownGraph,
  type TopoNode,
} from './graphModel';

export type TopologyMode =
  | { kind: 'focus'; ref: TopologyRef; contextNamespace?: string }
  | { kind: 'namespace'; namespace: string };

/** How often a visible graph refreshes its status. */
export const POLL_MS = 5000;

const refOfNode = (n: TopoNode): TopologyRef => ({ group: n.group, version: n.version, kind: n.kind, namespace: n.namespace, name: n.name });

export function useTopologyGraph(cluster: string, mode: TopologyMode | null, opts: { includeIdle?: boolean; poll?: boolean } = {}) {
  const [known, setKnown] = useState<KnownGraph>(emptyKnown);
  const [focusId, setFocusId] = useState<string | undefined>();
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loadingIds, setLoadingIds] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const expandedRef = useRef(expanded);
  expandedRef.current = expanded;
  const knownRef = useRef(known);
  knownRef.current = known;
  const generation = useRef(0);

  const modeKey = mode ? (mode.kind === 'focus' ? `f:${nodeIdFor(mode.ref.kind, mode.ref.namespace, mode.ref.name)}` : `n:${mode.namespace}`) : '';
  const contextNs = mode?.kind === 'focus' ? mode.contextNamespace || mode.ref.namespace : mode?.namespace;
  const includeIdle = !!opts.includeIdle;

  // (Re)load from scratch whenever what we look at changes.
  useEffect(() => {
    if (!cluster || !mode) return;
    const gen = ++generation.current;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    setKnown(emptyKnown());
    setExpanded(new Set());
    const req = mode.kind === 'focus'
      ? getTopologyNeighbors(cluster, mode.ref, { depth: 1, context: contextNs, includeIdle }, ctrl.signal)
      : getNamespaceTopology(cluster, mode.namespace, { includeIdle }, ctrl.signal);
    req
      .then((g) => {
        if (gen !== generation.current) return;
        setKnown(knownFrom(g));
        setFocusId(g.focus);
        setExpanded(g.focus ? new Set([g.focus]) : new Set());
        setUpdatedAt(Date.now());
      })
      .catch((e) => {
        if (gen !== generation.current || ctrl.signal.aborted) return;
        setError(failureMessage('Could not load the topology', e));
      })
      .finally(() => { if (gen === generation.current) setLoading(false); });
    return () => ctrl.abort();
  }, [cluster, modeKey, includeIdle]); // eslint-disable-line react-hooks/exhaustive-deps

  const expand = useCallback(async (id: string) => {
    const n = knownRef.current.nodes.get(id);
    if (!n || !cluster) return;
    const gen = generation.current;
    setLoadingIds((s) => new Set(s).add(id));
    try {
      const g = await getTopologyNeighbors(cluster, refOfNode(n), { depth: 1, context: n.namespace || contextNs, includeIdle });
      if (gen !== generation.current) return;
      setKnown((k) => mergeGraph(k, g, new Set([id])));
      setExpanded((s) => new Set(s).add(id));
    } catch (e) {
      if (gen === generation.current) setError(failureMessage(`Could not expand ${n.name}`, e));
    } finally {
      setLoadingIds((s) => { const next = new Set(s); next.delete(id); return next; });
    }
  }, [cluster, contextNs, includeIdle]);

  const collapse = useCallback((id: string) => {
    setExpanded((s) => { const next = new Set(s); next.delete(id); return next; });
  }, []);

  const expandAll = useCallback(async () => {
    // One more ring: expand every visible, not yet expanded node.
    const vis = focusId ? visibleFromFocus(knownRef.current, focusId, expandedRef.current) : new Set<string>();
    const todo = [...vis].filter((id) => !expandedRef.current.has(id) && !id.startsWith('Node/') && !id.startsWith('StorageClass/')).slice(0, 40);
    await Promise.all(todo.map((id) => expand(id)));
  }, [expand, focusId]);

  // Live updates: re-read what's on screen while the window is visible.
  const refresh = useCallback(async () => {
    if (!cluster || !mode) return;
    const gen = generation.current;
    try {
      if (mode.kind === 'namespace') {
        const g = await getNamespaceTopology(cluster, mode.namespace, { includeIdle });
        if (gen === generation.current) setKnown(knownFrom(g));
      } else {
        const ids = [...expandedRef.current];
        const results = await Promise.all(ids.map((id) => {
          const n = knownRef.current.nodes.get(id);
          return n ? getTopologyNeighbors(cluster, refOfNode(n), { depth: 1, context: n.namespace || contextNs, includeIdle }).catch(() => null) : null;
        }));
        if (gen !== generation.current) return;
        setKnown((k) => results.reduce((acc, g, i) => (g ? mergeGraph(acc, g, new Set([ids[i]])) : acc), k));
      }
      if (gen === generation.current) { setUpdatedAt(Date.now()); setError(null); }
    } catch (e) {
      if (gen === generation.current) setError(failureMessage('Could not refresh the topology', e));
    }
  }, [cluster, modeKey, contextNs, includeIdle]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!opts.poll || !mode) return;
    const t = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refresh();
    }, POLL_MS);
    return () => window.clearInterval(t);
  }, [opts.poll, refresh, modeKey]); // eslint-disable-line react-hooks/exhaustive-deps

  const visible = useMemo(() => {
    if (mode?.kind === 'namespace') return new Set(known.nodes.keys());
    return focusId ? visibleFromFocus(known, focusId, expanded) : new Set<string>();
  }, [known, focusId, expanded, mode?.kind]);

  return { known, visible, focusId, expanded, loadingIds, loading, error, updatedAt, expand, collapse, expandAll, refresh };
}
