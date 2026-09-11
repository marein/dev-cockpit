import { confirm, isVisible as dialogVisible } from "@dc/dialog";
import { notifyError } from "@dc/toast";
import "@dc/theme";
import { watchCtx, initCtxLayout } from "@dc/ctx";

// The glue around pe.js: a lazy custom element loader, the loading bar and the
// pe:* hooks. Every page is server rendered HTML, custom elements enhance it.

// A dialog is not an outside click. Every dropdown that stays open on one
// (data-bs-auto-close="outside") would otherwise lose its menu the moment a
// confirm is answered: the dialog renders outside the menu, so the click that
// dismisses it reads as a click elsewhere on the page. Bootstrap's events
// bubble, so one listener holds every dropdown open while a dialog stands,
// instead of each surface tracking its own dialogs.
document.addEventListener("hide.bs.dropdown", (event) => {
  if (dialogVisible()) event.preventDefault();
});

const bootBuild = buildId(document);

window.app = {
  navigate: (url) => (top.location.href = url),
  loadElements: (node) => Promise.allSettled([...node.querySelectorAll(":not(:defined)")]
    .filter((n) => !customElements.get(n.localName))
    .map((n) => import(n.localName))),
  showProgress(delay) {
    document.querySelector(".dc-page-progress")?.remove();
    const progress = document.createElement("div");
    progress.classList.add("dc-page-progress");
    const timeout = setTimeout(() => document.head.after(progress), delay ?? 250);
    return () => clearTimeout(timeout) || progress.classList.add("dc-page-progress--finish");
  },
  peInit() {
    if (!window.pe) return window.addEventListener("pe:init", window.app.peInit);
    window.app.navigate = window.pe.navigate;
    window.pe.selectSource = window.pe.selectTarget = (d) => d.querySelector("[data-page-content]");
  },
};

function buildId(doc) {
  return doc.querySelector('meta[name="dc-build"]')?.getAttribute("content") || "";
}

// The head is never swapped, so a redeploy leaves the tab on stale assets. Detect
// a build id mismatch while parsing, then reload in the succeed hook, which runs
// after pe.js pushed the response url, so a boosted form lands on its result page
// instead of back on the form.
function buildChanged(dom) {
  const build = buildId(dom);
  return Boolean(bootBuild && build && build !== bootBuild);
}

function syncJingle(dom) {
  const fresh = dom.querySelector('meta[name="dc-jingle"]');
  const meta = document.querySelector('meta[name="dc-jingle"]');
  if (fresh && meta) meta.setAttribute("content", fresh.getAttribute("content") || "");
}

window.app.peInit();

// The phone has no list column on the page: [data-ctx-area] (the tab bar's
// Projects, Terminals and Settings, the list button in every work head) opens
// the area's column as a sheet instead. A row in the sheet navigates and the
// sheet closes with it.
document.addEventListener("click", (event) => {
  const trigger = event.target instanceof Element && event.target.closest("[data-ctx-area]");
  if (!trigger) return;
  event.preventDefault();
  const sheet = document.querySelector("dc-ctx-sheet");
  if (!sheet || typeof sheet.open !== "function") return;
  const area = trigger.getAttribute("data-ctx-area");
  if (!sheet.hidden && sheet.area === area) sheet.close();
  else void sheet.open(area);
});

// data-no-pe opts a link or form out of boosting into a native load.
window.addEventListener("pe:click", (e) => e.detail.a.closest("[data-no-pe]") && e.preventDefault());
window.addEventListener("pe:submit", (e) => e.detail.form.closest("[data-no-pe]") && e.preventDefault());

// The work surface is the column that scrolls, never the page, so a move
// from one terminal to the next carries the column's scroll position over
// into the fresh body instead of landing at the top of the strip.
const isAttachPath = (path) => /^\/(coders|shells|splits)\/(?!new$)[^/]+$/.test(path);
// The fresh terminal has no rows until it renders, so the column is held at
// the old height for a moment, or the position would clamp to the top.
const keepScroll = (detail) => {
  const scroller = document.querySelector(".dc-work-body");
  const top = scroller ? scroller.scrollTop : 0;
  const height = scroller ? scroller.scrollHeight : 0;
  detail.succeed.push(() => {
    const next = document.querySelector(".dc-work-body");
    if (!next) return;
    const page = next.querySelector(".attach-page");
    if (page) {
      page.style.minHeight = `${height}px`;
      setTimeout(() => { page.style.minHeight = ""; }, 2000);
    }
    next.scrollTop = top;
  });
};

