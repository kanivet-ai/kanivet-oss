import { parseApiVersion } from './resourceUtils';

export interface OwnerRef {
  apiVersion?: string;
  kind: string;
  name: string;
  controller?: boolean;
}

export interface ParentRef {
  apiVersion?: string;
  kind: string;
  name: string;
  namespace?: string;
}

/** Fetches a resource by kind and apiVersion, or null when it cannot be read. */
export type FetchResource = (
  kind: string,
  apiVersion: string | undefined,
  name: string,
  namespace: string,
) => Promise<any | null>;

/** The managing owner if there is one, otherwise the first reference. */
export const pickOwner = (refs: unknown): OwnerRef | undefined => {
  if (!Array.isArray(refs) || refs.length === 0) return undefined;
  const valid = refs.filter((r): r is OwnerRef => !!r && !!r.kind && !!r.name);
  return valid.find((r) => r.controller) ?? valid[0];
};

/**
 * The resource a user thinks of as the parent: the owner from
 * metadata.ownerReferences, except that a ReplicaSet is looked through to the
 * Deployment (or Rollout) that manages it, so a pod links to its Deployment.
 */
export const resolveParent = async (
  item: any,
  fetchResource: FetchResource,
): Promise<ParentRef | null> => {
  const namespace: string | undefined = item?.metadata?.namespace || item?.namespace;
  const owner = pickOwner(item?.metadata?.ownerReferences);
  if (!owner) return null;

  let parent: ParentRef = { apiVersion: owner.apiVersion, kind: owner.kind, name: owner.name, namespace };
  if (owner.kind === 'ReplicaSet') {
    try {
      const rs = await fetchResource('ReplicaSet', owner.apiVersion, owner.name, namespace || '');
      const above = pickOwner(rs?.metadata?.ownerReferences);
      if (above) parent = { apiVersion: above.apiVersion, kind: above.kind, name: above.name, namespace };
    } catch {
      // keep the ReplicaSet itself when it cannot be read
    }
  }
  return parent;
};

// A ReplicaSet's owner never changes, so each is read once per session.
const replicaSetReads = new Map<string, Promise<any | null>>();

export const parentFetcher =
  (cluster: string, getDetails: (...a: any[]) => Promise<any>): FetchResource =>
  (kind, apiVersion, name, namespace) => {
    const { group, version } = parseApiVersion(apiVersion || 'apps/v1');
    const key = [cluster, group, version, kind, namespace, name].join('/');
    let read = replicaSetReads.get(key);
    if (!read) {
      if (replicaSetReads.size >= 500) replicaSetReads.clear();
      read = getDetails(cluster, group, version, kind, namespace, name);
      replicaSetReads.set(key, read);
      // A failed read is not remembered: the next open tries again.
      read.catch(() => replicaSetReads.delete(key));
    }
    return read;
  };
