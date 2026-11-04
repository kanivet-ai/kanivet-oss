/** Mirrors backend/internal/compare/types.go. */
export type CompareSource = 'live' | 'last-applied' | 'helm';

export interface CompareRef {
  cluster: string;
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
  source?: CompareSource;
}

export interface CompareOptions {
  includeStatus?: boolean;
  includeMetadata?: boolean;
  keepDefaults?: boolean;
  includeUndeclared?: boolean;
}

export interface CompareInsight {
  category: string;
  title: string;
  a?: string;
  b?: string;
  paths?: string[];
}

export interface CompareFieldChange {
  path: string;
  type: 'added' | 'removed' | 'changed';
  a?: string;
  b?: string;
}

export interface CompareSide {
  ref: CompareRef;
  label: string;
  yaml: string;
  error?: string;
  status?: number;
}

export interface CompareResult {
  a: CompareSide;
  b: CompareSide;
  identical: boolean;
  summary: CompareInsight[];
  changes: CompareFieldChange[];
  stats: { added: number; removed: number; changed: number };
  notes?: string[];
}

export interface CompareBaselines {
  lastApplied: boolean;
  helmRelease?: string;
  helmNamespace?: string;
}
