import { useState, useEffect, useCallback, useRef } from 'react';
import * as ScrollArea from '@radix-ui/react-scroll-area';
import { CounterClockwiseClockIcon, TrashIcon, ClockIcon, InfoCircledIcon, FileTextIcon, CodeIcon, Link2Icon, Pencil2Icon } from '@radix-ui/react-icons';
import HelmIcon from '../../icons/HelmIcon';
import Editor from '@monaco-editor/react';
import yaml from 'js-yaml';
import api from '../../../services/api';
import { HelmRelease, HelmReleaseDetail, HelmHistoryEntry, HelmManagedResource } from '../../../types/helm';
import { formatAge } from '../../../utils/formatters';
import useResourceNavigation from '../../../hooks/useResourceNavigation';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../../utils/monacoTheme';
import ClipboardCopy from '../../common/ClipboardCopy';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import Dialog from '../../common/Dialog';
import ExpandIcon from '../../icons/ExpandIcon';
import { useStore } from '../../../store';
import { useShallow } from 'zustand/react/shallow';
import '../../DetailView.css';
import '../shared/DetailViewShared.css';
import './HelmReleaseDetailView.css';

interface HelmReleaseDetailViewProps {
  cluster: string;
  release: HelmRelease;
  onRollback?: (revision: number) => void;
  onUninstall?: () => void;
  mode?: 'detail' | 'center';
}

const getStatusClass = (status: string) => {
  switch (status) {
    case 'deployed': return 'status-running';
    case 'failed': return 'status-failed';
    case 'pending-install':
    case 'pending-upgrade':
    case 'pending-rollback':
    case 'uninstalling': return 'status-pending';
    case 'superseded':
    case 'uninstalled': return 'status-unknown';
    default: return '';
  }
};

const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 Bytes';
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
};

const CodeBlock = ({ 
  title, 
  content, 
  format,
  searchTerm,
  extraControls,
  onContentClick
}: { 
  title: string;
  content: string;
  format: 'json' | 'yaml' | 'text';
  searchTerm?: string;
  onSearchChange?: (term: string) => void;
  showSearch?: boolean;
  extraControls?: React.ReactNode;
  onContentClick?: () => void;
}) => {
  const [isExpanded, setIsExpanded] = useState(false);
  const editorRef = useRef<any>(null);
  const monacoRef = useRef<any>(null);
  const lines = content.split('\n');
  const lineCount = lines.length;
  const size = formatBytes(new Blob([content]).size);
  const isMultiline = lineCount > 10;
  
  const collapsedHeight = 300;
  const expandedHeight = Math.min(lineCount * 19 + 20, 800);

  const handleEditorWillMount = useCallback((monaco: any) => {
    monacoRef.current = monaco;
    installKanivetMonacoTheme(monaco);
  }, []);

  const handleEditorMount = useCallback((editor: any) => {
    editorRef.current = editor;
    if (searchTerm && editor) {
      editor.getAction('actions.find').run();
    }
  }, [searchTerm]);

  useEffect(() => {
    if (editorRef.current && searchTerm) {
      editorRef.current.getAction('actions.find').run();
    }
  }, [searchTerm]);

  const language = format === 'json' ? 'json' : format === 'yaml' ? 'yaml' : 'plaintext';
  const height = isExpanded ? expandedHeight : (isMultiline ? collapsedHeight : Math.min(lineCount * 19 + 20, 300));

  return (
    <div className="helm-code-entry">
      <div className="helm-code-header">
        <div className="helm-code-key-info">
          {isMultiline && (
            <button 
              className="helm-expand-btn"
              onClick={() => setIsExpanded(!isExpanded)}
              title={isExpanded ? 'Collapse' : 'Expand'}
            >
              <ExpandIcon expanded={isExpanded} />
            </button>
          )}
          <span className="helm-code-key">{title}</span>
          <span className={`format-badge format-${format}`}>{format.toUpperCase()}</span>
        </div>
        <div className="helm-code-meta">
          <span className="entry-size">{size}</span>
          <span className="entry-lines">{lineCount} lines</span>
          {extraControls}
          <ClipboardCopy text={content} />
        </div>
      </div>
      <div 
        className={`helm-monaco-wrapper ${onContentClick ? 'clickable' : ''}`}
        style={{ height: `${height}px` }}
        onClick={onContentClick}
      >
        <Editor
          height={height}
          language={language}
          theme={KANIVET_MONACO_THEME}
          value={content}
          beforeMount={handleEditorWillMount}
          onMount={handleEditorMount}
          options={{
            readOnly: true,
            minimap: { enabled: false },
            fontSize: 12,
            lineNumbers: 'on',
            wordWrap: 'on',
            scrollBeyondLastLine: false,
            automaticLayout: true,
            fontFamily: "'JetBrains Mono', 'SF Mono', 'Cascadia Code', 'Fira Code', 'Monaco', 'Menlo', monospace",
            renderWhitespace: 'selection',
            scrollbar: {
              vertical: 'visible',
              horizontal: 'visible',
              verticalScrollbarSize: 8,
              horizontalScrollbarSize: 8,
            },
            folding: true,
            lineDecorationsWidth: 0,
            lineNumbersMinChars: 3,
            glyphMargin: false,
            contextmenu: false,
            find: {
              addExtraSpaceOnTop: false,
              autoFindInSelection: 'never',
              seedSearchStringFromSelection: 'never',
            },
          }}
        />
      </div>
    </div>
  );
};

