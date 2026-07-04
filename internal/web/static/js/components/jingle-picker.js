import { playJingle } from "@dc/jingle";

class JinglePicker extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.previewAbort = null;

    this.addEventListener("change", (event) => {
      const radio = event.target.closest('input[name="jingle"]');
      if (!radio) return;
      this.previewAbort?.abort();
      this.previewAbort = new AbortController();
      playJingle(radio.value, { signal: this.previewAbort.signal }).catch(() => {});
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.previewAbort?.abort();
    this.ac?.abort();
    this.ac = null;
  }
}

customElements.define("dc-jingle-picker", JinglePicker);
