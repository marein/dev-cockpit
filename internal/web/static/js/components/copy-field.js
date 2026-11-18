import { copyText } from "@dc/dom";
import { notifyError, notifySuccess } from "@dc/toast";

class CopyField extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.querySelector("[data-copy]")?.addEventListener("click", () => void this.copy(), { signal });
    this.querySelector("input")?.addEventListener("focus", (event) => event.target.select(), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async copy() {
    const source = this.querySelector("template[data-copy-source]");
    if (source) {
      if (await copyText(source.content.textContent)) notifySuccess("Copied.");
      else notifyError("The clipboard is not available.");
      return;
    }
    const input = this.querySelector("input");
    if (!input) return;
    if (await copyText(input.value)) {
      notifySuccess("Copied.");
      return;
    }
    input.select();
    notifyError("The clipboard is not available. Copy the selected text by hand.");
  }
}

customElements.define("dc-copy-field", CopyField);
