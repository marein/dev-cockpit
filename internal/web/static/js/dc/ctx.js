import { syncAnimations } from "@dc/dom";
import { get, set, remove } from "@dc/store";

const MIN_CTX_WIDTH = 200;
const areaKey = (kind) => `dc-ctx-${kind}:${document.querySelector(".dc-app")?.dataset.area || ""}`;
const clampWidth = (px) => Math.round(Math.min(Math.max(px, MIN_CTX_WIDTH), Math.max(MIN_CTX_WIDTH, window.innerWidth * 0.5)));
const applyWidth = (app, px) => {
  if (px > 0) app.style.setProperty("--dc-ctx-w", `${clampWidth(px)}px`);
  else app.style.removeProperty("--dc-ctx-w");
};

export function initCtxLayout() {
  const app = document.querySelector(".dc-app");
  const ctx = app?.querySelector(":scope > .dc-ctx");
  if (!app || !ctx || ctx.dataset.ctxLayout) return;
  ctx.dataset.ctxLayout = "1";
  applyWidth(app, parseInt(get(areaKey("width"), "0"), 10) || 0);
  const handle = ctx.querySelector("[data-ctx-resize]");
  if (handle) {
    let dragging = false;
    handle.addEventListener("mousedown", (e) => e.preventDefault());
    handle.addEventListener("pointerdown", (e) => {
      dragging = true;
      handle.classList.add("active");
      handle.setPointerCapture(e.pointerId);
    });
    handle.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      applyWidth(app, e.clientX - ctx.getBoundingClientRect().left);
    });
    handle.addEventListener("pointerup", (e) => {
      dragging = false;
      handle.classList.remove("active");
      handle.releasePointerCapture(e.pointerId);
      set(areaKey("width"), String(Math.round(ctx.getBoundingClientRect().width)));
    });
    handle.addEventListener("dblclick", () => {
      applyWidth(app, 0);
      remove(areaKey("width"));
    });
  }
  const body = ctx.querySelector(".dc-ctx-body");
  if (!body) return;
  const top = parseInt(get(areaKey("scroll"), "0"), 10) || 0;
  if (top > 0 && !ctx.hasAttribute("data-ctx-own-scroll")) body.scrollTop = top;
  let timer = null;
  body.addEventListener("scroll", () => {
    if (timer !== null) return;
    timer = window.setTimeout(() => {
      timer = null;
      set(areaKey("scroll"), String(Math.round(body.scrollTop)));
    }, 150);
  }, { passive: true });
}

let inFlight = false;
let dirty = false;

// The area a rendered document's column belongs to. A column only ever
// replaces one of its own kind: a pull started on one page can land after a
// boosted navigation left it, and its answer describes the page that was left.
const areaOf = (root) => root.querySelector(".dc-app")?.getAttribute("data-area") || "";

export function swapCtx(doc) {
  const ctx = document.querySelector(".dc-ctx");
  const fresh = doc.querySelector(".dc-ctx");
  if (!ctx || !fresh) return;
  if (areaOf(document) !== areaOf(doc)) return;
  const title = ctx.querySelector(".dc-ctx-title");
  const freshTitle = fresh.querySelector(".dc-ctx-title");
  if (title && freshTitle && title.innerHTML !== freshTitle.innerHTML) title.replaceChildren(...freshTitle.childNodes);
  const body = ctx.querySelector(".dc-ctx-body");
  const freshBody = fresh.querySelector(".dc-ctx-body");
  if (!body || !freshBody || body.innerHTML === freshBody.innerHTML) return;
  const top = body.scrollTop;
  body.replaceChildren(...freshBody.childNodes);
  body.scrollTop = top;
  syncAnimations(body);
  document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: body } }));
}

export async function refreshCtx() {
  if (inFlight) {
    dirty = true;
    return;
  }
  inFlight = true;
  dirty = false;
  const here = () => window.location.pathname + window.location.search;
  const asked = here();
  try {
    const response = await fetch(asked, { credentials: "same-origin", headers: { Accept: "text/html" } });
    if (!response.ok) return;
    const html = await response.text();
    if (asked !== here()) return;
    swapCtx(new DOMParser().parseFromString(html, "text/html"));
  } catch (err) {
    void err;
  } finally {
    inFlight = false;
    if (dirty) void refreshCtx();
  }
}

export async function watchCtx() {
  const { onServerEvent } = await import("@dc/events");
  onServerEvent("projects", () => {
    if (document.querySelector(".dc-ctx [data-ctx-list='projects']")) void refreshCtx();
  });
  onServerEvent("terminals", () => {
    if (document.querySelector(".dc-ctx [data-ctx-list]")) void refreshCtx();
  });
  onServerEvent("docker", () => {
    if (document.querySelector(".dc-ctx [data-ctx-list='projects']")) void refreshCtx();
  });
}
