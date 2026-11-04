import React, { useMemo, useState } from 'react';
import type { CertificateInfo, CertStatus } from '../../types/certs';
import { CertificateCard, RefLink, StatusBadge } from './CertificateCard';
import {
  displayName,
  formatDaysLeft,
  groupByIssuer,
  issuerLabel,
  sortCerts,
  SortKey,
  STATUS_LABEL,
  STATUS_ORDER,
  STATUS_TONE,
  timelineBins,
} from './certsView';
import { useCertificates } from './useCertificates';
import { CertificatesIcon } from '../icons/kube';
import './Certificates.css';

const COLUMNS: { key: SortKey; label: string }[] = [
  { key: 'status', label: 'Status' },
  { key: 'name', label: 'Certificate' },
  { key: 'namespace', label: 'Stored in' },
  { key: 'issuer', label: 'Issuer' },
  { key: 'expires', label: 'Expires' },
  { key: 'key', label: 'Key' },
  { key: 'usedBy', label: 'Used by' },
];

const SOURCES = [
  { id: '', label: 'All' },
  { id: 'secret', label: 'Secrets' },
  { id: 'webhook', label: 'Webhooks' },
  { id: 'cert-manager', label: 'Pending' },
];

const fmtDate = (s?: string) =>
  s ? new Date(s).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }) : '—';

