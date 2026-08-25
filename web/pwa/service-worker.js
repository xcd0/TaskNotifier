const cacheName = "tasknotifier-pwa-v4";
const appShell = ["./", "./index.html", "./manifest.webmanifest", "./icons/app-192.png", "./icons/app-512.png"];

self.addEventListener("install", event => {
	event.waitUntil(caches.open(cacheName).then(cache => cache.addAll(appShell)));
	self.skipWaiting();
});

self.addEventListener("activate", event => {
	event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key !== cacheName).map(key => caches.delete(key)))));
	self.clients.claim();
});

self.addEventListener("fetch", event => {
	if (event.request.method !== "GET") {
		return;
	}
	event.respondWith(caches.match(event.request).then(cached => cached ?? fetch(event.request).then(response => {
		const copy = response.clone();
		void caches.open(cacheName).then(cache => cache.put(event.request, copy));
		return response;
	}).catch(() => caches.match("./index.html"))));
});

self.addEventListener("notificationclick", event => {
	event.notification.close();
	event.waitUntil(self.clients.matchAll({type: "window", includeUncontrolled: true}).then(clients => {
		for (const client of clients) {
			if ("focus" in client) {
				return client.focus();
			}
		}
		return self.clients.openWindow("./");
	}));
});
