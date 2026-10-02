import { describe, expect, it } from 'vitest';
import { activeProvider, providerChipLabel, providerPhase, providerReason, requestedProvider } from './metricsProvider';
import type { MetricsProvidersStatus } from './metricsProvider';

const detected: MetricsProvidersStatus = {
  prometheus: { type: 'prometheus', found: true, namespace: 'monitoring' },
  mimir: { type: 'mimir', found: true, namespace: 'observability' },
  'metrics-server': { type: 'metrics-server', found: true },
};

describe('chart provider selection', () => {
  it('switches the badge to the same provider requested by the stream', () => {
    expect(providerChipLabel(activeProvider(detected, 'auto'))).toBe('Prometheus · monitoring');
    const preferred = requestedProvider('mimir');
    expect(preferred).toBe('mimir');
    expect(providerChipLabel(activeProvider(detected, preferred))).toBe('Mimir · observability');
    expect(providerPhase(detected, preferred)).toBe('ready');
    expect(activeProvider(detected, 'prometheus')).toBe(detected.prometheus);
  });

  it('shows the selected Mimir tenant requirement even when Prometheus is ready', () => {
    const status = { ...detected, mimir: { ...detected.mimir!, needsTenant: true, reason: 'Choose a tenant' } };
    expect(activeProvider(status, 'mimir')).toBe(status.mimir);
    expect(providerPhase(status, 'mimir')).toBe('needs-tenant');
    expect(providerReason(status, 'mimir')).toBe('Choose a tenant');
    expect(providerPhase(status, 'auto')).toBe('ready');
    expect(activeProvider(status, 'auto')).toBe(status.prometheus);
  });

  it('does not fall back to Prometheus when the selected Mimir is missing', () => {
    const status = { ...detected, mimir: { type: 'mimir', found: false, reason: 'No Mimir gateway found' } };
    expect(activeProvider(status, 'mimir')).toBeNull();
    expect(providerPhase(status, 'mimir')).toBe('none');
    expect(providerReason(status, 'mimir')).toBe('No Mimir gateway found');
  });

  it('does not offer history from another provider when metrics-server is selected', () => {
    expect(activeProvider(detected, 'metrics-server')).toBeNull();
    expect(providerPhase(detected, 'metrics-server')).toBe('none');
    expect(providerReason(detected, 'metrics-server')).toContain('no history');
  });

  it('keeps automatic fallback and pending or failed detection states', () => {
    const status = { ...detected, prometheus: { type: 'prometheus', found: false } };
    expect(activeProvider(status)).toBe(status.mimir);
    expect(providerPhase(null, 'mimir')).toBe('detecting');
    expect(providerPhase({ ...detected, unavailable: true }, 'mimir')).toBe('unreachable');
  });
});
