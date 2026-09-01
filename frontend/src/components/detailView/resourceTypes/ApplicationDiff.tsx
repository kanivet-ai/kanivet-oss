import { useEffect, useState, useCallback, useMemo, useRef } from 'react';
import { DiffEditor } from '@monaco-editor/react';
import yaml from 'js-yaml';
import api from '../../../services/api';
import { ArgoManagedResource } from '../../../services/api/resources';
import { useTheme } from '../../ThemeProvider';
import './ApplicationDiff.css';

interface Props {
  cluster: string;
  namespace: string;
  name: string;
}

const toYaml = (raw: string): string => {
  if (!raw) return '';
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object') {
      const cleaned = stripNoise(parsed);
      return yaml.dump(cleaned, { noRefs: true, lineWidth: 120 });
    }
  } catch {}
  return raw;
};

const stripNoise = (obj: any): any => {
  if (!obj || typeof obj !== 'object') return obj;
  const clone: any = Array.isArray(obj) ? [...obj] : { ...obj };
  if (clone.metadata && typeof clone.metadata === 'object') {
    const m = { ...clone.metadata };
    delete m.managedFields;
    delete m.resourceVersion;
    delete m.uid;
    delete m.generation;
    delete m.creationTimestamp;
    if (m.annotations) {
      const a = { ...m.annotations };
      delete a['kubectl.kubernetes.io/last-applied-configuration'];
      delete a['deployment.kubernetes.io/revision'];
      m.annotations = a;
    }
    clone.metadata = m;
  }
  return clone;
};

const ApplicationDiff = ({ cluster, namespace, name }: Props) => {
  const [items, setItems] = useState<ArgoManagedResource[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const { theme } = useTheme();
  const activeKeyRef = useRef('');

  useEffect(() => {
    const key = `${cluster}|${namespace}|${name}`;
    activeKeyRef.current = key;
    setItems(null);
    setError(null);
    setLoading(true);
    setSelectedIdx(0);
  }, [cluster, namespace, name]);

  const load = useCallback(async () => {
    const key = `${cluster}|${namespace}|${name}`;
    setLoading(true);
    try {
      const result = await api.getArgoManagedResources(cluster, namespace, name);
      if (activeKeyRef.current !== key) return;
      setItems(result);
      setError(null);
    } catch (e: any) {
      if (activeKeyRef.current !== key) return;
      setError(e?.response?.data?.error || e?.message || 'Failed to load diff');
    } finally {
      if (activeKeyRef.current === key) setLoading(false);
    }
  }, [cluster, namespace, name]);

  useEffect(() => {
    load();
  }, [load]);

  const filtered = useMemo(() => {
    if (!items) return [];
    return items.filter((it) => {
      const t = toYaml(it.targetState || '');
      const l = toYaml(it.normalizedLiveState || it.liveState || '');
      return t !== l;
    });
  }, [items]);

  useEffect(() => {
    if (filtered.length > 0 && selectedIdx >= filtered.length) {
      setSelectedIdx(0);
    }
  }, [filtered, selectedIdx]);

  if (loading) {
    return <div className="argo-diff-empty">Loading diff…</div>;
  }
  if (error) {
    return (
      <div className="argo-diff-empty argo-diff-error">
        {error}
        <button className="argo-diff-retry" onClick={load}>Retry</button>
      </div>
    );
  }
  if (filtered.length === 0) {
    return <div className="argo-diff-empty">No differences. Application is fully in sync.</div>;
  }

  const current = filtered[selectedIdx];
  const target = toYaml(current.targetState || '');
  const live = toYaml(current.normalizedLiveState || current.liveState || '');

  return (
    <div className="argo-diff">
      <div className="argo-diff-list">
        {filtered.map((it, i) => {
          const label = `${it.kind}/${it.name}`;
          const ns = it.namespace ? `${it.namespace}` : '';
          return (
            <button
              key={`${it.kind}-${it.namespace}-${it.name}`}
              type="button"
              className={`argo-diff-item ${i === selectedIdx ? 'active' : ''}`}
              onClick={() => setSelectedIdx(i)}
              title={label}
            >
              <span className="argo-diff-item-kind">{it.kind}</span>
              <span className="argo-diff-item-name">{it.name}</span>
              {ns && <span className="argo-diff-item-ns">{ns}</span>}
            </button>
          );
        })}
      </div>
      <div className="argo-diff-viewer">
        <div className="argo-diff-header">
          <span className="argo-diff-header-left">Live</span>
          <span className="argo-diff-header-right">Target</span>
        </div>
        <div className="argo-diff-editor">
          <DiffEditor
            original={live}
            modified={target}
            language="yaml"
            theme={theme === 'dark' ? 'vs-dark' : 'vs-light'}
            options={{
              renderSideBySide: true,
              readOnly: true,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              automaticLayout: true,
              fontSize: 12,
              renderOverviewRuler: false,
            }}
          />
        </div>
      </div>
    </div>
  );
};

export default ApplicationDiff;
