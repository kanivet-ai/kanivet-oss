interface SearchableResource {
  id: string;
  cluster: string;
  kind: string;
  apiVersion: string;
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
