self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (error) {
    void error;
  }
  event.waitUntil(self.registration.showNotification(data.title || "Dev Cockpit", {
    body: data.body || "",
    tag: data.tag || undefined,
    icon: "/app-icon-192.png",
    badge: "/app-icon-192.png",
    data: { url: data.url || "/" },
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL(event.notification.data && event.notification.data.url || "/", self.location.origin).href;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of windows) {
      if (client.url === url && "focus" in client) return client.focus();
    }
    return self.clients.openWindow(url);
  })());
});
