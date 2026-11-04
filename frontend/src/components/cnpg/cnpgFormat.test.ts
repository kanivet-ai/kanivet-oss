import { describe, expect, it } from 'vitest';
import { formatLag, lagTone, promotable, backupMethodLabel } from './cnpgFormat';

describe('cnpg formatting', () => {
  it('formats replication lag', () => {
    expect(formatLag(undefined)).toBe('');
    expect(formatLag(0.2)).toBe('in sync');
    expect(formatLag(4.25)).toBe('4.3 s');
    expect(formatLag(42)).toBe('42 s');
    expect(formatLag(180)).toBe('3 min');
    expect(formatLag(7200)).toBe('2.0 h');
    expect(lagTone(2)).toBe('success');
    expect(lagTone(30)).toBe('warning');
    expect(lagTone(300)).toBe('danger');
  });

  it('only offers ready, healthy replicas for promotion', () => {
    const base = { restarts: 0, status: 'healthy' };
    const list = promotable([
      { ...base, name: 'pg-1', role: 'primary', ready: true },
      { ...base, name: 'pg-2', role: 'replica', ready: true },
      { ...base, name: 'pg-3', role: 'replica', ready: false },
      { ...base, name: 'pg-4', role: 'replica', ready: true, status: 'failed' },
    ]);
    expect(list.map((i) => i.name)).toEqual(['pg-2']);
  });

  it('labels backup methods', () => {
    expect(backupMethodLabel('plugin', 'barman-cloud.cloudnative-pg.io')).toBe('Plugin barman-cloud.cloudnative-pg.io');
    expect(backupMethodLabel(undefined)).toBe('Not configured');
  });
});
