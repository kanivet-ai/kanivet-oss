import { describe, it, expect } from 'vitest';
import { createLatestRequest } from './latestRequest';

describe('createLatestRequest', () => {
  it('lets only the newest request answer', () => {
    const requests = createLatestRequest();
    const first = requests.start();
    const second = requests.start();
    expect(first.signal.aborted).toBe(true);
    expect(first.isCurrent()).toBe(false);
    expect(second.signal.aborted).toBe(false);
    expect(second.isCurrent()).toBe(true);
  });

  it('cancel drops the pending request', () => {
    const requests = createLatestRequest();
    const pending = requests.start();
    requests.cancel();
    expect(pending.signal.aborted).toBe(true);
    expect(pending.isCurrent()).toBe(false);
  });
});
