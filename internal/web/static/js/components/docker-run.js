import { ensureOk, postForm } from "@dc/http";
import { notifyError } from "@dc/toast";

const POLL_MS = 1500;
const MAX_FAILURES = 5;

class DockerRun extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.output = this.querySelector("[data-run-output]");
    this.status = this.querySelector("[data-run-status]");
    this.stopButton = this.querySelector("[data-run-stop]");
    this.outputUrl = this.getAttribute("output-url") || "";
    this.stopUrl = this.getAttribute("stop-url") || "";
    this.stopButton?.addEventListener("click", () => void this.stop(), { signal: this.ac.signal });
    this.failures = 0;
    this.toBottom();
    if (this.stopButton && !this.stopButton.hidden) this.schedule();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    clearTimeout(this.timer);
    this.timer = null;
  }

  schedule() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.poll(), POLL_MS);
  }

  async poll() {
    if (!this.ac || !this.outputUrl) return;
    try {
      const response = await fetch(this.outputUrl, { credentials: "same-origin", signal: this.ac.signal });
      if (!response.ok) throw new Error("output");
      const data = await response.json();
      this.failures = 0;
      this.apply(data);
      if (data.running) this.schedule();
    } catch {
      if (!this.ac) return;
      this.failures += 1;
      if (this.failures < MAX_FAILURES) this.schedule();
    }
  }

  apply(data) {
    const stick = this.atBottom();
    if (this.output && this.output.textContent !== data.output) this.output.textContent = data.output;
    if (this.status) {
      this.status.textContent = data.status;
      this.status.className = "badge " + (data.running ? "bg-blue-lt" : data.failed ? "bg-red-lt" : "bg-green-lt");
    }
    if (this.stopButton) this.stopButton.hidden = !data.running;
    if (stick) this.toBottom();
  }

  atBottom() {
    if (!this.output) return false;
    return this.output.scrollTop + this.output.clientHeight >= this.output.scrollHeight - 24;
  }

  toBottom() {
    if (this.output) this.output.scrollTop = this.output.scrollHeight;
  }

  async stop() {
    if (!this.stopUrl) return;
    try {
      const response = await postForm(this.stopUrl, {});
      await ensureOk(response, "Could not cancel the run.");
      await this.poll();
    } catch (error) {
      notifyError(error.message);
    }
  }
}

customElements.define("dc-docker-run", DockerRun);
