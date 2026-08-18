import { openMenu } from "@dc/contextmenu";
import { restoreActions } from "@dc/docker";

const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 56;
const EDGE_STEP = 10;

class DockerActions extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.rows = this.querySelector("[data-actions-rows]");
    this.template = this.querySelector("[data-action-template]");
    this.icons = (this.getAttribute("icons") || "").split(" ").filter(Boolean).map((pair) => {
      const [name, icon] = pair.split(":");
      return { name, icon };
    });
    this.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-action-remove]");
      if (remove && this.contains(remove)) {
        event.preventDefault();
        remove.closest("[data-action-row]")?.remove();
        return;
      }
      const pick = event.target.closest("[data-action-icon-pick]");
      if (pick && this.contains(pick)) {
        event.preventDefault();
        this.pickIcon(pick);
        return;
      }
      const add = event.target.closest("[data-action-add]");
      if (add && this.contains(add)) {
        event.preventDefault();
        this.addRow();
        return;
      }
      const restore = event.target.closest("[data-actions-restore]");
      if (restore && this.contains(restore)) {
        event.preventDefault();
        void this.restore(restore);
      }
    }, { signal: this.ac.signal });
    this.addEventListener("change", (event) => {
      const box = event.target.closest("[data-action-confirm-box]");
      if (!box || !this.contains(box)) return;
      const value = box.closest("[data-action-row]")?.querySelector("[data-action-confirm]");
      if (value) value.value = box.checked ? "1" : "0";
    }, { signal: this.ac.signal });
    this.wireDrag();
  }

  wireDrag() {
    if (!this.rows) return;
    const signal = this.ac.signal;
    let drag = null;
    const docY = (clientY) => clientY + window.scrollY;
    const update = () => {
      if (!drag || !drag.active) return;
      const dy = docY(drag.lastClientY) - drag.startDocY;
      const center = drag.centers[drag.fromIndex] + dy;
      let toIndex = 0;
      for (let i = 0; i < drag.centers.length; i++) {
        if (i !== drag.fromIndex && drag.centers[i] < center) toIndex += 1;
      }
      drag.toIndex = toIndex;
      drag.row.style.transform = `translateY(${dy}px)`;
      drag.els.forEach((el, i) => {
        if (el === drag.row) return;
        let shift = 0;
        if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.step;
        else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.step;
        el.style.transform = shift ? `translateY(${shift}px)` : "";
      });
    };
    const tick = () => {
      if (!drag || !drag.active) return;
      let delta = 0;
      if (drag.lastClientY < EDGE_ZONE) delta = -EDGE_STEP;
      else if (drag.lastClientY > window.innerHeight - EDGE_ZONE) delta = EDGE_STEP;
      if (delta) {
        const before = window.scrollY;
        window.scrollBy(0, delta);
        if (window.scrollY !== before) update();
      }
      drag.raf = window.requestAnimationFrame(tick);
    };
    const clear = () => {
      if (!drag) return;
      if (drag.active) {
        window.cancelAnimationFrame(drag.raf);
        this.classList.remove("dc-drag-list");
        drag.row.classList.remove("dc-drag-lift");
        for (const el of drag.els) el.style.transform = "";
      }
      drag = null;
    };
    this.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || drag) return;
      const grip = event.target.closest("[data-action-grip]");
      if (!grip || !this.contains(grip)) return;
      const row = grip.closest("[data-action-row]");
      if (!row) return;
      drag = {
        row,
        grip,
        pointerId: event.pointerId,
        startClientX: event.clientX,
        startClientY: event.clientY,
        lastClientY: event.clientY,
        active: false,
        raf: 0,
      };
    }, { signal });
    this.addEventListener("pointermove", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      if (!drag.active) {
        if (Math.hypot(event.clientX - drag.startClientX, event.clientY - drag.startClientY) < DRAG_THRESHOLD) return;
        drag.active = true;
        drag.els = [...this.rows.querySelectorAll("[data-action-row]")];
        drag.fromIndex = drag.els.indexOf(drag.row);
        drag.toIndex = drag.fromIndex;
        const rect = drag.row.getBoundingClientRect();
        drag.step = rect.height + parseFloat(getComputedStyle(drag.row).marginBottom) || rect.height;
        drag.centers = drag.els.map((el) => {
          const box = el.getBoundingClientRect();
          return box.top + box.height / 2 + window.scrollY;
        });
        drag.startDocY = docY(event.clientY);
        this.classList.add("dc-drag-list");
        drag.row.classList.add("dc-drag-lift");
        try {
          drag.grip.setPointerCapture(event.pointerId);
        } catch (error) {
          void error;
        }
        drag.raf = window.requestAnimationFrame(tick);
      }
      event.preventDefault();
      drag.lastClientY = event.clientY;
      update();
    }, { passive: false, signal });
    this.addEventListener("pointerup", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      const done = drag;
      clear();
      if (!done.active || done.toIndex === done.fromIndex) return;
      const others = done.els.filter((el) => el !== done.row);
      this.rows.insertBefore(done.row, others[done.toIndex] || null);
    }, { signal });
    this.addEventListener("pointercancel", clear, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  pickIcon(button) {
    const row = button.closest("[data-action-row]");
    if (!row) return;
    const value = row.querySelector("[data-action-icon-value]");
    const preview = row.querySelector("[data-action-icon-preview]");
    const rect = button.getBoundingClientRect();
    openMenu({
      x: Math.round(rect.left),
      y: Math.round(rect.bottom),
      signal: this.ac.signal,
      items: this.icons.map((entry) => ({
        label: entry.name,
        icon: entry.icon,
        action: () => {
          if (value) value.value = entry.name;
          if (preview) preview.className = `ti ${entry.icon}`;
        },
      })),
    });
  }

  async restore(button) {
    button.disabled = true;
    const ok = await restoreActions();
    button.disabled = false;
    if (!ok) return;
    if (window.pe) window.pe.navigate(window.location.href);
    else window.location.reload();
  }

  addRow() {
    if (!this.rows || !this.template) return;
    const row = this.template.content.firstElementChild.cloneNode(true);
    row.querySelector("[data-action-id]").value = this.freeId();
    this.rows.appendChild(row);
    this.querySelector("[data-actions-empty]")?.remove();
    row.querySelector('input[name="action_label"]')?.focus();
  }

  freeId() {
    const taken = new Set([...this.querySelectorAll("[data-action-id]")].map((input) => input.value));
    for (let n = 1; ; n++) {
      const id = `action-${n}`;
      if (!taken.has(id)) return id;
    }
  }
}

customElements.define("dc-docker-actions", DockerActions);
