import { describe, expect, it } from 'vitest';
import { formatTimeLeft, ssoSessionValidity } from './ssoSessionLabel';

const NOW = 1_800_000_000_000;
const MINUTE = 60_000;

describe('formatTimeLeft', () => {
  it('formats minutes, hours and days', () => {
    expect(formatTimeLeft(NOW + 57 * MINUTE, NOW)).toBe('57m left');
    expect(formatTimeLeft(NOW + 125 * MINUTE, NOW)).toBe('2h 5m left');
    expect(formatTimeLeft(NOW + 26 * 60 * MINUTE, NOW)).toBe('1d 2h left');
  });

  it('never shows 0m and reports a past expiry', () => {
    expect(formatTimeLeft(NOW + 10_000, NOW)).toBe('1m left');
    expect(formatTimeLeft(NOW - 1, NOW)).toBe('expired');
  });
});

describe('ssoSessionValidity', () => {
  it('does not count down the hourly access token when it renews silently', () => {
    expect(
      ssoSessionValidity({ refreshable: true, expiresAt: NOW + 57 * MINUTE }, NOW),
    ).toBe('renews automatically');
  });

  it('counts down a token that has no refresh token', () => {
    expect(
      ssoSessionValidity({ refreshable: false, expiresAt: NOW + 57 * MINUTE }, NOW),
    ).toBe('57m left');
  });
});
