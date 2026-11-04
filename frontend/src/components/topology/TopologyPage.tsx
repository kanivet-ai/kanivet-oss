// The Topology tab: a focused, expandable graph around one resource, or the
// whole of a namespace. Opened from the sidebar, the palette and the
// inspector's Topology action.
import React, { useEffect, useMemo, useState } from 'react';
import { useShallow } from 'zustand/react/shallow';
import api from '../../services/api';
import { useStore } from '../../store';
import { getResourceIcon } from '../../utils/resourceIcons';
import type { TopologyRef } from '../../services/api/topology';
import TopologyView from './TopologyView';
import { useTopologyNav } from './topologyNav';
import type { TopologyMode } from './useTopologyGraph';
import './Topology.css';

interface Props {
  cluster: string;
}

const TopologyPage: React.FC<Props> = ({ cluster }) => {
  const request = useTopologyNav((s) => s.requests[cluster]);
  const selectedNamespace = useStore(useShallow((s) => s.activeTabs.find((t) => t.id === cluster)?.state.selectedNamespace));
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [focus, setFocus] = useState<TopologyRef | undefined>(request?.focus);
  const [namespace, setNamespace] = useState<string>(
    request?.namespace || request?.focus?.namespace || (selectedNamespace && selectedNamespace !== 'all' ? selectedNamespace : 'default'),
  );
  const [scope, setScope] = useState<'focus' | 'namespace'>(request?.focus ? 'focus' : 'namespace');

  // A new request (inspector, palette) refocuses the open tab.
  useEffect(() => {
    if (!request) return;
    setFocus(request.focus);
    if (request.namespace) setNamespace(request.namespace);
    setScope(request.focus ? 'focus' : 'namespace');
  }, [request?.nonce]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let live = true;
    api.getNamespaces(cluster).then((ns: string[]) => { if (live) setNamespaces(ns); }).catch(() => {});
    return () => { live = false; };
  }, [cluster]);

  const mode: TopologyMode | null = useMemo(() => {
    if (scope === 'focus' && focus) return { kind: 'focus', ref: focus, contextNamespace: namespace };
    if (namespace) return { kind: 'namespace', namespace };
    return null;
  }, [scope, focus, namespace]);

  const nsOptions = namespaces.includes(namespace) || !namespace ? namespaces : [namespace, ...namespaces];

  const leading = (
    <>
      <h2 className="topo-page-title">Topology</h2>
      <div className="ap-segmented topo-scope" role="group" aria-label="Scope">
        <button type="button" aria-pressed={scope === 'focus'} disabled={!focus} onClick={() => setScope('focus')}>
          Resource
        </button>
        <button type="button" aria-pressed={scope === 'namespace'} onClick={() => setScope('namespace')}>
          Namespace
        </button>
      </div>
      {scope === 'namespace' || !focus ? (
        <select
          className="ap-select topo-ns"
          aria-label="Namespace"
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
        >
          {nsOptions.map((ns) => <option key={ns} value={ns}>{ns}</option>)}
        </select>
      ) : (
        <span className="topo-focus-title" title={`${focus.kind} ${focus.namespace ? `${focus.namespace}/` : ''}${focus.name}`}>
          <span className="topo-focus-icon" aria-hidden>{getResourceIcon(focus.kind)}</span>
          <span className="topo-focus-name">{focus.name}</span>
          <span className="topo-focus-kind">{focus.kind}{focus.namespace ? ` · ${focus.namespace}` : ''}</span>
        </span>
      )}
    </>
  );

  return (
    <div className="topo-page">
      <TopologyView
        key={`${scope}`}
        cluster={cluster}
        mode={mode}
        leading={leading}
        onRefocus={(n) => {
          setFocus({ group: n.group, version: n.version, kind: n.kind, namespace: n.namespace, name: n.name });
          setScope('focus');
        }}
      />
    </div>
  );
};

export default TopologyPage;
