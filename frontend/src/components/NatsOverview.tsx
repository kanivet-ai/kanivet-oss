import { useCallback, useEffect, useMemo, useState } from 'react';
import api from '../services/api';
import {
  formatBytes,
  formatCompactNumber,
} from '../utils/detailViewFormatters';
import { parseStreamName } from '../utils/natsStreamKind';
import { formatNatsAccountName } from '../utils/natsAccountName';
import {
  NatsAccountDetail,
  NatsConsumerDetail,
  NatsDetection,
  NatsOverview as NatsOverviewData,
  NatsStreamDetail,
} from '../types/nats';
import { useVisibleInterval } from '../hooks/useVisibleInterval';
import NatsSparkline from './NatsSparkline';
import ResourceLink from './ResourceLink';
import './NatsOverview.css';

interface NatsOverviewProps {
  cluster: string;
}

const HISTORY_LENGTH = 20;
const ACK_PENDING_WARN = 1000;
const REDELIVERED_WARN = 20;

interface RateSample {
  inMsgsPerSec: number;
  outMsgsPerSec: number;
}

interface Issue {
  key: string;
  accountName: string;
  streamName: string;
  consumerName: string;
  reason: string;
}

const streamNodeId = (accountName: string, stream: NatsStreamDetail) =>
  `${accountName}/${stream.name}`;
const byName = <T extends { name: string }>(a: T, b: T) =>
  a.name.localeCompare(b.name);

/** Compact count with the exact value on hover. */
const Count = ({ value }: { value: number }) => (
  <span title={value.toLocaleString()}>{formatCompactNumber(value)}</span>
);

/** Compact byte size with the exact byte count on hover. */
const Bytes = ({ value }: { value: number }) => (
  <span title={`${value.toLocaleString()} bytes`}>{formatBytes(value)}</span>
);

const STREAM_KIND_LABEL: Record<string, string> = {
  kv: 'KV',
  objectstore: 'Object Store',
};

const findIssues = (accounts: NatsAccountDetail[]): Issue[] => {
  const issues: Issue[] = [];
  for (const account of accounts) {
    for (const stream of account.stream_detail ?? []) {
      for (const consumer of stream.consumer_detail ?? []) {
        if (consumer.num_ack_pending > ACK_PENDING_WARN) {
          issues.push({
            key: `${account.name}/${stream.name}/${consumer.name}/ack`,
            accountName: account.name,
            streamName: stream.name,
            consumerName: consumer.name,
            reason: `${consumer.num_ack_pending.toLocaleString()} messages awaiting ack`,
          });
        }
        if (consumer.num_redelivered > REDELIVERED_WARN) {
          issues.push({
            key: `${account.name}/${stream.name}/${consumer.name}/redelivered`,
            accountName: account.name,
            streamName: stream.name,
            consumerName: consumer.name,
            reason: `${consumer.num_redelivered.toLocaleString()} messages redelivered`,
          });
        }
      }
    }
  }
  return issues;
};

