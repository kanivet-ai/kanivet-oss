import { describe, expect, it } from 'vitest';
import { terminalOutputData } from './terminalOutput';

const base64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

describe('terminalOutputData', () => {
  it('passes UTF-8 text through', () => {
    expect(terminalOutputData('─┬─ ok', 'utf8')).toBe('─┬─ ok');
    expect(terminalOutputData('plain')).toBe('plain');
  });

  it('decodes base64 output to the original bytes', () => {
    const bytes = new TextEncoder().encode('ok ─┬─');
    const out = terminalOutputData(base64(bytes), 'base64');
    expect(out).toBeInstanceOf(Uint8Array);
    expect(Array.from(out as Uint8Array)).toEqual(Array.from(bytes));
    expect(new TextDecoder().decode(out as Uint8Array)).toBe('ok ─┬─');
  });

  it('keeps bytes that are not UTF-8', () => {
    const bytes = new Uint8Array([0x6f, 0x6b, 0xff, 0xe2, 0x94]);
    expect(
      Array.from(terminalOutputData(base64(bytes), 'base64') as Uint8Array),
    ).toEqual(Array.from(bytes));
  });
});
