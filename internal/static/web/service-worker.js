const CACHE_NAME = 'ollama-chat-tone-static-v1';
const STATIC_ASSETS = [
	'/styles.css',
	'/theme.js',
	'/app.js',
	'/admin.js',
	'/pwa.js',
	'/logo.svg',
	'/manifest.webmanifest',
];

self.addEventListener('install', (event) => {
	event.waitUntil(
		caches.open(CACHE_NAME)
			.then((cache) => cache.addAll(STATIC_ASSETS))
			.then(() => self.skipWaiting()),
	);
});

self.addEventListener('activate', (event) => {
	event.waitUntil(
		caches.keys()
			.then((names) => Promise.all(names
				.filter((name) => name !== CACHE_NAME)
				.map((name) => caches.delete(name))))
			.then(() => self.clients.claim()),
	);
});

self.addEventListener('fetch', (event) => {
	const request = event.request;
	if (request.method !== 'GET') {
		return;
	}
	if (request.mode === 'navigate') {
		return;
	}

	const url = new URL(request.url);
	if (url.origin !== self.location.origin || url.pathname.startsWith('/api/') || url.pathname.startsWith('/auth/')) {
		return;
	}

	event.respondWith(cacheFirst(request));
});

async function cacheFirst(request) {
	const cached = await caches.match(request);
	if (cached) {
		return cached;
	}

	const response = await fetch(request);
	if (response.ok) {
		const cache = await caches.open(CACHE_NAME);
		cache.put(request, response.clone());
	}
	return response;
}
