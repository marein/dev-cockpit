import { onServerEvent } from "@dc/events";

class BackupList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("backups", () => this.refresh(), { signal: this.ac.signal });
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
    fetch("/settings/backup/list", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`status ${r.status}`))))
      .then((html) => {
        const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("[data-backups-body]");
        const body = this.querySelector("[data-backups-body]");
        if (fresh && body) body.replaceWith(fresh);
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty) {
          this.dirty = false;
          this.refresh();
        }
      });
  }
}

customElements.define("dc-backup-list", BackupList);
