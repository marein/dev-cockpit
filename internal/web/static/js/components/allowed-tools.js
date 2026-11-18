import { ensureOk, postForm } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

// A row that just slid up under the finger is not the one the finger aimed
// at: the second tap of a double tap, or a quick tap on the next row, would
// remove a tool nobody chose. Same span as a tap after a closed context menu.
const MOVED_MS = 350;

// The tools allowed for good on the Approvals tab. A row goes only once the
// server took its Remove, so a refusal leaves it standing, and the count and
// the empty state are read from the rows that are left, which keeps two
// removes on the wire at once from counting each other wrong.
class AllowedTools extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.moved = new WeakSet();
    this.movedAt = 0;
    this.addEventListener("submit", (event) => void this.remove(event), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async remove(event) {
    const form = event.target;
    const row = form instanceof HTMLFormElement ? form.closest("[data-allowed-tool]") : null;
    if (!row || !this.ac) return;
    event.preventDefault();
    event.stopPropagation();
    const button = form.querySelector('button[type="submit"]');
    if (!button || button.disabled) return;
    if (this.moved.has(row) && performance.now() - this.movedAt < MOVED_MS) return;
    button.disabled = true;
    button.classList.add("btn-loading");
    try {
      const response = await postForm(form.action, new FormData(form), { signal: this.ac.signal });
      await ensureOk(response, "Could not remove the tool.");
      const data = await response.json().catch(() => null);
      this.drop(row);
      notifySuccess(data?.message);
    } catch (error) {
      if (error?.name === "AbortError") return;
      button.disabled = false;
      button.classList.remove("btn-loading");
      notifyError(error?.status ? error.message : "Could not remove the tool, the connection failed or was cut off.");
    }
  }

  drop(row) {
    this.moved = new WeakSet();
    for (let below = row.nextElementSibling; below; below = below.nextElementSibling) this.moved.add(below);
    this.movedAt = performance.now();
    row.remove();
    const left = this.querySelectorAll("[data-allowed-tool]").length;
    const count = this.querySelector("[data-allowed-tools-count]");
    if (count) {
      count.textContent = String(left);
      count.hidden = left === 0;
    }
    const list = this.querySelector("[data-allowed-tools-list]");
    if (list) list.hidden = left === 0;
    const empty = this.querySelector("[data-allowed-tools-empty]");
    if (empty) empty.hidden = left > 0;
  }
}

customElements.define("dc-allowed-tools", AllowedTools);
