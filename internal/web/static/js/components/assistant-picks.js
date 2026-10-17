import { postForm, ensureOk } from "@dc/http";
import { notifyError, showToast } from "@dc/toast";

const KINDS = {
  chat: { model: "model", effort: "effort", frameModel: "chat", frameEffort: "chatEffort" },
  check: { model: "check_model", effort: "check_effort", frameModel: "check", frameEffort: "checkEffort" },
  trigger: { model: "trigger_model", effort: "trigger_effort", frameModel: "trigger", frameEffort: "triggerEffort" },
};

class AssistantPicks extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.at = "";
    this.held = new Map();
    this.saved = this.key(this.values());
    const save = this.querySelector("[data-assistant-model-save]");
    if (save) {
      save.hidden = true;
      save.form?.addEventListener("submit", (event) => {
        event.preventDefault();
        void this.save();
      }, { signal });
    }
    this.addEventListener("dc-model-change", () => void this.save(), { signal });
    this.addEventListener("change", (event) => {
      if (event.target.matches?.("[data-effort-select]")) void this.save();
    }, { signal });
    this.addEventListener("focusout", (event) => {
      for (const [control, value] of this.held) {
        if (!control.contains(event.target) || control.contains(event.relatedTarget)) continue;
        this.held.delete(control);
        this.put(control, value);
        const kind = control.getAttribute("data-assistant-model-pick");
        if (kind) this.control(kind, "effort")?.closest("dc-effort-pick")?.follow?.(value);
      }
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  control(kind, part) {
    const attribute = part === "model" ? "data-assistant-model-pick" : "data-assistant-effort-pick";
    return this.querySelector(`[${attribute}="${kind}"]`);
  }

  read(control) {
    if (!control) return "";
    if (typeof control.value === "string") return control.value;
    return control.querySelector("[data-model-select]")?.value ?? "";
  }

  put(control, value) {
    if (typeof control.set === "function") {
      control.set(value);
      return;
    }
    const select = control.matches("select") ? control : control.querySelector("select");
    if (!select) return;
    if (![...select.options].some((option) => option.value === value)) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      select.append(option);
    }
    select.value = value;
  }

  values() {
    const out = {};
    for (const kind of Object.keys(KINDS)) {
      out[kind] = { model: this.read(this.control(kind, "model")), effort: this.read(this.control(kind, "effort")) };
    }
    return out;
  }

  key(values) {
    return Object.keys(KINDS).map((kind) => `${values[kind].model}\u0000${values[kind].effort}`).join("\u0000");
  }

  apply(models, own) {
    if (!models || models.assistant !== this.getAttribute("assistant-id")) return;
    const at = models.updatedAt || "";
    if (at && this.at && Date.parse(at) < Date.parse(this.at)) return;
    this.at = at;
    const next = {};
    for (const [kind, names] of Object.entries(KINDS)) {
      next[kind] = { model: models[names.frameModel] || "", effort: models[names.frameEffort] || "" };
    }
    this.saved = this.key(next);
    const launched = models.launcher === "ollama";
    for (const icon of this.querySelectorAll("[data-assistant-ring-icon]")) icon.toggleAttribute("hidden", launched);
    for (const icon of this.querySelectorAll("[data-assistant-ring-launcher]")) icon.toggleAttribute("hidden", !launched);
    const warning = this.querySelector("[data-ollama-warning]");
    if (warning) {
      warning.textContent = models.warning || "";
      warning.hidden = !models.warning;
    }
    if (own) this.held.clear();
    for (const kind of Object.keys(KINDS)) {
      for (const part of ["model", "effort"]) {
        const control = this.control(kind, part);
        if (!control) continue;
        if (!own && control.contains(document.activeElement)) this.held.set(control, next[kind][part]);
        else this.put(control, next[kind][part]);
      }
      this.control(kind, "effort")?.closest("dc-effort-pick")?.follow?.(next[kind].model);
    }
  }

  async save() {
    if (this.saving) return;
    const values = this.values();
    if (this.key(values) === this.saved) return;
    this.saving = true;
    this.held.clear();
    const fields = { form: "model" };
    for (const [kind, names] of Object.entries(KINDS)) {
      fields[names.model] = values[kind].model;
      fields[names.effort] = values[kind].effort;
    }
    try {
      const response = await postForm(this.getAttribute("post-url"), fields);
      await ensureOk(response, "The models could not be saved.");
      const data = await response.json().catch(() => ({}));
      this.apply(data.models, true);
      showToast({ icon: "success", title: data.message || "Models saved." });
    } catch (error) {
      notifyError(error);
    } finally {
      this.saving = false;
    }
  }
}

customElements.define("dc-assistant-picks", AssistantPicks);
