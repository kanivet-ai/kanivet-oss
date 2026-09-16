import { getAWSIdentityStatus } from '../../services/api/awsIdentity';
import { AWSIdentityClusterStatus } from '../../types/awsIdentity';

const statusCache = new Map<string, Promise<AWSIdentityClusterStatus>>();

export function getCachedAWSIdentityStatus(
  cluster: string,
): Promise<AWSIdentityClusterStatus> {
  let pending = statusCache.get(cluster);
  if (!pending) {
    pending = getAWSIdentityStatus(cluster).catch((error) => {
      statusCache.delete(cluster);
      throw error;
    });
    statusCache.set(cluster, pending);
  }
  return pending;
}

export function invalidateAWSIdentityStatus(cluster?: string): void {
  if (cluster) statusCache.delete(cluster);
  else statusCache.clear();
}
