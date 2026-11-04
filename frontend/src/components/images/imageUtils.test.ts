import { describe, expect, it } from 'vitest';
import { cleanCreatedBy, formatCommand, formatSize, imageTabResource, languageForPath, layerBars, parentPath, shortImageRef } from './imageUtils';
import { imagePaletteResults, imageRefFromQuery, knownImages } from './imagePalette';

const D = 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';

describe('imageUtils', () => {
  it('shortens references', () => {
    expect(shortImageRef('nginx')).toBe('nginx');
    expect(shortImageRef('localhost:5000/team/web:v2')).toBe('web:v2');
    expect(shortImageRef('localhost:5000/team/web')).toBe('web');
    expect(shortImageRef(`ghcr.io/a/b@${D}`)).toBe('b@0123456789ab');
    expect(shortImageRef(`ghcr.io/a/b:v1@${D}`)).toBe('b:v1');
  });

  it('keeps one tab per image and pod', () => {
    const a = imageTabResource({ ref: 'nginx:1', pullContext: { namespace: 'web', pod: 'p1', container: 'c' } });
    const b = imageTabResource({ ref: 'nginx:1', pullContext: {} });
    expect(a.name).toBe('image:nginx:1|web/p1/c');
    expect(b.name).toBe('image:nginx:1');
    expect(a.kind).toBe('ImageInspector');
    expect(a.title).toBe('nginx:1');
  });

  it('formats sizes and commands', () => {
    expect(formatSize(512)).toBe('512 B');
    expect(formatSize(1536)).toBe('1.50 KB');
    expect(formatSize(50 * 1024 * 1024)).toBe('50.0 MB');
    expect(formatCommand(['sh', '-c', 'echo hi'])).toBe('sh -c "echo hi"');
    expect(cleanCreatedBy('/bin/sh -c #(nop)  CMD ["nginx"]')).toBe('CMD ["nginx"]');
    expect(cleanCreatedBy('/bin/sh -c apt-get update')).toBe('RUN apt-get update');
    expect(cleanCreatedBy('RUN x # buildkit')).toBe('RUN x');
  });

  it('scales layer bars to the largest layer', () => {
    const bars = layerBars([
      { index: 0, digest: 'a', size: 100, mediaType: '' },
      { index: 1, digest: 'b', size: 300, mediaType: '', createdBy: 'COPY . /app' },
    ]);
    expect(bars[1].width).toBe(1);
    expect(bars[0].width).toBeCloseTo(1 / 3);
    expect(bars[0].share).toBeCloseTo(0.25);
    expect(bars[1].label).toBe('COPY . /app');
    expect(bars[0].label).toBe('Layer 1');
  });

  it('picks editor languages and parents', () => {
    expect(languageForPath('/etc/nginx/nginx.conf')).toBe('ini');
    expect(languageForPath('/app/package.json')).toBe('json');
    expect(languageForPath('/usr/bin/env')).toBe('plaintext');
    expect(parentPath('/etc/nginx')).toBe('/etc');
    expect(parentPath('/etc')).toBe('/');
  });
});

describe('imagePalette', () => {
  it('parses the command', () => {
    expect(imageRefFromQuery('> inspect image nginx:1')).toBe('nginx:1');
    expect(imageRefFromQuery('>inspect image… ')).toBe('');
    expect(imageRefFromQuery('> switch cluster')).toBeNull();
    expect(imageRefFromQuery('nginx')).toBeNull();
  });

  it('collects images from loaded lists', () => {
    const cache = new Map<string, any>([
      ['pods', new Map([['a', { spec: { containers: [{ image: 'nginx:1' }], initContainers: [{ image: 'busybox' }] } }]])],
      ['deploy', new Map([['b', { spec: { template: { spec: { containers: [{ image: 'redis:7' }] } } } }]])],
    ]);
    expect(knownImages(cache)).toEqual(['busybox', 'nginx:1', 'redis:7']);
    const res = imagePaletteResults('ngi', 'c1', ['nginx:1', 'redis:7']);
    expect(res.map((r) => r.resource.name)).toEqual(['ngi', 'nginx:1']);
    expect(res[0].resource.category).toBe('Inspect image');
  });
});
