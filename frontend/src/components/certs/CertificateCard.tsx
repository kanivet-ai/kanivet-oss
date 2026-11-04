import React from 'react';
import AIButton from '../ai/AIButton';
import { certFixSeed } from '../ai/aiContext';
import ResourceLink from '../ResourceLink';
import type { CertificateInfo, CertIssue, CertRef } from '../../types/certs';
import { displayName, formatDaysLeft, STATUS_LABEL, STATUS_TONE, validityFraction } from './certsView';
import './Certificates.css';

const fmtDate = (s?: string) =>
  s ? new Date(s).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }) : '—';

export const RefLink: React.FC<{ cluster: string; r: CertRef; label?: string }> = ({ cluster, r, label }) => (
  <ResourceLink cluster={cluster} apiVersion={r.apiVersion} kind={r.kind} name={r.name} namespace={r.namespace}>
    {label ?? (r.namespace ? `${r.namespace}/${r.name}` : r.name)}
  </ResourceLink>
);

export const StatusBadge: React.FC<{ cert: CertificateInfo }> = ({ cert }) => (
  <span className={`cert-status cert-status--${cert.status}`}>
    <span className={`ap-dot ap-dot--${STATUS_TONE[cert.status]}`} />
    {cert.status === 'invalid' ? (cert.certManager?.secretMissing ? 'Not issued' : 'Invalid') : STATUS_LABEL[cert.status]}
  </span>
);

/** Elapsed-validity bar: the fill is time used, the marker is today. */
export const ValidityBar: React.FC<{ cert: CertificateInfo }> = ({ cert }) => {
  const f = validityFraction(cert);
  return (
    <div className="cert-validity" aria-label={`${Math.round(f * 100)}% of validity elapsed`}>
      <div className="cert-validity-track">
        <div className={`cert-validity-fill cert-validity-fill--${cert.status}`} style={{ width: `${f * 100}%` }} />
      </div>
      <div className="cert-validity-dates ap-tnum">
        <span>{fmtDate(cert.notBefore)}</span>
        <span className={`cert-days cert-days--${cert.status}`}>{formatDaysLeft(cert)}</span>
        <span>{fmtDate(cert.notAfter)}</span>
      </div>
    </div>
  );
};

interface Props {
  cluster: string;
  cert: CertificateInfo;
  issues?: CertIssue[];
  /** Hide the owner row (when shown inside the owner's own inspector). */
  hideOwner?: boolean;
}

