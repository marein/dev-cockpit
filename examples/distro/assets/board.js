import { csrfHeaders } from "@dc/http";
import { notifyError } from "@dc/toast";

// Pulse's page: the beats, the lines typed into it and the events the plugin
// heard while on.
export default class PulseBoard extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.lines = this.querySelector("[data-pulse-lines]");
    this.events = this.querySelector("[data-pulse-events]");
    this.beats = this.querySelector("[data-pulse-beats]");
    const form = this.querySelector("[data-pulse-form]");
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

  async get(url) {
    const response = await fetch(url, { credentials: "same-origin", signal: this.ac?.signal }).catch(() => null);
    return response?.ok && this.ac ? response.json() : null;
  }

  async load() {
    const lines = await this.get(this.dataset.linesUrl);
    if (lines) this.render(lines.lines);
    const events = await this.get(this.dataset.eventsUrl);
    if (events) this.events.replaceChildren(...events.events.map((text) => this.row(text, "pulseEvent")));
    const beats = await this.get(this.dataset.beatsUrl);
    if (beats) this.beats.textContent = `${beats.beats} beats so far.`;
  }

  async add(input) {
    const text = input.value.trim();
    if (!text) return;
    const response = await fetch(this.dataset.linesUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ text }),
    }).catch(() => null);
    if (!response?.ok) {
      notifyError(response ? await response.text() : "Pulse did not answer.");
      return;
    }
    input.value = "";
    this.render((await response.json()).lines);
  }

  render(lines) {
    if (!this.ac) return;
    this.lines.replaceChildren(...lines.map((text) => this.row(text, "pulseLine")));
  }

  row(text, mark) {
    const row = document.createElement("div");
    row.className = "list-group-item text-break";
    row.dataset[mark] = "";
    row.textContent = text;
    return row;
  }
}
