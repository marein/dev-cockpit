import { csrfHeaders } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

// Pulse's page: the beats, the lines typed into it, the assistants it starts
// on a listed coder and reads through the cockpit and the events the plugin
// heard while on.
export default class PulseBoard extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.lines = this.querySelector("[data-pulse-lines]");
    this.events = this.querySelector("[data-pulse-events]");
    this.beats = this.querySelector("[data-pulse-beats]");
    this.assistants = this.querySelector("[data-pulse-assistants]");
    this.coders = this.querySelector("[data-pulse-coders]");
    const signal = this.ac.signal;
    const form = this.querySelector("[data-pulse-form]");
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.add(form.elements.text);
    }, { signal });
    const ask = this.querySelector("[data-pulse-assistant-form]");
    ask.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.start(ask);
    }, { signal });
    this.assistants.addEventListener("click", (event) => {
      const button = event.target.closest("[data-pulse-last]");
      if (button) void this.readLast(button.closest("[data-pulse-assistant]"));
    }, { signal });
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

  async call(url, init = {}) {
    const response = await fetch(url, { credentials: "same-origin", signal: this.ac?.signal, ...init }).catch(() => null);
    return { response, body: response ? await response.json().catch(() => ({})) : {} };
  }

  async load() {
    const lines = await this.get(this.dataset.linesUrl);
    if (lines) this.render(lines.lines);
    await this.loadCoders();
    await this.loadAssistants();
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

  async start(form) {
    const name = form.elements.name.value.trim();
    const message = form.elements.message.value.trim();
    if (!message) return;
    const { response, body } = await this.call(this.dataset.assistantsUrl, {
      method: "POST",
      headers: csrfHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ name, message, coder_id: form.elements.coder_id.value }),
    });
    if (!response?.ok) {
      notifyError(body.error || "Pulse could not start the assistant.");
      return;
    }
    form.reset();
    if (body.error) notifyError(body.error);
    else notifySuccess(`Pulse started ${body.assistant.name}.`);
    await this.loadAssistants();
  }

  async loadCoders() {
    const coders = await this.get(`${this.dataset.assistantsUrl}/coders`);
    if (coders) this.coders.replaceChildren(...coders.coders.map((coder) => new Option(coder.name, coder.id, coder.default, coder.default)));
  }

  async loadAssistants() {
    const { response, body } = await this.call(this.dataset.assistantsUrl);
    if (!this.ac) return;
    if (!response?.ok) {
      this.assistants.replaceChildren(this.row(body.error || "The assistants did not answer.", "pulseNote"));
      return;
    }
    this.assistants.replaceChildren(...body.assistants.map((assistant) => {
      const row = document.createElement("div");
      row.className = "list-group-item";
      row.dataset.pulseAssistant = assistant.id;
      const head = document.createElement("div");
      head.className = "d-flex align-items-center gap-2";
      const link = document.createElement("a");
      link.className = "text-reset text-truncate flex-fill";
      link.href = assistant.url;
      link.textContent = assistant.name;
      const count = document.createElement("span");
      count.className = "text-secondary small text-nowrap";
      count.textContent = `${assistant.message_count} messages`;
      const button = document.createElement("button");
      button.type = "button";
      button.className = "btn btn-sm";
      button.dataset.pulseLast = "";
      button.textContent = "Last message";
      head.append(link, count, button);
      const last = document.createElement("div");
      last.className = "small text-secondary text-break mt-1";
      last.dataset.pulseLastText = "";
      last.hidden = true;
      row.append(head, last);
      return row;
    }));
  }

  async readLast(row) {
    const { response, body } = await this.call(`${this.dataset.assistantsUrl}/${encodeURIComponent(row.dataset.pulseAssistant)}/last-message`);
    if (!this.ac) return;
    const last = row.querySelector("[data-pulse-last-text]");
    if (!response?.ok) last.textContent = body.error || "The assistant did not answer.";
    else if (!body.id) last.textContent = "No message yet.";
    else last.textContent = `${body.role}, ${new Date(body.time).toLocaleString()}, ${body.state}: ${body.text}`;
    last.hidden = false;
  }

  row(text, mark) {
    const row = document.createElement("div");
    row.className = "list-group-item text-break";
    row.dataset[mark] = "";
    row.textContent = text;
    return row;
  }
}
