// Runs in the extension's own world, the page sees neither its names nor its
// messages. It takes only trusted input, listens to the extension alone and
// never holds the token.
(() => {
  if (globalThis.dcExtensionPage === true) return;
  globalThis.dcExtensionPage = true;
  const api = typeof globalThis.browser?.runtime?.sendMessage === "function" ? globalThis.browser : globalThis.chrome;
  const doc = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;

  function send(message) {
    try {
      api.runtime.sendMessage({ ...message, doc }).catch(() => {});
    } catch {}
  }

  let drawing = null;
  const picker = dcPicker({
    tag: "dc-extension-pick",
    closed: true,
    trusted: true,
    skip: (node) => drawing !== null && drawing.owns(node),
    tell: (type, extra) => send({ type, url: location.href, title: document.title, ...extra }),
  });
  drawing = dcDrawing({
    picker,
    tag: "dc-extension-draw",
    closed: true,
    trusted: true,
    tell: (type, extra) => send({ type, ...extra }),
  });

  function setPicking(on) {
    picker.setPicking(on);
    if (!on) picker.highlight(0, "", false, "");
  }

  window.addEventListener("keydown", (event) => {
    if (!picker.picking() || !event.isTrusted || event.key !== "Escape") return;
    event.preventDefault();
    event.stopImmediatePropagation();
    setPicking(false);
    send({ type: "picking", on: false });
  }, true);

  // Nothing tells this script that its extension was updated, turned off or
  // removed, it would go on taking the page's clicks with nobody to answer.
  // The script the extension puts in next cannot reach it, so it lets go of
  // the page on its own.
  const watch = setInterval(letGo, 1000);
  document.addEventListener("visibilitychange", letGo);

  function letGo() {
    try {
      if (api.runtime?.id) return;
    } catch {}
    clearInterval(watch);
    document.removeEventListener("visibilitychange", letGo);
    setPicking(false);
    drawing.clear();
    drawing.set(false);
  }

  const frame = () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

  // Two frames, so the picture shows what this script changed.
  async function prepare(reply) {
    drawing.finish();
    await frame();
    reply({ url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, marks: drawing.marks() });
  }

  api.runtime.onMessage.addListener((message, sender, reply) => {
    if (sender.id !== api.runtime.id || sender.tab || !message || typeof message !== "object") return false;
    switch (message.type) {
      case "hello":
        reply({ doc, picking: picker.picking() });
        return false;
      case "pick":
        setPicking(message.on === true);
        reply({ doc, picking: picker.picking() });
        return false;
      case "adjust":
        picker.adjust(Number(message.handle), message.toward === "parent" ? "parent" : "child");
        return false;
      case "highlight":
        picker.highlight(Number(message.handle) || 0, typeof message.selector === "string" ? message.selector : "", message.reveal === true, String(message.key || "").slice(0, 40));
        reply({ doc });
        return false;
      case "draw":
        drawing.set(message.on === true, message.tool, message.color);
        reply(drawing.state());
        return false;
      case "draw-step":
        drawing.step(message.step === 1 ? 1 : -1);
        return false;
      case "draw-clear":
        drawing.clear();
        return false;
      case "draw-end":
        drawing.set(false);
        drawing.reset();
        reply(drawing.state());
        return false;
      case "moved":
        drawing.left();
        reply(drawing.state());
        return false;
      case "draw-prepare":
        void prepare(reply);
        return true;
      default:
        return false;
    }
  });
})();
