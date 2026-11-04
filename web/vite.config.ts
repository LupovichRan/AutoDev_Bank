/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Plugin } from 'vite';

// Stamp the service worker with a per-build id so each deploy gets a fresh shell cache.
function swBuildId(): Plugin {
  let outDir = 'dist';
  return {
    name: 'sw-build-id',
    apply: 'build',
    configResolved(c) {
      outDir = c.build.outDir;
    },
    closeBundle() {
      const f = join(outDir, 'sw.js');
      writeFileSync(f, readFileSync(f, 'utf8').replace(/__BUILD_ID__/g, Date.now().toString(36)));
    },
  };
}

// The Go binary serves web/dist; in development Vite proxies /api to it.
export default defineConfig({
  plugins: [svelte(), swBuildId()],
  server: {
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
