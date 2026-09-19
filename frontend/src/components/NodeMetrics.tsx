import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Line } from 'react-chartjs-2';
import api from '../services/api';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import MonitoringSettingsModal from './MonitoringSettingsModal';
import {
  chartAreaGradient,
  chartMetricColor,
  registerMetricsChart,
  useChartTheme,
} from './metrics/chartTheme';
import { referenceDataset, useLineChartOptions } from './metrics/chartOptions';
import { METRIC_TITLES, describeTimeRange, type MetricType, type TimeRange } from './metrics/metricsFormat';
import { providerChipLabel, providerDetail, requestedProvider } from './metrics/metricsProvider';
import { useMetricsProvider } from './metrics/useMetricsProvider';
import { MetricsToolbar } from './metrics/MetricsToolbar';
import { MetricsHeadline } from './metrics/MetricsHeadline';
import { MetricsChartFrame, type ChartFrameState } from './metrics/MetricsChartFrame';
import { MetricsProviderState } from './metrics/MetricsProviderState';
import './PodMetrics.css';

registerMetricsChart();

interface NodeMetricsProps {
  cluster: string;
  nodeName: string;
  resourceCapacity?: {
    cpu?: number;
    memory?: number;
  };
  resourceAllocatable?: {
    cpu?: number;
    memory?: number;
  };
}

const REFERENCE_LABELS = ['Capacity', 'Allocatable'];

