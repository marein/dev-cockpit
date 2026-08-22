import { getJSON, setJSON } from "@dc/store";

// The detached server status card. The layout mounts it once, outside the
// region pe.js swaps, so it survives every boosted navigation; the values in it
// are painted by dc-host-status, which addresses every [data-host-row] on the
// page. This element only owns being open, being somewhere, and staying inside
// the viewport.

const KEY = "dc-host-float";
const MARGIN = 8;

class HostFloat extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.drag = null;
    this.x = 0;
    this.y = 0;

    const state = getJSON(KEY, {}) || {};
    if (state.open) this.show(state);

    // The detach buttons live in the dropdown, which re-renders per page, so
    // the always-mounted float listens at the document instead of wiring them.
    document.addEventListener("click", (event) => {
      const open = event.target.closest("[data-host-float-open]");
      if (open) {
        event.preventDefault();
        const toggle = open.closest(".dropdown")?.querySelector('[data-bs-toggle="dropdown"]');
        if (toggle) window.bootstrap?.Dropdown.getInstance(toggle)?.hide();
        this.show(getJSON(KEY, {}) || {});
        this.persist();
        this.flash();
        return;
      }
      if (event.target.closest("[data-host-float-close]")) {
        event.preventDefault();
        this.hidden = true;
        this.persist();
      }
    }, { signal });

    this.addEventListener("pointerdown", (event) => this.onPointerDown(event), { signal });
    this.addEventListener("pointermove", (event) => this.onPointerMove(event), { signal });
    this.addEventListener("pointerup", (event) => this.onPointerUp(event), { signal });
    this.addEventListener("pointercancel", () => { this.drag = null; }, { signal });

    // A window that shrinks must never leave the card stranded outside.
    window.addEventListener("resize", () => {
      if (this.hidden) return;
      this.place(this.x, this.y);
      this.persist();
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  show(state) {
    this.hidden = false;
    this.place(state.x, state.y);
  }

  flash() {
    this.classList.remove("dc-host-float-flash");
    void this.offsetWidth;
    this.classList.add("dc-host-float-flash");
  }

  // place clamps the wanted position into the viewport. A missing x lands the
  // card at the right edge under the header, which is where it detaches from.
  place(x, y) {
    const width = this.offsetWidth || 280;
    const height = this.offsetHeight || 220;
    const maxX = Math.max(MARGIN, window.innerWidth - width - MARGIN);
    const maxY = Math.max(MARGIN, window.innerHeight - height - MARGIN);
    this.x = Math.min(Math.max(x ?? maxX, MARGIN), maxX);
    this.y = Math.min(Math.max(y ?? 64, MARGIN), maxY);
    this.style.left = `${this.x}px`;
    this.style.top = `${this.y}px`;
  }

  persist() {
    setJSON(KEY, { open: !this.hidden, x: this.x, y: this.y });
  }

  onPointerDown(event) {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    if (!event.target.closest("[data-host-float-grip]")) return;
    if (event.target.closest("[data-host-float-close]")) return;
    event.preventDefault();
    this.drag = { id: event.pointerId, dx: event.clientX - this.x, dy: event.clientY - this.y };
    this.setPointerCapture(event.pointerId);
  }

  onPointerMove(event) {
    if (!this.drag || event.pointerId !== this.drag.id) return;
    this.place(event.clientX - this.drag.dx, event.clientY - this.drag.dy);
  }

  onPointerUp(event) {
    if (!this.drag || event.pointerId !== this.drag.id) return;
    this.drag = null;
    this.persist();
  }
}

customElements.define("dc-host-float", HostFloat);
