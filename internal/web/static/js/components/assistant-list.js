import { onServerEvent } from "@dc/events";
import { getText, postForm, postJSON, ensureOk } from "@dc/http";
import { confirm, fire } from "@dc/dialog";
import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { RowDrag } from "@dc/rowdrag";
import { notifyError, notifySuccess } from "@dc/toast";

// A self-refreshing assistant list: the steered jobs, the assistants in the
// list column, and the memory. The list swaps its [data-assistant-body] on the
// assistant event, so a check that finished or an assistant that answered
// elsewhere changes what is on screen without anybody pulling on a timer, and
// an action acts in place. With the history attribute the rows carry a context
// menu (right click, long press, and the kebab button) for what a row does not
// need a page for; with the memory attribute a row edits in place.
class AssistantList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.url = this.getAttribute("list-url");
    if (!this.url) return;
    this.pulling = false;
    this.dirty = false;
    // A quiet list refreshes only after its own actions: the memory list holds
    // open forms the user types into, a background swap would throw them away.
    if (!this.hasAttribute("quiet")) {
      onServerEvent("assistant", () => void this.refresh(), { signal: this.ac.signal });
    }
    this.addEventListener("submit", (event) => void this.onAction(event), { signal: this.ac.signal });

    if (this.hasAttribute("history")) {
      wireRowMenus(this, "[data-assistant-instance]", (row, x, y) => this.openRowMenu(row, x, y), { signal: this.ac.signal });
      this.addEventListener("click", (event) => {
        const button = event.target.closest("[data-assistant-menu]");
        if (!button) return;
        event.preventDefault();
        event.stopPropagation();
        const rect = button.getBoundingClientRect();
        this.openRowMenu(button.closest("[data-assistant-instance]"), rect.left, rect.bottom + 4);
      }, { signal: this.ac.signal });
      this.wireDrag(this.ac.signal);
    }

    if (this.hasAttribute("memory")) this.wireMemory(this.ac.signal);

    // A subscription row carries the app's menu: remove it, open the coder it
    // is about. The whole row is the finger's target, the three dots the
    // mouse's, and a right click or a long press opens the same menu.
    if (this.hasAttribute("subscriptions")) {
      wireRowMenus(this, "[data-assistant-subscription]", (row, x, y) => this.openSubscriptionMenu(row, x, y), { signal: this.ac.signal });
      this.addEventListener("click", (event) => {
        const button = event.target.closest("[data-assistant-subscription-menu]");
        if (!button) return;
        event.preventDefault();
        event.stopPropagation();
        const rect = button.getBoundingClientRect();
        this.openSubscriptionMenu(button.closest("[data-assistant-subscription]"), rect.left, rect.bottom + 4);
      }, { signal: this.ac.signal });
    }

    // Opening is the moment where being current matters most; closing drops the
    // work nobody is looking at. A list inside a sheet renders empty and pulls
    // itself every time the sheet opens.
    this.modal = this.querySelector(".modal");
    this.modal?.addEventListener("show.bs.modal", () => void this.refresh(), { signal: this.ac.signal });
    this.modal?.addEventListener("hidden.bs.modal", () => { this.dirty = false; }, { signal: this.ac.signal });
    this.sheet = this.closest(".offcanvas");
    this.sheet?.addEventListener("show.bs.offcanvas", () => void this.refresh(), { signal: this.ac.signal });

    this.revealActive(true);
  }

  revealActive(center = false) {
    const row = this.querySelector("[data-assistant-instance].active");
    if (!row) return;
    row.scrollIntoView({ block: center ? "center" : "nearest" });
  }

  // A memory row edits where it stands: the pencil hides the reading half and
  // shows the row's own form in its place, so nothing repeats itself and
  // nothing jumps. Only one row is open at a time. Cancel resets the form back
  // to the rendered values, so a reopened row starts from what is stored, and
  // Escape cancels the edit instead of closing the whole overlay.
  wireMemory(signal) {
    this.addEventListener("click", (event) => {
      const open = event.target.closest("[data-memory-edit-open]");
      if (open) {
        event.preventDefault();
        this.openMemoryEdit(open.closest("[data-memory-entry]"));
        return;
      }
      const cancel = event.target.closest("[data-memory-edit-cancel]");
      if (cancel) {
        event.preventDefault();
        this.closeMemoryEdit(cancel.closest("[data-memory-entry]"));
      }
    }, { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      const entry = event.target.closest("[data-memory-entry]");
      if (!entry || entry.querySelector("[data-memory-edit]")?.hidden !== false) return;
      event.stopPropagation();
      this.closeMemoryEdit(entry);
    }, { signal });
  }

  openMemoryEdit(entry) {
    if (!entry) return;
    for (const other of this.querySelectorAll("[data-memory-entry]")) {
      if (other !== entry) this.closeMemoryEdit(other);
    }
    entry.querySelector("[data-memory-view]").hidden = true;
    const form = entry.querySelector("[data-memory-edit]");
    form.hidden = false;
    const title = form.querySelector('input[name="title"]');
    title?.focus();
    title?.setSelectionRange(title.value.length, title.value.length);
  }

  closeMemoryEdit(entry) {
    const form = entry?.querySelector("[data-memory-edit]");
    if (!form || form.hidden) return;
    form.reset();
    form.hidden = true;
    entry.querySelector("[data-memory-view]").hidden = false;
  }

  // An assistant's row menu: open it, give it a name of its own, or delete it.
  // Renaming is here and not on the page because with several of them the name
  // is what the list is read by, and this is the list.
  openRowMenu(row, x, y) {
    if (!row) return null;
    // A long press on the grip is both a drag and a menu; the menu wins.
    this.rowDrag?.cancel();
    const url = row.dataset.assistantUrl || "/assistants/" + row.dataset.assistantInstance;
    const name = row.dataset.assistantName || "";
    return openMenu({
      x,
      y,
      signal: this.ac.signal,
      items: [
        {
          label: "Open",
          icon: "ti-message",
          href: url,
          action: () => {
            if (window.app?.navigate) window.app.navigate(url);
            else window.location.assign(url);
          },
        },
        { label: "Rename", icon: "ti-pencil", action: () => void this.renameAssistant(url, name) },
        { label: "Delete", icon: "ti-trash", danger: true, action: () => void this.deleteAssistant(url, name) },
      ],
    });
  }

  async renameAssistant(url, name) {
    const result = await fire({
      title: "Rename this assistant",
      input: "text",
      inputValue: name,
      inputAttributes: { "aria-label": "Name" },
      showCancelButton: true,
      confirmButtonText: "Rename",
      cancelButtonText: "Cancel",
      reverseButtons: true,
    });
    if (!result.isConfirmed) return;
    const title = (result.value || "").trim();
    if (!title || title === name) return;
    try {
      const response = await postForm(url, { form: "rename", title });
      await ensureOk(response, "The assistant could not be renamed.");
    } catch (err) {
      notifyError(err?.message || "The assistant could not be renamed.");
      return;
    }
    await this.refresh();
  }

  async deleteAssistant(url, name) {
    const ok = await confirm({
      title: name ? `Delete "${name}"?` : "Delete this assistant?",
      text: "Its thread goes with it. The coders it steers are released and yours again, the shared memory is kept.",
      confirmText: "Delete",
    });
    if (!ok) return;
    let released = "";
    try {
      const response = await postForm(url, { form: "delete" });
      await ensureOk(response, "The assistant could not be deleted.");
      // The answer names the coders that came back, and that is the one thing
      // the user cannot see for themselves once the row is gone.
      released = (await response.json().catch(() => ({})))?.released || "";
    } catch (err) {
      notifyError(err?.message || "The assistant could not be deleted.");
      return;
    }
    if (released) notifySuccess(released);
    // The page of the deleted assistant is gone with it, the bare address opens
    // whichever one is left; every other page just sees the row go.
    if (window.location.pathname === url) {
      if (window.app?.navigate) window.app.navigate("/assistants");
      else window.location.assign("/assistants");
      return;
    }
    await this.refresh();
  }

  openSubscriptionMenu(row, x, y) {
    if (!row) return null;
    const id = row.dataset.assistantSubscription;
    const label = row.dataset.assistantSubscriptionLabel || "";
    const items = [];
    if (row.dataset.assistantSubscriptionUrl) {
      items.push({ label: "Open coder", icon: "ti-terminal-2", href: row.dataset.assistantSubscriptionUrl });
    }
    // Only a subscription that still fires carries its stand, and that is what
    // decides whether it can be changed: one that is over is spent.
    if (row.dataset.assistantSubscriptionEdit) {
      items.push({ label: "Edit", icon: "ti-pencil", action: () => void this.editSubscription(row.dataset.assistantSubscriptionEdit) });
    }
    items.push({ label: "Remove", icon: "ti-trash", danger: true, action: () => void this.removeSubscription(id, label) });
    return openMenu({ x, y, signal: this.ac.signal, items });
  }

  // Editing opens the form that makes one, filled with this subscription: one
  // form, one set of fields, one path. The aside holds both, so the row hands
  // its stand over and the form does the rest.
  async editSubscription(stand) {
    const form = document.querySelector("[data-assistant-subscribe]");
    if (!form) return;
    await customElements.whenDefined("dc-assistant-subscribe");
    let data = null;
    try {
      data = JSON.parse(stand);
    } catch {
      return;
    }
    form.fill?.(data);
    form.scrollIntoView({ block: "nearest" });
  }

  async removeSubscription(id, label) {
    const ok = await confirm({
      title: label ? `Remove "${label}"?` : "Remove this subscription?",
      text: "The assistant stops reacting to it. What it already did stays in the thread.",
      confirmText: "Remove",
      target: this.sheet?.classList.contains("show") ? this.sheet : undefined,
      heightAuto: false,
    });
    if (!ok) return;
    try {
      const response = await postForm(this.url.split("?")[0], { form: "remove", id });
      await ensureOk(response, "The subscription could not be removed.");
    } catch (err) {
      notifyError(err?.message || "The subscription could not be removed.");
      return;
    }
    await this.refresh();
  }

  // The list is sorted by hand, so the rows are dragged into place and the
  // order is posted to the server, which is where it lives: it comes back on
  // the next reload, on this device and on every other one. The gesture is the
  // tab strip's own, @dc/rowdrag, and this is one list with no groups in it, so
  // a row only ever changes seats. The one deviation is the capture: this row
  // is a container with the link inside it, so a capture on the press would
  // retarget every click that opens an assistant.
  wireDrag(signal) {
    this.rowDrag = new RowDrag(this, {
      rowSelector: "[data-assistant-instance]",
      gripSelector: "[data-assistant-grip]",
      ignoreSelector: "[data-assistant-menu]",
      capture: "drag",
      classes: { list: "dc-rows-dragging", row: "dc-row-dragging" },
      rows: () => this.rows(),
      scroller: () => this.querySelector(".dc-ctx-body") || this,
      onDrop: () => this.persistOrder(),
      onEnd: () => { if (this.dirty) void this.refresh(); },
    });
    this.rowDrag.wire(signal);
  }

  rows() {
    return Array.from(this.querySelectorAll("[data-assistant-instance]"));
  }

  persistOrder() {
    const ids = this.rows().map((row) => row.dataset.assistantInstance).filter(Boolean);
    postJSON("/assistants/order", { ids })
      .then((response) => ensureOk(response, "Could not save the order."))
      .catch((error) => notifyError(error.message));
  }

  disconnectedCallback() {
    this.rowDrag?.cancel();
    this.ac?.abort();
    this.ac = null;
  }

  // A job action is a normal form, posting the same route the page and the
  // command line post to. Everything that ends something carries data-confirm,
  // and that is the cockpit's one confirmation, from @dc/dialog with its native
  // fallback.
  async onAction(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || form.dataset.ajaxRefresh === undefined) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (form.dataset.confirm) {
      const ok = await confirm({
        title: form.dataset.confirm,
        confirmText: form.dataset.confirmButton || "Confirm",
        target: this.modal?.classList.contains("show") ? this.modal : undefined,
        heightAuto: false,
      });
      if (!ok) return;
    }
    const buttons = Array.from(form.querySelectorAll("button[type=submit]"));
    buttons.forEach((button) => { button.disabled = true; });
    try {
      const response = await fetch(form.action, {
        method: "POST",
        headers: { Accept: "application/json" },
        body: new URLSearchParams(new FormData(form)),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload.error || "The cockpit refused that.");
      }
    } catch (err) {
      notifyError(err?.message || "Could not reach the cockpit.");
    } finally {
      buttons.forEach((button) => { button.disabled = false; });
      await this.refresh();
    }
  }

  async refresh() {
    // A swap in the middle of a drag would take the row out from under the
    // pointer, so the pull waits for the gesture and runs when it ends.
    if (this.rowDrag?.busy) {
      this.dirty = true;
      return;
    }
    if (this.pulling) {
      this.dirty = true;
      return;
    }
    this.pulling = true;
    this.dirty = false;
    try {
      const html = await getText(this.url);
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.querySelector("[data-assistant-body]");
      const current = this.querySelector("[data-assistant-body]");
      if (fresh && current) {
        current.replaceWith(fresh);
        this.revealActive();
      }
    } catch {
      void 0;
    } finally {
      this.pulling = false;
      if (this.dirty) void this.refresh();
    }
  }

}

customElements.define("dc-assistant-list", AssistantList);
