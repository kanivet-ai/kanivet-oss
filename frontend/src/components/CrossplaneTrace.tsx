import { useState, useEffect, useRef, useCallback } from 'react';
import api from '../services/api';
import ResourceLink from './ResourceLink';
import './CrossplaneTrace.css';

interface TraceNode {
  apiVersion: string;
  kind: string;
  name: string;
  namespace?: string;
  status: string;
  ready: boolean;
  synced?: boolean;
  message?: string;
  children?: TraceNode[];
  metadata?: Record<string, any>;
}

interface CrossplaneTraceProps {
  cluster: string;
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
}

const CrossplaneTrace = ({
  cluster,
  group,
  version,
  kind,
  namespace,
  name,
}: CrossplaneTraceProps) => {
  const [trace, setTrace] = useState<TraceNode | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedNodes, setExpandedNodes] = useState<Set<string>>(new Set());
  const refreshIntervalRef = useRef<NodeJS.Timeout | null>(null);

  const loadTrace = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await api.getCrossplaneTrace(
        cluster,
        group,
        version,
        kind,
        namespace || '',
        name,
      );
      setTrace(result);
      // Expand root node and its immediate children by default
      if (result) {
        const expanded = new Set([getNodeId(result)]);
        if (result.children) {
          result.children.forEach((child: TraceNode) =>
            expanded.add(getNodeId(child)),
          );
        }
        setExpandedNodes(expanded);
      }
    } catch (err: any) {
      console.error('Failed to load trace:', err);
      setError(err.message || 'Failed to load trace');
    } finally {
      setLoading(false);
    }
  }, [cluster, group, version, kind, namespace, name]);

  const loadTraceSilently = useCallback(async () => {
    try {
      const result = await api.getCrossplaneTrace(
        cluster,
        group,
        version,
        kind,
        namespace || '',
        name,
      );
      setTrace(result);
    } catch (err: any) {
      console.error('Failed to refresh trace:', err);
    }
  }, [cluster, group, version, kind, namespace, name]);

  useEffect(() => {
    loadTrace();

    // Set up auto-refresh every 10 seconds
    refreshIntervalRef.current = setInterval(() => {
      loadTraceSilently();
    }, 10000);

    return () => {
      if (refreshIntervalRef.current) {
        clearInterval(refreshIntervalRef.current);
      }
    };
  }, [
    cluster,
    group,
    version,
    kind,
    namespace,
    name,
    loadTrace,
    loadTraceSilently,
  ]);

  const getNodeId = (node: TraceNode): string => {
    return `${node.apiVersion}-${node.kind}-${node.namespace || 'cluster'}-${
      node.name
    }`;
  };

  const toggleNode = (nodeId: string) => {
    const newExpanded = new Set(expandedNodes);
    if (newExpanded.has(nodeId)) {
      newExpanded.delete(nodeId);
    } else {
      newExpanded.add(nodeId);
    }
    setExpandedNodes(newExpanded);
  };

  const renderNode = (node: TraceNode, level: number = 0): JSX.Element => {
    const nodeId = getNodeId(node);
    const isExpanded = expandedNodes.has(nodeId);
    const hasChildren = node.children && node.children.length > 0;

    const getStatusClass = () => {
      if (node.status.toLowerCase() === 'unknown') return 'unknown';
      if (node.ready && node.synced !== false) return 'ready';
      if (!node.ready) return 'not-ready';
      if (node.synced === false) return 'out-of-sync';
      return 'unknown';
    };

    const statusClass = getStatusClass();

    return (
      <div
        key={nodeId}
        className="trace-node compact"
        style={{ marginLeft: `${level * 16}px` }}
      >
        <div
          className="trace-node-header"
          onClick={() => hasChildren && toggleNode(nodeId)}
        >
          {hasChildren && (
            <span className="trace-node-arrow">{isExpanded ? '▼' : '▶'}</span>
          )}
          {!hasChildren && <span className="trace-node-spacer" />}

          <div className="trace-node-indicators">
            {node.synced !== undefined && (
              <span
                className={`trace-indicator synced-${node.synced}`}
                title={`Synced: ${node.synced}`}
              >
                Synced
              </span>
            )}
            <span
              className={`trace-indicator ready-${node.ready}`}
              title={`Ready: ${node.ready}`}
            >
              Ready
            </span>
          </div>

          <span className="trace-node-kind">{node.kind}</span>
          <ResourceLink
            cluster={cluster}
            apiVersion={node.apiVersion}
            kind={node.kind}
            name={node.name}
            namespace={node.namespace}
            className="trace-node-name"
          >
            {node.name}
          </ResourceLink>

          <span className={`trace-node-status-text ${statusClass}`}>
            {node.status}
          </span>
        </div>

        {(!node.ready || node.synced === false) && node.message && (
          <div
            className="trace-node-message compact"
            style={{ marginLeft: `${(level + 1) * 16}px` }}
          >
            {node.message}
          </div>
        )}

        {isExpanded && hasChildren && (
          <div className="trace-node-children">
            {node.children!.map((child) => renderNode(child, level + 1))}
          </div>
        )}
      </div>
    );
  };

  if (loading) {
    return (
      <div className="crossplane-trace loading">
        <div className="loading-message">Loading...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="crossplane-trace error">
        <div className="error-message">Failed to load trace</div>
        <button onClick={loadTrace} className="retry-button">
          Retry
        </button>
      </div>
    );
  }

  if (!trace) {
    return (
      <div className="crossplane-trace empty">
        <div className="empty-message">No trace data</div>
      </div>
    );
  }

  return (
    <div className="crossplane-trace">
      <div className="trace-tree">{renderNode(trace)}</div>
    </div>
  );
};

export default CrossplaneTrace;
