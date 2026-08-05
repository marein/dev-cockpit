import { restoreLinkRules } from "@dc/docker";

// The link rules section of the docker settings: rows come and go in the
// browser and travel with the one form on the page, the same way the compose
// actions next to it work. What a rule finds and what is wrong with it are
// server side, they come out of the same matcher the pages build links with,
// so this element never judges a pattern itself.
class DockerLinkRules extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.rows = this.querySelector("[data-link-rows]");
    this.template = this.querySelector("[data-link-template]");
    this.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-link-remove]");
      if (remove && this.contains(remove)) {
        event.preventDefault();
        remove.closest("[data-link-row]")?.remove();
        return;
      }
      const add = event.target.closest("[data-link-add]");
      if (add && this.contains(add)) {
        event.preventDefault();
        this.addRow();
        return;
      }
      const restore = event.target.closest("[data-link-restore]");
      if (restore && this.contains(restore)) {
        event.preventDefault();
        void this.restore(restore);
      }
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async restore(button) {
    button.disabled = true;
    const ok = await restoreLinkRules();
    button.disabled = false;
    if (!ok) return;
    if (window.pe) window.pe.navigate(window.location.href);
    else window.location.reload();
  }

  addRow() {
    if (!this.rows || !this.template) return;
    const row = this.template.content.firstElementChild.cloneNode(true);
    this.rows.appendChild(row);
    this.querySelector("[data-link-empty]")?.remove();
    row.querySelector('input[name="link_label"]')?.focus();
  }
}

customElements.define("dc-docker-link-rules", DockerLinkRules);
