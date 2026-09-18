export interface SearchableResource {
  id: string;
  cluster: string;
  kind: string;
  apiVersion: string;
  /** Plural API resource name (deployments), as discovery spells it. */
  resource?: string;
  name: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  description?: string;
  keywords?: string[];
  createdAt: string;
  updatedAt: string;
  category: string;
  group: string;
  version: string;
  score?: number;
}

interface SearchMatch {
  field: string;
  value: string;
  indices?: number[];
}

export interface SearchResult {
  resource: SearchableResource;
  score: number;
  matches: SearchMatch[];
}

export interface SearchOptions {
  clusters?: string[];
  namespaces?: string[];
  kinds?: string[];
  limit?: number;
  offset?: number;
}

export interface SearchResponse {
  results: SearchResult[];
  query: string;
  count: number;
}

/** A resource the user opened from search, as the backend remembers it. */
export interface RecentResource {
  name: string;
  kind: string;
  namespace?: string;
  cluster: string;
  apiVersion?: string;
  category?: string;
  /** Plural API resource name (deployments), when known. */
  resource?: string;
}
