import { postForm, ensureOk } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

// The form that makes a subscription, behind the plus in the section's head,
// and the form that changes one: a row's Edit fills these very fields and the
// post goes to the same path with form=edit, so there is one form and one set
// of fields. The picked event decides which target field shows, the jobs of the
// assistant, the running coders or a cron schedule, and the batch window
// follows the source until somebody typed into it: a schedule has none. Both
// target fields take several terminals, so the mode beside them shows wherever
// they do: any of them fires it, or every one has to. The post goes the way the
// job forms go, the answer is a toast, and the list beside it refreshes itself
// on the assistant event the server publishes for a new subscription.
class AssistantSubscribe extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.form = this.querySelector("[data-assistant-subscribe-form]");
    this.event = this.querySelector("[data-subscribe-event]");
    this.mode = this.querySelector("[data-subscribe-mode]");
    this.batch = this.querySelector("[data-subscribe-batch]");
    this.batchTouched = false;
    this.editing = "";
    this.event?.addEventListener("change", () => this.syncFields(), { signal });
    this.batch?.addEventListener("input", () => { this.batchTouched = true; }, { signal });
    this.form?.addEventListener("submit", (event) => {
      event.preventDefault();
      event.stopPropagation();
      void this.submit();
    }, { signal });
    // The opened form puts the cursor into the task, the one field every
    // subscription needs typed. Fine pointer only, a phone would raise its
    // keyboard over the form.
    this.form?.addEventListener("shown.bs.collapse", () => {
      if (window.matchMedia?.("(pointer: fine)").matches) this.form.querySelector('[name="task"]')?.focus({ preventScroll: true });
    }, { signal });
    // Leaving the form is where an edit ends, whichever way out was taken:
    // Cancel, the plus, a saved change. The form goes back to what a new
    // subscription starts from, so the next open is never somebody else's.
    this.form?.addEventListener("hidden.bs.collapse", () => this.leaveEdit(), { signal });
    this.syncFields();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  source() {
    return this.event?.selectedOptions?.[0]?.dataset.source || "";
  }

  // One target field at a time: the others are hidden and disabled, so only
  // the shown one posts under its name.
  syncFields() {
    const source = this.source();
    for (const box of this.querySelectorAll("[data-subscribe-target]")) {
      const on = box.dataset.subscribeTarget === source;
      box.hidden = !on;
      for (const field of box.querySelectorAll("select, input")) field.disabled = !on;
    }
    if (this.mode) {
      const targets = source !== "cron";
      this.mode.hidden = !targets;
      for (const field of this.mode.querySelectorAll("select")) field.disabled = !targets;
    }
    if (this.batch && !this.batchTouched) this.batch.value = source === "cron" ? "0" : "30";
  }

  // fill opens the form on a subscription that stands: every field takes the
  // value it has, the event is locked because another event is another
  // subscription, and the expiry starts on an entry of its own that changes
  // nothing, because what is stored is a moment and the field asks for a span.
  fill(data) {
    if (!data || !this.form) return;
    this.editing = data.id;
    this.form.querySelector("[data-subscribe-form-field]").value = "edit";
    this.form.querySelector("[data-subscribe-id]").value = data.id;
    if (this.event) {
      this.event.value = data.event;
      this.event.disabled = true;
    }
    this.syncFields();
    this.fillTargets(data);
    this.form.querySelector('[name="task"]').value = data.task || "";
    const spec = this.form.querySelector('[name="spec"]');
    if (spec) spec.value = data.spec || "";
    const mode = this.form.querySelector('[name="mode"]');
    if (mode) mode.value = data.mode || "any";
    const once = this.form.querySelector('input[type="checkbox"][name="once"]');
    if (once) once.checked = !!data.once;
    this.form.querySelector('[name="max_per_hour"]').value = data.maxPerHour || "";
    if (this.batch) {
      this.batch.value = data.batch ?? 0;
      this.batchTouched = true;
    }
    this.fillUntil(data.until);
    this.label("Save");
    window.bootstrap?.Collapse.getOrCreateInstance(this.form, { toggle: false }).show();
  }

  // The picked terminals, and an option for one the select does not offer: a
  // job that closed or a coder that stopped is still a terminal this
  // subscription waits for, and saving must not drop it because the list of
  // what can be picked has moved on.
  fillTargets(data) {
    const select = this.querySelector(`[data-subscribe-target="${data.source}"] select`);
    if (!select) return;
    for (const stale of select.querySelectorAll("[data-subscribe-added]")) stale.remove();
    const picked = new Set((data.targets || []).map((t) => t.terminal));
    for (const target of data.targets || []) {
      if ([...select.options].some((option) => option.value === target.terminal)) continue;
      const option = document.createElement("option");
      option.value = target.terminal;
      option.textContent = target.name || target.terminal;
      option.dataset.subscribeAdded = "";
      select.append(option);
    }
    for (const option of select.options) option.selected = picked.has(option.value);
  }

  fillUntil(until) {
    const select = this.form.querySelector('[name="until"]');
    if (!select) return;
    let keep = select.querySelector("[data-subscribe-keep]");
    if (!keep) {
      keep = document.createElement("option");
      keep.value = "";
      keep.dataset.subscribeKeep = "";
      select.prepend(keep);
    }
    const when = until ? new Date(until).toLocaleString([], { dateStyle: "short", timeStyle: "short" }) : "no expiry";
    keep.textContent = `Unchanged (${when})`;
    select.value = "";
  }

  // leaveEdit puts the form back to a new subscription: the event is pickable
  // again, the options a target brought along go, and so does the entry that
  // leaves the expiry alone, which a new one has no use for.
  leaveEdit() {
    if (!this.form) return;
    this.editing = "";
    this.form.reset();
    this.form.querySelector("[data-subscribe-form-field]").value = "new";
    this.form.querySelector("[data-subscribe-id]").value = "";
    if (this.event) this.event.disabled = false;
    for (const stale of this.querySelectorAll("[data-subscribe-added], [data-subscribe-keep]")) stale.remove();
    this.label("Subscribe");
    this.batchTouched = false;
    this.syncFields();
  }

  label(text) {
    const submit = this.form?.querySelector("[data-assistant-subscribe-submit]");
    if (submit) submit.textContent = text;
  }

  async submit() {
    if (!this.form) return;
    const editing = this.editing;
    const refused = editing ? "The subscription could not be changed." : "The subscription could not be made.";
    const submit = this.form.querySelector("[data-assistant-subscribe-submit]");
    if (submit) submit.disabled = true;
    try {
      const response = await postForm(this.form.action, new URLSearchParams(new FormData(this.form)));
      await ensureOk(response, refused);
      const payload = await response.json().catch(() => ({}));
      if (editing) notifySuccess(payload.changed ? `Changed: ${payload.changed}.` : "Nothing changed.");
      else notifySuccess(payload.summary ? `Subscribed: ${payload.summary}.` : "Subscribed.");
      this.leaveEdit();
      window.bootstrap?.Collapse.getOrCreateInstance(this.form, { toggle: false }).hide();
    } catch (error) {
      notifyError(error?.message || refused);
    } finally {
      if (submit) submit.disabled = false;
    }
  }
}

customElements.define("dc-assistant-subscribe", AssistantSubscribe);
