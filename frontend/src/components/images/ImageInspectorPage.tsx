import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ChevronDownIcon,
  ChevronRightIcon,
  FileIcon,
  FileTextIcon,
  Link2Icon,
  LockClosedIcon,
  ReloadIcon,
  ExclamationTriangleIcon,
  CheckCircledIcon,
  Cross2Icon,
  LayersIcon,
} from '@radix-ui/react-icons';
import { Editor } from '../MonacoEditor';
import ClipboardCopy from '../common/ClipboardCopy';
import ResourceLink from '../ResourceLink';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../utils/monacoTheme';
import {
  getImageIndexStatus,
  getImageInfo,
  getImageLayerChanges,
  ImageApiError,
  ImageDirListing,
  ImageFileContent,
  ImageFileEntry,
  ImageIndexStatus,
  ImageInfo,
  ImageLayerChanges,
  ImagePullContext,
  ImageSearchResult,
  listImageDir,
  readImageFile,
  searchImageFiles,
} from '../../services/api/images';
import {
  cleanCreatedBy,
  formatCommand,
  formatSize,
  ImageTabTarget,
  languageForPath,
  layerBars,
  shortDigest,
  shortImageRef,
  sortChanges,
} from './imageUtils';
import './ImageInspector.css';

type View = 'overview' | 'layers' | 'files';

const AUTH_LABELS: Record<string, string> = {
  'pull-secret': 'Pull secret',
  'docker-config': 'Docker config',
  gcloud: 'Google Cloud',
  'aws-ecr': 'AWS ECR',
  'azure-acr': 'Azure ACR',
  anonymous: 'Anonymous',
};

const ERROR_TITLES: Record<string, string> = {
  auth: 'The registry needs credentials',
  not_found: 'Image not found',
  unreachable: 'Registry unreachable',
  invalid: 'Invalid image reference',
};

const ErrorState: React.FC<{ error: ImageApiError; onRetry: () => void }> = ({ error, onRetry }) => (
  <div className="image-inspector-state" role="alert">
    <div className="image-inspector-state-icon">
      {error.kind === 'auth' ? <LockClosedIcon width={22} height={22} /> : <ExclamationTriangleIcon width={22} height={22} />}
    </div>
    <div className="image-inspector-state-title">{ERROR_TITLES[error.kind] || 'Could not inspect the image'}</div>
    <div className="image-inspector-state-text">{error.message}</div>
    {error.kind === 'auth' && (
      <div className="image-inspector-state-text">
        Kanivet tries the pod's image pull secrets, your local docker login and credential helpers, then gcloud, AWS and
        Azure logins. Run <code>docker login</code> for this registry, or inspect from a pod that has a pull secret.
      </div>
    )}
    <button className="ap-btn" onClick={onRetry}><ReloadIcon /> Try again</button>
  </div>
);

const Loading: React.FC<{ label: string; status?: ImageIndexStatus | null }> = ({ label, status }) => (
  <div className="image-inspector-state">
    <div className="ap-spinner" />
    <div className="image-inspector-state-text">{label}</div>
    {status && status.layersTotal > 0 && (
      <>
        <div className="image-inspector-progress" aria-hidden>
          <div style={{ width: `${Math.min(100, (status.bytesDone / Math.max(status.bytesTotal, 1)) * 100)}%` }} />
        </div>
        <div className="image-inspector-state-sub">
          {status.layersDone} of {status.layersTotal} layers · {formatSize(status.bytesDone)} of {formatSize(status.bytesTotal)}
        </div>
      </>
    )}
  </div>
);

const Row: React.FC<{ label: string; children: React.ReactNode; mono?: boolean; copy?: string }> = ({ label, children, mono, copy }) => (
  <div className="ap-card-row image-inspector-row">
    <span className="image-inspector-row-label">{label}</span>
    <span className={`image-inspector-row-value${mono ? ' ap-mono' : ''}`}>
      {children}
      {copy && <ClipboardCopy text={copy} />}
    </span>
  </div>
);

