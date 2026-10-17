const UNITS = { minutes: 60e3, hours: 36e5, days: 864e5 };
const LONGEST = 30 * UNITS.days;

class ShareForm extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.amount = this.querySelector("[data-share-amount]");
    this.unit = this.querySelector("[data-share-unit]");
    this.hint = this.querySelector("[data-share-expiry]");
    this.entries = this.querySelector("[data-share-entries]");
    this.amount?.addEventListener("input", () => this.syncExpiry(), { signal });
    this.unit?.addEventListener("change", () => this.syncExpiry(), { signal });
    this.addEventListener("change", (event) => {
      if (event.target.matches?.("[data-share-remove]")) this.syncRemoved(event.target);
      if (event.target.matches?.("[data-picker-value]")) this.syncPicked(event.target);
    }, { signal });
    this.addEventListener("click", (event) => {
      const unpick = event.target instanceof Element ? event.target.closest("[data-share-unpick]") : null;
      if (unpick) this.unpick(unpick);
    }, { signal });
    this.querySelectorAll("[data-share-remove]").forEach((box) => this.syncRemoved(box));
    this.syncExpiry();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  syncExpiry() {
    if (!this.amount || !this.unit || !this.hint) return;
    const at = this.hint.querySelector("[data-share-expiry-at]");
    const time = this.hint.querySelector("dc-time");
    const save = this.hint.querySelector("[data-share-expiry-save]");
    const stored = this.hint.dataset.shareStored;
    const raw = this.amount.value.trim();
    if (!raw && stored) {
      time.setAttribute("datetime", stored);
      save.hidden = true;
      at.hidden = false;
      return;
    }
    const span = Number(raw) * (UNITS[this.unit.value] || 0);
    const valid = /^\d+$/.test(raw) && span >= UNITS.minutes && span <= LONGEST;
    at.hidden = !valid;
    save.hidden = !stored;
    if (valid) time.setAttribute("datetime", new Date(Date.now() + span).toISOString());
  }

  syncPicked(box) {
    if (!this.entries) return;
    const key = box.dataset.pickerValue;
    const entry = [...this.entries.querySelectorAll("[data-share-picked]")].find((el) => el.dataset.sharePicked === key);
    if (box.checked && !entry) {
      const template = [...this.querySelectorAll("template[data-share-entry]")].find((el) => el.dataset.shareEntry === key);
      if (template) this.entries.append(template.content.cloneNode(true));
    } else if (!box.checked) {
      entry?.remove();
    }
    this.entries.hidden = !this.entries.children.length;
  }

  unpick(button) {
    const picker = this.querySelector("dc-picker");
    const focused = button.contains(document.activeElement);
    picker?.set(button.dataset.shareUnpick, false);
    if (focused) picker?.querySelector("[data-picker-toggle]")?.focus();
  }

  syncRemoved(box) {
    const row = box.closest("[data-share-resource]");
    if (!row) return;
    const off = box.checked;
    row.querySelector("[data-share-resource-name]")?.classList.toggle("text-decoration-line-through", off);
    row.querySelector("[data-share-resource-body]")?.classList.toggle("opacity-50", off);
    row.querySelectorAll("[data-share-grant]").forEach((grant) => { grant.disabled = off; });
    const label = row.querySelector(`label[for="${box.id}"]`);
    const icon = label?.querySelector(".ti");
    icon?.classList.toggle("ti-x", !off);
    icon?.classList.toggle("ti-arrow-back-up", off);
    label?.setAttribute("title", off ? "Keep on the link" : "Remove from the link");
  }
}

customElements.define("dc-share-form", ShareForm);
