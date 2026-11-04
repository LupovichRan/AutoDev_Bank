// AutoDev Bank service worker.
// - App shell: precached on install; navigations are network-first with the
//   cached shell as fallback; hashed /assets/ are cache-first.
// - Read-only data: GET /api/accounts* and GET /api/opendata/* are
//   network-first with the last good response as offline fallback.
// - Everything else (all non-GET, auth, transfers, approvals) bypasses the
//   worker entirely: money never moves from a cache.
/* eslint-env serviceworker */

const VERSION = 'v1';
const SHELL_CACHE = `autodev-shell-${VERSION}`;
const API_CACHE = 'autodev-api'; // cleared by the page on sign-out
const SHELL = ['/', '/index.html', '/manifest.webmanifest', '/icons/icon.svg', '/icons/icon-maskable.svg'];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((c) => c.addAll(SHELL))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('autodev-shell-') && k !== SHELL_CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'clear-api-cache') {
    event.waitUntil(caches.delete(API_CACHE));
  }
});

function isCachedApi(url) {
  return url.pathname === '/api/accounts' || url.pathname.startsWith('/api/accounts/') || url.pathname.startsWith('/api/opendata/');
}

async function networkFirst(request, cacheName, fallbackKey) {
  const cache = await caches.open(cacheName);
  try {
    const response = await fetch(request);
    if (response.ok) await cache.put(fallbackKey || request, response.clone());
    return response;
  } catch (err) {
    const cached = await cache.match(fallbackKey || request);
    if (cached) return cached;
    if (fallbackKey) {
      const shell = (await cache.match('/index.html')) || (await cache.match('/'));
      if (shell) return shell;
    }
    throw err;
  }
}

async function cacheFirst(request) {
  const cache = await caches.open(SHELL_CACHE);
  const cached = await cache.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok) await cache.put(request, response.clone());
  return response;
}

self.addEventListener('fetch', (event) => {
  const { request } = event;
  if (request.method !== 'GET') return; // never cache or intercept writes
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  if (request.mode === 'navigate') {
    event.respondWith(networkFirst(request, SHELL_CACHE, '/index.html'));
    return;
  }
  if (url.pathname.startsWith('/api/')) {
    if (isCachedApi(url)) event.respondWith(networkFirst(request, API_CACHE));
    return; // other API calls go straight to the network
  }
  if (url.pathname.startsWith('/assets/') || SHELL.includes(url.pathname)) {
    event.respondWith(cacheFirst(request));
  }
});