const CertificatesPage: React.FC<{ cluster: string }> = ({ cluster }) => {
  const { report, loading, error, reload, refresh } = useCertificates(cluster);
  const [statusFilter, setStatusFilter] = useState<CertStatus | null>(null);
  const [binFilter, setBinFilter] = useState<number | null>(null);
  const [source, setSource] = useState('');
  const [query, setQuery] = useState('');
  const [view, setView] = useState<'table' | 'issuer'>('table');
  const [sort, setSort] = useState<{ key: SortKey; dir: 'asc' | 'desc' }>({ key: 'status', dir: 'asc' });
  const [selected, setSelected] = useState<string | null>(null);

  const all = report?.certificates ?? [];
  const bins = useMemo(() => timelineBins(all), [all]);
  const maxBin = Math.max(1, ...bins.map((b) => b.certs.length));

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const inBin = binFilter != null ? new Set(bins[binFilter]?.certs.map((c) => c.id)) : null;
    const list = all.filter(
      (c) =>
        (!statusFilter || c.status === statusFilter) &&
        (!source || c.source === source) &&
        (!inBin || inBin.has(c.id)) &&
        (!q ||
          [displayName(c), c.owner.name, c.owner.namespace || '', issuerLabel(c), ...c.sans].some((f) => f.toLowerCase().includes(q))),
    );
    return sortCerts(list, sort.key, sort.dir);
  }, [all, statusFilter, source, query, sort, binFilter, bins]);

  const selectedCert = visible.find((c) => c.id === selected) ?? null;
  const issuesFor = (c: CertificateInfo) => (report?.issues ?? []).filter((i) => i.certificateId === c.id);
  const orphanIssues = (report?.issues ?? []).filter((i) => !i.certificateId);

  const toggleSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' }));

  const renderRow = (c: CertificateInfo) => (
    <tr
      key={c.id}
      className={`cert-row${selected === c.id ? ' selected' : ''}`}
      onClick={() => setSelected((s) => (s === c.id ? null : c.id))}
    >
      <td><StatusBadge cert={c} /></td>
      <td>
        <div className="cert-name">{displayName(c)}</div>
        {c.sans.length > 1 && <div className="cert-sub">+{c.sans.length - 1} more names</div>}
      </td>
      <td onClick={(e) => e.stopPropagation()}>
        <RefLink cluster={cluster} r={c.owner} label={c.source === 'webhook' ? c.owner.name : undefined} />
        {c.source === 'webhook' && <div className="cert-sub">Webhook CA bundle</div>}
      </td>
      <td>{issuerLabel(c)}</td>
      <td className="ap-tnum">
        <div>{fmtDate(c.notAfter)}</div>
        <div className={`cert-sub cert-days--${c.status}`}>{formatDaysLeft(c)}</div>
      </td>
      <td className="ap-tnum">{c.keyAlgorithm ? `${c.keyAlgorithm} ${c.keySize || ''}` : '—'}</td>
      <td onClick={(e) => e.stopPropagation()}>
        {c.usedBy.length === 0 ? (
          <span className="cert-sub">—</span>
        ) : (
          c.usedBy.slice(0, 2).map((u, i) => (
            <div key={i}><RefLink cluster={cluster} r={u} label={`${u.kind} ${u.name}`} /></div>
          ))
        )}
        {c.usedBy.length > 2 && <div className="cert-sub">+{c.usedBy.length - 2} more</div>}
      </td>
    </tr>
  );

  const table = (rows: CertificateInfo[]) => (
    <table className="cert-table">
      <thead>
        <tr>
          {COLUMNS.map((col) => (
            <th key={col.key} onClick={() => toggleSort(col.key)} aria-sort={sort.key === col.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
              {col.label}
              {sort.key === col.key && (
                <svg className={`cert-sort${sort.dir === 'desc' ? ' desc' : ''}`} viewBox="0 0 10 10" width="9" height="9" fill="none" stroke="currentColor" strokeWidth="1.8"><path d="M2 6.5 5 3.5l3 3" /></svg>
              )}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>{rows.map(renderRow)}</tbody>
    </table>
  );

  return (
    <div className="cert-page">
      <div className="cert-header">
        <div className="cert-title">
          <CertificatesIcon />
          <h2>Certificates</h2>
          {report?.certManagerDetected && <span className="cert-source-chip">cert-manager</span>}
          {report?.gatewayApiDetected && <span className="cert-source-chip">Gateway API</span>}
        </div>
        <div className="cert-actions">
          <div className="ap-segmented" role="tablist">
            <button className={view === 'table' ? 'is-active' : ''} onClick={() => setView('table')}>List</button>
            <button className={view === 'issuer' ? 'is-active' : ''} onClick={() => setView('issuer')}>By issuer</button>
          </div>
          <label className="ap-search ap-search--sm cert-search">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"><circle cx="7" cy="7" r="4.5" /><path d="m10.5 10.5 3 3" /></svg>
            <input placeholder="Filter names, hosts, issuers" value={query} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <button className="ap-icon-btn" title="Refresh" onClick={refresh} disabled={loading}>
            <svg className={loading ? 'cert-spin' : undefined} viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"><path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5v2.5h-2.5" /></svg>
          </button>
        </div>
      </div>

      {error && (
        <div className="cert-banner cert-banner--error" role="alert">
          {error}
          <button className="ap-btn ap-btn--sm" onClick={reload}>Retry</button>
        </div>
      )}
      {report?.warnings?.map((w) => (
        <div key={w} className="cert-banner" role="status">{w}</div>
      ))}

      {!report && loading && <div className="cert-empty">Reading certificates…</div>}

      {report && (
        <div className="cert-body">
          <div className="cert-stats">
            {STATUS_ORDER.map((s) => {
              const n = report.summary[s];
              if (s === 'invalid' && n === 0) return null;
              return (
                <button
                  key={s}
                  className={`cert-stat${statusFilter === s ? ' active' : ''}`}
                  onClick={() => setStatusFilter((f) => (f === s ? null : s))}
                >
                  <span className="cert-stat-value ap-tnum">{n}</span>
                  <span className="cert-stat-label"><span className={`ap-dot ap-dot--${STATUS_TONE[s]}`} />{STATUS_LABEL[s]}</span>
                </button>
              );
            })}
          </div>

          <div className="cert-strip-wrap">
            <div className="cert-strip-label">Expiry over the next 90 days</div>
            <div className="cert-strip" role="list">
              {bins.map((b, i) => (
                <button
                  key={i}
                  role="listitem"
                  className={`cert-bin cert-bin--${b.tone}${b.certs.length ? '' : ' empty'}${binFilter === i ? ' active' : ''}`}
                  style={{ ['--cert-bin-level' as any]: b.certs.length / maxBin }}
                  title={`${b.label}: ${b.certs.length} certificate${b.certs.length === 1 ? '' : 's'}`}
                  onClick={() => setBinFilter((f) => (f === i ? null : i))}
                >
                  <span className="cert-bin-count ap-tnum">{b.certs.length || ''}</span>
                </button>
              ))}
            </div>
            <div className="cert-strip-axis">
              <span>Expired</span><span>Now</span><span>2 wk</span><span>30 days</span><span>Later</span>
            </div>
          </div>

          {orphanIssues.length > 0 && (
            <div className="cert-findings">
              <div className="ap-group-label">Configuration findings</div>
              <div className="ap-card">
                {orphanIssues.map((i, n) => (
                  <div key={n} className="ap-card-row cert-finding">
                    <span className={`ap-dot ap-dot--${i.severity === 'critical' ? 'danger' : 'warning'}`} />
                    <span className="cert-finding-msg">{i.message}</span>
                    <RefLink cluster={cluster} r={i.resource} label={`${i.resource.kind} ${i.resource.name}`} />
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="cert-filterbar">
            <div className="ap-segmented ap-segmented--sm">
              {SOURCES.map((s) => (
                <button key={s.id} className={source === s.id ? 'is-active' : ''} onClick={() => setSource(s.id)}>{s.label}</button>
              ))}
            </div>
            <span className="cert-count ap-tnum">{visible.length} of {all.length}</span>
            {(statusFilter || binFilter != null || query || source) && (
              <button className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => { setStatusFilter(null); setBinFilter(null); setQuery(''); setSource(''); }}>
                Clear filters
              </button>
            )}
          </div>

          <div className="cert-main">
            <div className="cert-list">
              {all.length === 0 ? (
                <div className="cert-empty">No TLS certificates found in this cluster.</div>
              ) : visible.length === 0 ? (
                <div className="cert-empty">No certificates match these filters.</div>
              ) : view === 'table' ? (
                table(visible)
              ) : (
                groupByIssuer(visible).map((g) => (
                  <div key={g.issuer} className="cert-group">
                    <div className="cert-group-head">
                      <span className={`ap-dot ap-dot--${STATUS_TONE[g.worst]}`} />
                      <span className="cert-group-name">{g.issuer}</span>
                      <span className="cert-group-count ap-tnum">{g.certs.length}</span>
                      {(() => {
                        const iss = report.issuers.find((x) => `${x.ref.kind} ${x.ref.name}` === g.issuer);
                        return iss ? (
                          <span className="cert-group-meta">
                            {iss.type} · {iss.ready ? 'Ready' : `Not ready${iss.reason ? ` (${iss.reason})` : ''}`} ·{' '}
                            <RefLink cluster={cluster} r={iss.ref} label="Open" />
                          </span>
                        ) : null;
                      })()}
                    </div>
                    {table(g.certs)}
                  </div>
                ))
              )}
            </div>
            {selectedCert && (
              <aside className="cert-detail">
                <div className="cert-detail-head">
                  <span>Certificate</span>
                  <button className="ap-icon-btn ap-icon-btn--sm" title="Close" onClick={() => setSelected(null)}>
                    <svg viewBox="0 0 12 12" width="10" height="10" stroke="currentColor" strokeWidth="1.8"><path d="m2.5 2.5 7 7m0-7-7 7" /></svg>
                  </button>
                </div>
                <CertificateCard cluster={cluster} cert={selectedCert} issues={issuesFor(selectedCert)} />
              </aside>
            )}
          </div>
        </div>
      )}
    </div>
  );
};

export default CertificatesPage;
