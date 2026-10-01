/** Pure pieces of the feature tour: where it has been seen, and where its
 * card goes next to the element a step points at. */

export interface Rect {
  top: number;
  left: number;
  width: number;
  height: number;
}

export type Side = 'right' | 'bottom' | 'left' | 'top' | 'center';

export interface Placement {
  top: number;
  left: number;
  side: Side;
}

const GAP = 14;
const MARGIN = 16;

/**
 * Places a card of the given size beside a target, trying right, below, left
 * and above in that order and keeping it inside the viewport. With no target,
 * or no side with room, it is centred.
 */
export function placeCard(
  target: Rect | null,
  card: { width: number; height: number },
  viewport: { width: number; height: number },
): Placement {
  const center = {
    top: Math.max(MARGIN, (viewport.height - card.height) / 2),
    left: Math.max(MARGIN, (viewport.width - card.width) / 2),
    side: 'center' as const,
  };
  if (!target) return center;
  const clampTop = (t: number) =>
    Math.min(Math.max(MARGIN, t), viewport.height - card.height - MARGIN);
  const clampLeft = (l: number) =>
    Math.min(Math.max(MARGIN, l), viewport.width - card.width - MARGIN);
  const midY = target.top + target.height / 2 - card.height / 2;
  const midX = target.left + target.width / 2 - card.width / 2;
  const right = target.left + target.width + GAP;
  if (right + card.width + MARGIN <= viewport.width)
    return { top: clampTop(midY), left: right, side: 'right' };
  const below = target.top + target.height + GAP;
  if (below + card.height + MARGIN <= viewport.height)
    return { top: below, left: clampLeft(midX), side: 'bottom' };
  const left = target.left - GAP - card.width;
  if (left >= MARGIN) return { top: clampTop(midY), left, side: 'left' };
  const above = target.top - GAP - card.height;
  if (above >= MARGIN)
    return { top: above, left: clampLeft(midX), side: 'top' };
  return center;
}

const SEEN_KEY = 'kanivet.featureTours.seen';

/** Tours this installation has already shown. Browser storage can be missing
 * or blocked; then nothing counts as seen and the caller keeps its own
 * in-memory flag so a tour still shows at most once per session. */
export function seenTours(storage: Pick<Storage, 'getItem'> | null): string[] {
  try {
    const raw = storage?.getItem(SEEN_KEY);
    const v = raw ? JSON.parse(raw) : [];
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [];
  } catch {
    return [];
  }
}

export function markTourSeen(
  storage: Pick<Storage, 'getItem' | 'setItem'> | null,
  id: string,
): void {
  try {
    const seen = seenTours(storage);
    if (!seen.includes(id))
      storage?.setItem(SEEN_KEY, JSON.stringify([...seen, id]));
  } catch {
    // Storage is a convenience: a private window may refuse it.
  }
}
