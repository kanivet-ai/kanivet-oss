import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  ArrowLeftIcon,
  ExclamationTriangleIcon,
  InfoCircledIcon,
  MixIcon,
  ReloadIcon,
} from '@radix-ui/react-icons';
import api from '../../services/api';
import { parseClusterName } from '../../utils/clusterUtils';
import {
  ClusterCostSummary,
  FinOpsDashboardData,
  NamespaceCost,
  formatCost,
  formatPercent,
} from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import { PricingSourceBadge } from './CostBadge';
import { LastCalculatedBadge } from './LastCalculatedBadge';
import { EfficiencyMeter } from './EfficiencyMeter';
import { IdleCostCard } from './IdleCostCard';
import { EfficiencyExplainer } from './EfficiencyExplainer';
import {
  DEFAULT_FINOPS_FILTERS,
  FinOpsFilters,
  FinOpsFilterState,
} from './FinOpsFilters';
import { FinOpsLoadingSkeleton } from './FinOpsLoadingSkeleton';
import { NamespaceCostTable } from './NamespaceCostTable';
import { ClusterNodeTable, VClusterHostNodeTable } from './NodeCostTable';
import { SavingsOpportunities } from './SavingsOpportunities';
import { filterNamespaces, filterNodes, isFiltered as filtersActive } from './finopsView';
import { useFinOpsNavigation } from './useFinOpsNavigation';
import { EvidenceSheet } from '../rightsizing/lazyEvidenceSheet';
import { useRightsizingPrefs, useRightsizingReport } from '../rightsizing/useRightsizingReport';
import { potentialSavings, rightsizingRecommendations, rightsizingSavingsIndex, workloadId } from '../rightsizing/rightsizingView';
import type { WorkloadReport } from '../../types/rightsizing';
import './FinOpsDashboard.css';

const POLL_MS = 60_000;
/** While prices are still downloading, poll faster so costs appear as soon as they land. */
const PRICING_POLL_MS = 10_000;
const PRICING_POLL_LIMIT = 30;

/** The last dashboard per cluster. The tab unmounts when left, so a revisit
 * shows these costs at once and refreshes them behind, instead of a
 * skeleton. Bounded: a few clusters at most. */
const held = new Map<string, FinOpsDashboardData>();
const HELD_MAX = 4;

function remember(cluster: string, data: FinOpsDashboardData) {
  held.delete(cluster);
  held.set(cluster, data);
  if (held.size > HELD_MAX) held.delete(held.keys().next().value!);
}

interface Segment {
  key: string;
  label: string;
  cost: number;
  className: string;
  description?: string;
  detail?: string;
}

const CostDistribution: React.FC<{
  segments: Segment[];
  total: number;
  filtered: boolean;
}> = ({ segments, total, filtered }) => {
  const visible = segments.filter((s) => s.cost > 0);
  if (total <= 0 || visible.length === 0) return null;
  return (
    <div className="cost-allocation">
      <div className="allocation-label">
        Where the money goes
        {filtered && <span className="filtered-indicator"> (filtered)</span>}
      </div>
      <div className="allocation-bar" role="img" aria-label="Cost distribution">
        {visible.map((s) => {
          const pct = (s.cost / total) * 100;
          return (
            <Tooltip
              key={s.key}
              side="top"
              content={
                <div className="alloc-tooltip">
                  <div className="alloc-tooltip-title">{s.label}</div>
                  <div className="alloc-tooltip-row">
                    <span>Cost</span>
                    <span>{formatCost(s.cost)}/mo</span>
                  </div>
                  <div className="alloc-tooltip-row">
                    <span>Share</span>
                    <span>{formatPercent(pct)}</span>
                  </div>
                  {s.detail && (
                    <div className="alloc-tooltip-row">
                      <span>Pods</span>
                      <span>{s.detail}</span>
                    </div>
                  )}
                  {s.description && (
                    <div className="alloc-tooltip-desc">{s.description}</div>
                  )}
                </div>
              }
            >
              <div
                className={`allocation-segment ${s.className}`}
                style={{ width: `${pct}%` }}
              />
            </Tooltip>
          );
        })}
      </div>
      <div className="allocation-legend">
        {visible.map((s) => (
          <span key={s.key} className="legend-item">
            <span className={`legend-dot ${s.className}`} />
            <span className="legend-label">{s.label}</span>
          </span>
        ))}
      </div>
    </div>
  );
};

