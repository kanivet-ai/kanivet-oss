import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  fileURLToPath(new URL('./StarProjectDialog.tsx', import.meta.url)),
  'utf8',
);

describe('StarProjectDialog', () => {
  it('uses a local accessible Radix dialog with the Kanivet tile', () => {
    expect(source).toContain(
      "import { Button, Dialog as RDialog, Flex } from '@radix-ui/themes';",
    );
    expect(source).toContain("import KanivetMark from './icons/KanivetMark';");
    expect(source).toMatch(
      /<RDialog\.Root open=\{isOpen\}[\s\S]*?<RDialog\.Content/,
    );
    expect(source).toMatch(/<KanivetMark size=\{32\} tile\s*\/>/);
    expect(source).toMatch(
      /<RDialog\.Title[\s\S]*?>\s*Enjoying Kanivet\?\s*<\/RDialog\.Title>/,
    );
    expect(source).toMatch(/<RDialog\.Description>[\s\S]*?GitHub star/);
  });

  it('provides bug reporting and star actions with the expected close behavior', () => {
    expect(source).toMatch(/<Button[\s\S]*?>\s*Report a bug\s*<\/Button>/);
    expect(source).toMatch(/<Button[\s\S]*?>\s*Maybe later\s*<\/Button>/);
    expect(source).toMatch(
      /<Button onClick=\{handleStarProject\}>Star on GitHub<\/Button>/,
    );
    expect(source).toMatch(
      /window\.open\(\s*'https:\/\/github\.com\/kanivet-ai\/kanivet-oss\/issues\/new\?template=bug_report\.md',\s*'_blank',\s*'noopener,noreferrer',?\s*\)/,
    );
    expect(source).toMatch(
      /window\.open\(GITHUB_REPOSITORY_URL, '_blank', 'noopener,noreferrer'\);\s*onClose\(\);/,
    );
  });
});
