import { onServerEvent } from "@dc/events";
import { getText } from "@dc/http";

// A heading that keeps its terminal's name current. The name of a coder lives
// with the program, not with the page: the CLI writes it into its own session
// record, so a rename happens where no handler can announce it and the heading
// would stand on whatever was true when the page was rendered. The server sees
// the moved name on its next watch tick and sends the plain `terminals` event
// every other terminal change sends; this pulls the one name from name-url and
// re-applies it to the label and, with a title-suffix, to the browser title.
class LiveName extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.url = this.getAttribute("name-url");
    this.titleSuffix = this.getAttribute("title-suffix") ?? "";
    this.label = this.querySelector("[data-name-label]");
    if (!this.url || !this.label) return;
    this.pulling = false;
    this.dirty = false;
    onServerEvent("terminals", () => void this.sync(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // sync pulls the current name and applies it when it moved. Overlapping
  // events coalesce into one trailing pull, so a burst costs two fetches at
  // most instead of one per event.
  async sync() {
    if (!this.ac) return;
    if (this.pulling) {
      this.dirty = true;
      return;
    }
    this.dirty = false;
    this.pulling = true;
    try {
      const name = (await getText(this.url, { signal: this.ac.signal })).trim();
      if (name && name !== this.label.textContent.trim()) {
        this.label.textContent = name;
        if (this.titleSuffix) document.title = name + this.titleSuffix;
      }
    } catch (error) {
      void error;
    } finally {
      this.pulling = false;
      if (this.dirty && this.ac) void this.sync();
    }
  }
}

customElements.define("dc-live-name", LiveName);