if (document.querySelector(".dc-app")) {
  void watchCtx();
  initCtxLayout();
}
window.addEventListener("dc:navigated", initCtxLayout);

window.addEventListener("pe:navigate", (e) => {
  if (isAttachPath(window.location.pathname) && isAttachPath(new URL(e.detail.url, window.location.origin).pathname)) {
    keepScroll(e.detail);
  }
});

window.addEventListener("pe:form", (e) => {
  if (isAttachPath(window.location.pathname)
    && /^\/coders\/[^/]+\/resume$/.test(new URL(e.detail.form.action, window.location.origin).pathname)) {
    keepScroll(e.detail);
  }
});

const closeOpenModals = () => {
  document.querySelectorAll(".modal.show").forEach((modal) => window.bootstrap?.Modal.getInstance(modal)?.hide());
};
const dropModalOverlay = () => {
  document.querySelectorAll(".modal-backdrop").forEach((backdrop) => backdrop.remove());
  document.body.classList.remove("modal-open");
  document.body.style.removeProperty("overflow");
  document.body.style.removeProperty("padding-right");
};

window.addEventListener("pe:navigate", (e) => {
  let stale = false;
  closeOpenModals();
  e.detail.succeed.push(dropModalOverlay);
  e.detail.parsed.push((dom) => { stale = buildChanged(dom); syncJingle(dom); if (!stale) window.app.loadElements(dom.body); });
  e.detail.succeed.push(() => { if (stale) location.reload(); });
  e.detail.succeed.push(() => window.dispatchEvent(new CustomEvent("dc:navigated")));
  e.detail.catch.push((err) => err?.name !== "AbortError" && notifyError("Could not load the page."));
  e.detail.finally.push(window.app.showProgress(0));
});

window.addEventListener("pe:include", (e) => {
  e.detail.parsed.push((dom) => window.app.loadElements(dom.body));
});

window.addEventListener("pe:form", (e) => {
  const buttons = [...e.detail.form.querySelectorAll("button")];
  // The loading look belongs to something styled as a button: it paints the
  // label transparent and puts a spinner in the element's own box. On anything
  // else (a chip, an icon in a row) that leaves an empty shape with a spinner
  // wherever the nearest positioned ancestor is, so those only go dead and the
  // surface they sit on shows the wait.
  buttons.forEach((b) => { b.disabled = true; if (b.classList.contains("btn")) b.classList.add("btn-loading"); });
  let stale = false;
  closeOpenModals();
  e.detail.succeed.push(dropModalOverlay);
  e.detail.parsed.push((dom) => { stale = buildChanged(dom); syncJingle(dom); if (!stale) window.app.loadElements(dom.body); });
  e.detail.succeed.push(() => { if (stale) location.reload(); });
  e.detail.succeed.push(() => window.dispatchEvent(new CustomEvent("dc:navigated")));
  e.detail.catch.push((err) => err?.name !== "AbortError" && notifyError("Could not submit, the connection failed or was cut off."));
  e.detail.finally.push(window.app.showProgress(0));
  e.detail.finally.push(() => buttons.forEach((b) => { b.disabled = false; b.classList.remove("btn-loading"); }));
});

// data-confirm forms confirm first, then submit through pe.js (native when
// opted out).
document.addEventListener("submit", async (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.dataset.confirm) return;
  if (form.dataset.ajaxDelete !== undefined || form.dataset.ajaxRefresh !== undefined) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  const ok = await confirm({
    title: form.dataset.confirm,
    text: form.dataset.confirmText,
    confirmText: form.dataset.confirmButton || "Confirm",
  });
  if (!ok) return;
  if (form.closest("[data-no-pe]")) form.submit();
  else window.pe.submit(form);
}, true);

await window.app.loadElements(document.body).finally(window.app.showProgress());
