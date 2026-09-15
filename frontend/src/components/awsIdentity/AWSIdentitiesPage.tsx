import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { ReloadIcon } from '@radix-ui/react-icons';
import ScrollContainer from '../ScrollContainer';
import SearchInput from '../common/SearchInput';
import AWSIcon from '../AWSIcon';
import { useStore } from '../../store';
import { listAWSIdentities } from '../../services/api/awsIdentity';
import { getCachedAWSIdentityStatus } from './awsIdentityStatusCache';
import AWSCredentialsBar from './AWSCredentialsBar';
import AWSPermissionError from './AWSPermissionError';
import { AWSMechanismBadge, AWSStatusPill } from './AWSStatusPill';
import {
  AWSCredentials,
  AWSIdentitiesResponse,
  AWSIdentityClusterStatus,
  AWSIdentityMechanism,
  AWSIdentitySummary,
} from '../../types/awsIdentity';
import {
  IdentityFilters,
  IdentitySortKey,
  filterIdentities,
  formatRelativeTime,
  sortIdentities,
} from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSIdentitiesPage.css';

interface Props {
  cluster: string;
}

const COLUMNS: Array<{ key: IdentitySortKey; label: string }> = [
  { key: 'namespace', label: 'Namespace' },
  { key: 'serviceAccount', label: 'Service account' },
  { key: 'mechanism', label: 'Mechanism' },
  { key: 'roleName', label: 'Role' },
  { key: 'trustStatus', label: 'Trust' },
  { key: 'podCount', label: 'Pods' },
  { key: 'lastUsedAt', label: 'Last used' },
];

const trustLabel = (id: AWSIdentitySummary): string => {
  switch (id.trustStatus) {
    case 'ok':
      return 'Trusted';
    case 'error':
      return 'Broken';
    case 'warning':
      return 'Check';
    default:
      return 'Unknown';
  }
};

