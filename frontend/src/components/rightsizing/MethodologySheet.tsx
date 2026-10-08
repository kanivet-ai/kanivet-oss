import React from 'react';
import { createPortal } from 'react-dom';
import type { RightsizingReport } from '../../types/rightsizing';
import { PROFILE_META, formatPct } from './rightsizingView';

/** How recommendations are made, in plain words, with this cluster's specifics. */
export const MethodologySheet: React.FC<{
  report: RightsizingReport;
  onClose: () => void;
}> = ({ report, onClose }) => {
  const p = PROFILE_META[report.profile];
  const cal = report.calibration;
  const sheet = (
    <div
      className="ap-overlay rs-overlay"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="ap-sheet rs-sheet rs-method"
        role="dialog"
        aria-modal="true"
        aria-labelledby="rs-method-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="rs-sheet-header">
          <h3 className="ap-sheet-title" id="rs-method-title">
            How recommendations are made
          </h3>
          <button
            type="button"
            className="ap-icon-btn"
            onClick={onClose}
            aria-label="Close"
            title="Close"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              aria-hidden="true"
            >
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>
        <div className="rs-sheet-body rs-method-body">
          <section>
            <h4>The data</h4>
            <p>
              {parseInt(report.window, 10)} days of history from{' '}
              {report.source.flavor || report.source.type} (
              {report.source.namespace}/{report.source.service}), sampled every{' '}
              {report.step}. Pods are grouped into workloads by name, so the
              history of pods replaced by rollouts counts too, and vcluster pods
              are traced back to their virtual workload. Only workloads running
              now are shown.
            </p>
          </section>
          <section>
            <h4>CPU: a quantile of replica-time</h4>
            <p>
              Every replica's CPU at every sample is pooled into one
              distribution, and the request is its {p.cpu} (
              {p.label.toLowerCase()} profile): usage stays under it{' '}
              {p.cpu.slice(1)}% of replica-time. Pooling keeps the answer the
              same whether a workload runs 2 replicas or 100; sizing for the
              busiest replica would not. A replica that is hot all the time is
              flagged separately, since a request can't fix uneven load.
              {report.signals.startupExclusion &&
                ' The first 10 minutes after a pod starts are left out and reported on their own.'}
            </p>
          </section>
          <section>
            <h4>Bursty workloads</h4>
            <p>
              Some workloads idle most of the time and do their real work in
              short bursts: backups, batch workers, admission webhooks. When CPU
              is busy less than 10% of the time and its peaks are 10× the
              median, a quantile of replica-time only describes the idling, and
              a request sized to it starves the bursts whenever the node is
              busy. For these the request covers the work, P90 of CPU while
              busy, and the cheaper idle-sized value is shown as the
              alternative.
            </p>
          </section>
          <section>
            <h4>Autoscaled workloads</h4>
            <p>
              An HPA that scales on CPU or memory utilisation divides usage by
              the request, so lowering the request alone just adds replicas.
              Request and target are recommended together: target × old ÷ new
              keeps today's replica counts with smaller pods. Targets stop at
              90%, which leaves the HPA room to react.
            </p>
          </section>
          <section>
            <h4>Memory: the peak, with headroom</h4>
            <p>
              Memory can't be throttled, only killed, so it is sized for the
              busiest replica's peak plus {p.mem} or {p.memFloor}, whichever is
              more, and the limit is set equal to the request. An OOM kill means
              the real peak was never measured, so the recommendation steps 25%
              above the limit it died at.
              {report.signals.memoryMetric === 'usage' &&
                ' This cluster only reports total memory usage, which includes reclaimable page cache: memory recommendations here only ever go down, never up, unless an OOM kill shows pressure.'}
            </p>
          </section>
          <section>
            <h4>Not fooled by change</h4>
            <p>
              Daily usage is scanned for a step change, such as a release that
              doubled CPU. A two-sample t-test on log values, corrected for
              scanning every split (family-wise 1%), has to find a shift of at
              least 30% before older data is dropped. A steady climb
              (Mann-Kendall, p &lt; 0.01) is projected a week ahead with a
              Theil-Sen slope.
            </p>
          </section>
          <section>
            <h4>Honest uncertainty</h4>
            <p>
              Samples five minutes apart are far from independent, so the CPU
              interval comes from resampling whole days (a block bootstrap, 400
              resamples, 90% interval). For memory, a Gumbel fit to the daily
              peaks gives the worst day to expect in a month. A workload is
              called over-provisioned only when even the pessimistic end is 20%
              below its request.
            </p>
          </section>
          <section>
            <h4>Checked on data it didn't see</h4>
            <p>
              Each recommendation is re-fitted without the most recent quarter
              of the window and scored on it.
              {cal.containers > 0
                ? ` In this cluster ${cal.calibrated.toLocaleString()} of ${cal.containers.toLocaleString()} containers (${formatPct(cal.calibrated / cal.containers, 0)}) held up: CPU stayed within twice its target and memory never went over.`
                : ' This needs at least 5 days of history.'}
            </p>
          </section>
          <section>
            <h4>Confidence</h4>
            <p>
              High: a week or more of data, 80%+ coverage, an interval within
              25% of the value, and a passed backtest. Medium: at least 3 days
              and an interval within 50%. Low: anything less.
            </p>
          </section>
        </div>
      </div>
    </div>
  );
  return createPortal(sheet, document.body);
};
