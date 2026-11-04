// Mirrors backend/internal/changes/types.go.

export type ChangeSource =
  | 'replicaset'
  | 'controllerrevision'
  | 'helm'
  | 'argocd'
  | 'flux'
  | 'managedfields'
  | 'event'
  | 'creation';

export type ChangeCategory =
  | 'created'
  | 'deleted'
  | 'rollout'
  | 'image'
  | 'env'
  | 'config'
  | 'resources'
  | 'replicas'
  | 'restart'
  | 'secret'
  | 'sync'
  | 'edit'
  | 'event'
  | 'other';

export interface ChangeResourceRef {
  cluster: string;
  group: string;
  version: string;
  kind: string;
  namespace: string;
  name: string;
}

export interface FieldChange {
  path: string;
  old?: string;
  new?: string;
}

export interface ChangeEntry {
  id: string;
  source: ChangeSource;
  time: string;
  subject: ChangeResourceRef;
  revision?: string;
  title: string;
  summary?: string;
  categories: ChangeCategory[];
  actor?: string;
  fields?: string[];
  diff?: FieldChange[];
  snapshot?: string;
  hasSnapshot: boolean;
  origin?: ChangeResourceRef;
  warning?: boolean;
  current?: boolean;
}

export interface ChangeHistory {
  subject: ChangeResourceRef;
  entries: ChangeEntry[];
  sources: ChangeSource[];
  warnings?: string[];
}

export interface ChangeFeed {
  cluster: string;
  since: string;
  entries: ChangeEntry[];
  warnings?: string[];
}

export interface ChangeFeedFilters {
  namespaces?: string[];
  kinds?: string[];
  categories?: ChangeCategory[];
  sources?: ChangeSource[];
  since?: Date;
  limit?: number;
}
