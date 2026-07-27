import { getText } from "@dc/http";
import { onServerEvent } from "@dc/events";
import * as store from "@dc/store";

// The assistant overlay, the assistant's only surface. It lives in the layout
// next to the swapped page region, never in it, so an open surface, a streaming
// answer and the words in the composer survive every boosted navigation
// untouched, nothing is re-fetched. From the lg breakpoint
// up it docks as a side panel: the page shrinks by the panel's width and
// re-centers in what is left, the corner button that opens it replaces the
// floating quick nav, and the left edge drags to resize. Below lg the same
// surface covers the whole screen and scrolls internally.
//
// The overlay is its own world: its jobs, memory and history render as views
// inside it, an earlier conversation opens read-only inside it, and only a
// link that really leaves (a job's coder) navigates the page. There are no
// assistant pages; a notification link carries `?assistant=<conversation>`
// plus a `#message-` fragment, which this element consumes to open the
// overlay on the announced answer.
const STORE_KEY = "dc-assistant-panel";
const WIDTH_KEY = "dc-assistant-panel-w";

function docked() {
  return window.matchMedia("(min-width: 992px)").matches;
}

// The page next to the panel keeps at least the width its desktop header
// needs: media queries read the viewport, not the squeezed page, so a panel
// that leaves less than that would break the navbar's md+ layout. On windows
// too narrow to hold both, the 320px panel floor wins and style.css compacts
// the header to icons for that range.
const MIN_PAGE = 900;
const DEFAULT_WIDTH = 400;

function clampWidth(px) {
  const max = Math.max(320, Math.min(Math.round(window.innerWidth * 0.6), window.innerWidth - MIN_PAGE));
  return Math.min(Math.max(Math.round(px), 320), max);
}

function lastMessage(root) {
  const messages = root.querySelectorAll("[data-assistant-message][data-message-id]");
  const last = messages[messages.length - 1];
  return last ? `${last.getAttribute("data-message-id")}:${last.getAttribute("data-state")}` : "";
}

function composerHoldsUnsavedWords(surface, fresh) {
  const input = surface.querySelector("[data-assistant-input]");
  const freshInput = fresh.querySelector("[data-assistant-input]");
  if (!input || !freshInput) return false;
  return input.value !== freshInput.value;
}

