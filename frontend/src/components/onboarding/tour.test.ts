import { describe, expect, it } from 'vitest';
import { markTourSeen, placeCard, seenTours } from './tour';

const vp = { width: 1200, height: 800 };
const card = { width: 340, height: 180 };

describe('placeCard', () => {
  it('goes to the right of a target with room there', () => {
    const p = placeCard(
      { top: 100, left: 20, width: 200, height: 30 },
      card,
      vp,
    );
    expect(p.side).toBe('right');
    expect(p.left).toBe(20 + 200 + 14);
  });

  it('goes below a wide target and stays inside the viewport', () => {
    const p = placeCard(
      { top: 100, left: 300, width: 880, height: 120 },
      card,
      vp,
    );
    expect(p.side).toBe('bottom');
    expect(p.left + card.width).toBeLessThanOrEqual(vp.width - 16);
  });

  it('centres without a target', () => {
    const p = placeCard(null, card, vp);
    expect(p.side).toBe('center');
    expect(p.left).toBe((vp.width - card.width) / 2);
  });

  it('centres when no side has room', () => {
    const p = placeCard(
      { top: 10, left: 10, width: 1180, height: 780 },
      card,
      vp,
    );
    expect(p.side).toBe('center');
  });
});

describe('seen tours', () => {
  const mem = () => {
    const m = new Map<string, string>();
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
    };
  };

  it('remembers a tour once', () => {
    const s = mem();
    expect(seenTours(s)).toEqual([]);
    markTourSeen(s, 'rightsizing-v1');
    markTourSeen(s, 'rightsizing-v1');
    expect(seenTours(s)).toEqual(['rightsizing-v1']);
  });

  it('treats broken or blocked storage as nothing seen', () => {
    expect(seenTours({ getItem: () => '{not json' })).toEqual([]);
    expect(seenTours(null)).toEqual([]);
    expect(() =>
      markTourSeen(
        {
          getItem: () => null,
          setItem: () => {
            throw new Error('blocked');
          },
        },
        'x',
      ),
    ).not.toThrow();
  });
});
