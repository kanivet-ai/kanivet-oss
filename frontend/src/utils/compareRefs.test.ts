import { describe, expect, it } from 'vitest';
import { lineForPath, refFromListItem, refFromObject, splitChangePath, isCompleteRef, refKey } from './compareRefs';

// As backend/internal/compare.ToYAML renders it.
const YAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels:
    app: web
    app.kubernetes.io/name: web
  namespace: default
spec:
  replicas: 2
  template:
    spec:
      containers:
        - name: app
          env:
            - name: FOO
              value: "1"
          image: nginx:1
          ports:
            - containerPort: 8080
        - name: sidecar
          image: envoy
`;

describe('splitChangePath', () => {
  it('keeps list and quoted segments whole', () => {
    expect(splitChangePath('spec.template.spec.containers[app].image')).toEqual(['spec', 'template', 'spec', 'containers', '[app]', 'image']);
    expect(splitChangePath('metadata.labels["app.kubernetes.io/name"]')).toEqual(['metadata', 'labels', '["app.kubernetes.io/name"]']);
  });
});

describe('lineForPath', () => {
  it('finds scalar, named list item and quoted key lines', () => {
    expect(lineForPath(YAML, 'spec.replicas')).toBe(10);
    expect(lineForPath(YAML, 'spec.template.spec.containers[app].image')).toBe(18);
    expect(lineForPath(YAML, 'spec.template.spec.containers[sidecar].image')).toBe(22);
    expect(lineForPath(YAML, 'spec.template.spec.containers[app].env[FOO].value')).toBe(17);
    expect(lineForPath(YAML, 'metadata.labels["app.kubernetes.io/name"]')).toBe(7);
    expect(lineForPath(YAML, 'spec.template.spec.containers[1]')).toBe(21);
  });

  it('falls back to the deepest ancestor found', () => {
    expect(lineForPath(YAML, 'spec.template.spec.volumes')).toBe(12);
    expect(lineForPath('', 'spec')).toBe(0);
  });
});

describe('refs', () => {
  it('builds refs from an inspector object and a list row', () => {
    const r = refFromObject({ apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'web', namespace: 'prod' } }, 'c1');
    expect(r).toEqual({ cluster: 'c1', group: 'apps', version: 'v1', kind: 'Deployment', namespace: 'prod', name: 'web', source: 'live' });
    const core = refFromObject({ apiVersion: 'v1', kind: 'ConfigMap', metadata: { name: 'x' } }, 'c1');
    expect(core.group).toBe('');
    const row = refFromListItem({ name: 'a', namespace: 'ns' }, { group: 'apps', version: 'v1', kind: 'Deployment' }, 'c2');
    expect(isCompleteRef(row)).toBe(true);
    expect(refKey(row)).toBe('c2|apps|v1|Deployment|ns|a|live');
    expect(isCompleteRef({ cluster: 'c', kind: 'Pod', version: 'v1', name: '' })).toBe(false);
  });
});
