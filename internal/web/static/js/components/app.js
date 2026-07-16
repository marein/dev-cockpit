import { confirm } from "@dc/dialog";
import { notifyError } from "@dc/toast";

// The glue around pe.js: a lazy custom element loader, the loading bar and the
// pe:* hooks. Every page is server rendered HTML, custom elements enhance it.

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

window.app.peInit();

window.matchMedia("(prefers-color-scheme:dark)").addEventListener(
  "change",
  (e) => document.documentElement.setAttribute("data-bs-theme", e.matches ? "dark" : "light"),
);

// data-no-pe opts a link or form out of boosting into a native load.
window.addEventListener("pe:click", (e) => e.detail.a.closest("[data-no-pe]") && e.preventDefault());
window.addEventListener("pe:submit", (e) => e.detail.form.closest("[data-no-pe]") && e.preventDefault());

const isAttachPath = (path) => /^\/(coders|shells|splits)\/(?!new$)[^/]+$/.test(path);
let heightHoldTimer;
const releaseHeightHold = () => {
  clearTimeout(heightHoldTimer);
  document.body.style.minHeight = "";
};
const keepScroll = (detail) => {
  const x = window.scrollX;
  const y = window.scrollY;
  document.body.style.minHeight = document.documentElement.scrollHeight + "px";
  clearTimeout(heightHoldTimer);
  heightHoldTimer = setTimeout(releaseHeightHold, 2000);
  detail.succeed.push(() => window.scrollTo({ left: x, top: y, behavior: "instant" }));
};

window.addEventListener("pe:navigate", (e) => {
  if (isAttachPath(window.location.pathname) && isAttachPath(new URL(e.detail.url, window.location.origin).pathname)) {
    keepScroll(e.detail);
  } else {
    releaseHeightHold();
  }
});

window.addEventListener("pe:form", (e) => {
  if (isAttachPath(window.location.pathname)
    && /^\/coders\/[^/]+\/resume$/.test(new URL(e.detail.form.action, window.location.origin).pathname)) {
    keepScroll(e.detail);
  } else {
    releaseHeightHold();
  }
});

window.addEventListener("pe:navigate", (e) => {
  let stale = false;
  e.detail.parsed.push((dom) => { stale = buildChanged(dom); if (!stale) window.app.loadElements(dom.body); });
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
  buttons.forEach((b) => { b.disabled = true; b.classList.add("btn-loading"); });
  let stale = false;
  e.detail.parsed.push((dom) => { stale = buildChanged(dom); if (!stale) window.app.loadElements(dom.body); });
  e.detail.succeed.push(() => { if (stale) location.reload(); });
  e.detail.succeed.push(() => window.dispatchEvent(new CustomEvent("dc:navigated")));
  e.detail.catch.push((err) => err?.name !== "AbortError" && notifyError("Could not submit."));
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
  const ok = await confirm({ title: form.dataset.confirm, confirmText: form.dataset.confirmButton || "Confirm" });
  if (!ok) return;
  if (form.closest("[data-no-pe]")) form.submit();
  else window.pe.submit(form);
}, true);

await window.app.loadElements(document.body).finally(window.app.showProgress());