const NatsOverview = ({ cluster }: NatsOverviewProps) => {
  const [detection, setDetection] = useState<NatsDetection | null>(null);
  const [overview, setOverview] = useState<NatsOverviewData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedStreams, setExpandedStreams] = useState<Set<string>>(
    new Set(),
  );
  const [history, setHistory] = useState<RateSample[]>([]);

  const load = useCallback(
    async (silent: boolean) => {
      if (!silent) {
        setLoading(true);
        setError(null);
      }
      try {
        const det = await api.getNatsDetection(cluster);
        setDetection(det);
        if (det.installed && det.hasMonitor) {
          const data = await api.getNatsOverview(cluster);
          setOverview(data);
          // rates are computed server-side, once per real fetch from NATS -
          // never re-derived here, so this history is never out of phase
          // with the poll cache and can't alias into a sawtooth.
          if (data.rates) {
            setHistory((h) =>
              [
                ...h,
                {
                  inMsgsPerSec: data.rates!.inMsgsPerSec,
                  outMsgsPerSec: data.rates!.outMsgsPerSec,
                },
              ].slice(-HISTORY_LENGTH),
            );
          }
        }
      } catch (err: any) {
        if (!silent) setError(err.message || 'Failed to load NATS status');
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [cluster],
  );

  useEffect(() => {
    load(false);
  }, [load]);
  useVisibleInterval(() => load(true), 5000);

  const toggleStream = (nodeId: string) => {
    setExpandedStreams((prev) => {
      const next = new Set(prev);
      if (next.has(nodeId)) next.delete(nodeId);
      else next.add(nodeId);
      return next;
    });
  };

  const accounts = overview?.jsz?.account_details ?? [];
  const issues = useMemo(() => findIssues(accounts), [accounts]);
  const sortedAccounts = useMemo(
    () =>
      accounts
        .slice()
        .sort(byName)
        .map((account) => ({
          ...account,
          stream_detail: (account.stream_detail ?? []).slice().sort(byName),
        })),
    [accounts],
  );

  if (loading) {
    return (
      <div className="nats-overview centered">
        <div className="nats-status-message">Loading NATS status…</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="nats-overview centered">
        <div className="nats-status-message error">{error}</div>
        <button onClick={() => load(false)} className="nats-retry-button">
          Retry
        </button>
      </div>
    );
  }

  if (!detection?.installed) {
    return (
      <div className="nats-overview centered">
        <div className="nats-empty-icon" aria-hidden="true">
          ⦿
        </div>
        <div className="nats-status-message">
          No NATS deployment detected in this cluster.
        </div>
        <div className="nats-status-hint">
          Looked for a Service labeled <code>app.kubernetes.io/name=nats</code>{' '}
          and for NACK's JetStream CRDs.
        </div>
      </div>
    );
  }

  if (!detection.hasMonitor) {
    return (
      <div className="nats-overview centered">
        <div className="nats-empty-icon" aria-hidden="true">
          ⦿
        </div>
        <div className="nats-status-message">
          NATS-related resources were found
          {detection.legacyOperator ? ' (legacy nats-operator CRD)' : ''}
          {detection.hasJetStreamCrds ? ' (NACK JetStream CRDs)' : ''}, but no
          monitoring Service was found.
        </div>
        <div className="nats-status-hint">
          Expose the server's monitor port (default 8222) through a Service to
          see live status here.
        </div>
      </div>
    );
  }

  const varz = overview?.varz;
  const jsz = overview?.jsz;
  const healthz = overview?.healthz;
  const connections = overview?.connz?.connections ?? [];
  const healthy = healthz?.status === 'ok';
  const inRates = history.map((h) => h.inMsgsPerSec);
  const outRates = history.map((h) => h.outMsgsPerSec);
  const inRateNow = overview?.rates?.inMsgsPerSec;
  const outRateNow = overview?.rates?.outMsgsPerSec;

  return (
    <div className="nats-overview">
      <div className="nats-card nats-header-card">
        <div
          className={`nats-health-pill ${healthy ? 'healthy' : 'unhealthy'}`}
        >
          <span className="nats-health-dot" />
          {healthz?.status ?? 'unknown'}
        </div>
        <div className="nats-header-text">
          <div className="nats-server-name">{detection.serviceName}</div>
          <div className="nats-server-meta">
            <span>{detection.namespace}</span>
            {varz?.version && <span>v{varz.version}</span>}
            {varz?.uptime && <span>up {varz.uptime}</span>}
            {varz?.cluster?.name && <span>cluster {varz.cluster.name}</span>}
          </div>
        </div>
      </div>

      {issues.length > 0 && (
        <div className="nats-card nats-issues-card">
          <div className="nats-section-title warning">
            Needs attention ({issues.length})
          </div>
          <div className="nats-issue-list">
            {issues.slice(0, 5).map((issue) => (
              <div key={issue.key} className="nats-issue-row">
                <span className="nats-issue-dot" />
                <span className="nats-issue-target">
                  {issue.streamName} <span className="nats-issue-sep">→</span>{' '}
                  {issue.consumerName}
                </span>
                <span className="nats-issue-reason">{issue.reason}</span>
              </div>
            ))}
            {issues.length > 5 && (
              <div className="nats-issue-more">+{issues.length - 5} more</div>
            )}
          </div>
        </div>
      )}

      <div className="nats-stats">
        <div className="nats-stat-card">
          <span className="nats-stat-label">Connections</span>
          <span className="nats-stat-value">
            <Count value={varz?.connections ?? 0} />
          </span>
        </div>
        <div className="nats-stat-card">
          <span className="nats-stat-label">Leaf Nodes</span>
          <span className="nats-stat-value">
            <Count value={varz?.leafnodes ?? 0} />
          </span>
        </div>
        <div
          className="nats-stat-card nats-stat-card-rate"
          title="Messages received per second, since the last real sample from the server"
        >
          <span className="nats-stat-label">In Msgs/sec</span>
          <span className="nats-stat-row">
            <span className="nats-stat-value">
              {inRateNow !== undefined ? inRateNow.toFixed(1) : '–'}
            </span>
            <span className="nats-sparkline-wrap">
              <NatsSparkline values={inRates} />
            </span>
          </span>
        </div>
        <div
          className="nats-stat-card nats-stat-card-rate"
          title="Messages sent per second, since the last real sample from the server"
        >
          <span className="nats-stat-label">Out Msgs/sec</span>
          <span className="nats-stat-row">
            <span className="nats-stat-value">
              {outRateNow !== undefined ? outRateNow.toFixed(1) : '–'}
            </span>
            <span className="nats-sparkline-wrap">
              <NatsSparkline values={outRates} />
            </span>
          </span>
        </div>
        <div className="nats-stat-card">
          <span className="nats-stat-label">In / Out Bytes</span>
          <span className="nats-stat-value small">
            <Bytes value={varz?.in_bytes ?? 0} /> /{' '}
            <Bytes value={varz?.out_bytes ?? 0} />
          </span>
        </div>
        <div className="nats-stat-card">
          <span className="nats-stat-label">Streams / Consumers</span>
          <span className="nats-stat-value">
            <Count value={jsz?.streams ?? 0} /> /{' '}
            <Count value={jsz?.consumers ?? 0} />
          </span>
        </div>
      </div>

      <div className="nats-panels">
        <div className="nats-card nats-jetstream-card">
          <div className="nats-section-title">
            JetStream
            {sortedAccounts.length === 1 && (
              <>
                <span className="nats-section-sep">—</span>
                <span className="nats-section-subtitle">
                  {formatNatsAccountName(sortedAccounts[0].name).label}
                </span>
              </>
            )}
          </div>
          {sortedAccounts.length === 0 ? (
            <div className="nats-status-hint inline">
              JetStream is disabled, or no accounts have streams yet.
            </div>
          ) : (
            <div className="nats-account-tree">
              {sortedAccounts.map((account: NatsAccountDetail) => {
                const accountLabel = formatNatsAccountName(account.name);
                return (
                  <div key={account.name} className="nats-account">
                    {sortedAccounts.length > 1 && (
                      <div className="nats-account-name">
                        {accountLabel.label}
                      </div>
                    )}
                    {account.stream_detail.length === 0 ? (
                      <div className="nats-status-hint inline">
                        No streams in this account.
                      </div>
                    ) : (
                      <div className="nats-stream-table">
                        <div className="nats-stream-row nats-stream-head">
                          <span />
                          <span>Name</span>
                          <span>Storage</span>
                          <span>Messages</span>
                          <span>Bytes</span>
                          <span>Consumers</span>
                        </div>
                        {account.stream_detail.map((stream) => {
                          const nodeId = streamNodeId(account.name, stream);
                          const expanded = expandedStreams.has(nodeId);
                          const consumers = (stream.consumer_detail ?? [])
                            .slice()
                            .sort(byName);
                          const parsed = parseStreamName(stream.name);
                          const kindLabel = STREAM_KIND_LABEL[parsed.kind];
                          const subjectsTitle = (
                            stream.config?.subjects ?? []
                          ).join(', ');
                          return (
                            <div key={nodeId} className="nats-stream-group">
                              <div
                                className={`nats-stream-row nats-stream-data${consumers.length > 0 ? ' clickable' : ''}`}
                                onClick={() =>
                                  consumers.length > 0 && toggleStream(nodeId)
                                }
                              >
                                <span className="nats-tree-arrow">
                                  {consumers.length > 0
                                    ? expanded
                                      ? '▾'
                                      : '▸'
                                    : ''}
                                </span>
                                <span
                                  className="nats-stream-name-cell"
                                  title={subjectsTitle || undefined}
                                >
                                  <span className="nats-stream-name">
                                    {parsed.displayName}
                                  </span>
                                  {kindLabel && (
                                    <span className="nats-stream-badge">
                                      {kindLabel}
                                    </span>
                                  )}
                                </span>
                                <span>
                                  {stream.config?.storage ?? '–'}
                                  {stream.config?.retention &&
                                    stream.config.retention !== 'limits' && (
                                      <span className="nats-stream-tag">
                                        {stream.config.retention}
                                      </span>
                                    )}
                                </span>
                                <span>
                                  <Count value={stream.state?.messages ?? 0} />
                                </span>
                                <span>
                                  <Bytes value={stream.state?.bytes ?? 0} />
                                </span>
                                <span>
                                  <Count value={consumers.length} />
                                </span>
                              </div>
                              {expanded && consumers.length > 0 && (
                                <div className="nats-consumer-table">
                                  <div className="nats-consumer-row nats-consumer-head">
                                    <span>Consumer</span>
                                    <span>Pending</span>
                                    <span>Ack Pending</span>
                                    <span>Redelivered</span>
                                    <span>Waiting</span>
                                  </div>
                                  {consumers.map(
                                    (consumer: NatsConsumerDetail) => (
                                      <div
                                        key={consumer.name}
                                        className="nats-consumer-row"
                                      >
                                        <span className="nats-consumer-name">
                                          {consumer.name}
                                        </span>
                                        <span>
                                          <Count value={consumer.num_pending} />
                                        </span>
                                        <span>
                                          <Count
                                            value={consumer.num_ack_pending}
                                          />
                                        </span>
                                        <span>
                                          <Count
                                            value={consumer.num_redelivered}
                                          />
                                        </span>
                                        <span>
                                          <Count value={consumer.num_waiting} />
                                        </span>
                                      </div>
                                    ),
                                  )}
                                </div>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {connections.length > 0 && (
          <div className="nats-card nats-clients-card">
            <div className="nats-section-title">
              Connected Clients ({connections.length})
            </div>
            <div className="nats-clients-table">
              <div className="nats-clients-row nats-clients-head">
                <span>Client</span>
                <span>Pod</span>
                <span>Account</span>
                <span title="Number of subject subscriptions this client currently has open">
                  Subscriptions
                </span>
                <span>In / Out Msgs</span>
                <span>In / Out Bytes</span>
                <span>Uptime</span>
              </div>
              <div className="nats-clients-body">
                {connections.map((conn) => (
                  <div key={conn.cid} className="nats-clients-row">
                    <span
                      className="nats-client-name"
                      title={`${conn.ip}:${conn.port}`}
                    >
                      {conn.name ||
                        `${conn.lang ?? 'client'} ${conn.version ?? ''}`.trim() ||
                        conn.ip}
                    </span>
                    <span className="nats-client-pod">
                      {conn.podName ? (
                        <ResourceLink
                          cluster={cluster}
                          apiVersion="v1"
                          kind="Pod"
                          name={conn.podName}
                          namespace={conn.podNamespace}
                          openInDetailTab
                          className="nats-client-pod-link"
                        >
                          {conn.podName}
                        </ResourceLink>
                      ) : (
                        '—'
                      )}
                    </span>
                    <span>{conn.account || '—'}</span>
                    <span>
                      <Count value={conn.subscriptions} />
                    </span>
                    <span>
                      <Count value={conn.in_msgs} /> /{' '}
                      <Count value={conn.out_msgs} />
                    </span>
                    <span>
                      <Bytes value={conn.in_bytes} /> /{' '}
                      <Bytes value={conn.out_bytes} />
                    </span>
                    <span>{conn.uptime}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

export default NatsOverview;
