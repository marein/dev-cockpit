import * as dialog from "@dc/dialog";
import { missedServerEvent, onServerEvent } from "@dc/events";
import { getText } from "@dc/http";
import { formView } from "@dc/prompt-view";

// The rows of Settings › Plugins, pulled again on every plugins event. The
// pull sends X-DC-Pull and gets the rows alone, so it does not consume the
// flash of a post still landing. Release notes opens a dialog whose confirm
// submits the row's update.
class PluginList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("plugins", () => this.refresh(), { signal: this.ac.signal });
    this.addEventListener("click", (e) => {
      const open = e.target instanceof Element ? e.target.closest("[data-plugin-notes-open]") : null;
      if (!open) return;
      e.preventDefault();
      this.showNotes(open);
    }, { signal: this.ac.signal });
    if (missedServerEvent("plugins")) this.refresh();
  }

  showNotes(button) {
    const step = button.closest("[data-plugin-step]");
    const id = button.closest("[data-plugin-row]")?.dataset.pluginRow || "";
    const version = step?.dataset.pluginStep || "";
    const html = step?.querySelector("template[data-plugin-notes]")?.innerHTML || "";
    const view = formView({
      title: button.dataset.pluginNotesTitle || "Release notes",
      details: [{ as: "long", label: "Release notes", html }],
      icon: "arrow-up-circle",
      confirmText: `Update to ${version}`,
    });
    dialog.fire({ ...view, width: "42rem" }).then((result) => {
      if (!result.isConfirmed || !this.ac) return;
      const form = [...this.querySelectorAll("[data-plugin-row] [data-plugin-step]")]
        .find((s) => s.closest("[data-plugin-row]").dataset.pluginRow === id && s.dataset.pluginStep === version)
        ?.querySelector("[data-plugin-update-form]");
      form?.requestSubmit();
    });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  refresh() {
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    getText(window.location.pathname, { signal: this.ac?.signal, headers: { "X-DC-Pull": "1" } })
      .then((html) => {
        if (!this.ac) return;
        const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("[data-plugin-list]");
        const list = this.querySelector("[data-plugin-list]");
        if (fresh && list && fresh.innerHTML !== list.innerHTML) list.replaceWith(fresh);
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty && this.ac) {
          this.dirty = false;
          this.refresh();
        }
      });
  }
}

customElements.define("dc-plugin-list", PluginList);
