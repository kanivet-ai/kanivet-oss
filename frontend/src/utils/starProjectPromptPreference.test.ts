import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  isStarProjectPromptDismissed,
  setStarProjectPromptDismissed,
  STAR_PROJECT_PROMPT_DISMISSED_KEY,
} from './starProjectPromptPreference';

const createStorage = () => {
  const values = new Map<string, string>();
  return {
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => values.set(key, value)),
    removeItem: vi.fn((key: string) => values.delete(key)),
  };
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('star project prompt preference', () => {
  it('shows by default and saves an opt-out', () => {
    const storage = createStorage();
    vi.stubGlobal('window', { localStorage: storage });

    expect(isStarProjectPromptDismissed()).toBe(false);

    setStarProjectPromptDismissed(true);

    expect(storage.setItem).toHaveBeenCalledWith(
      STAR_PROJECT_PROMPT_DISMISSED_KEY,
      'true',
    );
    expect(isStarProjectPromptDismissed()).toBe(true);
  });

  it('removes an opt-out when unchecked', () => {
    const storage = createStorage();
    vi.stubGlobal('window', { localStorage: storage });
    setStarProjectPromptDismissed(true);

    setStarProjectPromptDismissed(false);

    expect(storage.removeItem).toHaveBeenCalledWith(
      STAR_PROJECT_PROMPT_DISMISSED_KEY,
    );
    expect(isStarProjectPromptDismissed()).toBe(false);
  });

  it('treats unavailable or throwing storage as no opt-out', () => {
    vi.stubGlobal('window', {
      get localStorage() {
        throw new Error('unavailable');
      },
    });

    expect(isStarProjectPromptDismissed()).toBe(false);
    expect(() => setStarProjectPromptDismissed(true)).not.toThrow();
  });
});
