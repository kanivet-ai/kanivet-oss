import { describe, expect, it, vi } from 'vitest';
import type { MonitoringSettings } from '../../store/types';
import {
  changedSettings,
  defaultExpandedProvider,
  mimirSettingsChange,
  saveClusterMetrics,
  type SaveSteps,
} from './clusterMetricsForm';

const stored: MonitoringSettings = {
  preferredProvider: 'auto',
  autoRefreshInterval: 30,
  showMetricsPanel: true,
};

const describeErr = (err: unknown, fallback: string) =>
  err instanceof Error ? err.message : fallback;

describe('which provider settings start open', () => {
  it('follows the caller, then a Mimir preference, then a tenant-less Mimir', () => {
    expect(defaultExpandedProvider('mimir', 'prometheus', null)).toBe('mimir');
    expect(defaultExpandedProvider(null, 'mimir', null)).toBe('mimir');
    expect(
      defaultExpandedProvider(undefined, 'auto', {
        mimir: { type: 'mimir', found: true, needsTenant: true },
      }),
    ).toBe('mimir');
  });

  it('stays closed when nothing calls for the Mimir fields', () => {
    expect(defaultExpandedProvider(null, 'auto', null)).toBeNull();
    expect(
      defaultExpandedProvider(null, 'prometheus', {
        mimir: { type: 'mimir', found: true },
      }),
    ).toBeNull();
  });
});

describe('changed settings', () => {
  it('keeps only the fields that differ from the latest stored value', () => {
    expect(changedSettings({}, stored)).toEqual({});
    expect(
      changedSettings(
        { preferredProvider: 'auto', showMetricsPanel: false },
        stored,
      ),
    ).toEqual({ showMetricsPanel: false });
  });

  it('does not write back a field another window changed meanwhile', () => {
    // The form was opened while the provider was "auto"; elsewhere it became
    // "mimir". The user only toggled the panel, so only that is written.
    const latest = { ...stored, preferredProvider: 'mimir' as const };
    expect(changedSettings({ showMetricsPanel: false }, latest)).toEqual({
      showMetricsPanel: false,
    });
  });
});

describe('Mimir settings change', () => {
  it('is null when neither the tenant nor the instance changed', () => {
    const s = { tenant: 'a', instance: 'mon/mimir' };
    expect(mimirSettingsChange(s, { ...s })).toBeNull();
  });

  it('carries a new instance only once saved, split into namespace and service', () => {
    expect(
      mimirSettingsChange(
        { tenant: 'a', instance: '' },
        { tenant: 'a', instance: 'observability/mimir-gateway' },
      ),
    ).toEqual({
      mimirNamespace: 'observability',
      mimirService: 'mimir-gateway',
    });
    expect(
      mimirSettingsChange(
        { tenant: 'a', instance: 'mon/mimir' },
        { tenant: 'b', instance: '' },
      ),
    ).toEqual({ mimirTenant: 'b', mimirNamespace: '', mimirService: '' });
  });
});

describe('saving the form', () => {
  const steps = (over: Partial<SaveSteps>): SaveSteps => ({
    local: { showMetricsPanel: false },
    previousLocal: { showMetricsPanel: true },
    backend: { mimirTenant: 'b' },
    writeLocal: vi.fn(),
    writeBackend: vi.fn().mockResolvedValue(undefined),
    describe: describeErr,
    ...over,
  });

  it('writes local preferences before the backend', async () => {
    const order: string[] = [];
    const result = await saveClusterMetrics(
      steps({
        writeLocal: () => order.push('local'),
        writeBackend: async () => {
          order.push('backend');
        },
      }),
    );
    expect(result).toEqual({ ok: true });
    expect(order).toEqual(['local', 'backend']);
  });

  it('sends nothing to the backend when the local write fails', async () => {
    const writeBackend = vi.fn();
    const result = await saveClusterMetrics(
      steps({
        writeLocal: () => {
          throw new Error('Storage full');
        },
        writeBackend,
      }),
    );
    expect(writeBackend).not.toHaveBeenCalled();
    expect(result).toEqual({
      ok: false,
      message: 'Nothing was saved: Storage full',
    });
  });

  it('puts the local preferences back when the backend refuses', async () => {
    const writeLocal = vi.fn();
    const result = await saveClusterMetrics(
      steps({
        writeLocal,
        writeBackend: vi.fn().mockRejectedValue(new Error('timeout')),
      }),
    );
    expect(writeLocal.mock.calls).toEqual([
      [{ showMetricsPanel: false }],
      [{ showMetricsPanel: true }],
    ]);
    expect(result).toEqual({
      ok: false,
      message: 'Nothing was saved: timeout',
    });
  });

  it('says which part stuck when the undo fails too', async () => {
    let calls = 0;
    const result = await saveClusterMetrics(
      steps({
        writeLocal: () => {
          if (++calls > 1) throw new Error('Storage full');
        },
        writeBackend: vi.fn().mockRejectedValue(new Error('timeout')),
      }),
    );
    expect(result.ok).toBe(false);
    expect(!result.ok && result.message).toBe(
      'The provider and display choices were saved, but the Mimir settings were not: timeout',
    );
  });

  it('skips writes that have nothing to change', async () => {
    const writeLocal = vi.fn();
    const writeBackend = vi.fn();
    const result = await saveClusterMetrics(
      steps({ local: {}, backend: null, writeLocal, writeBackend }),
    );
    expect(result).toEqual({ ok: true });
    expect(writeLocal).not.toHaveBeenCalled();
    expect(writeBackend).not.toHaveBeenCalled();
  });
});
