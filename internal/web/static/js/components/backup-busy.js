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
    if (this.hasAttribute("download")) this.wireDownload();
    else this.wireBoosted();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    clearInterval(this.timer);
  }

  wireBoosted() {
    window.addEventListener("pe:form", (e) => {
      if (e.detail.form !== this.form) return;
      this.status.classList.remove("d-none");
      e.detail.finally.push(() => this.status.classList.add("d-none"));
    }, { signal: this.ac.signal });
  }

  wireDownload() {
    this.form.addEventListener("submit", () => {
      const token = crypto.randomUUID();
      let field = this.form.querySelector('input[name="download_token"]');
      if (!field) {
        field = document.createElement("input");
        field.type = "hidden";
        field.name = "download_token";
        this.form.append(field);
      }
      field.value = token;
      const buttons = [...this.form.querySelectorAll("button")];
      buttons.forEach((b) => { b.disabled = true; b.classList.add("btn-loading"); });
      this.status.classList.remove("d-none");
      const started = Date.now();
      clearInterval(this.timer);
      this.timer = setInterval(() => {
        const seen = document.cookie.split("; ").some((c) => c === `dc-download=${token}`);
        if (!seen && Date.now() - started < 600000) return;
        clearInterval(this.timer);
        document.cookie = "dc-download=; Max-Age=0; path=/settings/backup";
        this.status.classList.add("d-none");
        buttons.forEach((b) => { b.disabled = false; b.classList.remove("btn-loading"); });
      }, 250);
    }, { signal: this.ac.signal });
  }
}

customElements.define("dc-backup-busy", BackupBusy);
