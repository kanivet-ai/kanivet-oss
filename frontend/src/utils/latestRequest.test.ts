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

  it('reports pending until the current request finishes or is cancelled', () => {
    const states: boolean[] = [];
    const requests = createLatestRequest((pending) => states.push(pending));
    const answered = requests.start();
    answered.finish();
    expect(states).toEqual([true, false]);

    // Closed mid-search: the aborted request never finishes as current, so
    // only the cancel can clear the indicator.
    const dropped = requests.start();
    requests.cancel();
    dropped.finish();
    expect(states).toEqual([true, false, true, false]);

    // An earlier request answering late leaves the newer one pending.
    const older = requests.start();
    requests.start();
    older.finish();
    expect(states[states.length - 1]).toBe(true);
  });

  it('cancel drops the pending request', () => {
    const requests = createLatestRequest();
    const pending = requests.start();
    requests.cancel();
    expect(pending.signal.aborted).toBe(true);
    expect(pending.isCurrent()).toBe(false);
  });
});
