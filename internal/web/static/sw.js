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

// A window is never matched by URL: the page consumes an assistant link and
// strips it from its address, so the exact match never hits, and on Apple
// devices openWindow is unreliable in installed web apps. Instead any window
// is focused and handed the URL, the page opens the assistant overlay in
// place or navigates itself and answers over the reply port. A page that
// stays silent, a suspended web app page is killed and reloads without ever
// seeing the message, is driven by client.navigate. A new window opens only
// when none exists.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL(event.notification.data && event.notification.data.url || "/", self.location.origin).href;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of windows) {
      if (!("focus" in client)) continue;
      try {
        const focused = await client.focus();
        const target = focused || client;
        if (await handledByPage(target, url)) return;
        if ("navigate" in target) return await target.navigate(url);
      } catch (error) {
        void error;
      }
    }
    return self.clients.openWindow(url);
  })());
});

function handledByPage(client, url) {
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => resolve(false), 500);
    channel.port1.onmessage = () => {
      clearTimeout(timer);
      resolve(true);
    };
    client.postMessage({ type: "open-url", url }, [channel.port2]);
  });
}
