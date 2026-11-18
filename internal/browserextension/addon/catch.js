// Runs in the page's own world, so it holds nothing of the extension: the page
// can read and forge these events, its own errors are all they carry.
(() => {
  const start = globalThis.dcConsole;
  delete globalThis.dcConsole;
  if (typeof start !== "function") return;
  const dispatch = EventTarget.prototype.dispatchEvent;
  const Custom = CustomEvent;
  const json = JSON.stringify;
  // console.js's frames carry the address the zip gives this file too.
  const own = /[a-z][a-z0-9+.-]*:\/\/\S*?\/(?=catch\.js:\d)/.exec(new Error().stack || "")?.[0] || "";
  start({
    own,
    tell(type, extra) {
      try {
        dispatch.call(window, new Custom("dc-extension-console", { detail: json({ type, entries: extra.entries, dropped: extra.dropped }) }));
      } catch {}
    },
  });
})();
