import { describe, expect, it } from 'vitest';
import { collectPodConfigMapRefs } from './podConfigMapRefs';

describe('collectPodConfigMapRefs', () => {
  it('returns nothing for a pod without ConfigMap references', () => {
    expect(collectPodConfigMapRefs({ containers: [{ name: 'app', env: [{ name: 'A', value: '1' }] }] })).toEqual([]);
    expect(collectPodConfigMapRefs(undefined)).toEqual([]);
  });

  it('merges volume, projected, envFrom and keyRef usages per ConfigMap', () => {
    const refs = collectPodConfigMapRefs({
      volumes: [
        { name: 'cfg', configMap: { name: 'app-config', items: [{ key: 'app.yaml', path: 'app.yaml' }] } },
        { name: 'proj', projected: { sources: [{ configMap: { name: 'extra', optional: true } }, { secret: { name: 's' } }] } },
        { name: 'unused', configMap: { name: 'orphan' } },
      ],
      initContainers: [{ name: 'init', envFrom: [{ configMapRef: { name: 'app-config' }, prefix: 'CFG_' }] }],
      containers: [
        {
          name: 'app',
          volumeMounts: [{ name: 'cfg', mountPath: '/etc/app' }, { name: 'proj', mountPath: '/etc/extra', subPath: 'x' }],
          env: [
            { name: 'LEVEL', valueFrom: { configMapKeyRef: { name: 'app-config', key: 'log.level' } } },
            { name: 'SECRET', valueFrom: { secretKeyRef: { name: 's', key: 'k' } } },
          ],
        },
      ],
    });

    expect(refs.map((r) => r.name)).toEqual(['app-config', 'extra', 'orphan']);

    const [appConfig, extra, orphan] = refs;
    expect(appConfig.optional).toBe(false);
    expect(appConfig.referencedKeys).toEqual(['app.yaml', 'log.level']);
    expect(appConfig.usages).toEqual([
      { container: 'app', text: 'mounted at /etc/app' },
      { container: 'init', text: 'all keys as env (prefix CFG_)' },
      { container: 'app', text: '$LEVEL ← log.level' },
    ]);

    expect(extra.optional).toBe(true);
    expect(extra.usages).toEqual([{ container: 'app', text: 'mounted at /etc/extra (subPath x)' }]);

    expect(orphan.usages).toEqual([{ text: 'volume unused (not mounted)' }]);
  });

  it('treats a ConfigMap as required when any consumer requires it', () => {
    const [ref] = collectPodConfigMapRefs({
      containers: [{
        name: 'app',
        envFrom: [{ configMapRef: { name: 'cm', optional: true } }],
        env: [{ name: 'X', valueFrom: { configMapKeyRef: { name: 'cm', key: 'x' } } }],
      }],
    });
    expect(ref.optional).toBe(false);
  });
});
