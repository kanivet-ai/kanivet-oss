import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  fileURLToPath(new URL('./TabBar.tsx', import.meta.url)),
  'utf8',
);

describe('TabBar bug reporting', () => {
  it('provides an accessible toolbar button that opens the bug report template externally', () => {
    expect(source).toContain('title="Report a bug"');
    expect(source).toContain('aria-label="Report a bug"');
    expect(source).toContain(
      "window.open('https://github.com/kanivet-ai/kanivet-oss/issues/new?template=bug_report.md', '_blank', 'noopener,noreferrer')",
    );
  });
});
