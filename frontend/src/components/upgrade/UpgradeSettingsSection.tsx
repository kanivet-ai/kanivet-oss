import { useEffect, useState } from 'react';
import { getUpgradeReport } from '../../services/api/upgrade';
import type { UpgradeReport } from '../../types/upgrade';
import { openUpgradeReadiness } from './openUpgradeReadiness';
import './UpgradeReadiness.css';

/** Cluster settings entry: the version and a one-line readiness for the next minor. */
export default function UpgradeSettingsSection({ cluster }: { cluster: string }) {
  const [report, setReport] = useState<UpgradeReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    getUpgradeReport(cluster, '')
      .then((r) => alive && setReport(r))
      .catch((e: any) => alive && setError(e?.response?.data?.error || e?.message || 'Could not check'));
    return () => { alive = false; };
  }, [cluster]);

  const s = report?.summary;
  return (
    <div className="upgrade-settings-entry">
      <div className="ap-card">
        <div className="ap-card-row">
          <span>Kubernetes version</span>
          <span className="ap-card-row-value ap-tnum" title={report?.serverGitVersion}>{report?.currentVersion || (error ? 'Unknown' : '…')}</span>
        </div>
        <div className="ap-card-row">
          <span>Next release{report?.targetVersion ? ` (${report.targetVersion})` : ''}</span>
          <span className="ap-card-row-value">
            {error
              ? error
              : !s
                ? 'Checking…'
                : s.verdict === 'ready'
                  ? 'Ready'
                  : `${s.blockers} blocker${s.blockers === 1 ? '' : 's'}, ${s.warnings} warning${s.warnings === 1 ? '' : 's'}`}
          </span>
        </div>
      </div>
      <div>
        <button type="button" className="ap-btn ap-btn--primary" onClick={() => void openUpgradeReadiness(cluster)}>
          Open upgrade readiness
        </button>
      </div>
    </div>
  );
}
