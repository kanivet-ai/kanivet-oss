import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  KANIVET_MARK_PATH,
  KANIVET_MARK_SEGMENTS,
  KANIVET_MARK_STROKE,
  KANIVET_TILE_GRADIENT,
} from './kanivetMarkPath';

const read = (rel: string) =>
  readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

describe('Kanivet mark geometry', () => {
  it('matches the generated app icon in assets/icon.svg', () => {
    const svg = read('../../../assets/icon.svg');
    for (const d of Object.values(KANIVET_MARK_SEGMENTS))
      expect(svg).toContain(`d="${d}"`);
    expect(svg).toContain(`stroke-width="${KANIVET_MARK_STROKE}"`);
    for (const stop of KANIVET_TILE_GRADIENT)
      expect(svg).toContain(`stop-color="${stop}"`);
  });

  it('matches the pre-hydration splash in index.html', () => {
    const html = read('../../../index.html');
    expect(html).toContain(`d="${KANIVET_MARK_PATH}"`);
    expect(html).toContain(`stroke-width="${KANIVET_MARK_STROKE}"`);
    for (const stop of KANIVET_TILE_GRADIENT)
      expect(html).toContain(`stop-color="${stop}"`);
  });
});
