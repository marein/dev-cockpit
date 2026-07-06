class CoderSelect extends HTMLElement {
  connectedCallback() {
    if (this.abort) {
      return;
    }
    this.abort = new AbortController();
    const coder = this.querySelector('select[name="coder"]');
    if (!coder) {
      return;
    }
    const apply = () => {
      for (const group of this.querySelectorAll("[data-coder-agents]")) {
        const active = group.dataset.coderAgents === coder.value;
        group.hidden = !active;
        const agents = group.querySelector("select");
        if (agents) {
          agents.disabled = !active;
        }
      }
    };
    coder.addEventListener("change", apply, { signal: this.abort.signal });
    apply();
  }

  disconnectedCallback() {
    this.abort?.abort();
    this.abort = null;
  }
}

customElements.define("dc-coder-select", CoderSelect);