const AWSIdentitiesPage: React.FC<Props> = ({ cluster }) => {
  const openBottomTab = useStore((s) => s.openBottomTab);
  const [status, setStatus] = useState<AWSIdentityClusterStatus | null>(null);
  const [data, setData] = useState<AWSIdentitiesResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filters, setFilters] = useState<IdentityFilters>({
    search: '',
    mechanism: 'all',
    brokenOnly: false,
  });
  const [sortKey, setSortKey] = useState<IdentitySortKey | null>(null);
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('asc');

  const loadStatus = useCallback(async () => {
    try {
      setStatus(await getCachedAWSIdentityStatus(cluster));
    } catch {
      setStatus({ available: false, credentials: { source: 'none' } });
    }
  }, [cluster]);

  const load = useCallback(async () => {
    if (!cluster) return;
    setLoading(true);
    setError(null);
    try {
      setData(await listAWSIdentities(cluster));
    } catch (e: any) {
      setError(
        e?.response?.data?.error || e?.message || 'Failed to list identities',
      );
    } finally {
      setLoading(false);
    }
  }, [cluster]);

  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  useEffect(() => {
    if (status?.available) void load();
  }, [status?.available, load]);

  const handleCredentialsChanged = useCallback(
    (_next: AWSCredentials) => {
      void loadStatus().then(() => load());
    },
    [loadStatus, load],
  );

  const rows = useMemo(() => {
    const filtered = filterIdentities(data?.identities || [], filters);
    return sortIdentities(filtered, sortKey, sortOrder);
  }, [data, filters, sortKey, sortOrder]);

  const handleSort = (key: IdentitySortKey) => {
    if (sortKey === key) {
      if (sortOrder === 'asc') setSortOrder('desc');
      else {
        setSortKey(null);
        setSortOrder('asc');
      }
    } else {
      setSortKey(key);
      setSortOrder('asc');
    }
  };

  const openIdentity = (id: AWSIdentitySummary) => {
    openBottomTab(
      'aws-identity',
      {
        kind: 'ServiceAccount',
        apiVersion: 'v1',
        metadata: { name: id.serviceAccount, namespace: id.namespace },
      },
      cluster,
    );
  };

  const credentials: AWSCredentials = data?.credentials ||
    status?.credentials || { source: 'none' };

  if (status && !status.available) {
    return (
      <div className="awsid-page">
        <div className="awsid-page-empty">
          <AWSIcon size={40} className="awsid-page-empty-icon" />
          <div className="awsid-page-empty-title">
            This cluster doesn&apos;t use AWS EKS authentication
          </div>
          <div className="awsid-page-empty-text">
            {status.reason ||
              'Kanivet derives AWS credentials from the kubeconfig exec command. Choose a profile manually to inspect IAM roles anyway.'}
          </div>
          <AWSCredentialsBar
            cluster={cluster}
            credentials={credentials}
            onChanged={handleCredentialsChanged}
          />
        </div>
      </div>
    );
  }

  const totals = data?.totals;

  return (
    <div className="awsid-page">
      <div className="awsid-page-toolbar">
        <div className="awsid-page-title">
          <AWSIcon size={14} />
          AWS Identities
        </div>
        <div className="awsid-page-search">
          <SearchInput
            value={filters.search}
            onChange={(v) => setFilters((f) => ({ ...f, search: v }))}
            placeholder="Search namespace, service account, role…"
          />
        </div>
        <select
          className="awsid-page-select"
          value={filters.mechanism}
          onChange={(e) =>
            setFilters((f) => ({
              ...f,
              mechanism: e.target.value as AWSIdentityMechanism | 'all',
            }))
          }
        >
          <option value="all">All mechanisms</option>
          <option value="irsa">IRSA</option>
          <option value="pod-identity">Pod Identity</option>
        </select>
        <button
          type="button"
          className={`awsid-btn awsid-page-toggle ${filters.brokenOnly ? 'active' : ''}`}
          onClick={() =>
            setFilters((f) => ({ ...f, brokenOnly: !f.brokenOnly }))
          }
          aria-pressed={filters.brokenOnly}
        >
          Broken only
        </button>
        <span className="awsid-page-spacer" />
        <AWSCredentialsBar
          cluster={cluster}
          credentials={credentials}
          onChanged={handleCredentialsChanged}
          compact
        />
        <button
          type="button"
          className="awsid-btn awsid-btn-icon"
          onClick={load}
          disabled={loading}
          title="Refresh"
        >
          <ReloadIcon className={loading ? 'awsid-spin' : ''} />
        </button>
      </div>

      <div className="awsid-page-summary">
        <div className="awsid-stat">
          <span className="awsid-stat-value">{totals?.total ?? '–'}</span>
          <span className="awsid-stat-label">Identities</span>
        </div>
        <div className="awsid-stat">
          <span className="awsid-stat-value">{totals?.irsa ?? '–'}</span>
          <span className="awsid-stat-label">IRSA</span>
        </div>
        <div className="awsid-stat">
          <span className="awsid-stat-value">{totals?.podIdentity ?? '–'}</span>
          <span className="awsid-stat-label">Pod Identity</span>
        </div>
        <div
          className={`awsid-stat ${totals && totals.broken > 0 ? 'stat-broken' : ''}`}
        >
          <span className="awsid-stat-value">{totals?.broken ?? '–'}</span>
          <span className="awsid-stat-label">Broken trust</span>
        </div>
        <div
          className={`awsid-stat ${totals && totals.unused90d > 0 ? 'stat-unused' : ''}`}
        >
          <span className="awsid-stat-value">{totals?.unused90d ?? '–'}</span>
          <span className="awsid-stat-label">Unused 90d</span>
        </div>
      </div>

      {data?.permissionError && (
        <div className="awsid-page-banner">
          <AWSPermissionError
            cluster={cluster}
            message={data.permissionError}
            requiredPermissions={data.requiredPermissions}
            credentials={credentials}
            onCredentialsChanged={handleCredentialsChanged}
          />
        </div>
      )}

      {error && (
        <div className="awsid-page-banner">
          <div className="awsid-callout callout-error">
            <div className="awsid-callout-title">
              Couldn&apos;t list identities
            </div>
            <div>{error}</div>
          </div>
        </div>
      )}

      <div className="list-viewport awsid-page-viewport">
        <ScrollContainer className="table-container">
          <table
            className="resource-table awsid-page-table"
            style={{ width: '100%' }}
          >
            <thead>
              <tr>
                {COLUMNS.map((col) => (
                  <th
                    key={col.key}
                    className={`sortable-column column-${col.key}`}
                    onClick={() => handleSort(col.key)}
                  >
                    <div className="th-content">
                      {col.label}
                      {sortKey === col.key && (
                        <span className="awsid-sort-indicator">
                          {sortOrder === 'asc' ? '↑' : '↓'}
                        </span>
                      )}
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {loading && !data ? (
                <tr>
                  <td colSpan={COLUMNS.length} className="empty-message">
                    Scanning service accounts and Pod Identity associations…
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={COLUMNS.length} className="empty-message">
                    {data && data.identities.length === 0
                      ? 'No workload identities found — no service account is annotated for IRSA and there are no Pod Identity associations.'
                      : 'No identities match the current filters.'}
                  </td>
                </tr>
              ) : (
                rows.map((id) => (
                  <tr
                    key={`${id.namespace}/${id.serviceAccount}`}
                    className={`resource-row awsid-row trust-${id.trustStatus}`}
                    onClick={() => openIdentity(id)}
                  >
                    <td className="column-namespace">
                      <div className="cell-content">{id.namespace}</div>
                    </td>
                    <td className="column-serviceAccount">
                      <div className="cell-content awsid-row-sa">
                        {id.serviceAccount}
                      </div>
                    </td>
                    <td className="column-mechanism">
                      <div className="cell-content">
                        <AWSMechanismBadge mechanism={id.mechanism} />
                      </div>
                    </td>
                    <td className="column-roleName" title={id.roleArn}>
                      <div className="cell-content awsid-row-role">
                        <span className="awsid-mono awsid-truncate">
                          {id.roleName}
                        </span>
                        {id.crossAccount && (
                          <span
                            className="awsid-row-cross"
                            title={`Role lives in account ${id.accountId}`}
                          >
                            cross-account
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="column-trustStatus">
                      <div className="cell-content">
                        <AWSStatusPill
                          status={id.trustStatus}
                          label={trustLabel(id)}
                          title={id.trustMessage}
                        />
                      </div>
                    </td>
                    <td className="column-podCount">
                      <div className="cell-content">{id.podCount}</div>
                    </td>
                    <td className="column-lastUsedAt">
                      <div
                        className={`cell-content ${id.lastUsedAt ? '' : 'awsid-muted'}`}
                        title={id.lastUsedAt || 'Never used'}
                      >
                        {formatRelativeTime(id.lastUsedAt)}
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </ScrollContainer>
      </div>
    </div>
  );
};

export default AWSIdentitiesPage;
