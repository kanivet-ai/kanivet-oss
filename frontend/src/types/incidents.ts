export type IncidentSeverity = 'critical' | 'warning' | 'info';

export interface IncidentTimelineEntry {
  id: string;
  severity: IncidentSeverity;
  reason: string;
  message: string;
  kind: string;
  apiVersion: string;
  namespace: string;
  name: string;
  count: number;
  firstTimestamp: string;
  lastTimestamp: string;
  sourceComponent: string;
  routine: boolean;
  eventIds: number[];
  /** IDs into IncidentTimelineResponse.changes that preceded this entry. */
  relatedChanges?: string[];
}

export interface IncidentTimelineSummary {
  total: number;
  critical: number;
  warning: number;
  info: number;
  routine: number;
}

export interface IncidentTimelineResponse {
  entries: IncidentTimelineEntry[];
  summary: IncidentTimelineSummary;
  /** Resource changes (rollouts, upgrades, syncs) correlated with entries. */
  changes?: IncidentChangeMarker[];
}

export interface IncidentChangeMarker {
  id: string;
  source: string;
  time: string;
  group: string;
  version: string;
  kind: string;
  namespace: string;
  name: string;
  title: string;
  summary?: string;
  categories: string[];
}

export interface IncidentTimelineFilters {
  namespaces?: string[];
  kinds?: string[];
  severities?: IncidentSeverity[];
  search?: string;
  includeRoutine?: boolean;
  since?: Date;
  until?: Date;
  limit?: number;
}
