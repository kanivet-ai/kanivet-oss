import React, { useCallback, useEffect, useMemo, useState } from 'react';
import api from '../../services/api';
import {
  IncidentTimelineEntry,
  IncidentTimelineFilters,
  IncidentTimelineResponse,
} from '../../types/incidents';
import IncidentTimelineToolbar from './IncidentTimelineToolbar';
import IncidentTimelineSummary from './IncidentTimelineSummary';
import IncidentTimelineList from './IncidentTimelineList';
import { useStore } from '../../store';
import { kindToResource, parseApiVersion } from '../../utils/resourceUtils';
import './IncidentTimeline.css';

interface Props {
  cluster: string;
}

const DEFAULT_RANGE_MS = 60 * 60 * 1000;

const IncidentTimelinePage: React.FC<Props> = ({ cluster }) => {
  const openDetailTab = useStore((s) => s.openDetailTab);
  const loadDetails = useStore((s) => s.loadDetails);
  const [data, setData] = useState<IncidentTimelineResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rangeMs, setRangeMs] = useState<number>(DEFAULT_RANGE_MS);
  const [filters, setFilters] = useState<IncidentTimelineFilters>({
    severities: ['critical', 'warning'],
    includeRoutine: false,
    since: new Date(Date.now() - DEFAULT_RANGE_MS),
    limit: 500,
  });

  const applyRange = useCallback((ms: number) => {
    setRangeMs(ms);
    setFilters((f) => ({ ...f, since: ms === 0 ? undefined : new Date(Date.now() - ms) }));
  }, []);

  const load = useCallback(async () => {
    if (!cluster) return;
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getIncidentTimeline(cluster, filters);
      setData(resp);
    } catch (e: any) {
      setError(e?.message || 'Failed to load incident timeline');
    } finally {
      setLoading(false);
    }
  }, [cluster, filters]);

  useEffect(() => {
    void load();
    const id = setInterval(() => { void load(); }, 30000);
    return () => clearInterval(id);
  }, [load]);

  const namespaces = useMemo(() => {
    const set = new Set<string>();
    data?.entries.forEach((e) => { if (e.namespace) set.add(e.namespace); });
    return Array.from(set).sort();
  }, [data]);
  const kinds = useMemo(() => {
    const set = new Set<string>();
    data?.entries.forEach((e) => { if (e.kind) set.add(e.kind); });
    return Array.from(set).sort();
  }, [data]);

  const handleOpenResource = useCallback(async (entry: IncidentTimelineEntry) => {
    const { group, version } = parseApiVersion(entry.apiVersion || 'v1');
    const resource = {
      kind: entry.kind,
      group,
      version,
      name: kindToResource(entry.kind),
      namespaced: !!entry.namespace,
    };
    const item = {
      name: entry.name,
      namespace: entry.namespace,
      metadata: { name: entry.name, namespace: entry.namespace },
      kind: entry.kind,
      apiVersion: entry.apiVersion || 'v1',
    };
    openDetailTab(resource, item, cluster);
    try {
      await loadDetails(cluster, resource, item);
    } catch (e) {
      console.error('Failed to load incident resource details:', e);
    }
  }, [cluster, openDetailTab, loadDetails]);

  return (
    <div className="incident-timeline-page">
      <IncidentTimelineToolbar
        filters={filters}
        namespaces={namespaces}
        kinds={kinds}
        activeRangeMs={rangeMs}
        onChange={(patch) => setFilters((f) => ({ ...f, ...patch }))}
        onRangeChange={applyRange}
        onRefresh={() => { void load(); }}
      />
      {data?.summary && <IncidentTimelineSummary summary={data.summary} />}
      {error && <div className="incident-empty">{error}</div>}
      {!error && (loading && !data ? (
        <div className="incident-empty">Loading…</div>
      ) : (
        <IncidentTimelineList entries={data?.entries || []} onOpenResource={handleOpenResource} />
      ))}
    </div>
  );
};

export default IncidentTimelinePage;
