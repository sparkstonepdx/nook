// nook's service worker: makes the dashboard installable and keeps its shell, so the installed app opens even
// when the server is slow to answer. It never touches /api: everything nook shows is live.
const CACHE = "nook-shell";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("fetch", (e) => {
  const req = e.request;
  const url = new URL(req.url);
  if (req.method !== "GET" || url.origin !== location.origin || url.pathname.startsWith("/api/")) return;
  // built files are named by their content: once cached, they're right forever
  if (url.pathname.startsWith("/assets/")) {
    e.respondWith(caches.open(CACHE).then(async (c) => {
      const hit = await c.match(req);
      if (hit) return hit;
      const res = await fetch(req);
      if (res.ok) c.put(req, res.clone());
      return res;
    }));
    return;
  }
  // pages: the network first, the last copy if it doesn't answer
  if (req.mode === "navigate") {
    e.respondWith(fetch(req).then((res) => {
      if (res.ok) caches.open(CACHE).then((c) => c.put("/", res.clone()));
      return res;
    }).catch(async () => (await caches.match("/")) || Response.error()));
  }
});
