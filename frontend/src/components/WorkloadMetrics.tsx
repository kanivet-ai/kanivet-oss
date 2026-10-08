import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Line } from 'react-chartjs-2';
import api from '../services/api';
import {
  startWorkloadMetricsStream,
  type WorkloadSeries,
} from '../services/api/metrics';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import MonitoringSettingsModal from './MonitoringSettingsModal';
import { registerMetricsChart, useChartTheme, withAlpha, type ChartTheme } from './metrics/chartTheme';
import { referenceDataset, useLineChartOptions } from './metrics/chartOptions';
import { METRIC_TITLES, describeTimeRange, type MetricType, type TimeRange } from './metrics/metricsFormat';
import { providerChipLabel, providerDetail, requestedProvider } from './metrics/metricsProvider';
import { useMetricsProvider } from './metrics/useMetricsProvider';
import { MetricsToolbar } from './metrics/MetricsToolbar';
import { MetricsHeadline } from './metrics/MetricsHeadline';
import { MetricsChartFrame, type ChartFrameState } from './metrics/MetricsChartFrame';
import { MetricsProviderState } from './metrics/MetricsProviderState';
import {
  perPodReference,
  sumPods,
  totalRequest,
} from './metrics/workloadSeries';
import './PodMetrics.css';
import './WorkloadMetrics.css';

registerMetricsChart();

interface WorkloadMetricsProps {
  cluster: string;
  kind: string;
  namespace: string;
  name: string;
}

interface PodInfo {
  name: string;
  status: string;
  node: string;
  containers: string[];
  ready: boolean;
  restarts: number;
  age: string;
  resourceLimits: { cpu: number; memory: number };
  resourceRequests: { cpu: number; memory: number };
}

// Series colours: the system palette, read from the tokens at runtime
const POD_COLOR_KEYS: (keyof ChartTheme)[] = ['blue', 'green', 'orange', 'red', 'purple', 'pink', 'teal', 'yellow', 'indigo', 'gray'];

const podColor = (theme: ChartTheme, index: number): string => theme[POD_COLOR_KEYS[index % POD_COLOR_KEYS.length]];

// Maximum pods drawn at once, for legibility and stream count
const MAX_PODS_DISPLAYED = 8;
// Legend chips shown before "+N more"
const MAX_LEGEND_CHIPS = 20;

const REFERENCE_LABELS = ['Limit per pod', 'Request per pod'];

const shortPodName = (fullName: string): string => {
  const parts = fullName.split('-');
  return parts.length >= 2 ? parts.slice(-2).join('-') : fullName.slice(-15);
};

