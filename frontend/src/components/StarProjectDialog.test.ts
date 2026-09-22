import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  fileURLToPath(new URL('./StarProjectDialog.tsx', import.meta.url)),
  'utf8',
);

describe('StarProjectDialog bug reporting', () => {
  it('provides a secondary Radix button that opens the bug report template externally', () => {
    expect(source).toContain("import { Button } from '@radix-ui/themes';");
    expect(source).toMatch(/<Button[\s\S]*?>\s*Report a bug\s*<\/Button>/);
    expect(source).toMatch(
      /window\.open\(\s*'https:\/\/github\.com\/kanivet-ai\/kanivet-oss\/issues\/new\?template=bug_report\.md',\s*'_blank',\s*'noopener,noreferrer',?\s*\)/,
    );
  });
});
