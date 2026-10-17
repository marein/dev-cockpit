class EffortPick extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const model = this.previousElementSibling;
    if (!model || model.localName !== "dc-model-pick") return;
    model.addEventListener("dc-model-change", (event) => this.follow(event.detail?.value || ""), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  follow(model) {
    const empty = this.querySelector("select")?.options[0];
    if (!empty || empty.value !== "") return;
    empty.textContent = this.getAttribute(model ? "own-label" : "follow-label") || empty.textContent;
  }
}

customElements.define("dc-effort-pick", EffortPick);
