class BackupSections extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.addEventListener("change", (e) => {
      const box = e.target;
      if (!(box instanceof HTMLInputElement) || box.type !== "checkbox") return;
      if (box.checked) this.checkRequired(box);
      else this.uncheckDependents(box.value);
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  boxes() {
    return [...this.querySelectorAll('input[type="checkbox"][name="sections"]')];
  }

  checkRequired(box) {
    (box.dataset.requires || "").split(" ").filter(Boolean).forEach((required) => {
      const dep = this.boxes().find((b) => b.value === required);
      if (dep && !dep.disabled && !dep.checked) {
        dep.checked = true;
        this.checkRequired(dep);
      }
    });
  }

  uncheckDependents(value) {
    this.boxes().forEach((b) => {
      if (!b.checked) return;
      if ((b.dataset.requires || "").split(" ").includes(value)) {
        b.checked = false;
        this.uncheckDependents(b.value);
      }
    });
  }
}

customElements.define("dc-backup-sections", BackupSections);
