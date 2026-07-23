class BackupBusy extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.form = this.querySelector("form");
    this.status = document.createElement("div");
    this.status.className = "text-secondary small mt-2 d-flex align-items-center gap-2 d-none";
    const spinner = document.createElement("span");
    spinner.className = "spinner-border spinner-border-sm flex-shrink-0";
    const text = document.createElement("span");
    text.textContent = this.getAttribute("busy-text") || "Working, this can take a while.";
    this.status.append(spinner, text);
    this.append(this.status);
    if (!this.form) return;
    window.addEventListener("pe:form", (e) => {
      if (e.detail.form !== this.form) return;
      this.status.classList.remove("d-none");
      e.detail.finally.push(() => this.status.classList.add("d-none"));
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }
}

customElements.define("dc-backup-busy", BackupBusy);
