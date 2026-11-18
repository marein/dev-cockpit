// The page can send such batches too, they are its own errors and nothing
// more. location.href follows a view the page loads itself, the browser still
// names the address the document loaded with.
(() => {
  if (globalThis.dcExtensionRelay === true) return;
  globalThis.dcExtensionRelay = true;
  const api = typeof globalThis.browser?.runtime?.sendMessage === "function" ? globalThis.browser : globalThis.chrome;
  const MAX_DETAIL = 1 << 20;
  // The catch sends at most every 80 ms, a page sending faster is cut off.
  const MAX_PER_SECOND = 20;
  let since = 0;
  let sent = 0;
  let holding = false;
  let held = 0;

  function send(message) {
    try {
      api.runtime.sendMessage({ ...message, url: location.href, viewport: { w: innerWidth, h: innerHeight } }).catch(() => {});
    } catch {}
  }

  function loadKind() {
    try {
      return performance.getEntriesByType("navigation")[0]?.type || "";
    } catch {
      return "";
    }
  }

  send({ type: "console-doc", nav: loadKind() });
  window.addEventListener("pageshow", (event) => {
    if (event.isTrusted && event.persisted) send({ type: "console-doc", nav: "back_forward" });
  });
  window.addEventListener("dc-extension-console", (event) => {
    const detail = event.detail;
    if (typeof detail !== "string" || detail.length > MAX_DETAIL) return;
    const now = Date.now();
    if (now - since > 1000) {
      since = now;
      sent = 0;
    }
    sent += 1;
    if (sent > MAX_PER_SECOND) return;
    let data;
    try {
      data = JSON.parse(detail);
    } catch {
      return;
    }
    if (!data || data.type !== "console") return;
    const entries = Array.isArray(data.entries) ? data.entries : [];
    // Past the catch's bound a failing page sends only its count, which goes
    // to disk with the whole list, so it goes on at most once a second.
    if (!entries.length) {
      held = data.dropped;
      if (holding) return;
      holding = true;
      setTimeout(() => {
        holding = false;
        send({ type: "console", entries: [], dropped: held });
      }, 1000);
      return;
    }
    send({ type: "console", entries, dropped: data.dropped });
  });
})();
