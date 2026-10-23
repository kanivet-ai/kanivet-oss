import React, { useEffect, useRef, useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, ExternalLinkIcon } from '@radix-ui/react-icons';
import PropertyGroup from './PropertyGroup';
import ClipboardCopy from '../../common/ClipboardCopy';
import api from '../../../services/api';
import { collectPodConfigMapRefs, PodConfigMapRef } from '../../../utils/podConfigMapRefs';
import { getResourceIcon } from '../../../utils/resourceIcons';
import './PodConfigMapsSection.css';

type FetchState =
  | { status: 'loading' }
  | { status: 'ok'; data: Record<string, string>; binaryKeys: string[] }
  | { status: 'missing' }
  | { status: 'error'; message: string };

interface PodConfigMapsSectionProps {
  spec: any;
  namespace: string;
  cluster: string;
  handleResourceClick: (kind: string, name: string, namespace?: string, e?: React.MouseEvent, apiVersion?: string) => void;
}

const ConfigMapValues: React.FC<{ cmRef: PodConfigMapRef; state?: FetchState }> = ({ cmRef, state }) => {
  if (!state || state.status === 'loading') return <div className="pod-cm-note">Loading…</div>;
  if (state.status === 'missing') {
    return (
      <div className={`pod-cm-note ${cmRef.optional ? '' : 'pod-cm-note--danger'}`}>
        ConfigMap not found{cmRef.optional ? ' (optional)' : ' — the pod cannot start until it exists'}
      </div>
    );
  }
  if (state.status === 'error') return <div className="pod-cm-note pod-cm-note--danger">Failed to load: {state.message}</div>;

  const keys = Object.keys(state.data);
  const missingKeys = cmRef.referencedKeys.filter((k) => !(k in state.data) && !state.binaryKeys.includes(k));
  if (keys.length === 0 && state.binaryKeys.length === 0 && missingKeys.length === 0) {
    return <div className="pod-cm-note">No data</div>;
  }

  return (
    <div className="pod-cm-entries">
      {keys.map((key) => (
        <div key={key} className="pod-cm-entry">
          <div className="pod-cm-entry-header">
            <span className="pod-cm-key">{key}</span>
            {cmRef.referencedKeys.includes(key) && <span className="ap-badge ap-badge--sm ap-badge--info">referenced</span>}
            <ClipboardCopy text={state.data[key]} />
          </div>
          <pre className="pod-cm-value">{state.data[key]}</pre>
        </div>
      ))}
      {state.binaryKeys.map((key) => (
        <div key={key} className="pod-cm-entry">
          <div className="pod-cm-entry-header">
            <span className="pod-cm-key">{key}</span>
            <span className="ap-badge ap-badge--sm">binary</span>
          </div>
        </div>
      ))}
      {missingKeys.map((key) => (
        <div key={key} className="pod-cm-entry">
          <div className="pod-cm-entry-header">
            <span className="pod-cm-key">{key}</span>
            <span className="ap-badge ap-badge--sm ap-badge--danger">missing key</span>
          </div>
        </div>
      ))}
    </div>
  );
};

const PodConfigMapsSection: React.FC<PodConfigMapsSectionProps> = ({ spec, namespace, cluster, handleResourceClick }) => {
  const refs = collectPodConfigMapRefs(spec);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [states, setStates] = useState<Record<string, FetchState>>({});
  const mounted = useRef(true);

  useEffect(() => () => { mounted.current = false; }, []);

  if (refs.length === 0) return null;

  const load = async (name: string) => {
    setStates((prev) => ({ ...prev, [name]: { status: 'loading' } }));
    let next: FetchState;
    try {
      const cm = await api.getResourceDetails(cluster, '', 'v1', 'ConfigMap', namespace, name);
      next = { status: 'ok', data: cm?.data || {}, binaryKeys: Object.keys(cm?.binaryData || {}) };
    } catch (e: any) {
      next = e?.response?.status === 404 ? { status: 'missing' } : { status: 'error', message: e?.message || 'unknown error' };
    }
    if (mounted.current) setStates((prev) => ({ ...prev, [name]: next }));
  };

  const toggle = (name: string) => {
    const opening = !expanded.has(name);
    setExpanded((prev) => { const s = new Set(prev); opening ? s.add(name) : s.delete(name); return s; });
    if (opening && (!states[name] || states[name].status === 'error')) load(name);
  };

  return (
    <PropertyGroup title="ConfigMaps" count={refs.length} icon={getResourceIcon('ConfigMap')} persistKey="pod-configmaps">
      <div className="pod-cm-list">
        {refs.map((cmRef) => {
          const isExpanded = expanded.has(cmRef.name);
          return (
            <div key={cmRef.name} className={`pod-cm-item ${isExpanded ? 'expanded' : ''}`}>
              <div className="pod-cm-header">
                <button className="pod-cm-toggle" onClick={() => toggle(cmRef.name)} aria-expanded={isExpanded}>
                  <span className="pod-cm-chevron">{isExpanded ? <ChevronDownIcon /> : <ChevronRightIcon />}</span>
                  <span className="pod-cm-name">{cmRef.name}</span>
                  {cmRef.optional && <span className="ap-badge ap-badge--sm">optional</span>}
                </button>
                <button
                  className="pod-cm-open"
                  onClick={(e) => handleResourceClick('ConfigMap', cmRef.name, namespace, e, 'v1')}
                  title={`Open ConfigMap ${cmRef.name}`}
                  aria-label={`Open ConfigMap ${cmRef.name}`}
                >
                  <ExternalLinkIcon />
                </button>
              </div>
              <ul className="pod-cm-usages">
                {cmRef.usages.map((u, i) => (
                  <li key={i}>
                    {u.container && <span className="pod-cm-container">{u.container}</span>}
                    <span className="pod-cm-usage">{u.text}</span>
                  </li>
                ))}
              </ul>
              {isExpanded && <ConfigMapValues cmRef={cmRef} state={states[cmRef.name]} />}
            </div>
          );
        })}
      </div>
    </PropertyGroup>
  );
};

export default PodConfigMapsSection;
