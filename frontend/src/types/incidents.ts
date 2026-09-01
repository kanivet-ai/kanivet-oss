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
