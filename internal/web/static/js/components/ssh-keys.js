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
    this.read();
    if (missedServerEvent("sshagent")) this.refresh();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    clearInterval(this.timer);
    this.timer = null;
  }

  read() {
    const now = performance.now();
    this.deadlines = [...this.querySelectorAll("[data-ssh-key-left]")].map((el) => [el, now + Number(el.dataset.left)]);
    this.tick();
  }

  tick() {
    const now = performance.now();
    for (const [el, deadline] of this.deadlines) el.textContent = leftText(Math.max(0, deadline - now));
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
        this.read();
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
