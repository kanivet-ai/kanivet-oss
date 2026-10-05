import { createHash } from 'node:crypto';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

// Production builds pin what the renderer may load: the app's own files, the
// local backend and index.html's inline boot scripts (by hash). No code can be
// pulled from the network into the window that holds the preload bridge.
// Monaco injects <style> tags, hence 'unsafe-inline' for styles only. The dev
// server is left alone: its inline HMR preamble would not pass.
export function contentSecurityPolicy(): Plugin {
  return {
    name: 'kanivet-content-security-policy',
    apply: 'build',
    transformIndexHtml: {
      order: 'post',
      handler(html) {
        const inlineScripts = [
          ...html.matchAll(/<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/g),
        ];
        // The browser hashes a script's text after the HTML parser has turned
        // every CRLF into LF; a Windows checkout of index.html has CRLFs.
        const hashes = inlineScripts.map(
          ([, body]) =>
            `'sha256-${createHash('sha256').update(body.replace(/\r\n?/g, '\n')).digest('base64')}'`,
        );
        const policy = [
          "default-src 'self'",
          `script-src 'self' ${hashes.join(' ')}`,
          "style-src 'self' 'unsafe-inline'",
          "img-src 'self' data:",
          "font-src 'self' data:",
          "worker-src 'self' blob:",
          "connect-src 'self' http://127.0.0.1:* ws://127.0.0.1:*",
          "object-src 'none'",
          "base-uri 'self'",
        ].join('; ');
        return {
          html,
          tags: [
            {
              tag: 'meta',
              attrs: {
                'http-equiv': 'Content-Security-Policy',
                content: policy,
              },
              injectTo: 'head-prepend',
            },
          ],
        };
      },
    },
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), contentSecurityPolicy()],
  server: {
    port: 5173,
    host: true
  },
  build: {
    outDir: 'build',
    sourcemap: false
  },
  base: './',
  esbuild: {
    pure: mode === 'production' ? ['console.log', 'console.debug', 'console.info'] : [],
    drop: mode === 'production' ? ['debugger'] : [],
  },
}))
