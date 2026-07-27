import { onServerEvent } from "@dc/events";
import { getText, postForm, ensureOk } from "@dc/http";
import { confirm } from "@dc/dialog";
import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { notifyError } from "@dc/toast";

// A self-refreshing assistant list: the steered jobs in their modal, the
// history rows, and the memory. The list swaps itself on the assistant event,
// so a check that finished or a conversation that ended elsewhere changes what
// is on screen without anybody pulling on a timer, and an action acts in place.
// With the history attribute the rows carry a context menu (right click, long
// press, and the kebab button) for what a row does not need a page for; with
// the memory attribute a row edits in place.
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
      wireRowMenus(this, "[data-assistant-conversation]", (row, x, y) => this.openRowMenu(row, x, y), { signal: this.ac.signal });
      this.addEventListener("click", (event) => {
        const button = event.target.closest("[data-conversation-menu]");
        if (!button) return;
        event.preventDefault();
        event.stopPropagation();
        const rect = button.getBoundingClientRect();
        this.openRowMenu(button.closest("[data-assistant-conversation]"), rect.left, rect.bottom + 4);
      }, { signal: this.ac.signal });
    }

    if (this.hasAttribute("memory")) this.wireMemory(this.ac.signal);

    // Opening is the moment where being current matters most; closing drops the
    // work nobody is looking at.
    this.modal = this.querySelector(".modal");
    this.modal?.addEventListener("show.bs.modal", () => void this.refresh(), { signal: this.ac.signal });
    this.modal?.addEventListener("hidden.bs.modal", () => { this.dirty = false; }, { signal: this.ac.signal });
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

  openRowMenu(row, x, y) {
    if (!row) return null;
    const url = row.dataset.conversationUrl || "/assistant/" + row.dataset.assistantConversation;
    return openMenu({
      x,
      y,
      signal: this.ac.signal,
      items: [
        {
          label: "Open",
          icon: "ti-message",
          action: () => {
            // Inside the overlay a conversation opens in the overlay, it is
            // its own world; the history page navigates.
            const panel = this.closest("dc-assistant-panel");
            if (panel?.openConversation) panel.openConversation(url);
            else if (window.pe) window.pe.navigate(url);
            else window.location.assign(url);
          },
        },
        { label: "Delete", icon: "ti-trash", danger: true, action: () => void this.deleteConversation(url) },
      ],
    });
  }

  async deleteConversation(url) {
    const ok = await confirm({
      title: "Delete this conversation? The memory is kept.",
      confirmText: "Delete",
    });
    if (!ok) return;
    try {
      const response = await postForm(url, { form: "delete" });
      await ensureOk(response, "The conversation could not be deleted.");
    } catch (err) {
      notifyError(err?.message || "The conversation could not be deleted.");
    }
    await this.refresh();
  }

  disconnectedCallback() {
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
      if (fresh && current) current.replaceWith(fresh);
    } catch {
      void 0;
    } finally {
      this.pulling = false;
      if (this.dirty) void this.refresh();
    }
  }

}

customElements.define("dc-assistant-list", AssistantList);
