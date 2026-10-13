import { missedServerEvent, onServerEvent } from "@dc/events";
import { getText } from "@dc/http";

function leftText(ms) {
  const minutes = Math.ceil(ms / 60000);
  if (minutes <= 1) return "less than a minute left";
  if (minutes < 60) return `${minutes} min left`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest ? `${hours} h ${rest} min left` : `${hours} h left`;
}

class SSHKeys extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("sshagent", () => this.refresh(), { signal: this.ac.signal });
    this.timer = setInterval(() => this.tick(), 15000);
    this.tick();
    if (missedServerEvent("sshagent")) this.refresh();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    clearInterval(this.timer);
    this.timer = null;
  }

  tick() {
    let expired = false;
    for (const el of this.querySelectorAll("[data-ssh-key-left]")) {
      const left = Date.parse(el.dataset.expires) - Date.now();
      if (left <= 0) expired = true;
      el.textContent = leftText(Math.max(0, left));
    }
    if (expired) this.refresh();
  }

  refresh() {
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    const signal = this.ac?.signal;
    getText("/settings/ssh-agent/keys", { signal })
      .then((html) => {
        if (!this.ac) return;
        const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("[data-ssh-keys-body]");
        const body = this.querySelector("[data-ssh-keys-body]");
        if (fresh && body) body.replaceWith(fresh);
        this.tick();
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty && this.ac) {
          this.dirty = false;
          this.refresh();
        }
      });
  }
}

customElements.define("dc-ssh-keys", SSHKeys);
