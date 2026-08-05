import { openMenu } from "@dc/contextmenu";
import { restoreActions } from "@dc/docker";

class DockerActions extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.rows = this.querySelector("[data-actions-rows]");
    this.template = this.querySelector("[data-action-template]");
    this.icons = (this.getAttribute("icons") || "").split(" ").filter(Boolean).map((pair) => {
      const [name, icon] = pair.split(":");
      return { name, icon };
    });
    this.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-action-remove]");
      if (remove && this.contains(remove)) {
        event.preventDefault();
        remove.closest("[data-action-row]")?.remove();
        return;
      }
      const pick = event.target.closest("[data-action-icon-pick]");
      if (pick && this.contains(pick)) {
        event.preventDefault();
        this.pickIcon(pick);
        return;
      }
      const add = event.target.closest("[data-action-add]");
      if (add && this.contains(add)) {
        event.preventDefault();
        this.addRow();
        return;
      }
      const restore = event.target.closest("[data-actions-restore]");
      if (restore && this.contains(restore)) {
        event.preventDefault();
        void this.restore(restore);
      }
    }, { signal: this.ac.signal });
    this.addEventListener("change", (event) => {
      const box = event.target.closest("[data-action-confirm-box]");
      if (!box || !this.contains(box)) return;
      const value = box.closest("[data-action-row]")?.querySelector("[data-action-confirm]");
      if (value) value.value = box.checked ? "1" : "0";
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  pickIcon(button) {
    const row = button.closest("[data-action-row]");
    if (!row) return;
    const value = row.querySelector("[data-action-icon-value]");
    const preview = row.querySelector("[data-action-icon-preview]");
    const rect = button.getBoundingClientRect();
    openMenu({
      x: Math.round(rect.left),
      y: Math.round(rect.bottom),
      signal: this.ac.signal,
      items: this.icons.map((entry) => ({
        label: entry.name,
        icon: entry.icon,
        action: () => {
          if (value) value.value = entry.name;
          if (preview) preview.className = `ti ${entry.icon}`;
        },
      })),
    });
  }

  async restore(button) {
    button.disabled = true;
    const ok = await restoreActions();
    button.disabled = false;
    if (!ok) return;
    if (window.pe) window.pe.navigate(window.location.href);
    else window.location.reload();
  }

  addRow() {
    if (!this.rows || !this.template) return;
    const row = this.template.content.firstElementChild.cloneNode(true);
    row.querySelector("[data-action-id]").value = this.freeId();
    this.rows.appendChild(row);
    this.querySelector("[data-actions-empty]")?.remove();
    row.querySelector('input[name="action_label"]')?.focus();
  }

  freeId() {
    const taken = new Set([...this.querySelectorAll("[data-action-id]")].map((input) => input.value));
    for (let n = 1; ; n++) {
      const id = `action-${n}`;
      if (!taken.has(id)) return id;
    }
  }
}

customElements.define("dc-docker-actions", DockerActions);
