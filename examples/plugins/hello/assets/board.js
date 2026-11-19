import { csrfHeaders } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

// Hello's page, markup in page.html: its notes, kept in the plugin's state
// directory, the assistants it starts on a listed coder and reads through the
// cockpit, and the last events it heard.
export default class HelloBoard extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.list = this.querySelector("[data-hello-notes]");
    this.events = this.querySelector("[data-hello-events]");
    this.assistants = this.querySelector("[data-hello-assistants]");
    this.coders = this.querySelector("[data-hello-coders]");
    const form = this.querySelector("[data-hello-form]");
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.add(form.elements.text);
    }, { signal });
    const ask = this.querySelector("[data-hello-assistant-form]");
    ask.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.start(ask);
    }, { signal });
    this.assistants.addEventListener("click", (event) => {
      const button = event.target.closest("[data-hello-last]");
      if (button) void this.readLast(button.closest("[data-hello-assistant]"));
    }, { signal });
    void this.load();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async get(url) {
    const response = await fetch(url, { credentials: "same-origin", signal: this.ac?.signal }).catch(() => null);
    return { response, body: response ? await response.json().catch(() => ({})) : {} };
  }

  async load() {
    const notes = await this.get(this.dataset.notesUrl);
    if (notes.response?.ok) this.render(notes.body.notes);
    await this.loadCoders();
    await this.loadAssistants();
    const events = await this.get(this.dataset.eventsUrl);
    if (events.response?.ok && this.ac) {
      this.events.replaceChildren(...events.body.events.map((ev) => {
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

  async start(form) {
    const name = form.elements.name.value.trim();
    const message = form.elements.message.value.trim();
    if (!message) return;
    const response = await fetch(this.dataset.assistantsUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ name, message, coder_id: form.elements.coder_id.value }),
    }).catch(() => null);
    const body = response ? await response.json().catch(() => ({})) : {};
    if (!response?.ok) {
      notifyError(body.error || "Hello could not start the assistant.");
      return;
    }
    form.reset();
    if (body.error) notifyError(body.error);
    else notifySuccess(`Hello started ${body.assistant.name}.`);
    await this.loadAssistants();
  }

  async loadCoders() {
    const { response, body } = await this.get(`${this.dataset.assistantsUrl}/coders`);
    if (!this.ac || !response?.ok) return;
    this.coders.replaceChildren(...body.coders.map((coder) => new Option(coder.name, coder.id, coder.default, coder.default)));
  }

  async loadAssistants() {
    const { response, body } = await this.get(this.dataset.assistantsUrl);
    if (!this.ac) return;
    if (!response?.ok) {
      this.assistants.replaceChildren(this.line(body.error || "The assistants did not answer."));
      return;
    }
    this.assistants.replaceChildren(...body.assistants.map((assistant) => {
      const row = document.createElement("div");
      row.className = "list-group-item";
      row.dataset.helloAssistant = assistant.id;
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
      button.dataset.helloLast = "";
      button.textContent = "Last message";
      head.append(link, count, button);
      const last = this.line("");
      last.className = "small text-secondary text-break mt-1";
      last.dataset.helloLastText = "";
      last.hidden = true;
      row.append(head, last);
      return row;
    }));
  }

  async readLast(row) {
    const { response, body } = await this.get(`${this.dataset.assistantsUrl}/${encodeURIComponent(row.dataset.helloAssistant)}/last-message`);
    if (!this.ac) return;
    const last = row.querySelector("[data-hello-last-text]");
    if (!response?.ok) last.textContent = body.error || "The assistant did not answer.";
    else if (!body.id) last.textContent = "No message yet.";
    else last.textContent = `${body.role}, ${new Date(body.time).toLocaleString()}, ${body.state}: ${body.text}`;
    last.hidden = false;
  }

  line(text) {
    const row = document.createElement("div");
    row.className = "list-group-item small text-secondary";
    row.textContent = text;
    return row;
  }
}
