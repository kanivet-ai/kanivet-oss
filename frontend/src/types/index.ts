export type CloudProvider = 'aws' | 'gcp' | 'azure' | 'other';

export interface ClusterStatus {
  name: string;
  healthy: boolean;
  error?: string;
  version?: string;
  platform?: string;
  gitVersion?: string;
  responseTimeMs: number;
  nodeCount?: number;
  namespaceCount?: number;
  provider?: CloudProvider;
  region?: string;
}

export type FocusArea = 'tree' | 'list' | 'detail';
