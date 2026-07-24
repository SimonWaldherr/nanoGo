// CACHE_NAME is versioned per deployment: the GitHub Pages workflow replaces
// the __BUILD_ID__ token with the commit SHA (see
// .github/workflows/deploy-pages.yml), so every deploy gets a distinct cache
// and returning visitors never keep stale assets. When served without that
// substitution (local dev), the literal token is a valid, stable name — bump
// the "uiN" suffix if you need to force a fresh local cache.
const CACHE_NAME = 'nanogo-playground-ui1-__BUILD_ID__';
const PRECACHE_URLS = [
  './',
  './index.html',
  './styles.css',
  './wasm_exec.js',
  './wasm_worker.js',
  './examples.js',
  './nanogo.wasm',
  './manifest.webmanifest',
  './assets/logo.svg',
  'https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.13/codemirror.min.css',
  'https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.13/codemirror.min.js',
  'https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.13/mode/go/go.min.js'
];

function isCodeMirrorAsset(url) {
  return url.origin === 'https://cdnjs.cloudflare.com' &&
    url.pathname.includes('/ajax/libs/codemirror/5.65.13/');
}

function isLocalAsset(url) {
  if (url.origin !== self.location.origin) return false;
  return (
    url.pathname.endsWith('/') ||
    url.pathname.endsWith('/index.html') ||
    url.pathname.endsWith('/styles.css') ||
    url.pathname.endsWith('/wasm_exec.js') ||
    url.pathname.endsWith('/wasm_worker.js') ||
    url.pathname.endsWith('/examples.js') ||
    url.pathname.endsWith('/nanogo.wasm') ||
    url.pathname.endsWith('/manifest.webmanifest') ||
    url.pathname.endsWith('/assets/logo.svg')
  );
}

async function cacheResponse(cache, request, response) {
  if (!response || (!response.ok && response.type !== 'opaque')) return response;
  await cache.put(request, response.clone());
  return response;
}

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(CACHE_NAME);
    await Promise.allSettled(PRECACHE_URLS.map(async (url) => {
      const request = new Request(url, { cache: 'reload' });
      const response = await fetch(request);
      await cacheResponse(cache, request, response);
    }));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(names.filter((name) => name !== CACHE_NAME).map((name) => caches.delete(name)));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') return;

  const url = new URL(event.request.url);
  const shouldHandle = event.request.mode === 'navigate' || isLocalAsset(url) || isCodeMirrorAsset(url);
  if (!shouldHandle) return;

  event.respondWith((async () => {
    const cache = await caches.open(CACHE_NAME);

    if (event.request.mode === 'navigate') {
      const cachedIndex = await cache.match('./index.html') || await cache.match('index.html');
      if (cachedIndex) {
        try {
          const fresh = await fetch(event.request);
          void cacheResponse(cache, './index.html', fresh.clone());
          return fresh;
        } catch (_) {
          return cachedIndex;
        }
      }
    }

    const cached = await cache.match(event.request, { ignoreSearch: url.origin === self.location.origin });
    if (cached) return cached;

    try {
      const response = await fetch(event.request);
      return await cacheResponse(cache, event.request, response);
    } catch (err) {
      if (event.request.mode === 'navigate') {
        const fallback = await cache.match('./index.html') || await cache.match('index.html');
        if (fallback) return fallback;
      }
      throw err;
    }
  })());
});
