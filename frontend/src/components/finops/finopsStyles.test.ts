import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const read = (path: string) =>
  readFileSync(fileURLToPath(new URL(path, import.meta.url)), 'utf8');

const classesOf = (css: string) =>
  new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((m) => m[1]));

describe('FinOps styles', () => {
  // NodeLink is bundled with the main stylesheet, after FinOps' rules: a
  // FinOps class of the same name took its blue, pointer link look on a
  // node name that cannot be clicked.
  it('defines none of the classes NodeLink styles globally', () => {
    const nodeLink = classesOf(read('../NodeLink.css'));
    const finops = classesOf(read('./FinOpsDashboard.css'));
    expect([...nodeLink].filter((c) => finops.has(c))).toEqual([]);
  });
});