const OverviewView: React.FC<{ info: ImageInfo }> = ({ info }) => {
  const c = info.config;
  const labels = Object.entries(c.labels || {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className="image-inspector-scroll">
      <div className="ap-group-label">Runtime</div>
      <div className="ap-card image-inspector-card">
        <Row label="Entrypoint" mono copy={formatCommand(c.entrypoint) || undefined}>{formatCommand(c.entrypoint) || <span className="ap-muted">none</span>}</Row>
        <Row label="Command" mono copy={formatCommand(c.cmd) || undefined}>{formatCommand(c.cmd) || <span className="ap-muted">none</span>}</Row>
        <Row label="User">{c.user ? <span className="ap-mono">{c.user}</span> : <span className="image-inspector-warn">root (no USER set)</span>}</Row>
        {c.workingDir && <Row label="Working directory" mono>{c.workingDir}</Row>}
        {(c.exposedPorts?.length ?? 0) > 0 && <Row label="Exposed ports" mono>{c.exposedPorts!.join(', ')}</Row>}
        {(c.volumes?.length ?? 0) > 0 && <Row label="Volumes" mono>{c.volumes!.join(', ')}</Row>}
        {c.stopSignal && <Row label="Stop signal" mono>{c.stopSignal}</Row>}
        <Row label="Platform" mono>{info.platform}</Row>
        {c.author && <Row label="Author">{c.author}</Row>}
      </div>

      <div className="ap-group-label">Environment</div>
      <div className="ap-card image-inspector-card">
        {(c.env || []).length === 0 && <div className="ap-card-row ap-muted">No environment variables</div>}
        {(c.env || []).map((e) => (
          <div key={e.name} className="ap-card-row image-inspector-env">
            <span className="ap-mono image-inspector-env-name">{e.name}</span>
            <span className={`ap-mono image-inspector-env-value${e.redacted ? ' is-redacted' : ''}`} title={e.redacted ? 'Hidden: looks like a secret' : e.value}>
              {e.value}
              {e.redacted && <LockClosedIcon />}
            </span>
          </div>
        ))}
      </div>

      {labels.length > 0 && (
        <>
          <div className="ap-group-label">Labels</div>
          <div className="ap-card image-inspector-card">
            {labels.map(([k, v]) => (
              <div key={k} className="ap-card-row image-inspector-env">
                <span className="ap-mono image-inspector-env-name">{k}</span>
                <span className="ap-mono image-inspector-env-value" title={v}>{v}</span>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
};

const LayersView: React.FC<{ info: ImageInfo; onPick: (layer: number) => void }> = ({ info, onPick }) => {
  const [showEmpty, setShowEmpty] = useState(false);
  const bars = useMemo(() => layerBars(info.layers), [info.layers]);
  const steps = info.history || [];
  return (
    <div className="image-inspector-scroll">
      <div className="image-inspector-layers-head">
        <span className="ap-group-label">{info.layers.length} layers · {formatSize(info.size)}</span>
        {steps.some((s) => s.emptyLayer) && (
          <label className="image-inspector-check">
            <input type="checkbox" className="ap-checkbox" checked={showEmpty} onChange={(e) => setShowEmpty(e.target.checked)} />
            Show metadata steps
          </label>
        )}
      </div>
      <div className="ap-card image-inspector-card">
        {showEmpty
          ? steps.map((s, i) =>
              s.layerIndex < 0 ? (
                <div key={`h-${i}`} className="image-inspector-layer is-empty">
                  <span className="image-inspector-layer-index">·</span>
                  <span className="image-inspector-layer-cmd ap-mono">{cleanCreatedBy(s.createdBy)}</span>
                </div>
              ) : (
                <LayerRow key={`l-${s.layerIndex}`} bar={bars[s.layerIndex]} onPick={onPick} />
              ),
            )
          : bars.map((b) => <LayerRow key={b.index} bar={b} onPick={onPick} />)}
      </div>
      <div className="image-inspector-hint">Click a layer to see the files it added, changed or removed.</div>
    </div>
  );
};

const LayerRow: React.FC<{ bar: ReturnType<typeof layerBars>[number]; onPick: (layer: number) => void }> = ({ bar, onPick }) => {
  if (!bar) return null;
  return (
    <button className="image-inspector-layer" onClick={() => onPick(bar.index)} title={bar.label}>
      <span className="image-inspector-layer-index ap-tnum">{bar.index + 1}</span>
      <span className="image-inspector-layer-main">
        <span className="image-inspector-layer-cmd ap-mono">{bar.label}</span>
        <span className="image-inspector-layer-bar"><span style={{ width: `${bar.width * 100}%` }} /></span>
      </span>
      <span className="image-inspector-layer-size ap-tnum">{formatSize(bar.size)}</span>
      <span className="image-inspector-layer-share ap-tnum">{(bar.share * 100).toFixed(bar.share < 0.1 ? 1 : 0)}%</span>
    </button>
  );
};

interface FilesProps {
  target: ImageTabTarget;
  pc: ImagePullContext;
  info: ImageInfo;
  layerFilter: number | null;
  setLayerFilter: (l: number | null) => void;
}

const EntryIcon: React.FC<{ e: { type: string } }> = ({ e }) =>
  e.type === 'symlink' || e.type === 'hardlink' ? <Link2Icon /> : <FileIcon />;

const FilesView: React.FC<FilesProps> = ({ target, pc, info, layerFilter, setLayerFilter }) => {
  const ref = target.ref;
  const [dirs, setDirs] = useState<Record<string, ImageDirListing>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['/']));
  const [rootError, setRootError] = useState<ImageApiError | null>(null);
  const [status, setStatus] = useState<ImageIndexStatus | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [preview, setPreview] = useState<ImageFileContent | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [query, setQuery] = useState('');
  const [search, setSearch] = useState<ImageSearchResult | null>(null);
  const [changes, setChanges] = useState<ImageLayerChanges | null>(null);
  const [attempt, setAttempt] = useState(0);
  const previewSeq = useRef(0);

  const loadDir = useCallback(async (path: string) => {
    const listing = await listImageDir(ref, pc, path);
    setDirs((d) => ({ ...d, [path]: listing }));
    return listing;
  }, [ref, pc]);

  // Root listing; it waits for indexing, so poll progress meanwhile.
  useEffect(() => {
    let alive = true;
    setRootError(null);
    const poll = window.setInterval(() => {
      getImageIndexStatus(ref, pc).then((s) => alive && setStatus(s)).catch(() => {});
    }, 800);
    loadDir('/')
      .catch((e) => alive && setRootError(e instanceof ImageApiError ? e : new ImageApiError(String(e), 'internal')))
      .finally(() => window.clearInterval(poll));
    return () => {
      alive = false;
      window.clearInterval(poll);
    };
  }, [loadDir, attempt]);

  useEffect(() => {
    if (layerFilter === null) {
      setChanges(null);
      return;
    }
    let alive = true;
    setChanges(null);
    getImageLayerChanges(ref, pc, layerFilter).then((c) => alive && setChanges(c)).catch(() => {});
    return () => { alive = false; };
  }, [ref, pc, layerFilter]);

  useEffect(() => {
    const q = query.trim();
    if (!q || layerFilter !== null) {
      setSearch(null);
      return;
    }
    let alive = true;
    const t = window.setTimeout(() => {
      searchImageFiles(ref, pc, q).then((r) => alive && setSearch(r)).catch(() => {});
    }, 200);
    return () => { alive = false; window.clearTimeout(t); };
  }, [ref, pc, query, layerFilter]);

  const openFile = useCallback(async (path: string) => {
    setSelected(path);
    const seq = ++previewSeq.current;
    setPreviewLoading(true);
    setPreviewError(null);
    try {
      const fc = await readImageFile(ref, pc, path);
      if (seq === previewSeq.current) setPreview(fc);
    } catch (e: any) {
      if (seq === previewSeq.current) {
        setPreview(null);
        setPreviewError(e?.message || 'Could not read the file');
      }
    } finally {
      if (seq === previewSeq.current) setPreviewLoading(false);
    }
  }, [ref, pc]);

  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else {
        next.add(path);
        if (!dirs[path]) loadDir(path).catch(() => {});
      }
      return next;
    });
  }, [dirs, loadDir]);

  const rows = useMemo(() => {
    const out: { e: ImageFileEntry; depth: number }[] = [];
    const walk = (path: string, depth: number) => {
      const l = dirs[path];
      if (!l) return;
      for (const e of l.entries) {
        out.push({ e, depth });
        if (e.type === 'dir' && expanded.has(e.path)) walk(e.path, depth + 1);
      }
    };
    walk('/', 0);
    return out;
  }, [dirs, expanded]);

  if (rootError) return <ErrorState error={rootError} onRetry={() => setAttempt((a) => a + 1)} />;
  if (!dirs['/']) return <Loading label={info.indexed ? 'Loading files…' : 'Downloading and indexing layers…'} status={status} />;

  const root = dirs['/'];
  const layer = layerFilter !== null ? info.layers[layerFilter] : null;

  const renderFlat = (entries: { path: string; type: string; size: number; op?: string; layer?: number; totalSize?: number }[]) =>
    entries.map((e) => (
      <button
        key={`${e.op || ''}${e.path}`}
        className={`image-inspector-tree-row${selected === e.path ? ' is-selected' : ''}${e.op === 'deleted' ? ' is-deleted' : ''}`}
        onClick={() => e.type !== 'dir' && e.op !== 'deleted' && openFile(e.path)}
        disabled={e.op === 'deleted'}
      >
        {e.op && <span className={`image-inspector-op is-${e.op}`}>{e.op === 'added' ? 'A' : e.op === 'modified' ? 'M' : 'D'}</span>}
        <span className="image-inspector-tree-icon">{e.type === 'dir' ? <LayersIcon /> : <EntryIcon e={e} />}</span>
        <span className="image-inspector-tree-name ap-mono">{e.path}</span>
        <span className="image-inspector-tree-size ap-tnum">{e.type === 'dir' ? formatSize(e.totalSize) : formatSize(e.size)}</span>
      </button>
    ));

  return (
    <div className="image-inspector-files">
      <div className="image-inspector-files-left">
        <div className="image-inspector-files-bar">
          <input
            className="ap-input ap-input--sm image-inspector-search"
            placeholder={layerFilter !== null ? 'Filter changes' : 'Search files'}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search files"
          />
          {layer && (
            <span className="image-inspector-chip" title={cleanCreatedBy(layer.createdBy)}>
              Layer {layer.index + 1}
              <button aria-label="Clear layer filter" onClick={() => setLayerFilter(null)}><Cross2Icon /></button>
            </span>
          )}
        </div>
        <div className="image-inspector-tree" role="tree">
          {layerFilter !== null ? (
            !changes ? (
              <Loading label="Loading layer changes…" />
            ) : (
              <>
                <div className="image-inspector-tree-summary">
                  {changes.added} added · {changes.modified} modified · {changes.deleted} removed
                  {changes.truncated && ' · list truncated'}
                </div>
                {renderFlat(sortChanges(changes.changes, query))}
              </>
            )
          ) : search ? (
            <>
              <div className="image-inspector-tree-summary">
                {search.entries.length}{search.truncated ? '+' : ''} matches
              </div>
              {renderFlat(search.entries)}
            </>
          ) : (
            rows.map(({ e, depth }) => (
              <button
                key={e.path}
                role="treeitem"
                aria-expanded={e.type === 'dir' ? expanded.has(e.path) : undefined}
                className={`image-inspector-tree-row${selected === e.path ? ' is-selected' : ''}`}
                style={{ paddingLeft: 8 + depth * 14 }}
                onClick={() => (e.type === 'dir' ? toggle(e.path) : openFile(e.path))}
                title={e.linkTarget ? `${e.path} → ${e.linkTarget}` : e.path}
              >
                <span className="image-inspector-tree-chevron">
                  {e.type === 'dir' ? expanded.has(e.path) ? <ChevronDownIcon /> : <ChevronRightIcon /> : null}
                </span>
                <span className="image-inspector-tree-icon">{e.type === 'dir' ? <LayersIcon /> : <EntryIcon e={e} />}</span>
                <span className="image-inspector-tree-name">
                  {e.name}
                  {e.linkTarget && <span className="image-inspector-link"> → {e.linkTarget}</span>}
                </span>
                <span className="image-inspector-tree-mode ap-mono">{e.mode}</span>
                <span className="image-inspector-tree-layer ap-tnum" title={`Last written by layer ${e.layer + 1}`}>L{e.layer + 1}</span>
                <span className="image-inspector-tree-size ap-tnum">{e.type === 'dir' ? formatSize(e.totalSize) : formatSize(e.size)}</span>
              </button>
            ))
          )}
        </div>
        <div className="ap-statusbar">
          {root.totalFiles.toLocaleString()} files · {formatSize(root.totalSize)} uncompressed
        </div>
      </div>
      <div className="image-inspector-preview">
        {!selected ? (
          <div className="image-inspector-state">
            <FileTextIcon width={22} height={22} />
            <div className="image-inspector-state-text">Select a file to preview it</div>
          </div>
        ) : previewLoading ? (
          <Loading label="Reading file…" />
        ) : previewError ? (
          <div className="image-inspector-state"><div className="image-inspector-state-text">{previewError}</div></div>
        ) : preview ? (
          <>
            <div className="image-inspector-preview-bar">
              <span className="ap-mono ap-truncate">{preview.path}</span>
              <span className="image-inspector-preview-meta ap-tnum">
                {formatSize(preview.size)} · layer {preview.layer + 1}
                {preview.truncated && ' · showing the first ' + formatSize(preview.content?.length ?? 0)}
              </span>
            </div>
            {preview.binary ? (
              <div className="image-inspector-state"><div className="image-inspector-state-text">Binary file, no preview</div></div>
            ) : (
              <div className="image-inspector-editor">
                <Editor
                  height="100%"
                  theme={KANIVET_MONACO_THEME}
                  beforeMount={installKanivetMonacoTheme}
                  language={languageForPath(preview.path)}
                  value={preview.content || ''}
                  options={{ readOnly: true, domReadOnly: true, minimap: { enabled: false }, fontSize: 12, scrollBeyondLastLine: false, wordWrap: 'off', renderLineHighlight: 'none' }}
                />
              </div>
            )}
          </>
        ) : null}
      </div>
    </div>
  );
};

const TagBadge: React.FC<{ info: ImageInfo }> = ({ info }) => {
  const t = info.tag;
  if (!t) return null;
  if (t.moved) {
    return (
      <span className="ap-badge ap-badge--warning" title={`Running ${shortDigest(t.runningDigest)}, :${t.tag} now points at ${shortDigest(t.currentDigest)}`}>
        <ExclamationTriangleIcon /> Tag moved
      </span>
    );
  }
  if (t.runningDigest && t.currentDigest) {
    return (
      <span className="ap-badge ap-badge--success" title={`:${t.tag} still points at the running image`}>
        <CheckCircledIcon /> Tag current
      </span>
    );
  }
  return null;
};

const ImageInspectorPage: React.FC<{ cluster: string; target?: ImageTabTarget }> = ({ cluster, target }) => {
  const [view, setView] = useState<View>('overview');
  const [platform, setPlatform] = useState<string>('');
  const [info, setInfo] = useState<ImageInfo | null>(null);
  const [error, setError] = useState<ImageApiError | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [layerFilter, setLayerFilter] = useState<number | null>(null);

  const pc = useMemo<ImagePullContext>(
    () => ({ ...(target?.pullContext || {}), cluster: target?.pullContext?.cluster || cluster, ...(platform ? { platform } : {}) }),
    [target, cluster, platform],
  );

  useEffect(() => {
    if (!target?.ref) return;
    let alive = true;
    setInfo(null);
    setError(null);
    getImageInfo(target.ref, pc)
      .then((i) => alive && setInfo(i))
      .catch((e) => alive && setError(e instanceof ImageApiError ? e : new ImageApiError(String(e), 'internal')));
    return () => { alive = false; };
  }, [target?.ref, pc, attempt]);

  if (!target?.ref) {
    return <div className="image-inspector-state"><div className="image-inspector-state-text">No image selected</div></div>;
  }

  const pickLayer = (l: number) => {
    setLayerFilter(l);
    setView('files');
  };

  const source = target.pullContext;
  return (
    <div className="image-inspector">
      <div className="image-inspector-header">
        <div className="ap-tile image-inspector-tile"><LayersIcon width={20} height={20} /></div>
        <div className="image-inspector-heading">
          <div className="image-inspector-title">
            {shortImageRef(target.ref)}
            {info && <TagBadge info={info} />}
          </div>
          <div className="image-inspector-subtitle ap-mono ap-truncate" title={target.ref}>{target.ref}</div>
          <div className="image-inspector-meta">
            {info ? (
              <>
                <span className="ap-mono" title={info.digest}>{shortDigest(info.digest)}<ClipboardCopy text={info.digest} /></span>
                <span className="ap-sep">·</span>
                <span className="ap-tnum">{formatSize(info.size)}</span>
                {info.created && (<><span className="ap-sep">·</span><span>Created {new Date(info.created).toLocaleString()}</span></>)}
                <span className="ap-sep">·</span>
                <span>{AUTH_LABELS[info.authMethod] || info.authMethod}</span>
              </>
            ) : (
              <span className="ap-muted">{error ? 'Not available' : 'Fetching manifest…'}</span>
            )}
            {source?.pod && source.namespace && (
              <>
                <span className="ap-sep">·</span>
                <span>Running in <ResourceLink cluster={pc.cluster || cluster} apiVersion="v1" kind="Pod" name={source.pod} namespace={source.namespace} /></span>
              </>
            )}
          </div>
        </div>
        {info && (info.platforms?.length ?? 0) > 1 && (
          <select
            className="ap-select image-inspector-platform"
            value={platform || info.platform}
            onChange={(e) => { setPlatform(e.target.value); setLayerFilter(null); }}
            aria-label="Platform"
          >
            {info.platforms!.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        )}
        <button className="ap-icon-btn" title="Reload" aria-label="Reload" onClick={() => setAttempt((a) => a + 1)}><ReloadIcon /></button>
      </div>
      {info?.tag?.moved && (
        <div className="image-inspector-banner">
          <ExclamationTriangleIcon />
          <span>
            This pod runs <span className="ap-mono">{shortDigest(info.tag.runningDigest)}</span>, but <span className="ap-mono">:{info.tag.tag}</span> now
            points at <span className="ap-mono">{shortDigest(info.tag.currentDigest)}</span>. A restart would pull a different image.
          </span>
        </div>
      )}
      <div className="image-inspector-tabs">
        <div className="ap-segmented" role="tablist">
          {(['overview', 'layers', 'files'] as View[]).map((v) => (
            <button key={v} role="tab" aria-selected={view === v} className={view === v ? 'is-active' : ''} onClick={() => setView(v)}>
              {v === 'overview' ? 'Overview' : v === 'layers' ? `Layers${info ? ` (${info.layers.length})` : ''}` : 'Files'}
            </button>
          ))}
        </div>
      </div>
      <div className="image-inspector-body">
        {error ? (
          <ErrorState error={error} onRetry={() => setAttempt((a) => a + 1)} />
        ) : !info ? (
          <Loading label="Fetching manifest and config…" />
        ) : view === 'overview' ? (
          <OverviewView info={info} />
        ) : view === 'layers' ? (
          <LayersView info={info} onPick={pickLayer} />
        ) : (
          <FilesView key={`${info.digest}-${attempt}`} target={target} pc={pc} info={info} layerFilter={layerFilter} setLayerFilter={setLayerFilter} />
        )}
      </div>
    </div>
  );
};

export default ImageInspectorPage;