export const NodeMetrics: React.FC<NodeMetricsProps> = ({
  cluster,
  nodeName,
  resourceCapacity,
  resourceAllocatable,
}) => {
  const { monitoringSettings } = useStore(useShallow((s) => ({ monitoringSettings: s.monitoringSettings })));
  const theme = useChartTheme();
  const providerState = useMetricsProvider(cluster);
  const { phase, provider, reason, revision, markUnavailable } = providerState;

  const [selectedMetric, setSelectedMetric] = useState<MetricType>('cpu');
  const [selectedTimeRange, setSelectedTimeRange] = useState<TimeRange>('15m');
  const [metricsData, setMetricsData] = useState<{ labels: string[]; values: number[]; unit?: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [isZoomed, setIsZoomed] = useState(false);
  const [streamRun, setStreamRun] = useState(0);
  const cleanupRef = useRef<(() => void) | null>(null);
  const chartRef = useRef<any>(null);

  const ready = phase === 'ready';
  const preferred = requestedProvider(monitoringSettings.preferredProvider);

  const stopStream = useCallback(() => {
    if (cleanupRef.current) {
      cleanupRef.current();
      cleanupRef.current = null;
    }
  }, []);

  useEffect(() => {
    stopStream();
    if (!ready) {
      setLoading(false);
      return;
    }
    setError(null);
    setLoading(true);

    cleanupRef.current = api.startMetricsStream(
      cluster,
      '', // no namespace for node metrics
      '', // no pod for node metrics
      selectedMetric,
      selectedTimeRange,
      (data) => {
        setMetricsData(data);
        setLoading(false);
        setError(null);
      },
      (errorMsg) => {
        if (api.isMetricsProviderUnavailableError(errorMsg)) {
          stopStream();
          setMetricsData(null);
          setLoading(false);
          setError(null);
          markUnavailable(errorMsg);
          return;
        }
        setError(errorMsg);
        setLoading(false);
      },
      undefined,
      preferred,
      5,
      nodeName, // node metrics are keyed by node name
    );

    return stopStream;
  }, [ready, cluster, nodeName, selectedMetric, selectedTimeRange, preferred, revision, streamRun, stopStream, markUnavailable]);

  useEffect(() => {
    setMetricsData(null);
    setIsZoomed(false);
  }, [cluster, nodeName]);

  const handleRefresh = () => {
    setMetricsData(null);
    setIsZoomed(false);
    setStreamRun((n) => n + 1);
  };

  const handleResetZoom = () => {
    const chart = chartRef.current;
    if (chart && typeof chart.resetZoom === 'function') {
      chart.resetZoom();
      setIsZoomed(false);
    }
  };

  const onZoomComplete = useCallback(() => setIsZoomed(true), []);

  const capacity = selectedMetric === 'cpu' ? resourceCapacity?.cpu : selectedMetric === 'memory' ? resourceCapacity?.memory : undefined;
  const allocatable = selectedMetric === 'cpu' ? resourceAllocatable?.cpu : selectedMetric === 'memory' ? resourceAllocatable?.memory : undefined;

  const chartData = useMemo(() => {
    const color = chartMetricColor(theme, selectedMetric);
    const length = metricsData?.labels.length || 0;
    const datasets: any[] = [
      {
        label: METRIC_TITLES[selectedMetric],
        data: metricsData?.values || [],
        borderColor: color,
        backgroundColor: chartAreaGradient(color),
        borderWidth: 2,
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointBackgroundColor: color,
        pointBorderColor: 'transparent',
        pointBorderWidth: 0,
        pointHoverBackgroundColor: color,
        pointHoverBorderColor: theme.card,
        pointHoverBorderWidth: 2,
      },
    ];
    if (capacity) datasets.push(referenceDataset('Capacity', capacity, length, theme.orange, [4, 4]));
    if (allocatable) datasets.push(referenceDataset('Allocatable', allocatable, length, theme.text3, [3, 3]));
    return { labels: metricsData?.labels || [], datasets };
  }, [theme, selectedMetric, metricsData, capacity, allocatable]);

  const chartOptions = useLineChartOptions({
    theme,
    metric: selectedMetric,
    unit: metricsData?.unit,
    referenceLabels: REFERENCE_LABELS,
    onZoomComplete,
  });

  if (!monitoringSettings.showMetricsPanel || monitoringSettings.preferredProvider === 'disabled') {
    return null;
  }

  const frameState: ChartFrameState = error
    ? 'error'
    : !metricsData && loading
      ? 'loading'
      : !metricsData || metricsData.values.length === 0
        ? 'empty'
        : 'ready';

  return (
    <div className="pod-metrics mx-card">
      {ready ? (
        <>
          <MetricsToolbar
            metric={selectedMetric}
            onMetricChange={setSelectedMetric}
            timeRange={selectedTimeRange}
            onTimeRangeChange={setSelectedTimeRange}
            allowCustomRange={false}
          />

          <MetricsHeadline
            metric={selectedMetric}
            values={metricsData?.values}
            unit={metricsData?.unit}
            reference={allocatable}
            referenceLabel="allocatable"
          />

          <MetricsChartFrame
            title={METRIC_TITLES[selectedMetric]}
            providerLabel={providerChipLabel(provider)}
            providerTitle={providerDetail(provider)}
            state={frameState}
            stale={loading && !!metricsData}
            errorMessage={error || undefined}
            emptyMessage={`No samples in the last ${describeTimeRange(selectedTimeRange)}`}
            zoomed={isZoomed}
            onResetZoom={handleResetZoom}
            onRefresh={handleRefresh}
            refreshing={loading}
            onSettings={() => setShowSettings(true)}
          >
            <div className="mx-canvas">
              <Line ref={chartRef} data={chartData} options={chartOptions} />
            </div>
          </MetricsChartFrame>
        </>
      ) : (
        <MetricsProviderState
          phase={phase}
          reason={reason}
          provider={provider}
          detecting={providerState.detecting}
          onDetectAgain={() => void providerState.detect({ refresh: true })}
          onInstall={phase === 'none' ? providerState.install : undefined}
          installing={providerState.installing}
          installError={providerState.installError}
          onSettings={() => setShowSettings(true)}
        />
      )}

      {showSettings && <MonitoringSettingsModal cluster={cluster} onClose={() => setShowSettings(false)} />}
    </div>
  );
};

export default NodeMetrics;
