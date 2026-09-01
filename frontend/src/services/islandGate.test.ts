import { describe, it, expect } from 'vitest';
import { createIslandGate } from './islandNotifications';

const gateAt = (focused = false) => {
  let t = 0;
  const gate = createIslandGate({ now: () => t, hasFocus: () => focused });
  return { gate, tick: (ms: number) => { t += ms; } };
};

describe('island notification gate', () => {
  it('allows a first notification and blocks repeats within the key cooldown', () => {
    const { gate, tick } = gateAt();
    expect(gate.allows('rollout:web', { cooldownMs: 60000 })).toBe(true);
    tick(30000);
    expect(gate.allows('rollout:web', { cooldownMs: 60000 })).toBe(false);
    tick(31000);
    expect(gate.allows('rollout:web', { cooldownMs: 60000 })).toBe(true);
  });

  it('enforces a global minimum gap across different keys', () => {
    const { gate, tick } = gateAt();
    expect(gate.allows('a')).toBe(true);
    tick(1000);
    expect(gate.allows('b')).toBe(false);
    tick(4000);
    expect(gate.allows('b')).toBe(true);
  });

  it('suppresses backgroundOnly notifications while the window is focused', () => {
    const { gate } = gateAt(true);
    expect(gate.allows('cluster-lost:x', { backgroundOnly: true })).toBe(false);
    expect(gate.allows('rollout:web')).toBe(true);
  });

  it('allows backgroundOnly notifications when unfocused', () => {
    const { gate } = gateAt(false);
    expect(gate.allows('cluster-lost:x', { backgroundOnly: true })).toBe(true);
  });

  it('a suppressed attempt does not consume the cooldown', () => {
    const { gate, tick } = gateAt();
    expect(gate.allows('a')).toBe(true);
    tick(1000);
    expect(gate.allows('b')).toBe(false);
    tick(4000);
    expect(gate.allows('b')).toBe(true);
  });

  it('gap-exempt events bypass the global gap and do not consume it', () => {
    const { gate, tick } = gateAt();
    expect(gate.allows('rollout:web')).toBe(true);
    tick(1000);
    expect(gate.allows('cluster-switch', { cooldownMs: 0, ignoreGlobalGap: true })).toBe(true);
    tick(1000);
    expect(gate.allows('cluster-switch', { cooldownMs: 0, ignoreGlobalGap: true })).toBe(true);
    tick(2100);
    expect(gate.allows('drain:n1')).toBe(true);
  });

  it('an effectively infinite cooldown makes a key fire once per session', () => {
    const { gate, tick } = gateAt();
    expect(gate.allows('welcome', { cooldownMs: Number.MAX_SAFE_INTEGER })).toBe(true);
    tick(86_400_000);
    expect(gate.allows('welcome', { cooldownMs: Number.MAX_SAFE_INTEGER })).toBe(false);
  });
});