const HISTORY_PAGE_SIZE = 10;

const HelmReleaseDetailView = ({ cluster, release, onRollback, onUninstall, mode = 'detail' }: HelmReleaseDetailViewProps) => {
  const [detail, setDetail] = useState<HelmReleaseDetail | null>(null);
  const [history, setHistory] = useState<HelmHistoryEntry[]>([]);
  const [historyHasMore, setHistoryHasMore] = useState(false);
  const [historyLimit, setHistoryLimit] = useState(HISTORY_PAGE_SIZE);
  const [values, setValues] = useState<Record<string, any> | null>(null);
  const [manifest, setManifest] = useState<string>('');
  
  // Individual loading states for progressive loading
  const [detailLoading, setDetailLoading] = useState(true);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [valuesLoading, setValuesLoading] = useState(true);
  const [manifestLoading, setManifestLoading] = useState(true);
  
  const [showAllValues, setShowAllValues] = useState(false);
  const [rollbackRevision, setRollbackRevision] = useState<number | null>(null);
  const [showUninstallConfirm, setShowUninstallConfirm] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);
  const { navigateToLink } = useResourceNavigation(cluster);
  const { openBottomTab } = useStore(useShallow((s) => ({ openBottomTab: s.openBottomTab })));
  const [clickTimer, setClickTimer] = useState<NodeJS.Timeout | null>(null);

  const releaseKey = `${cluster}-${release.namespace}-${release.name}`;

  const handleResourceClick = useCallback((kind: string, name: string, namespace?: string, e?: React.MouseEvent) => {
    if (e) { e.preventDefault(); e.stopPropagation(); }
    const openInDetailTab = mode === 'detail';
    if (openInDetailTab) {
      if (clickTimer) {
        clearTimeout(clickTimer);
        setClickTimer(null);
        navigateToLink(kind, name, namespace, { openInDetailTab: true, isPinned: true });
      } else {
        const timer = setTimeout(() => { setClickTimer(null); navigateToLink(kind, name, namespace, { openInDetailTab: true, isPinned: false }); }, 200);
        setClickTimer(timer);
      }
    } else {
      navigateToLink(kind, name, namespace);
    }
  }, [clickTimer, navigateToLink, mode]);

  useEffect(() => { return () => { if (clickTimer) clearTimeout(clickTimer); }; }, [clickTimer]);

  const handleLoadMoreHistory = useCallback(async () => {
    const newLimit = historyLimit + HISTORY_PAGE_SIZE;
    setHistoryLoading(true);
    setHistoryLimit(newLimit);
    try {
      const historyResponse = await api.getHelmReleaseHistory(cluster, release.namespace, release.name, newLimit);
      setHistory(historyResponse?.entries ?? []);
      setHistoryHasMore(historyResponse?.hasMore ?? false);
    } catch (err) {
      console.error('Failed to load more history:', err);
    } finally {
      setHistoryLoading(false);
    }
  }, [historyLimit, cluster, release.namespace, release.name]);

  // Progressive loading - load each section independently
  useEffect(() => {
    let isCancelled = false;

    // Reset all state immediately when release changes
    setDetail(null);
    setHistory([]);
    setHistoryHasMore(false);
    setHistoryLimit(HISTORY_PAGE_SIZE);
    setValues(null);
    setManifest('');
    setDetailLoading(true);
    setHistoryLoading(true);
    setValuesLoading(true);
    setManifestLoading(true);
    setShowAllValues(false);

    // Load detail (fastest, most important)
    api.getHelmRelease(cluster, release.namespace, release.name)
      .then((detailData) => {
        if (!isCancelled) {
          setDetail(detailData);
          setDetailLoading(false);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to load Helm release detail:', err);
          setDetailLoading(false);
        }
      });

    // Load history in parallel
    api.getHelmReleaseHistory(cluster, release.namespace, release.name, HISTORY_PAGE_SIZE)
      .then((historyResponse) => {
        if (!isCancelled) {
          setHistory(historyResponse?.entries ?? []);
          setHistoryHasMore(historyResponse?.hasMore ?? false);
          setHistoryLoading(false);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to load Helm release history:', err);
          setHistoryLoading(false);
        }
      });

    // Load values in parallel
    api.getHelmReleaseValues(cluster, release.namespace, release.name, false)
      .then((valuesData) => {
        if (!isCancelled) {
          setValues(valuesData);
          setValuesLoading(false);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to load Helm release values:', err);
          setValuesLoading(false);
        }
      });

    // Load manifest in parallel
    api.getHelmReleaseManifest(cluster, release.namespace, release.name)
      .then((manifestData) => {
        if (!isCancelled) {
          setManifest(manifestData);
          setManifestLoading(false);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to load Helm release manifest:', err);
          setManifestLoading(false);
        }
      });

    // Cleanup function - marks this effect as stale
    return () => {
      isCancelled = true;
    };
  }, [releaseKey, cluster, release.namespace, release.name]);

  // Handle showAllValues toggle
  useEffect(() => {
    if (!detail) return;
    
    let isCancelled = false;
    setValuesLoading(true);
    
    api.getHelmReleaseValues(cluster, release.namespace, release.name, showAllValues)
      .then((valuesData) => {
        if (!isCancelled) {
          setValues(valuesData);
          setValuesLoading(false);
        }
      })
      .catch((err) => {
        if (!isCancelled) {
          console.error('Failed to load values:', err);
          setValuesLoading(false);
        }
      });
    
    return () => {
      isCancelled = true;
    };
  }, [showAllValues, cluster, release.namespace, release.name, detail]);

  // Reload function for use after actions like rollback - loads in parallel
  const reload = useCallback(async () => {
    setDetailLoading(true);
    setHistoryLoading(true);
    setValuesLoading(true);
    setManifestLoading(true);

    // Load all in parallel
    api.getHelmRelease(cluster, release.namespace, release.name)
      .then(setDetail)
      .catch(console.error)
      .finally(() => setDetailLoading(false));

    api.getHelmReleaseHistory(cluster, release.namespace, release.name, HISTORY_PAGE_SIZE)
      .then((resp) => {
        setHistory(resp?.entries ?? []);
        setHistoryHasMore(resp?.hasMore ?? false);
      })
      .catch(console.error)
      .finally(() => setHistoryLoading(false));

    api.getHelmReleaseValues(cluster, release.namespace, release.name, false)
      .then(setValues)
      .catch(console.error)
      .finally(() => setValuesLoading(false));

    api.getHelmReleaseManifest(cluster, release.namespace, release.name)
      .then(setManifest)
      .catch(console.error)
      .finally(() => setManifestLoading(false));
  }, [cluster, release.namespace, release.name]);

  const handleRollback = async (revision: number) => {
    try {
      setActionLoading(true);
      await api.rollbackHelmRelease(cluster, release.namespace, release.name, revision);
      setRollbackRevision(null);
      reload();
      onRollback?.(revision);
    } catch (err) {
      console.error('Failed to rollback:', err);
    } finally {
      setActionLoading(false);
    }
  };

  const handleUninstall = async () => {
    try {
      setActionLoading(true);
      await api.uninstallHelmRelease(cluster, release.namespace, release.name);
      setShowUninstallConfirm(false);
      onUninstall?.();
    } catch (err) {
      console.error('Failed to uninstall:', err);
    } finally {
      setActionLoading(false);
    }
  };

  const handleManagedResourceClick = (resource: HelmManagedResource) => {
    navigateToLink(resource.kind, resource.name, resource.namespace);
  };

  const handleEditValues = () => {
    const valuesYaml = values ? yaml.dump(values, { indent: 2, lineWidth: -1 }) : '';
    const helmValuesResource = {
      kind: 'HelmRelease',
      apiVersion: 'v1',
      metadata: {
        name: release.name,
        namespace: release.namespace,
      },
      spec: {
        chart: release.chart,
        values: valuesYaml,
      },
      _isHelmValues: true,
      _helmReleaseName: release.name,
      _helmReleaseNamespace: release.namespace,
    };
    openBottomTab('edit', helmValuesResource, cluster);
  };

  // Use detail if loaded, otherwise fall back to release prop for basic info
  const displayData = detail || release;
  const valuesContent = values ? JSON.stringify(values, null, 2) : '';

  // Skeleton placeholder components matching the product style
  const SkeletonLine = ({ width = '100%' }: { width?: string }) => (
    <span className="skeleton-line" style={{ width }} />
  );

  const HistorySkeleton = () => (
    <div className="helm-history-list">
      {[1, 2, 3].map(i => (
        <div key={i} className="helm-history-item skeleton-item">
          <div className="helm-history-left">
            <SkeletonLine width="40px" />
            <SkeletonLine width="60px" />
          </div>
          <div className="helm-history-center">
            <SkeletonLine width="120px" />
            <SkeletonLine width="80px" />
          </div>
          <div className="helm-history-right">
            <SkeletonLine width="70px" />
          </div>
        </div>
      ))}
    </div>
  );

  const CodeBlockSkeleton = () => (
    <div className="code-block-skeleton">
      <div className="code-block-skeleton-header">
        <SkeletonLine width="100px" />
      </div>
      <div className="code-block-skeleton-content">
        {[1, 2, 3, 4, 5, 6].map(i => (
          <SkeletonLine key={i} width={`${70 + Math.random() * 30}%`} />
        ))}
      </div>
    </div>
  );

  const metadata = {
    name: displayData.name,
    namespace: displayData.namespace,
    creationTimestamp: displayData.updated,
    labels: {},
    annotations: {},
  };

  return (
    <>
      <div className="resource-detail-view">
        <div className="resource-header">
          <div className="resource-header-top">
            <div className="resource-kind-row">
              <span className="resource-kind-badge">HelmRelease</span>
              <div className="resource-status-row">
                <span className={`status-badge ${getStatusClass(typeof displayData.status === 'string' ? displayData.status : 'unknown')}`}>
                  {typeof displayData.status === 'string' ? displayData.status : 'unknown'}
                </span>
              </div>
              <div className="resource-actions-row">
                <button 
                  className="tab-content-action-btn priority-low"
                  onClick={handleEditValues}
                  title="Edit values YAML"
                  disabled={valuesLoading}
                >
                  <Pencil2Icon />
                </button>
                <button 
                  className="tab-content-action-btn"
                  onClick={() => setShowUninstallConfirm(true)}
                  title="Uninstall release"
                >
                  <TrashIcon />
                </button>
              </div>
            </div>
            <h2 className="resource-name">
              <span>{displayData.name}</span>
              <ClipboardCopy text={displayData.name} />
            </h2>
          </div>
        </div>

        <ScrollArea.Root className="resource-detail-scroll">
          <ScrollArea.Viewport className="resource-detail-viewport">
            <div className="info-sections">
              <PropertyGroup title="Chart Information" icon={<HelmIcon />} defaultOpen>
                <PropertyRow label="Chart" value={displayData.chart} copyText={displayData.chart} />
                <PropertyRow label="Chart Version" value={displayData.chartVersion || (detailLoading ? '...' : '-')} copyText={displayData.chartVersion} />
                <PropertyRow label="App Version" value={displayData.appVersion || (detailLoading ? '...' : '-')} copyText={displayData.appVersion} />
                <PropertyRow label="Revision" value={String(displayData.revision)} copyText={String(displayData.revision)} />
                <PropertyRow label="Updated" value={displayData.updated ? `${formatAge(displayData.updated)} ago` : (detailLoading ? '...' : '-')} copyText={displayData.updated} />
                {displayData.description && <PropertyRow label="Description" value={displayData.description} copyText={displayData.description} />}
              </PropertyGroup>
              <div className="section-divider" />

              <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} showLabels={false} showAnnotations={false} />
              <div className="section-divider" />

              {detail?.chartMetadata && (
                <>
                  <PropertyGroup title="Chart Metadata" icon={<InfoCircledIcon />} defaultOpen={false}>
                    {detail.chartMetadata.description && (
                      <PropertyRow label="Description" value={detail.chartMetadata.description} copyText={detail.chartMetadata.description} />
                    )}
                    {detail.chartMetadata.home && (
                      <PropertyRow label="Home" value={
                        <a href={detail.chartMetadata.home} target="_blank" rel="noopener noreferrer" className="link-button">
                          {detail.chartMetadata.home}
                        </a>
                      } copyText={detail.chartMetadata.home} />
                    )}
                    {detail.chartMetadata.sources && detail.chartMetadata.sources.length > 0 && (
                      <PropertyRow label="Sources" value={
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', alignItems: 'flex-end' }}>
                          {detail.chartMetadata.sources.map((source, idx) => (
                            <a key={idx} href={source} target="_blank" rel="noopener noreferrer" className="link-button">
                              {source}
                            </a>
                          ))}
                        </div>
                      } />
                    )}
                    {detail.chartMetadata.maintainers && detail.chartMetadata.maintainers.length > 0 && (
                      <PropertyRow label="Maintainers" value={detail.chartMetadata.maintainers.join(', ')} copyText={detail.chartMetadata.maintainers.join(', ')} />
                    )}
                    {detail.chartMetadata.keywords && detail.chartMetadata.keywords.length > 0 && (
                      <PropertyRow label="Keywords" value={
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px', justifyContent: 'flex-end' }}>
                          {detail.chartMetadata.keywords.map((keyword, idx) => (
                            <span key={idx} className="resource-kind-label">{keyword}</span>
                          ))}
                        </div>
                      } />
                    )}
                  </PropertyGroup>
                  <div className="section-divider" />
                </>
              )}

              <PropertyGroup 
                title="Release History" 
                icon={<ClockIcon />} 
                count={history.length || undefined}
                defaultOpen
              >
                {historyLoading && history.length === 0 ? (
                  <HistorySkeleton />
                ) : history.length === 0 ? (
                  <div className="empty-section">No history available</div>
                ) : (
                  <div className="helm-history-list">
                    {history.map(entry => (
                      <div 
                        key={entry.revision} 
                        className={`helm-history-item ${entry.revision === displayData.revision ? 'current' : ''}`}
                      >
                        <div className="helm-history-left">
                          <span className="helm-history-revision">#{entry.revision}</span>
                          <span className={`status-badge ${getStatusClass(typeof entry.status === 'string' ? entry.status : 'unknown')}`}>
                            {typeof entry.status === 'string' ? entry.status : 'unknown'}
                          </span>
                          {entry.revision === displayData.revision && (
                            <span className="format-badge" style={{ background: 'var(--accent)' }}>CURRENT</span>
                          )}
                        </div>
                        <div className="helm-history-center">
                          <span className="helm-history-chart">{entry.chart}</span>
                          <span className="helm-history-date" title={new Date(entry.updated).toLocaleString()}>
                            {formatAge(entry.updated)} ago
                          </span>
                        </div>
                        <div className="helm-history-right">
                          {entry.revision !== displayData.revision && (
                            <button 
                              className="control-button"
                              onClick={() => setRollbackRevision(entry.revision)}
                              title={`Rollback to revision ${entry.revision}`}
                            >
                              <CounterClockwiseClockIcon /> Rollback
                            </button>
                          )}
                        </div>
                      </div>
                    ))}
                    {historyHasMore && (
                      <button
                        className="load-more-button"
                        onClick={handleLoadMoreHistory}
                        disabled={historyLoading}
                      >
                        {historyLoading ? 'Loading...' : `Load ${HISTORY_PAGE_SIZE} more revisions`}
                      </button>
                    )}
                  </div>
                )}
              </PropertyGroup>
              <div className="section-divider" />

              <PropertyGroup 
                title="Values" 
                icon={<CodeIcon />} 
                defaultOpen
                actions={
                  <label style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '11px', color: 'var(--text-secondary)' }}>
                    <input 
                      type="checkbox" 
                      checked={showAllValues} 
                      onChange={e => setShowAllValues(e.target.checked)} 
                    />
                    Show computed
                  </label>
                }
              >
                {valuesLoading && !values ? (
                  <CodeBlockSkeleton />
                ) : valuesContent ? (
                  <CodeBlock 
                    title="values.yaml"
                    content={valuesContent}
                    format="json"
                    showSearch={false}
                    onContentClick={handleEditValues}
                  />
                ) : (
                  <div className="empty-section">
                    <span>No values configured</span>
                    <button 
                      className="add-values-button"
                      onClick={handleEditValues}
                    >
                      Configure Values
                    </button>
                  </div>
                )}
              </PropertyGroup>
              <div className="section-divider" />

              <PropertyGroup title="Rendered Manifest" icon={<FileTextIcon />} defaultOpen>
                {manifestLoading && !manifest ? (
                  <CodeBlockSkeleton />
                ) : manifest ? (
                  <CodeBlock 
                    title="manifest.yaml"
                    content={manifest}
                    format="yaml"
                    showSearch={false}
                  />
                ) : (
                  <div className="empty-section">No manifest available</div>
                )}
              </PropertyGroup>
              <div className="section-divider" />

              {detail?.notes && (
                <>
                  <PropertyGroup title="Release Notes" icon={<FileTextIcon />} defaultOpen={false}>
                    <CodeBlock 
                      title="NOTES.txt"
                      content={detail.notes}
                      format="text"
                      showSearch={false}
                    />
                  </PropertyGroup>
                  <div className="section-divider" />
                </>
              )}

              {detail?.resources && detail.resources.length > 0 && (
                <>
                  <PropertyGroup title="Managed Resources" icon={<Link2Icon />} count={detail.resources.length} defaultOpen>
                    <div className="managed-resources-list">
                      {detail.resources.map((resource, idx) => (
                        <div 
                          key={`${resource.kind}-${resource.name}-${idx}`}
                          className="managed-resource-item"
                        >
                          <button 
                            className="link-button"
                            onClick={() => handleManagedResourceClick(resource)}
                          >
                            {resource.name}
                          </button>
                          <span className="resource-kind-label">{resource.kind}</span>
                          {resource.namespace && (
                            <span className="resource-namespace-label">{resource.namespace}</span>
                          )}
                        </div>
                      ))}
                    </div>
                  </PropertyGroup>
                  <div className="section-divider" />
                </>
              )}
            </div>
          </ScrollArea.Viewport>
          <ScrollArea.Scrollbar className="scrollbar" orientation="vertical">
            <ScrollArea.Thumb className="scrollbar-thumb" />
          </ScrollArea.Scrollbar>
        </ScrollArea.Root>
      </div>

      <Dialog
        isOpen={rollbackRevision !== null}
        onClose={() => setRollbackRevision(null)}
        onConfirm={() => rollbackRevision && handleRollback(rollbackRevision)}
        title="Confirm Rollback"
        confirmText="Rollback"
        isLoading={actionLoading}
        loadingText="Rolling back..."
      >
        <p>
          Are you sure you want to rollback <strong>{release.name}</strong> to revision <strong>{rollbackRevision}</strong>?
        </p>
      </Dialog>

      <Dialog
        isOpen={showUninstallConfirm}
        onClose={() => setShowUninstallConfirm(false)}
        onConfirm={handleUninstall}
        title="Confirm Uninstall"
        confirmText="Uninstall"
        isLoading={actionLoading}
        loadingText="Uninstalling..."
        variant="danger"
      >
        <p>
          Are you sure you want to uninstall <strong>{release.name}</strong>?
          This will delete all associated Kubernetes resources.
        </p>
      </Dialog>
    </>
  );
};

export default HelmReleaseDetailView;
