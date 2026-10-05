import React, { useState } from 'react';
import type { WorkloadReport } from '../../types/rightsizing';
import {
  ConfidenceMeter,
  HistorySourceState,
  ResourceDelta,
  VerdictBadge,
} from './RightsizingParts';
import { EvidenceSheet } from './lazyEvidenceSheet';
import { formatMoney, reasonTags, workloadId } from './rightsizingView';
import { Tooltip } from '../common/Tooltip';
import {
  useRightsizingPrefs,
  useRightsizingReport,
} from './useRightsizingReport';
import './Rightsizing.css';

interface Props {
  cluster: string;
  kind: string;
  namespace: string;
  name: string;
}

/** The workload's rightsizing verdict, read from the cluster report. */
export const WorkloadRightsizingCard: React.FC<Props> = ({
  cluster,
  kind,
  namespace,
  name,
}) => {
  const { profile, window } = useRightsizingPrefs();
  const { report, refresh, reload } = useRightsizingReport(
    cluster,
    profile,
    window,
  );
  const [open, setOpen] = useState(false);

  if (!report || report.status === 'computing') {
    return (
      <div className="rs-card rs-card-muted">
        <div className="ap-spinner" />
        <span>
          Analysing {parseInt(window, 10)} days of usage history
          {report?.progress && report.progress.total > 0
            ? ` (${report.progress.done} of ${report.progress.total} namespaces)`
            : ''}
          …
        </span>
      </div>
    );
  }
  if (
    report.status === 'no-history-source' ||
    report.status === 'needs-tenant' ||
    report.status === 'no-container-data'
  ) {
    return (
      <HistorySourceState
        report={report}
        cluster={cluster}
        onRetry={refresh}
        compact
      />
    );
  }
  if (report.status === 'error') {
    return (
      <div className="rs-card rs-card-muted">
        Couldn't compute recommendations: {report.error}
      </div>
    );
  }

  // Detail views name vcluster workloads by their virtual namespace; reports
  // do too in vcluster scope.
  // An exact namespace match wins; the virtual namespace only names
  // vcluster workloads seen from the host.
  const same = report.workloads.filter(
    (x) => x.kind === kind && x.name === name,
  );
  const w: WorkloadReport | undefined =
    same.find((x) => x.namespace === namespace && !x.vclusterNamespace) ??
    same.find((x) => x.namespace === namespace) ??
    same.find((x) => x.vclusterNamespace === namespace);
  if (!w) {
    return (
      <div className="rs-card rs-card-muted">
        No running pods for this workload in the latest report.
      </div>
    );
  }
  const { tags } = reasonTags(w, 5);
  return (
    <div className="rs-card">
      <div className="rs-card-top">
        <VerdictBadge verdict={w.verdict} />
        {w.verdict !== 'insufficient-data' && (
          <ConfidenceMeter level={w.confidence} />
        )}
        {w.priced && w.monthlySavings > 0 && (
          <span className="rs-money-save">
            Saves {formatMoney(w.monthlySavings)}/mo
          </span>
        )}
        {w.priced && w.monthlySavings < 0 && (
          <span className="rs-money-add">
            Needs {formatMoney(-w.monthlySavings)}/mo more
          </span>
        )}
      </div>
      <div className="rs-finding-tag-row">
        {tags.map((t) => (
          <Tooltip key={t.title} content={t.message}>
            <span className={`rs-tag rs-tag-${t.severity}`}>{t.title}</span>
          </Tooltip>
        ))}
        <button
          className="ap-btn ap-btn--sm rs-card-open"
          onClick={() => setOpen(true)}
        >
          Evidence
        </button>
      </div>
      <div className="rs-card-rows">
        {w.containers.map((c) => (
          <div key={c.container} className="rs-card-row">
            <span className="rs-card-container">{c.container}</span>
            <span className="rs-card-res">
              <span className="rs-muted">CPU</span>{' '}
              <ResourceDelta
                resource="cpu"
                from={c.cpu.request}
                to={c.cpu.recommended}
              />
            </span>
            <span className="rs-card-res">
              <span className="rs-muted">Memory</span>{' '}
              <ResourceDelta
                resource="memory"
                from={c.memory.request}
                to={c.memory.recommended}
              />
            </span>
          </div>
        ))}
      </div>
      {w.change && (
        <div className={`rs-card-change ${w.change.healthy ? 'ok' : 'bad'}`}>
          {w.change.summary}
        </div>
      )}
      {open && (
        <EvidenceSheet
          key={workloadId(w)}
          cluster={cluster}
          workload={w}
          profile={profile}
          window={window}
          onClose={() => setOpen(false)}
          onChanged={reload}
        />
      )}
    </div>
  );
};
