import { csrfHeaders } from "@dc/http";
import { notifyError } from "@dc/toast";

// Hello's page, markup in page.html: its notes, kept in the plugin's state
// directory, and the last events it heard.
export default class HelloBoard extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.list = this.querySelector("[data-hello-notes]");
    this.events = this.querySelector("[data-hello-events]");
    const form = this.querySelector("[data-hello-form]");
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.add(form.elements.text);
    }, { signal: this.ac.signal });
    void this.load();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async load() {
    const response = await fetch(this.dataset.notesUrl, { credentials: "same-origin", signal: this.ac?.signal }).catch(() => null);
    if (response?.ok) this.render((await response.json()).notes);
    const events = await fetch(this.dataset.eventsUrl, { credentials: "same-origin", signal: this.ac?.signal }).catch(() => null);
    if (events?.ok && this.ac) {
      this.events.replaceChildren(...(await events.json()).events.map((ev) => {
        const row = document.createElement("div");
        row.className = "list-group-item small text-break";
        row.textContent = `${ev.source}: ${ev.headline}`;
        return row;
      }));
    }
  }

  async add(input) {
    const text = input.value.trim();
    if (!text) return;
    const response = await fetch(this.dataset.notesUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ text }),
    }).catch(() => null);
    if (!response?.ok) {
      notifyError(response ? await response.text() : "Hello did not answer.");
      return;
    }
    input.value = "";
    this.render((await response.json()).notes);
  }

  render(notes) {
    if (!this.ac) return;
    this.list.replaceChildren(...notes.map((text) => {
      const row = document.createElement("div");
      row.className = "list-group-item text-break";
      row.dataset.helloNote = "";
      row.textContent = text;
      return row;
    }));
  }
}