export const CertificateCard: React.FC<Props> = ({ cluster, cert, issues = [], hideOwner }) => {
  const cm = cert.certManager;
  return (
    <div className="cert-card">
      <div className="cert-card-head">
        <div className={`cert-seal cert-seal--${cert.status}`} aria-hidden>
          <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="7" width="10" height="7" rx="1.5" />
            <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
          </svg>
        </div>
        <div className="cert-card-title">
          <div className="cert-card-name">{displayName(cert)}</div>
          <div className="cert-card-sub">
            {cert.selfSigned ? 'Self-signed' : `Issued by ${cert.issuerCommonName || cert.issuerOrganization || cert.issuer || 'unknown'}`}
          </div>
        </div>
        <StatusBadge cert={cert} />
      </div>

      {cert.notAfter ? <ValidityBar cert={cert} /> : <div className="cert-card-note">{cert.parseError}</div>}

      {issues.length > 0 && (
        <ul className="cert-issue-list">
          {issues.map((i, n) => (
            <li key={n} className={`cert-issue cert-issue--${i.severity}`}>
              <span className={`ap-dot ap-dot--${i.severity === 'critical' ? 'danger' : 'warning'}`} />
              {i.message}
            </li>
          ))}
        </ul>
      )}
      {(issues.length > 0 || cert.status !== 'ok') && (
        <div className="cert-ai-row">
          <AIButton label="Fix with AI" seed={() => {
            const target = cm?.certificate || cert.usedBy[0] || cert.owner;
            const detail = [
              `Certificate ${displayName(cert)} (status ${cert.status}${cert.notAfter ? `, expires ${cert.notAfter}` : ''})`,
              cert.sans.length ? `SANs: ${cert.sans.join(', ')}` : '',
              `Issuer: ${cert.issuerCommonName || cert.issuer || 'unknown'}${cert.selfSigned ? ' (self-signed)' : ''}`,
              `Stored in ${cert.owner.kind} ${cert.owner.namespace ? cert.owner.namespace + '/' : ''}${cert.owner.name}`,
              ...cert.usedBy.map((u) => `Used by ${u.kind} ${u.namespace ? u.namespace + '/' : ''}${u.name}${u.hosts?.length ? ` hosts ${u.hosts.join(', ')}` : ''}`),
              cm ? `cert-manager: ${cm.ready ? 'ready' : cm.reason || 'not ready'} ${cm.message || ''}; issuer ${cm.issuerRef.kind} ${cm.issuerRef.name}` : '',
              ...issues.map((i) => `Problem: ${i.message}`),
            ].filter(Boolean).join('\n');
            return certFixSeed(cluster, target as any, issues[0]?.message || `certificate is ${cert.status}`, detail);
          }} />
        </div>
      )}

      {cert.sans.length > 0 && (
        <div className="cert-section">
          <div className="cert-section-label">Subject alternative names</div>
          <div className="cert-chips">
            {cert.sans.map((s) => (
              <span key={s} className="cert-chip ap-mono">{s}</span>
            ))}
          </div>
        </div>
      )}

      {cert.chain.length > 0 && (
        <div className="cert-section">
          <div className="cert-section-label">Chain</div>
          <ol className="cert-chain">
            {cert.chain.map((c, i) => (
              <li key={i} className="cert-chain-item">
                <span className="cert-chain-node" />
                <span className="cert-chain-subject">{c.subject}</span>
                <span className="cert-chain-meta ap-tnum">
                  {i === 0 ? 'Leaf' : c.selfSigned ? 'Root' : c.isCA ? 'Intermediate' : 'Certificate'} · until {fmtDate(c.notAfter)}
                </span>
              </li>
            ))}
          </ol>
        </div>
      )}

      <div className="ap-card cert-props">
        {!hideOwner && (
          <div className="ap-card-row">
            <span>Stored in</span>
            <span className="ap-card-row-value"><RefLink cluster={cluster} r={cert.owner} /></span>
          </div>
        )}
        {cert.keyAlgorithm && (
          <div className="ap-card-row">
            <span>Key</span>
            <span className="ap-card-row-value ap-tnum">{cert.keyAlgorithm}{cert.keySize ? ` ${cert.keySize}-bit` : ''}</span>
          </div>
        )}
        {cert.signatureAlgorithm && (
          <div className="ap-card-row">
            <span>Signature</span>
            <span className="ap-card-row-value">{cert.signatureAlgorithm}</span>
          </div>
        )}
        {cert.serialNumber && (
          <div className="ap-card-row">
            <span>Serial</span>
            <span className="ap-card-row-value ap-mono cert-ellipsis" title={cert.serialNumber}>{cert.serialNumber}</span>
          </div>
        )}
        {cert.fingerprintSha256 && (
          <div className="ap-card-row">
            <span>SHA-256</span>
            <span className="ap-card-row-value ap-mono cert-ellipsis" title={cert.fingerprintSha256}>{cert.fingerprintSha256}</span>
          </div>
        )}
        {cert.usedBy.length > 0 && (
          <div className="ap-card-row cert-row-top">
            <span>Used by</span>
            <span className="ap-card-row-value cert-usedby">
              {cert.usedBy.map((u, i) => (
                <span key={i} className="cert-usedby-item">
                  <RefLink cluster={cluster} r={u} label={`${u.kind} ${u.namespace ? `${u.namespace}/` : ''}${u.name}`} />
                  {u.hosts?.length ? <span className="cert-usedby-hosts">{u.hosts.join(', ')}</span> : null}
                </span>
              ))}
            </span>
          </div>
        )}
        {cm && (
          <>
            <div className="ap-card-row">
              <span>cert-manager</span>
              <span className="ap-card-row-value">
                <span className={`ap-dot ap-dot--${cm.ready ? 'success' : 'danger'}`} /> {cm.ready ? 'Ready' : cm.reason || 'Not ready'} ·{' '}
                <RefLink cluster={cluster} r={cm.certificate} label={cm.certificate.name} />
              </span>
            </div>
            <div className="ap-card-row">
              <span>Issuer</span>
              <span className="ap-card-row-value"><RefLink cluster={cluster} r={cm.issuerRef} label={`${cm.issuerRef.kind} ${cm.issuerRef.name}`} /></span>
            </div>
            {cm.renewalTime && (
              <div className="ap-card-row">
                <span>Renews</span>
                <span className="ap-card-row-value ap-tnum">{fmtDate(cm.renewalTime)}</span>
              </div>
            )}
            {(cm.failedRequests > 0 || cm.pendingRequests > 0) && (
              <div className="ap-card-row">
                <span>Requests</span>
                <span className="ap-card-row-value ap-tnum">{cm.failedRequests} failed · {cm.pendingRequests} pending</span>
              </div>
            )}
            {!cm.ready && cm.message && <div className="cert-card-note">{cm.message}</div>}
          </>
        )}
      </div>
    </div>
  );
};
