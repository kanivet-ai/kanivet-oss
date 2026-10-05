import { describe, it, expect, vi } from 'vitest';
import { pickOwner, resolveParent, parentFetcher } from './parentResource';

const pod = (refs: any[]) => ({ metadata: { namespace: 'ns', ownerReferences: refs } });

describe('pickOwner', () => {
  it('prefers the controller reference', () => {
    expect(pickOwner([{ kind: 'A', name: 'a' }, { kind: 'B', name: 'b', controller: true }])?.kind).toBe('B');
  });
  it('is undefined without owners', () => {
    expect(pickOwner(undefined)).toBeUndefined();
    expect(pickOwner([])).toBeUndefined();
  });
});

describe('resolveParent', () => {
  it('returns null for an unowned resource', async () => {
    expect(await resolveParent({ metadata: {} }, vi.fn())).toBeNull();
  });
  it('returns a direct owner without fetching', async () => {
    const fetch = vi.fn();
    const p = await resolveParent(pod([{ kind: 'StatefulSet', name: 's', apiVersion: 'apps/v1' }]), fetch);
    expect(p).toEqual({ kind: 'StatefulSet', name: 's', apiVersion: 'apps/v1', namespace: 'ns' });
    expect(fetch).not.toHaveBeenCalled();
  });
  it('looks through a ReplicaSet to its Deployment', async () => {
    const fetch = vi.fn().mockResolvedValue({
      metadata: { ownerReferences: [{ kind: 'Deployment', name: 'web', apiVersion: 'apps/v1', controller: true }] },
    });
    const p = await resolveParent(pod([{ kind: 'ReplicaSet', name: 'web-5f', apiVersion: 'apps/v1' }]), fetch);
    expect(p?.kind).toBe('Deployment');
    expect(p?.name).toBe('web');
  });
  it('falls back to the ReplicaSet when it cannot be read or has no owner', async () => {
    const refs = [{ kind: 'ReplicaSet', name: 'web-5f', apiVersion: 'apps/v1' }];
    expect((await resolveParent(pod(refs), vi.fn().mockRejectedValue(new Error('x'))))?.kind).toBe('ReplicaSet');
    expect((await resolveParent(pod(refs), vi.fn().mockResolvedValue({ metadata: {} })))?.kind).toBe('ReplicaSet');
  });
});

describe('parentFetcher', () => {
  it('reads a ReplicaSet once and retries after a failure', async () => {
    const getDetails = vi.fn().mockResolvedValue({ metadata: {} });
    const fetch = parentFetcher('c1', getDetails);
    await fetch('ReplicaSet', 'apps/v1', 'web-5f', 'ns');
    await fetch('ReplicaSet', 'apps/v1', 'web-5f', 'ns');
    expect(getDetails).toHaveBeenCalledTimes(1);
    expect(getDetails).toHaveBeenCalledWith('c1', 'apps', 'v1', 'ReplicaSet', 'ns', 'web-5f');

    const failing = vi.fn().mockRejectedValueOnce(new Error('x')).mockResolvedValue({});
    const retry = parentFetcher('c2', failing);
    await expect(retry('ReplicaSet', 'apps/v1', 'api-1', 'ns')).rejects.toThrow();
    await retry('ReplicaSet', 'apps/v1', 'api-1', 'ns');
    expect(failing).toHaveBeenCalledTimes(2);
  });
});
