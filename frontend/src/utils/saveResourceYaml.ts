import * as yaml from 'js-yaml';
import api from '../services/api';

export async function saveResourceYaml(cluster: string, content: string) {
  const resource = yaml.load(content) as any;
  if (!resource?.metadata?.resourceVersion) {
    throw new Error('The resource version is missing. Refresh the editor before saving.');
  }
  // Keep the version the user edited so Kubernetes can reject stale writes.
  return api.updateResource(cluster, content);
}
