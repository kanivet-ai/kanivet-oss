import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Line } from 'react-chartjs-2';
import api from '../services/api';
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

interface PodMetricsData {
  labels: string[];
  values: number[];
  unit?: string;
}

// Series colours: the system palette, read from the tokens at runtime
const POD_COLOR_KEYS: (keyof ChartTheme)[] = ['blue', 'green', 'orange', 'red', 'purple', 'pink', 'teal', 'yellow', 'indigo', 'gray'];

const podColor = (theme: ChartTheme, index: number): string => theme[POD_COLOR_KEYS[index % POD_COLOR_KEYS.length]];

// Maximum pods drawn at once, for legibility and stream count
const MAX_PODS_DISPLAYED = 8;
// Legend chips shown before "+N more"
const MAX_LEGEND_CHIPS = 20;

const REFERENCE_LABELS = ['Limit', 'Request'];

const shortPodName = (fullName: string): string => {
  const parts = fullName.split('-');
  return parts.length >= 2 ? parts.slice(-2).join('-') : fullName.slice(-15);
};

export const WorkloadMetrics: React.FC<WorkloadMetricsProps> = ({ cluster, kind, namespace, name }) => {
  const { monitoringSettings } = useStore(useShallow((s) => ({ monitoringSettings: s.monitoringSettings })));
  const theme = useChartTheme();
  const providerState = useMetricsProvider(cluster);
  const { phase, provider, reason, revision, markUnavailable } = providerState;

  const [pods, setPods] = useState<PodInfo[]>([]);
  const [podsLoading, setPodsLoading] = useState(true);
  const [podsError, setPodsError] = useState<string | null>(null);
  const [selectedMetric, setSelectedMetric] = useState<MetricType>('cpu');
  const [selectedTimeRange, setSelectedTimeRange] = useState<TimeRange>('15m');
  const [podMetricsData, setPodMetricsData] = useState<Record<string, PodMetricsData>>({});
  const [streamError, setStreamError] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [isZoomed, setIsZoomed] = useState(false);
  const [visiblePods, setVisiblePods] = useState<Set<string>>(new Set());
  const [streamRun, setStreamRun] = useState(0);
  const chartRef = useRef<any>(null);
  const cleanupFnsRef = useRef<Map<string, () => void>>(new Map());

  const ready = phase === 'ready';
  const preferred = requestedProvider(monitoringSettings.preferredProvider);

  const stopStreams = useCallback(() => {
    cleanupFnsRef.current.forEach((cleanup) => cleanup());
    cleanupFnsRef.current.clear();
  }, []);

  // Pods of the workload
  const fetchPods = useCallback(async () => {
    setPodsLoading(true);
    setPodsError(null);
    try {
      const result = await api.getWorkloadPods(cluster, kind, namespace, name);
      setPods(result.pods);
      setVisiblePods(new Set(result.pods.slice(0, MAX_PODS_DISPLAYED).map((p) => p.name)));
      setPodMetricsData({});
    } catch (err: any) {
      setPodsError(err?.message || 'Failed to fetch pods');
    } finally {
      setPodsLoading(false);
    }
  }, [cluster, kind, namespace, name]);

  useEffect(() => {
    void fetchPods();
  }, [fetchPods]);

  // One stream per visible pod. Old series stay until replaced so toggling
  // a pod or switching metric does not blank the chart.
  useEffect(() => {
    stopStreams();
    if (!ready || pods.length === 0) return;
    setStreamError(null);

    const podsToStream = pods.filter((p) => visiblePods.has(p.name)).slice(0, MAX_PODS_DISPLAYED);
    podsToStream.forEach((pod) => {
      const cleanup = api.startMetricsStream(
        cluster,
        namespace,
        pod.name,
        selectedMetric,
        selectedTimeRange,
        (data) => {
          setPodMetricsData((prev) => ({ ...prev, [pod.name]: data }));
        },
        (err) => {
          if (api.isMetricsProviderUnavailableError(err)) {
            stopStreams();
            setPodMetricsData({});
            markUnavailable(err);
            return;
          }
          setStreamError(err);
        },
        undefined,
        preferred,
        2,
      );
      cleanupFnsRef.current.set(pod.name, cleanup);
    });

    return stopStreams;
  }, [ready, pods, visiblePods, cluster, namespace, selectedMetric, selectedTimeRange, preferred, revision, streamRun, stopStreams, markUnavailable]);

  useEffect(() => {
    setPodMetricsData({});
    setIsZoomed(false);
  }, [selectedMetric, selectedTimeRange]);

  const handleRefresh = () => {
    setPodMetricsData({});
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

  // Aggregated limits/requests across all pods of the workload
  const aggregated = useMemo(() => {
    const sum = { limitCpu: 0, limitMemory: 0, requestCpu: 0, requestMemory: 0 };
    pods.forEach((pod) => {
      sum.limitCpu += pod.resourceLimits?.cpu || 0;
      sum.limitMemory += pod.resourceLimits?.memory || 0;
      sum.requestCpu += pod.resourceRequests?.cpu || 0;
      sum.requestMemory += pod.resourceRequests?.memory || 0;
    });
    return sum;
  }, [pods]);

  const limit = selectedMetric === 'cpu' ? aggregated.limitCpu : selectedMetric === 'memory' ? aggregated.limitMemory : 0;
  const request = selectedMetric === 'cpu' ? aggregated.requestCpu : selectedMetric === 'memory' ? aggregated.requestMemory : 0;

  // Longest label set across pods drives the x axis
  const longestLabels = useMemo(() => {
    let longest: string[] = [];
    Object.values(podMetricsData).forEach((data) => {
      if (data.labels.length > longest.length) longest = data.labels;
    });
    return longest;
  }, [podMetricsData]);

  // Sum of the latest sample of every visible pod → the headline
  const aggregateSeries = useMemo(() => {
    const visible = pods.filter((p) => visiblePods.has(p.name) && podMetricsData[p.name]?.values.length);
    if (visible.length === 0) return undefined;
    const length = longestLabels.length;
    const values: number[] = [];
    for (let i = 0; i < length; i++) {
      let total = 0;
      let any = false;
      visible.forEach((p) => {
        const series = podMetricsData[p.name].values;
        const offset = length - series.length;
        const v = series[i - offset];
        if (typeof v === 'number' && Number.isFinite(v)) {
          total += v;
          any = true;
        }
      });
      values.push(any ? total : NaN);
    }
    return values.filter((v) => Number.isFinite(v));
  }, [pods, visiblePods, podMetricsData, longestLabels]);

  const chartData = useMemo(() => {
    const datasets: any[] = [];
    pods.forEach((pod, index) => {
      if (!visiblePods.has(pod.name)) return;
      const data = podMetricsData[pod.name];
      const color = podColor(theme, index);
      datasets.push({
        label: shortPodName(pod.name),
        data: data?.values || [],
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
    if (limit > 0) datasets.push(referenceDataset('Limit', limit, longestLabels.length, theme.orange, [4, 4]));
    if (request > 0) datasets.push(referenceDataset('Request', request, longestLabels.length, theme.text3, [3, 3]));
    return { labels: longestLabels, datasets };
  }, [theme, pods, visiblePods, podMetricsData, longestLabels, limit, request]);

  const unit = Object.values(podMetricsData)[0]?.unit;

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

  const hasData = Object.values(podMetricsData).some((d) => d.values.length > 0);
  const streamsActive = cleanupFnsRef.current.size > 0 || visiblePods.size > 0;
  const frameState: ChartFrameState = streamError && !hasData
    ? 'error'
    : hasData
      ? 'ready'
      : streamsActive
        ? 'loading'
        : 'empty';

  const legendPods = pods.slice(0, Math.max(MAX_PODS_DISPLAYED, Math.min(pods.length, MAX_LEGEND_CHIPS)));
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
        values={aggregateSeries}
        unit={unit}
        reference={request > 0 ? request : undefined}
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