function buildSegments(
  namespaces: NamespaceCost[],
  summary: ClusterCostSummary,
  filtered: boolean,
): Segment[] {
  const isVCluster = summary.scope === 'vcluster';
  const top = namespaces.slice(0, 5);
  const rest = namespaces.slice(5);
  const segments: Segment[] = top.map((ns, i) => ({
    key: `ns:${ns.namespace}`,
    label: ns.vcluster ? `${ns.namespace} (vcluster)` : ns.namespace,
    cost: ns.monthlyCost,
    className: `seg-${i}`,
    detail: String(ns.podCount),
  }));
  if (rest.length > 0) {
    segments.push({
      key: 'other',
      label: `${rest.length} other namespace${rest.length === 1 ? '' : 's'}`,
      cost: rest.reduce((sum, ns) => sum + ns.monthlyCost, 0),
      className: 'seg-other',
    });
  }
  if (!filtered && !isVCluster) {
    segments.push({
      key: 'idle',
      label: 'Idle capacity',
      cost: summary.idleCost,
      className: 'seg-idle',
      description: 'Node capacity no pod requests',
    });
  }
  if (!filtered) {
    segments.push({
      key: 'cp',
      label: isVCluster ? 'vcluster control plane' : 'Control plane fee',
      cost: summary.breakdown.controlPlaneCost,
      className: 'seg-cp',
      description: isVCluster
        ? `The vcluster's API server and etcd pods in host namespace ${summary.vcluster?.namespace ?? ''}`
        : 'Managed Kubernetes control plane fee',
    });
  }
  return segments;
}

interface FinOpsDashboardProps {
  cluster: string;
}

