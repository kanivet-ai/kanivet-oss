import React, { useEffect, useState } from 'react';
import { getHealthAudit } from '../../services/api/health';
import { useIssues } from './healthStore';
import { openHealthPage } from './healthActions';
import { IssueRow, ScoreRing } from './HealthParts';
import './Health.css';

/** The overview's health card: open issues by severity, the top few, and the audit score. */
const HealthSummaryCard: React.FC<{ cluster: string }> = ({ cluster }) => {
  const { report, error } = useIssues(cluster);
  const [score, setScore] = useState<number | null>(null);
  useEffect(() => {
    let live = true;
    getHealthAudit(cluster).then((r) => { if (live) setScore(r.score); }).catch(() => {});
    return () => { live = false; };
  }, [cluster]);

  if (error && !report) return null;
  const s = report?.summary;
  return (
    <div className="health-summary">
      <div className="ap-card health-summary-card">
        <button type="button" className="health-summary-stats" onClick={() => void openHealthPage(cluster, 'issues')} title="Open Issues">
          <div className="health-summary-title">Issues</div>
          <div className="health-summary-counts">
            <span className="health-summary-count"><span className="ap-dot ap-dot--danger" /><b className="ap-tnum">{s ? s.critical : '–'}</b> critical</span>
            <span className="health-summary-count"><span className="ap-dot ap-dot--warning" /><b className="ap-tnum">{s ? s.warning : '–'}</b> warning</span>
            <span className="health-summary-count"><span className="ap-dot ap-dot--info" /><b className="ap-tnum">{s ? s.info : '–'}</b> info</span>
          </div>
        </button>
        <div className="health-summary-top">
          {report && report.issues.length === 0 && <div className="health-summary-ok">Nothing is broken right now.</div>}
          {report?.issues.slice(0, 3).map((i) => <IssueRow key={i.id} cluster={cluster} issue={i} />)}
        </div>
        <button type="button" className="health-summary-score" onClick={() => void openHealthPage(cluster, 'audit')} title="Open Audit">
          {score !== null ? <ScoreRing score={score} size={52} label="Audit score" /> : <div className="ap-skeleton health-ring-skeleton" />}
          <span className="health-summary-title">Audit</span>
        </button>
      </div>
    </div>
  );
};

export default HealthSummaryCard;