class AssistantPanel extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.panelUrl = this.getAttribute("panel-url") || "/assistant/panel";
    this.card = this.querySelector(".dc-assistant-panel-card");
    this.body = this.querySelector("[data-panel-body]");
    this.corner = this.querySelector("[data-assistant-corner]");
    this.view = "chat";
    this.conversation = "";
    this.loading = false;
    this.applyStoredWidth();

    // The capture listener kills the native navigation, the pe:click listener
    // keeps pe.js out of it; propagation stays intact so open menus and
    // dropdowns close like on any other click.
    document.addEventListener("click", (event) => {
      const trigger = this.trigger(event.target);
      if (!trigger) return;
      event.preventDefault();
      this.toggle();
    }, { signal, capture: true });
    window.addEventListener("pe:click", (event) => {
      if (this.trigger(event.detail.a)) event.preventDefault();
    }, { signal });
    this.corner?.addEventListener("click", () => this.toggle(), { signal });

    this.addEventListener("click", (event) => {
      if (event.target.closest("[data-assistant-panel-close]")) {
        event.preventDefault();
        this.close();
        return;
      }
      const view = event.target.closest("[data-assistant-view-open]");
      if (view) {
        event.preventDefault();
        event.stopPropagation();
        this.conversation = "";
        void this.load({ view: view.dataset.assistantViewOpen, focus: view.dataset.assistantViewOpen === "chat" });
        return;
      }
      // A history row and the way back from an earlier conversation open
      // inside the overlay, it is its own world. The kebab is the row's menu,
      // not the row.
      if (event.target.closest("[data-conversation-menu]")) return;
      const conversation = event.target.closest("[data-conversation-url], [data-assistant-current]");
      if (conversation && this.contains(conversation)) {
        event.preventDefault();
        event.stopPropagation();
        this.openConversation(conversation.getAttribute("href") || conversation.dataset.conversationUrl || "");
        return;
      }
      const all = event.target.closest("[data-assistant-all]");
      if (all) {
        // Older messages load into the surface, not into the page behind it.
        event.preventDefault();
        event.stopPropagation();
        const place = this.readingPlace();
        void this.load({ view: "chat", all: true }).then(() => this.restorePlace(place));
      }
    }, { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      this.close();
    }, { signal });

    // A form of the surface (a new conversation, a memory save) posts here and
    // the current view reloads; pe.js would navigate the page under the
    // overlay instead. The composer and the ajax lists prevent their submits
    // before this runs.
    this.addEventListener("submit", (event) => {
      if (event.defaultPrevented) return;
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      event.preventDefault();
      event.stopPropagation();
      void this.submitAndReload(form);
    }, { signal });

    // Another device may have started a new conversation, or a message arrived
    // while the event stream was down (the connect snapshot repeats the signal,
    // so a reconnect catches up like the tab strip does): the chat swaps to the
    // live conversation, and within the same one it re-renders only when the
    // transcript moved and the composer holds nothing unsaved, so a running
    // answer and words being typed stay untouched.
    onServerEvent("assistant", () => void this.syncConversation(), { signal });

    this.wireResize(signal);
    this.syncCornerTarget();
    window.addEventListener("dc:navigated", () => this.onNavigated(), { signal });
    window.addEventListener("resize", () => {
      // A window that shrank may no longer hold the stored width.
      this.applyStoredWidth();
      this.applyLocation();
    }, { signal, passive: true });
    if (!this.consumeUrlRequest()) this.applyLocation();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    document.body.classList.remove("dc-assistant-docked");
  }

  trigger(node) {
    return node?.closest?.("[data-assistant-link], [data-quicknav-assistant], [data-tabs-assistant]") || null;
  }

  get open() {
    return Boolean(this.card) && !this.card.hasAttribute("hidden");
  }

  // consumeUrlRequest opens what the address asked for: a notification link
  // and every old assistant URL redirect here with `?assistant=` naming a
  // conversation or a view, plus a #message fragment for the answer to land
  // on. The request is taken out of the address once consumed, so a reload or
  // a navigation away and back does not replay it.
  consumeUrlRequest() {
    const params = new URLSearchParams(window.location.search);
    const wanted = params.get("assistant");
    if (!wanted) return false;
    const anchor = window.location.hash.startsWith("#message-")
      ? decodeURIComponent(window.location.hash.slice("#message-".length))
      : "";
    params.delete("assistant");
    const query = params.toString();
    const clean = window.location.pathname + (query ? "?" + query : "");
    window.history.replaceState(null, "", clean);
    this.openRequest(wanted, anchor);
    return true;
  }

  // openFromUrl is the entry point for a notification click that stays on the
  // page: the caller hands in the URL the notification carries, so conversation
  // and message anchor are read from the same place the address flow reads
  // them. Returns false when the URL asks for no assistant, the caller
  // navigates then.
  openFromUrl(url) {
    const parsed = new URL(url, window.location.origin);
    const wanted = parsed.searchParams.get("assistant");
    if (!wanted) return false;
    const anchor = parsed.hash.startsWith("#message-")
      ? decodeURIComponent(parsed.hash.slice("#message-".length))
      : "";
    this.openRequest(wanted, anchor);
    return true;
  }

  // openRequest opens the overlay on what was asked for: a view, the live
  // chat, or one conversation with the answer to land on.
  openRequest(wanted, anchor) {
    this.card?.removeAttribute("hidden");
    this.syncBodyClass();
    if (docked()) store.set(STORE_KEY, "open");
    this.view = "chat";
    this.conversation = "";
    if (wanted === "memory" || wanted === "history" || wanted === "jobs") {
      void this.load({ view: wanted });
      return;
    }
    if (wanted !== "open") this.conversation = wanted;
    void this.load({ view: "chat", focus: !anchor }).then(async () => {
      if (anchor) await this.anchorMessage(anchor);
    });
  }

  // anchorMessage lands the transcript on one answer. A message the window
  // held back is reached by loading the whole transcript once.
  async anchorMessage(messageId) {
    if (await this.tryAnchor(messageId)) return;
    await this.load({ view: "chat", all: true });
    await this.tryAnchor(messageId);
  }

  async tryAnchor(messageId) {
    const surface = await this.readySurface();
    return Boolean(surface?.focusMessage?.(messageId));
  }

  async readySurface() {
    for (let i = 0; i < 20; i += 1) {
      const surface = this.body?.querySelector("dc-assistant[ready]");
      if (surface) return surface;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    return null;
  }

  readingPlace() {
    const scroller = this.body?.querySelector("[data-assistant-scroll]");
    if (!scroller) return null;
    const top = scroller.getBoundingClientRect().top;
    for (const message of scroller.querySelectorAll("[data-assistant-message][data-message-id]")) {
      const rect = message.getBoundingClientRect();
      if (rect.bottom > top) {
        return { id: message.getAttribute("data-message-id"), offset: rect.top - top };
      }
    }
    return null;
  }

  async restorePlace(place) {
    if (!place) return;
    (await this.readySurface())?.anchorTo?.(place.id, place.offset);
  }

  // The docked panel is a lasting arrangement and comes back on the next page.
  // The fullscreen overlay is a visit: a navigation out of it (a job's coder)
  // is a destination, so it closes; a fresh page load starts without it.
  onNavigated() {
    if (!this.syncSession()) return;
    this.syncCornerTarget();
    if (this.consumeUrlRequest()) return;
    if (this.open && !docked()) {
      this.hide();
      return;
    }
    this.applyLocation();
  }

  // The overlay outlives the page under it, so it also outlives the pages that
  // carry no app chrome at all: the login page an expired session swaps in, and
  // the error page. Both used to take the overlay with them when it still lived
  // in the swapped region. It closes and steps aside for as long as they stand,
  // and the next page with a header brings it back; signing in is a native load
  // and renders the whole layout fresh anyway.
  syncSession() {
    const chrome = Boolean(document.querySelector("[data-assistant-link]"));
    if (!chrome && this.open) this.hide();
    this.hidden = !chrome;
    return chrome;
  }

  // The corner button is rendered once per full page load, not per navigation
  // like the header's entry, so its news target would keep pointing at the
  // conversation that was live back then. It follows the live one: the loaded
  // surface names it, and with the overlay closed the freshly rendered header
  // link does. A target that moved carries no news of its own yet.
  syncCornerTarget() {
    const icon = this.corner?.querySelector(".dc-term-icon");
    if (!icon) return;
    const live = this.conversation ? null : this.body?.querySelector("dc-assistant[conversation-id]");
    const id = live?.getAttribute("conversation-id")
      || document.querySelector("[data-assistant-link] [data-notify-target]")?.getAttribute("data-notify-target")
      || "";
    if (id === (icon.getAttribute("data-notify-target") || "")) return;
    if (id) icon.setAttribute("data-notify-target", id);
    else icon.removeAttribute("data-notify-target");
    icon.classList.remove("news");
  }

  applyLocation() {
    if (this.open) return;
    if (docked() && store.get(STORE_KEY) === "open") this.show(false);
  }

  toggle() {
    if (this.open) this.close();
    else this.openPanel();
  }

  openPanel() {
    if (docked()) store.set(STORE_KEY, "open");
    this.show(true);
  }

  close() {
    if (docked()) store.set(STORE_KEY, "closed");
    this.hide();
  }

  // The composer is only focused on an intentional open: a restored panel on a
  // fresh page load must not steal the keyboard, the page may be a terminal.
  show(focus) {
    if (!this.card) return;
    this.card.removeAttribute("hidden");
    this.syncBodyClass();
    if (this.body && !this.body.firstElementChild) void this.load({ view: "chat", focus });
    else if (focus) this.body?.querySelector("[data-assistant-input]")?.focus();
  }

  // Hiding unloads the surface: its element disconnects, which closes the
  // conversation stream, so a closed overlay costs nothing. Opening fetches
  // the current state fresh.
  hide() {
    this.card?.setAttribute("hidden", "");
    this.syncBodyClass();
    this.view = "chat";
    this.conversation = "";
    if (this.body) this.body.replaceChildren();
  }

  syncBodyClass() {
    document.body.classList.toggle("dc-assistant-docked", this.open);
  }

  // openConversation shows one conversation inside the overlay: a history row,
  // a notification link, or the way back to the live one from a read-only
  // transcript.
  openConversation(url) {
    const id = (url.split("/assistant/")[1] || "").split(/[?#]/)[0];
    this.conversation = id;
    void this.load({ view: "chat", focus: true });
  }

  viewUrl(view, all) {
    const params = new URLSearchParams();
    if (view && view !== "chat") params.set("view", view);
    if (view === "chat" && this.conversation) params.set("conversation", this.conversation);
    if (all) params.set("all", "1");
    const query = params.toString();
    return this.panelUrl + (query ? "?" + query : "");
  }

  async load({ view = this.view, all = false, focus = false } = {}) {
    if (!this.body || this.loading) return;
    this.loading = true;
    this.view = view;
    const spinner = setTimeout(() => {
      this.body.innerHTML = '<div class="d-flex flex-fill align-items-center justify-content-center py-5"><div class="spinner-border text-secondary" role="status" aria-label="Loading the assistant"></div></div>';
    }, 250);
    try {
      const html = await getText(this.viewUrl(view, all));
      clearTimeout(spinner);
      this.body.innerHTML = html;
      await window.app?.loadElements?.(this.body);
      if (focus && view === "chat") this.body.querySelector("[data-assistant-input]")?.focus();
      if (view === "chat") {
        this.syncCornerTarget();
        document.dispatchEvent(new CustomEvent("dc:assistant-shown"));
      }
    } catch {
      clearTimeout(spinner);
      this.body.innerHTML = `
        <div class="dc-assistant-panel-view">
          <div class="d-flex align-items-center gap-1 px-3 flex-shrink-0" data-assistant-head>
            <span class="fw-medium flex-fill min-w-0 text-truncate">Assistant</span>
            <button type="button" class="btn btn-icon btn-ghost-secondary" aria-label="Close the assistant" title="Close" data-assistant-panel-close><i class="ti ti-x"></i></button>
          </div>
          <div class="d-flex flex-column flex-fill align-items-center justify-content-center gap-3 text-secondary py-4">
            <div>The assistant could not be loaded.</div>
            <button type="button" class="btn" data-assistant-panel-retry><i class="ti ti-refresh me-1"></i>Try again</button>
          </div>
        </div>`;
      this.body.querySelector("[data-assistant-panel-retry]")?.addEventListener("click", () => {
        void this.load({ view, all, focus });
      }, { once: true });
    } finally {
      this.loading = false;
    }
  }

  async submitAndReload(form) {
    const buttons = Array.from(form.elements).filter((el) => el instanceof HTMLButtonElement && el.type === "submit");
    buttons.forEach((button) => { button.disabled = true; });
    try {
      await fetch(form.action, { method: "POST", body: new FormData(form), headers: { Accept: "application/json" } });
    } catch {
      void 0;
    } finally {
      buttons.forEach((button) => { button.disabled = false; });
    }
    // A new conversation always lands in the live chat, everything else comes
    // back to the view the form was part of.
    if (form.querySelector('input[name="form"][value="new"]')) {
      this.conversation = "";
      this.view = "chat";
    }
    await this.load();
  }

  async syncConversation() {
    if (!this.open || this.view !== "chat" || this.conversation || !this.body?.firstElementChild) return;
    const surface = this.body.querySelector("dc-assistant");
    if (!surface || surface.hasAttribute("running")) return;
    try {
      const html = await getText(this.viewUrl("chat"));
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.querySelector("dc-assistant");
      if (!fresh) return;
      if (fresh.getAttribute("conversation-id") === surface.getAttribute("conversation-id")) {
        if (lastMessage(fresh) === lastMessage(surface)) return;
        if (composerHoldsUnsavedWords(surface, fresh)) return;
      }
      this.body.replaceChildren(...holder.childNodes);
      await window.app?.loadElements?.(this.body);
      this.syncCornerTarget();
    } catch {
      void 0;
    }
  }

  applyStoredWidth() {
    const stored = Number(store.get(WIDTH_KEY)) || DEFAULT_WIDTH;
    document.documentElement.style.setProperty("--dc-assistant-w", `${clampWidth(stored)}px`);
  }

  // The docked panel resizes by its left edge. The width lives in the one
  // custom property the card and the page margin both read, so the page
  // re-centers while the pointer moves.
  wireResize(signal) {
    const handle = this.querySelector("[data-assistant-resize]");
    if (!handle) return;
    handle.addEventListener("pointerdown", (event) => {
      if (!docked()) return;
      event.preventDefault();
      handle.setPointerCapture(event.pointerId);
      document.body.style.userSelect = "none";
      document.body.style.cursor = "col-resize";
      const move = (ev) => {
        const width = clampWidth(window.innerWidth - ev.clientX);
        document.documentElement.style.setProperty("--dc-assistant-w", `${width}px`);
      };
      const up = (ev) => {
        handle.removeEventListener("pointermove", move);
        handle.removeEventListener("pointerup", up);
        handle.removeEventListener("pointercancel", up);
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        const width = clampWidth(window.innerWidth - ev.clientX);
        store.set(WIDTH_KEY, String(width));
      };
      handle.addEventListener("pointermove", move, { signal });
      handle.addEventListener("pointerup", up, { signal });
      handle.addEventListener("pointercancel", up, { signal });
    }, { signal });
  }
}

customElements.define("dc-assistant-panel", AssistantPanel);