const FinOpsDashboard: React.FC<FinOpsDashboardProps> = ({ cluster }) => {
  const [dashboard, setDashboard] = useState<FinOpsDashboardData | null>(
    () => held.get(cluster) ?? null,
  );
  const [loading, setLoading] = useState(() => !held.has(cluster));
  const [refreshing, setRefreshing] = useState(() => held.has(cluster));
  const [error, setError] = useState<string | null>(null);
  const [showExplainer, setShowExplainer] = useState(false);
  const [filters, setFilters] = useState<FinOpsFilterState>(
    DEFAULT_FINOPS_FILTERS,
  );

  const abortRef = useRef<AbortController | null>(null);
  const pollTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pollSkipped = useRef(false);
  const pricingPolls = useRef(0);
  const nav = useFinOpsNavigation(cluster);

  const load = useCallback(
    async (refresh = false) => {
      if (!cluster) return;
      // A hidden window doesn't poll: nobody is looking, and with the poll as
      // long as the server's cache, each one recomputes the dashboard. Coming
      // back into view runs the poll it skipped.
      pollSkipped.current = !refresh && document.hidden;
      if (pollSkipped.current) {
        if (pollTimer.current) clearTimeout(pollTimer.current);
        pollTimer.current = null;
        return;
      }
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      const isCurrent = () => abortRef.current === ctrl;
      if (pollTimer.current) clearTimeout(pollTimer.current);
      if (refresh) setRefreshing(true);

      const acc: Partial<FinOpsDashboardData> = {};
      let streamError: string | null = null;
      try {
        await api.streamFinOpsDashboard(
          cluster,
          (type, data) => {
            if (!isCurrent()) return;
            if (type === 'error') {
              streamError =
                typeof data === 'string' ? data : 'Failed to load cost data';
              return;
            }
            if (
              type === 'summary' ||
              type === 'nodes' ||
              type === 'namespaces' ||
              type === 'recommendations'
            ) {
              (acc as any)[type] = data ?? [];
              if (acc.summary) {
                // What hasn't streamed in yet keeps the costs already shown.
                const prev = held.get(cluster);
                const next = {
                  summary: acc.summary,
                  nodes: acc.nodes ?? prev?.nodes ?? [],
                  namespaces: acc.namespaces ?? prev?.namespaces ?? [],
                  recommendations:
                    acc.recommendations ?? prev?.recommendations ?? [],
                };
                remember(cluster, next);
                setDashboard(next);
              }
            }
          },
          ctrl.signal,
          refresh,
        );
        if (isCurrent()) setError(streamError);
      } catch (err: any) {
        if (isCurrent() && err?.name !== 'AbortError')
          setError(err?.message || 'Failed to load cost data');
      } finally {
        if (isCurrent()) {
          setLoading(false);
          setRefreshing(false);
          const pricing = acc.summary?.pricingInfo;
          const pricingPending =
            !!pricing && pricing.supported && pricing.nodesMissingPrice > 0;
          let delay = POLL_MS;
          if (pricingPending && pricingPolls.current < PRICING_POLL_LIMIT) {
            pricingPolls.current++;
            delay = PRICING_POLL_MS;
          } else if (!pricingPending) {
            pricingPolls.current = 0;
          }
          pollTimer.current = setTimeout(() => load(), delay);
        }
      }
    },
    [cluster],
  );

  useEffect(() => {
    if (!cluster) return;
    api.preloadFinOpsPricing(cluster).catch(() => {});
    const last = held.get(cluster) ?? null;
    setDashboard(last);
    setLoading(!last);
    setRefreshing(!!last);
    setError(null);
    setFilters(DEFAULT_FINOPS_FILTERS);
    pricingPolls.current = 0;
    load();
    const onVisible = () => {
      if (!document.hidden && pollSkipped.current) load();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      if (pollTimer.current) clearTimeout(pollTimer.current);
      pollTimer.current = null;
      pollSkipped.current = false;
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [cluster, load]);

  // Request sizing comes from the rightsizing engine, which reads usage
  // history; FinOps prices and lists what it found.
  const rsPrefs = useRightsizingPrefs();
  const { report: rsReport, reload: reloadRightsizing } = useRightsizingReport(
    cluster,
    rsPrefs.profile,
    rsPrefs.window,
  );
  const [evidenceFor, setEvidenceFor] = useState<WorkloadReport | null>(null);
  const rsReady = rsReport?.status === 'ready';
  const rsIndex = useMemo(
    () => rightsizingSavingsIndex(rsReady ? rsReport!.workloads : []),
    [rsReady, rsReport],
  );

  const summary = dashboard?.summary;
  const namespaces = dashboard?.namespaces ?? [];
  const nodes = dashboard?.nodes ?? [];
  const recommendations = useMemo(
    () => [
      ...rightsizingRecommendations(rsReady ? rsReport!.workloads : []),
      ...(dashboard?.recommendations ?? []),
    ],
    [rsReady, rsReport, dashboard?.recommendations],
  );
  const isVCluster = summary?.scope === 'vcluster';
  const filtered = filtersActive(filters);

  const filteredNamespaces = useMemo(
    () => filterNamespaces(namespaces, filters),
    [namespaces, filters],
  );
  const filteredNodes = useMemo(
    () => filterNodes(nodes, filters),
    [nodes, filters],
  );
  const controlPlane = useMemo<NamespaceCost[]>(() => {
    const cp = summary?.vcluster?.controlPlane ?? [];
    if (!isVCluster || cp.length === 0) return [];
    const total = cp.reduce((s, w) => s + w.monthlyCost, 0);
    return [
      {
        namespace: summary!.vcluster!.namespace,
        podCount: cp.reduce((s, w) => s + w.replicas, 0),
        cpuRequest: cp.reduce((s, w) => s + w.cpuRequest, 0),
        memoryRequest: cp.reduce((s, w) => s + w.memoryRequest, 0),
        cpuLimit: 0,
        memoryLimit: 0,
        hourlyCost: total / 720,
        dailyCost: total / 30,
        monthlyCost: total,
        cpuEfficiency: 0,
        memoryEfficiency: 0,
        overallEfficiency: 0,
        topWorkloads: cp,
      },
    ];
  }, [summary, isVCluster]);

  const monthly = filtered
    ? filteredNamespaces.reduce((s, ns) => s + ns.monthlyCost, 0)
    : (summary?.monthlyCost ?? 0);
  // The report's own total, not the listed rows': those leave out the long
  // tail of small savings, which can add up.
  const { total: savings, smaller } = potentialSavings(
    rsReady ? rsReport!.summary : undefined,
    recommendations,
  );
  const opportunities = [
    recommendations.length > 0 &&
      `${recommendations.length} opportunit${recommendations.length === 1 ? 'y' : 'ies'} below`,
    smaller > 0 &&
      `${smaller} smaller workload${smaller === 1 ? '' : 's'} in Rightsizing`,
  ]
    .filter(Boolean)
    .join(' · ');
  const segments = useMemo(
    () => (summary ? buildSegments(filteredNamespaces, summary, filtered) : []),
    [filteredNamespaces, summary, filtered],
  );

  if (!cluster) {
    return (
      <div className="finops-dashboard finops-empty">
        <MixIcon />
        <p>Select a cluster to see its costs</p>
      </div>
    );
  }

  const clusterInfo = parseClusterName(cluster);
  const hostName = summary?.vcluster
    ? parseClusterName(summary.vcluster.host).displayName
    : '';
  const pricing = summary?.pricingInfo;
  const pricingPending =
    !!pricing && pricing.supported && pricing.nodesMissingPrice > 0;
  const unsupported = !!pricing && !pricing.supported;

  return (
    <div className="finops-dashboard">
      <div className="finops-header">
        <div className="finops-title">
          <MixIcon />
          <h2>Costs</h2>
          {summary && (
            <span className="finops-total-cost">
              {formatCost(monthly)}/mo
              {filtered && <span className="filtered-badge">filtered</span>}
            </span>
          )}
          {isVCluster && summary?.vcluster && (
            <span className="finops-scope">
              vcluster <strong>{summary.vcluster.name}</strong> on {hostName}
            </span>
          )}
        </div>
        <div className="finops-actions">
          {summary?.lastUpdated && (
            <LastCalculatedBadge
              lastUpdated={summary.lastUpdated}
              cacheDuration={60}
            />
          )}
          {pricing && <PricingSourceBadge pricingInfo={pricing} />}
          {summary && (
            <Tooltip content="How these costs are calculated">
              <button
                className="finops-refresh"
                onClick={() => setShowExplainer(true)}
                aria-label="How these costs are calculated"
              >
                <InfoCircledIcon />
              </button>
            </Tooltip>
          )}
          <Tooltip content="Recalculate now">
            <button
              className="finops-refresh"
              onClick={() => load(true)}
              disabled={refreshing || loading}
              aria-label="Recalculate costs"
            >
              <ReloadIcon className={refreshing ? 'spinning' : undefined} />
            </button>
          </Tooltip>
        </div>
      </div>

      {error && (
        <div className="finops-error" role="alert">
          <ExclamationTriangleIcon />
          <span>{error}</span>
          <button className="finops-banner-action" onClick={() => load(true)}>
            Retry
          </button>
        </div>
      )}
      {pricingPending && (
        <div className="finops-error finops-info" role="status">
          <InfoCircledIcon />
          <span>
            Fetching prices for {pricing!.nodesMissingPrice} of{' '}
            {pricing!.nodesWithPricing + pricing!.nodesMissingPrice} nodes. The
            first download of a region's price list can take a few minutes;
            costs fill in on their own.
          </span>
        </div>
      )}
      {unsupported && (
        <div className="finops-error finops-info" role="status">
          <InfoCircledIcon />
          <span>
            There's no public price list for{' '}
            {summary!.provider === 'Unknown'
              ? 'this cluster’s infrastructure'
              : summary!.provider}{' '}
            yet, so costs show as $0. Requests, allocation and usage below are
            still accurate.
          </span>
        </div>
      )}

      <div className="finops-content">
        {loading && !dashboard ? (
          <FinOpsLoadingSkeleton />
        ) : dashboard && summary ? (
          <div className="finops-body">
            <FinOpsFilters
              filters={filters}
              onFiltersChange={setFilters}
              stats={{
                totalNamespaces: namespaces.length,
                totalNodes: nodes.length,
                filteredNamespaces: filteredNamespaces.length,
                filteredNodes: filteredNodes.length,
              }}
              availableNamespaces={namespaces.map((ns) => ns.namespace)}
              usageAvailable={summary.usageAvailable}
            />

            <div className="finops-summary">
              <div className="finops-stats">
                <div className="stat-card primary">
                  <div className="stat-label">
                    Monthly
                    {filtered && (
                      <span className="filtered-indicator"> (filtered)</span>
                    )}
                  </div>
                  <div className="stat-value">{formatCost(monthly)}</div>
                  <div className="stat-sub">
                    {formatCost(monthly / 30)}/day · {formatCost(monthly * 12)}
                    /yr
                  </div>
                </div>

                {isVCluster && summary.vcluster && (
                  <div className="stat-card">
                    <div className="stat-label">Share of host</div>
                    <div className="stat-value">
                      {formatPercent(summary.vcluster.sharePercent)}
                    </div>
                    <div className="stat-sub">
                      of {formatCost(summary.vcluster.hostMonthlyCost)}/mo ·{' '}
                      <button
                        className="stat-link"
                        onClick={() =>
                          nav.openHostCosts(summary.vcluster!.host)
                        }
                      >
                        <ArrowLeftIcon /> host costs
                      </button>
                    </div>
                  </div>
                )}

                {isVCluster && (
                  <div className="stat-card">
                    <div className="stat-label">Control plane</div>
                    <div className="stat-value">
                      {formatCost(summary.breakdown.controlPlaneCost)}
                    </div>
                    <div className="stat-sub">API server and etcd pods</div>
                  </div>
                )}

                <div className={`stat-card${savings > 0 ? ' savings' : ''}`}>
                  <div className="stat-label">
                    Potential savings
                    <Tooltip
                      content={
                        rsReady
                          ? `Rightsizing requests to ${rsReport!.window} of usage history, plus consolidating underused nodes`
                          : rsReport?.status === 'no-history-source'
                            ? 'Consolidating underused nodes. Rightsizing needs Prometheus or Mimir history.'
                            : 'Consolidating underused nodes. Rightsizing is still reading usage history.'
                      }
                    >
                      <InfoCircledIcon className="stat-info" />
                    </Tooltip>
                  </div>
                  <div className="stat-value">
                    {savings > 0 ? formatCost(savings) : '—'}
                  </div>
                  <div className="stat-sub">
                    {!rsReady &&
                    rsReport &&
                    (rsReport.status === 'computing' || rsReport.progress)
                      ? 'Rightsizing is reading usage history…'
                      : opportunities || 'Nothing obvious to trim'}
                  </div>
                </div>
              </div>

              {!isVCluster && summary.idleCost > 0 && !filtered && (
                <IdleCostCard
                  idleCost={summary.idleCost}
                  idlePercentage={summary.idlePercentage}
                />
              )}

              {!isVCluster && summary.totalCpu > 0 && (
                <EfficiencyMeter
                  cpuEfficiency={summary.cpuEfficiency}
                  memoryEfficiency={summary.memoryEfficiency}
                  overallEfficiency={summary.overallEfficiency}
                  onExplainClick={() => setShowExplainer(true)}
                />
              )}

              <CostDistribution
                segments={segments}
                total={monthly}
                filtered={filtered}
              />
            </div>

            {summary.spotAtOnDemandCount > 0 && !unsupported && (
              <div className="finops-footnote">
                <InfoCircledIcon />
                <span>
                  {summary.spotAtOnDemandCount} spot node
                  {summary.spotAtOnDemandCount === 1 ? ' is' : 's are'} priced
                  at on-demand list price (
                  {formatCost(summary.spotAtOnDemandCost)}/mo
                  {isVCluster ? ' of this vcluster’s cost' : ''}), because the
                  price list has no spot rates. The real cost of{' '}
                  {summary.spotAtOnDemandCount === 1
                    ? 'that node'
                    : 'those nodes'}{' '}
                  is usually lower.
                </span>
              </div>
            )}

            <SavingsOpportunities
              recommendations={recommendations}
              onOpenWorkload={nav.openWorkload}
              onOpenNode={isVCluster ? undefined : nav.openNode}
              onOpenRightsizing={setEvidenceFor}
            />

            <div className="finops-section">
              <div className="section-header">
                <h3>
                  {isVCluster
                    ? 'Cost by vcluster Namespace'
                    : 'Cost by Namespace'}
                </h3>
                <span className="section-count">
                  {filteredNamespaces.length}
                  {filteredNamespaces.length !== namespaces.length &&
                    ` of ${namespaces.length}`}{' '}
                  namespaces
                </span>
              </div>
              <div className="section-content">
                <NamespaceCostTable
                  namespaces={filteredNamespaces}
                  totalCost={monthly}
                  nodes={nodes}
                  emptyMessage={
                    namespaces.length === 0
                      ? 'No running pods'
                      : 'No namespaces match your filters'
                  }
                  onOpenWorkload={nav.openWorkload}
                  onOpenVCluster={
                    isVCluster ? undefined : nav.openVClusterCosts
                  }
                  showUsage={summary.usageAvailable}
                  workloadSavings={rsIndex.workload}
                  namespaceSavings={rsIndex.namespace}
                />
              </div>
            </div>

            {controlPlane.length > 0 && (
              <div className="finops-section">
                <div className="section-header">
                  <h3>vcluster Control Plane</h3>
                  <span className="section-count">
                    host namespace {summary.vcluster!.namespace}
                  </span>
                </div>
                <div className="section-content">
                  <NamespaceCostTable
                    namespaces={controlPlane}
                    totalCost={monthly}
                    nodes={nodes}
                    emptyMessage="No control plane pods"
                    hideShare
                    showUsage={summary.usageAvailable}
                    nameHeader="Host Namespace / Workload / Pod"
                  />
                </div>
              </div>
            )}

            <div className="finops-section">
              <div className="section-header">
                <h3>{isVCluster ? 'Host Nodes' : 'Infrastructure'}</h3>
                <span className="section-count">
                  {filteredNodes.length}
                  {filteredNodes.length !== nodes.length &&
                    ` of ${nodes.length}`}{' '}
                  nodes
                  {isVCluster && ' shared with the host'}
                </span>
              </div>
              <div className="section-content">
                {isVCluster ? (
                  <VClusterHostNodeTable nodes={filteredNodes} />
                ) : (
                  <ClusterNodeTable
                    nodes={filteredNodes}
                    onOpenNode={nav.openNode}
                  />
                )}
              </div>
            </div>
          </div>
        ) : !error ? (
          <div className="finops-empty">
            <MixIcon />
            <p>No cost data for {clusterInfo.displayName}</p>
          </div>
        ) : null}
      </div>

      {evidenceFor && (
        <EvidenceSheet
          key={workloadId(evidenceFor)}
          cluster={cluster}
          workload={evidenceFor}
          profile={rsPrefs.profile}
          window={rsPrefs.window}
          onClose={() => setEvidenceFor(null)}
          onChanged={reloadRightsizing}
        />
      )}
      {showExplainer && summary && (
        <EfficiencyExplainer
          summary={summary}
          onClose={() => setShowExplainer(false)}
        />
      )}
    </div>
  );
};

export default FinOpsDashboard;
