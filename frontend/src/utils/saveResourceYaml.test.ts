import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as yaml from 'js-yaml';
import { saveResourceYaml } from './saveResourceYaml';

const { updateResource } = vi.hoisted(() => ({ updateResource: vi.fn() }));
vi.mock('../services/api', () => ({ default: { updateResource } }));

describe('saving resource YAML', () => {
  beforeEach(() => vi.clearAllMocks());

  it('preserves the edited version and propagates conflicts without retrying', async () => {
    const content = yaml.dump({ apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'app', resourceVersion: '10' }, spec: { replicas: 2 } });
    const conflict = new Error('the object has been modified');
    updateResource.mockImplementation(async (_cluster, submitted) => {
      expect(yaml.load(submitted)).toMatchObject({ metadata: { resourceVersion: '10' }, spec: { replicas: 2 } });
      // The live object is now version 11 after an external scale operation.
      throw conflict;
    });
    await expect(saveResourceYaml('cluster', content)).rejects.toBe(conflict);
    expect(updateResource).toHaveBeenCalledTimes(1);
  });

  it('returns a successful update', async () => {
    updateResource.mockResolvedValueOnce({ metadata: { resourceVersion: '11' } });
    await expect(saveResourceYaml('cluster', 'metadata:\n  resourceVersion: "10"')).resolves.toEqual({ metadata: { resourceVersion: '11' } });
  });

  it('refuses updates without a version', async () => {
    await expect(saveResourceYaml('cluster', 'metadata:\n  name: app')).rejects.toThrow('version is missing');
    expect(updateResource).not.toHaveBeenCalled();
  });
});
