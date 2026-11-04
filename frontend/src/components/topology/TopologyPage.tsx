// The Topology tab. Three views of how things connect: the cluster (each
// namespace's health), a namespace's applications, and one resource's
// expandable neighbourhood. Opened from the sidebar, the palette and the
// inspector's Topology action; a resource picker, a breadcrumb and
// back/forward move around inside it.
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useShallow } from 'zustand/react/shallow';
import api from '../../services/api';
import { useStore } from '../../store';
import { getResourceIcon } from '../../utils/resourceIcons';
import TopologyView from './TopologyView';
import ClusterOverview from './ClusterOverview';
import ResourcePicker, { type PickTarget } from './ResourcePicker';
import { useTopologyNav } from './topologyNav';
import type { TopologyMode } from './useTopologyGraph';
import type { TopoNode, ViewNode } from './graphModel';
import { back, breadcrumb, defaultLocation, forward, historyAt, locationKey, navigate, type TopoHistory, type TopoLocation } from './topoLocation';
import './Topology.css';

interface Props {
  cluster: string;
}

const NO_NODES: ViewNode[] = [];

const TopologyPage: React.FC<Props> = ({ cluster }) => {
  const request = useTopologyNav((s) => s.requests[cluster]);
  const selectedNamespace = useStore(useShallow((s) => s.activeTabs.find((t) => t.id === cluster)?.state.selectedNamespace));
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [history, setHistory] = useState<TopoHistory>(() =>
    historyAt(defaultLocation({ focus: request?.focus, namespace: request?.namespace, selectedNamespace })),
  );
  const [pickerSignal, setPickerSignal] = useState(0);
  const loc = history.present;
  const go = useCallback((to: TopoLocation) => setHistory((h) => navigate(h, to)), []);

  // A new request (inspector, palette) refocuses the open tab.
  useEffect(() => {
    if (!request) return;
    go(defaultLocation({ focus: request.focus, namespace: request.namespace, selectedNamespace }));
  }, [request?.nonce]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let live = true;
    api.getNamespaces(cluster).then((ns: string[]) => { if (live) setNamespaces(ns); }).catch(() => {});
    return () => { live = false; };
  }, [cluster]);

  // Back / forward: the mouse side buttons, ⌘[ / ⌘], and "/" for the picker.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const page = document.querySelector('.topo-page') as HTMLElement | null;
      if (!page || page.offsetParent === null) return; // another tab is showing
      const t = e.target as HTMLElement | null;
      const typing = !!t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable);
      if ((e.metaKey || e.ctrlKey) && e.key === '[') { e.preventDefault(); setHistory(back); }
      else if ((e.metaKey || e.ctrlKey) && e.key === ']') { e.preventDefault(); setHistory(forward); }
      else if (e.key === '/' && !typing && !e.metaKey && !e.ctrlKey) { e.preventDefault(); setPickerSignal((n) => n + 1); }
    };
    const onMouse = (e: MouseEvent) => {
      if (e.button === 3) { e.preventDefault(); setHistory(back); }
      if (e.button === 4) { e.preventDefault(); setHistory(forward); }
    };
    const root = document.querySelector('.topo-page');
    window.addEventListener('keydown', onKey);
    root?.addEventListener('mouseup', onMouse as EventListener);
    return () => { window.removeEventListener('keydown', onKey); root?.removeEventListener('mouseup', onMouse as EventListener); };
  }, []);

  const mode: TopologyMode | null = useMemo(() => {
    if (loc.view === 'focus') return { kind: 'focus', ref: loc.ref, contextNamespace: loc.namespace };
    if (loc.view === 'app') return { kind: 'namespace', namespace: loc.namespace };
    return null;
  }, [loc]);

  const refocus = useCallback((n: TopoNode) => {
    go({ view: 'focus', ref: { group: n.group, version: n.version, kind: n.kind, namespace: n.namespace, name: n.name }, namespace: n.namespace || (loc.view !== 'cluster' ? loc.namespace : 'default') });
  }, [go, loc]);

  const lastNamespace = loc.view === 'cluster'
    ? (selectedNamespace && selectedNamespace !== 'all' ? selectedNamespace : namespaces[0])
    : loc.namespace;

  const onPick = useCallback((t: PickTarget, selectNode?: (id: string) => void) => {
    if (t.type === 'node') selectNode?.(t.id);
    else if (t.type === 'namespace') go({ view: 'app', namespace: t.namespace });
    else go({ view: 'focus', ref: t.ref, namespace: t.ref.namespace || 'default' });
  }, [go]);

  const crumbs = breadcrumb(cluster, loc);

  const nav = (
    <div className="topo-nav">
      <div className="topo-nav-arrows">
        <button type="button" className="ap-icon-btn ap-icon-btn--sm" aria-label="Back" title="Back (⌘[)" disabled={!history.past.length} onClick={() => setHistory(back)}>
          <svg viewBox="0 0 16 16" aria-hidden><path d="M10 3.5 5.5 8 10 12.5" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg>
        </button>
        <button type="button" className="ap-icon-btn ap-icon-btn--sm" aria-label="Forward" title="Forward (⌘])" disabled={!history.future.length} onClick={() => setHistory(forward)}>
          <svg viewBox="0 0 16 16" aria-hidden><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg>
        </button>
      </div>
      <nav className="topo-crumbs" aria-label="Topology location">
        {crumbs.map((c, i) => (
          <React.Fragment key={`${c.kind}-${c.label}`}>
            {i > 0 && <span className="topo-crumb-sep" aria-hidden>›</span>}
            {c.to ? (
              <button type="button" className="topo-crumb" onClick={() => go(c.to!)} title={c.kind === 'Cluster' ? 'Cluster overview' : `Applications in ${c.label}`}>
                {c.label}
              </button>
            ) : (
              <span className="topo-crumb topo-crumb--current" aria-current="page">
                {c.kind && c.kind !== 'Cluster' && c.kind !== 'Namespace' && <span className="topo-crumb-icon" aria-hidden>{getResourceIcon(c.kind)}</span>}
                {c.kind && c.kind !== 'Cluster' && c.kind !== 'Namespace' && <span className="topo-crumb-kind">{c.kind}</span>}
                <span className="topo-crumb-label">{c.label}</span>
              </span>
            )}
          </React.Fragment>
        ))}
      </nav>
    </div>
  );

  const segmented = (
    <div className="ap-segmented ap-segmented--sm topo-scope" role="group" aria-label="View">
      <button type="button" aria-pressed={loc.view === 'cluster'} onClick={() => go({ view: 'cluster' })}>Cluster</button>
      <button
        type="button"
        aria-pressed={loc.view === 'app'}
        disabled={!lastNamespace}
        onClick={() => lastNamespace && go({ view: 'app', namespace: lastNamespace })}
        title={lastNamespace ? `Applications in ${lastNamespace}` : undefined}
      >
        Applications
      </button>
      <button
        type="button"
        aria-pressed={loc.view === 'focus'}
        onClick={() => {
          if (loc.view === 'focus') return;
          const prev = [...history.past].reverse().find((l) => l.view === 'focus');
          if (prev) go(prev);
          else setPickerSignal((n) => n + 1);
        }}
        title="Focus on one resource"
      >
        Resource
      </button>
    </div>
  );

  return (
    <div className="topo-page" key={cluster}>
      {nav}
      {mode ? (
        <TopologyView
          key={locationKey(loc)}
          cluster={cluster}
          mode={mode}
          onRefocus={refocus}
          leading={segmented}
          picker={(onScreen, selectNode) => (
            <ResourcePicker cluster={cluster} namespaces={namespaces} onScreen={onScreen} onPick={(t) => onPick(t, selectNode)} focusSignal={pickerSignal} />
          )}
        />
      ) : (
        <div className="topo-view">
          <div className="topo-toolbar">
            <div className="topo-toolbar-start">
              {segmented}
              <ResourcePicker cluster={cluster} namespaces={namespaces} onScreen={NO_NODES} onPick={(t) => onPick(t)} focusSignal={pickerSignal} />
            </div>
          </div>
          <div className="topo-body">
            <ClusterOverview cluster={cluster} onOpen={(ns) => go({ view: 'app', namespace: ns })} />
          </div>
        </div>
      )}
    </div>
  );
};

export default TopologyPage;