export const WorkloadMetrics: React.FC<WorkloadMetricsProps> = ({ cluster, kind, namespace, name }) => {
  const { monitoringSettings } = useStore(useShallow((s) => ({ monitoringSettings: s.monitoringSettingsByCluster[cluster] || s.monitoringSettings })));
  const theme = useChartTheme();
  const providerState = useMetricsProvider(cluster, monitoringSettings.preferredProvider);
  const { phase, provider, reason, revision, markUnavailable } = providerState;

  const [pods, setPods] = useState<PodInfo[]>([]);
  const [podsLoading, setPodsLoading] = useState(true);
  const [podsError, setPodsError] = useState<string | null>(null);
  const [selectedMetric, setSelectedMetric] = useState<MetricType>('cpu');
  const [selectedTimeRange, setSelectedTimeRange] = useState<TimeRange>('15m');
  const [series, setSeries] = useState<WorkloadSeries | null>(null);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [isZoomed, setIsZoomed] = useState(false);
  const [visiblePods, setVisiblePods] = useState<Set<string>>(new Set());
  const [streamRun, setStreamRun] = useState(0);
  const chartRef = useRef<any>(null);
  const podsRequest = useRef(0);

  const ready = phase === 'ready';
  const preferred = requestedProvider(monitoringSettings.preferredProvider);

  // Pods of the workload. Only the latest request may land: switching
  // workloads quickly must not show the previous one's pods.
  const fetchPods = useCallback(async () => {
    const request = ++podsRequest.current;
    setPodsLoading(true);
    setPodsError(null);
    try {
      const result = await api.getWorkloadPods(cluster, kind, namespace, name);
      if (request !== podsRequest.current) return;
      setPods(result.pods);
      setVisiblePods(
        new Set(result.pods.slice(0, MAX_PODS_DISPLAYED).map((p) => p.name)),
      );
      setSeries(null);
    } catch (err: any) {
      if (request !== podsRequest.current) return;
      setPodsError(err?.message || 'Failed to fetch pods');
    } finally {
      if (request === podsRequest.current) setPodsLoading(false);
    }
  }, [cluster, kind, namespace, name]);

  useEffect(() => {
    void fetchPods();
  }, [fetchPods]);

  // The pods drawn, in workload order.
  const streamedPods = useMemo(
    () =>
      pods
        .filter((p) => visiblePods.has(p.name))
        .slice(0, MAX_PODS_DISPLAYED)
        .map((p) => p.name),
    [pods, visiblePods],
  );
  const streamedKey = streamedPods.join(',');

  // One stream for all of them: one store query and one message per
  // refresh. The last series stays until replaced, so toggling a pod or
  // switching metric does not blank the chart.
  useEffect(() => {
    if (!ready || !streamedKey) return;
    setStreamError(null);
    return startWorkloadMetricsStream(
      cluster,
      namespace,
      streamedKey.split(','),
      selectedMetric,
      selectedTimeRange,
      (data) => {
        setSeries(data);
        setStreamError(null);
      },
      (err) => {
        if (api.isMetricsProviderUnavailableError(err)) {
          setSeries(null);
          markUnavailable(err);
          return;
        }
        setStreamError(err);
      },
      preferred,
      2,
    );
  }, [
    ready,
    streamedKey,
    cluster,
    namespace,
    selectedMetric,
    selectedTimeRange,
    preferred,
    revision,
    streamRun,
    markUnavailable,
  ]);

  useEffect(() => {
    setSeries(null);
    setIsZoomed(false);
  }, [selectedMetric, selectedTimeRange, preferred, revision]);

  const handleRefresh = () => {
    setSeries(null);
    setIsZoomed(false);
    setStreamRun((n) => n + 1);
  };

  const handleResetZoom = useCallback(() => {
    const chart = chartRef.current;
    if (chart && typeof chart.resetZoom === 'function') {
      chart.resetZoom();
      setIsZoomed(false);
    }
  }, []);

  const onZoomComplete = useCallback(() => setIsZoomed(true), []);

  const togglePodVisibility = (podName: string) => {
    setVisiblePods((prev) => {
      const next = new Set(prev);
      if (next.has(podName)) next.delete(podName);
      else if (next.size < MAX_PODS_DISPLAYED) next.add(podName);
      return next;
    });
  };

  const labels = series?.labels;

  // Each line is one pod, so the reference lines are one pod's limit and
  // request.
  const { limit, request: podRequest } = useMemo(
    () =>
      perPodReference(
        pods.filter((p) => visiblePods.has(p.name)),
        selectedMetric,
      ),
    [pods, visiblePods, selectedMetric],
  );

  // The headline: the visible pods' total, read against those same pods'
  // requests.
  const headline = useMemo(() => {
    if (!series || !labels) return undefined;
    const { values, counted } = sumPods(
      series.pods,
      streamedPods,
      labels.length,
    );
    if (counted.length === 0) return undefined;
    return {
      values,
      request: totalRequest(pods, counted, selectedMetric),
    };
  }, [series, labels, streamedPods, pods, selectedMetric]);

  const chartData = useMemo(() => {
    const datasets: any[] = [];
    const length = labels?.length || 0;
    pods.forEach((pod, index) => {
      if (!visiblePods.has(pod.name)) return;
      const color = podColor(theme, index);
      datasets.push({
        label: shortPodName(pod.name),
        data: series?.pods[pod.name] || [],
        borderColor: color,
        backgroundColor: withAlpha(color, 0.08),
        borderWidth: 1.75,
        fill: false,
        tension: 0.3,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointBackgroundColor: color,
        pointBorderColor: 'transparent',
        pointHoverBackgroundColor: color,
        pointHoverBorderColor: theme.card,
        pointHoverBorderWidth: 2,
      });
    });
    if (limit > 0)
      datasets.push(
        referenceDataset(
          REFERENCE_LABELS[0],
          limit,
          length,
          theme.orange,
          [4, 4],
        ),
      );
    if (podRequest > 0)
      datasets.push(
        referenceDataset(
          REFERENCE_LABELS[1],
          podRequest,
          length,
          theme.text3,
          [3, 3],
        ),
      );
    return { labels: labels || [], datasets };
  }, [theme, pods, visiblePods, series, labels, limit, podRequest]);

  const unit = series?.unit;

  const chartOptions = useLineChartOptions({
    theme,
    metric: selectedMetric,
    unit,
    referenceLabels: REFERENCE_LABELS,
    multiSeries: true,
    onZoomComplete,
  });

  if (!monitoringSettings.showMetricsPanel || monitoringSettings.preferredProvider === 'disabled') {
    return null;
  }

  const providerBlock = (
    <MetricsProviderState
      phase={phase as Exclude<typeof phase, 'ready'>}
      reason={reason}
      provider={provider}
      detecting={providerState.detecting}
      onDetectAgain={() => void providerState.detect({ refresh: true })}
      onInstall={phase === 'none' ? providerState.install : undefined}
      installing={providerState.installing}
      installError={providerState.installError}
      onSettings={() => setShowSettings(true)}
    />
  );

  const settingsSheet = showSettings && <MonitoringSettingsModal cluster={cluster} onClose={() => setShowSettings(false)} />;

  // Provider problems win over pod-list states: nothing to chart without one.
  if (!ready) {
    return (
      <div className="pod-metrics mx-card">
        {providerBlock}
        {settingsSheet}
      </div>
    );
  }

  if (podsLoading || podsError || pods.length === 0) {
    return (
      <div className="pod-metrics mx-card">
        {podsLoading ? (
          <div className="mx-message" role="status">
            <span className="ap-spinner" />
            <span>Loading pods…</span>
          </div>
        ) : podsError ? (
          <div className="mx-message is-error" role="alert">
            <span>{podsError}</span>
            <button type="button" className="ap-btn ap-btn--sm" onClick={() => void fetchPods()}>
              Retry
            </button>
          </div>
        ) : (
          <div className="mx-message">No pods belong to this {kind.toLowerCase()} right now</div>
        )}
        {settingsSheet}
      </div>
    );
  }

  const hasData =
    !!series && Object.values(series.pods).some((v) => v.length > 0);
  // An answer with no pod in it is "no samples", not still loading.
  const frameState: ChartFrameState =
    streamError && !hasData
      ? 'error'
      : hasData
        ? 'ready'
        : visiblePods.size > 0 && !series
          ? 'loading'
          : 'empty';

  const legendPods = pods.slice(
    0,
    Math.max(MAX_PODS_DISPLAYED, Math.min(pods.length, MAX_LEGEND_CHIPS)),
  );
  const atLimit = visiblePods.size >= MAX_PODS_DISPLAYED;

  return (
    <div className="pod-metrics mx-card">
      <MetricsToolbar
        metric={selectedMetric}
        onMetricChange={setSelectedMetric}
        timeRange={selectedTimeRange}
        onTimeRangeChange={setSelectedTimeRange}
      />

      <MetricsHeadline
        metric={selectedMetric}
        values={headline?.values}
        unit={unit}
        reference={
          headline && headline.request > 0 ? headline.request : undefined
        }
        referenceLabel="requested"
        suffix={`${visiblePods.size} of ${pods.length} pods`}
      />

      <MetricsChartFrame
        title={METRIC_TITLES[selectedMetric]}
        providerLabel={providerChipLabel(provider)}
        providerTitle={providerDetail(provider)}
        state={frameState}
        errorMessage={streamError || undefined}
        emptyMessage={visiblePods.size === 0 ? 'Pick a pod below to chart it' : `No samples in the last ${describeTimeRange(selectedTimeRange)}`}
        loadingMessage="Loading pod metrics…"
        zoomed={isZoomed}
        onResetZoom={handleResetZoom}
        onRefresh={handleRefresh}
        onSettings={() => setShowSettings(true)}
      >
        <div className="mx-canvas">
          <Line ref={chartRef} data={chartData} options={chartOptions} />
        </div>
      </MetricsChartFrame>

      <div className="mx-legend" role="group" aria-label="Pods on the chart">
        <span className="ap-badge mx-legend-count">
          {visiblePods.size} of {pods.length} pods
          {atLimit && pods.length > MAX_PODS_DISPLAYED && <span className="mx-legend-limit">&nbsp;· max {MAX_PODS_DISPLAYED}</span>}
        </span>
        {legendPods.map((pod, index) => {
          const color = podColor(theme, index);
          const isVisible = visiblePods.has(pod.name);
          const isDisabled = !isVisible && atLimit;
          return (
            <button
              key={pod.name}
              type="button"
              className="mx-legend-chip"
              aria-pressed={isVisible}
              disabled={isDisabled}
              title={isDisabled ? `Up to ${MAX_PODS_DISPLAYED} pods can be shown at once` : pod.name}
              onClick={() => togglePodVisibility(pod.name)}
            >
              <span className="mx-legend-swatch" style={{ backgroundColor: color }} />
              <span className="mx-legend-name">{shortPodName(pod.name)}</span>
            </button>
          );
        })}
        {pods.length > legendPods.length && <span className="mx-legend-more">+{pods.length - legendPods.length} more</span>}
      </div>

      {settingsSheet}
    </div>
  );
};

export default WorkloadMetrics;
