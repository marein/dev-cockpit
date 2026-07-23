import { notifyError } from "@dc/toast";

// The backup import upload can be large (a full projects archive), so it runs
// through XHR for a real progress bar instead of pe.js, whose fetch cannot
// report upload progress. The inspect handler answers an XHR with a JSON
// location (the flash stays in the session), and the element then hands off
// to pe.js so the target renders with its flash.
class BackupUpload extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.form = this.querySelector("form");
    this.progress = this.querySelector("[data-upload-progress]");
    this.bar = this.progress?.querySelector(".progress-bar");
    if (!this.form) return;
    this.form.addEventListener("submit", (e) => this.onSubmit(e), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.xhr?.abort();
    this.ac?.abort();
    this.ac = null;
  }

  onSubmit(event) {
    event.preventDefault();
    if (!this.form.reportValidity()) return;
    const buttons = [...this.form.querySelectorAll("button")];
    buttons.forEach((b) => { b.disabled = true; b.classList.add("btn-loading"); });
    this.setProgress(0, true);

    const xhr = new XMLHttpRequest();
    this.xhr = xhr;
    xhr.open("POST", this.form.action);
    xhr.setRequestHeader("X-Requested-With", "XMLHttpRequest");
    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable) this.setProgress(Math.round((e.loaded / e.total) * 100));
    });
    xhr.addEventListener("load", () => {
      this.xhr = null;
      let location = "";
      try { location = (JSON.parse(xhr.responseText) || {}).location || ""; } catch (_) { location = ""; }
      if (xhr.status >= 200 && xhr.status < 300 && location) {
        if (window.pe) window.pe.navigate(location, true).catch(() => { window.location.assign(location); });
        else window.location.assign(location);
        return;
      }
      this.fail(buttons, xhr.statusText || `HTTP ${xhr.status}`);
    });
    xhr.addEventListener("error", () => { this.xhr = null; this.fail(buttons, "The upload failed, the connection was lost."); });
    xhr.addEventListener("abort", () => { this.xhr = null; this.fail(buttons, ""); });
    xhr.send(new FormData(this.form));
  }

  setProgress(percent, show) {
    if (!this.progress) return;
    if (show) this.progress.classList.remove("d-none");
    if (this.bar) this.bar.style.width = `${percent}%`;
  }

  fail(buttons, message) {
    this.setProgress(0);
    this.progress?.classList.add("d-none");
    buttons.forEach((b) => { b.disabled = false; b.classList.remove("btn-loading"); });
    if (message) notifyError(message);
  }
}

customElements.define("dc-backup-upload", BackupUpload);
