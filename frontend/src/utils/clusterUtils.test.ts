import { describe, it, expect } from 'vitest';
import { parseClusterName } from './clusterUtils';

describe('parseClusterName', () => {
  it('recognises EKS from its ARN', () => {
    const info = parseClusterName('arn:aws:eks:eu-north-1:243517631187:cluster/sbx-aws-eu-north-1-1');
    expect(info.provider).toBe('aws');
    expect(info.displayName).toBe('sbx-aws-eu-north-1-1');
  });

  it('uses the provider the backend detected for a renamed context', () => {
    const info = parseClusterName('sbx-aws-eu-north-1-2', undefined, 'aws');
    expect(info.provider).toBe('aws');
    expect(info.isAWS).toBe(true);
    expect(info.displayName).toBe('sbx-aws-eu-north-1-2');
  });

  it('prefers the detected provider over a guess from the name', () => {
    expect(parseClusterName('azure-migration-eks', undefined, 'aws').provider).toBe('aws');
  });

  it('keeps an alias with a detected provider', () => {
    const info = parseClusterName('sbx-aws-eu-north-1-2', 'Sandbox 2', 'aws');
    expect(info.displayName).toBe('Sandbox 2');
    expect(info.hasAlias).toBe(true);
  });

  it('leaves unknown contexts as other', () => {
    expect(parseClusterName('minikube').provider).toBe('other');
  });
});
