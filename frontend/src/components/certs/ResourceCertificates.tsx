import React from 'react';
import { CertificateCard } from './CertificateCard';
import { useCertificates } from './useCertificates';

interface Props {
  cluster: string;
  kind: string;
  namespace?: string;
  name: string;
}

/** Inspector section: the certificates a resource holds or serves. */
export const ResourceCertificates: React.FC<Props> = ({ cluster, kind, namespace, name }) => {
  const { report, loading, error, reload } = useCertificates(cluster, { kind, resourceNamespace: namespace, name });

  if (error && !report) {
    return (
      <div className="cert-inline-state">
        {error} <button className="ap-btn ap-btn--sm" onClick={reload}>Retry</button>
      </div>
    );
  }
  if (!report) return <div className="cert-inline-state">{loading ? 'Reading certificates…' : 'No certificate data'}</div>;

  const owns = (ownerKind: string, ownerName: string) => ownerKind === kind && ownerName === name;
  const orphanIssues = report.issues.filter((i) => !i.certificateId);

  if (report.certificates.length === 0 && orphanIssues.length === 0) {
    return <div className="cert-inline-state">No TLS certificate found for this {kind}.</div>;
  }
  return (
    <div className="cert-inspector">
      {orphanIssues.map((i, n) => (
        <div key={n} className={`cert-issue cert-issue--${i.severity} cert-issue--block`}>
          <span className={`ap-dot ap-dot--${i.severity === 'critical' ? 'danger' : 'warning'}`} />
          {i.message}
        </div>
      ))}
      {report.certificates.map((c) => (
        <CertificateCard
          key={c.id}
          cluster={cluster}
          cert={c}
          hideOwner={owns(c.owner.kind, c.owner.name)}
          issues={report.issues.filter((i) => i.certificateId === c.id)}
        />
      ))}
    </div>
  );
};

export default ResourceCertificates;
