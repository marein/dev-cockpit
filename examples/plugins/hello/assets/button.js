import { promptText } from "@dc/dialog";
import { csrfHeaders } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

// Hello's button in the projects column, markup in slot.html. The plugin
// creates the project through the cockpit's callback socket.
export default class HelloButton extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.querySelector("[data-hello-button]").addEventListener("click", () => this.create(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async create() {
    const name = await promptText({ title: "New project", text: "Hello creates it through the cockpit.", placeholder: "Project name", confirmText: "Create" });
    if (!name || !this.ac) return;
    const response = await fetch(this.dataset.projectsUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ name }),
    }).catch(() => null);
    const body = response ? await response.json().catch(() => ({})) : {};
    if (response?.ok) notifySuccess(`Hello created ${body.name}.`);
    else notifyError(body.error || (response ? await response.text().catch(() => "") : "") || "Hello could not create the project.");
  }
}
