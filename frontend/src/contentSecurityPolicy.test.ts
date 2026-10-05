import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { contentSecurityPolicy } from '../vite.config';

const indexHtml = readFileSync(
  fileURLToPath(new URL('../index.html', import.meta.url)),
  'utf8',
);

const policyOf = (html: string): string => {
  const hook = contentSecurityPolicy().transformIndexHtml as any;
  const out = hook.handler(html);
  return out.tags[0].attrs.content;
};

// What the browser hashes: the text of each inline script once the HTML
// parser has normalised its line breaks to LF.
const browserHashes = (html: string) =>
  [...html.matchAll(/<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/g)].map(
    ([, body]) =>
      `'sha256-${createHash('sha256').update(body.replace(/\r\n?/g, '\n')).digest('base64')}'`,
  );

describe('content security policy', () => {
  it('allows index.html’s inline scripts', () => {
    const policy = policyOf(indexHtml);
    expect(browserHashes(indexHtml).length).toBeGreaterThan(0);
    for (const hash of browserHashes(indexHtml)) expect(policy).toContain(hash);
  });

  // The Windows release is built from a checkout with CRLF line endings.
  it('allows them when index.html has CRLF line endings', () => {
    const crlf = indexHtml.replace(/\r?\n/g, '\r\n');
    const policy = policyOf(crlf);
    for (const hash of browserHashes(crlf)) expect(policy).toContain(hash);
  });
});
