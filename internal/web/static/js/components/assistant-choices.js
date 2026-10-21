import { postForm, ensureOk, getText, getJSON } from "@dc/http";
import { notifyError } from "@dc/toast";
import { growTextarea } from "@dc/dom";
import { missedServerEvent, onServerEvent } from "@dc/events";

// The composer's draft waits the same 200ms for the same reason: a word being
// typed is not a request.
const DRAFT_DEBOUNCE_MS = 200;

// The questions of one answer while the page may still answer them; the
// server renders every state, this only answers an open block. Send waits
// until every question has a pick or words, a second tap takes a pick back,
// and once the answers went out, or a message of the user stands under the
// answer, the block reloads itself and the server renders it over. What is
// picked and typed belongs to the assistant's draft, like the composer's
// words: saved when the hand rests, rendered back by the server on every
// page, and pulled again when another device saved.
class AssistantChoices extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.form = this.querySelector("[data-choices-form]");
    this.postUrl = this.getAttribute("post-url");
    if (!this.form || !this.postUrl) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.draftUrl = this.getAttribute("draft-url");
    this.fieldset = this.form.querySelector("fieldset");
    this.sendButton = this.querySelector("[data-choices-send]");
    this.count = this.querySelector("[data-choices-count]");
    this.sending = false;
    this.saveSeq = 0;
    this.saved = this.answers();
    this.picked = this.readPicked();
    this.form.addEventListener("click", (event) => this.onClick(event), { signal });
    this.form.addEventListener("input", (event) => {
      if (event.target.matches("textarea")) this.grow(event.target);
      this.sync();
      this.queueSave();
    }, { signal });
    this.form.addEventListener("keydown", (event) => {
      if (!event.target.matches("textarea")) return;
      if ((event.ctrlKey || event.metaKey) && !event.shiftKey && !event.altKey && event.key === "Enter") {
        event.preventDefault();
        void this.send();
      }
    }, { signal });
    this.form.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.send();
    }, { signal });
    onServerEvent("draft", (event) => this.onDraftEvent(event.detail), { signal });
    this.form.querySelectorAll("textarea").forEach((note) => this.grow(note));
    this.watchThread();
    this.sync();
    if (missedServerEvent("draft")) void this.pull();
  }

  disconnectedCallback() {
    window.clearTimeout(this.saveTimer);
    this.ac?.abort();
    this.ac = null;
    this.observer?.disconnect();
    this.observer = null;
  }

  onClick(event) {
    const radio = event.target.closest('input[type="radio"]');
    if (!radio) return;
    if (this.picked.get(radio.name) === radio.value) {
      radio.checked = false;
      this.picked.delete(radio.name);
    } else {
      this.picked.set(radio.name, radio.value);
    }
    this.sync();
    this.queueSave();
  }

  readPicked() {
    return new Map([...this.form.querySelectorAll('input[type="radio"]:checked')].map((radio) => [radio.name, radio.value]));
  }

  grow(note) {
    growTextarea(note, () => window.innerHeight);
  }

  questions() {
    return [...this.form.querySelectorAll("[data-choices-question]")];
  }

  sync() {
    const questions = this.questions();
    const done = questions.filter((q) => q.querySelector("input:checked") || q.querySelector("textarea")?.value.trim()).length;
    if (this.sendButton) this.sendButton.disabled = this.sending || done < questions.length;
    if (this.count) this.count.textContent = `${done} of ${questions.length} answered`;
  }

  // answers reads the block in the shape the server stores, one pick per
  // question, so what the page shows and what the draft holds compare as text.
  answers() {
    return this.shape(this.questions().map((q) => ({
      option: Number(q.querySelector("input:checked")?.value ?? -1),
      text: q.querySelector("textarea")?.value || "",
    })));
  }

  shape(picks) {
    return JSON.stringify(this.questions().map((_, i) => [picks[i]?.option ?? -1, picks[i]?.text || ""]));
  }

  queueSave() {
    window.clearTimeout(this.saveTimer);
    this.saveTimer = window.setTimeout(() => void this.save(), DRAFT_DEBOUNCE_MS);
  }

  async save() {
    window.clearTimeout(this.saveTimer);
    const answers = this.answers();
    if (this.sending || answers === this.saved) return;
    this.saved = answers;
    const body = new URLSearchParams(new FormData(this.form));
    body.set("form", "answers-draft");
    body.set("message_id", this.getAttribute("message-id"));
    const seq = ++this.saveSeq;
    this.saving = true;
    let refused = false;
    try {
      refused = !(await postForm(this.postUrl, body)).ok;
    } catch {
      if (seq === this.saveSeq) this.saved = null;
    } finally {
      if (seq === this.saveSeq) this.saving = false;
    }
    if (seq !== this.saveSeq) return;
    // A refusal means the block is over, answered or passed somewhere else,
    // and the server renders it so.
    if (refused) {
      this.saved = null;
      void this.reload();
      return;
    }
    // Look once after the newest save: two devices that wrote at the same
    // moment both end on what the server kept.
    await this.pull();
  }

  onDraftEvent(detail) {
    const assistant = detail && typeof detail === "object" ? detail.assistant : "";
    if (assistant && assistant !== this.getAttribute("assistant-id")) return;
    void this.pull();
  }

  // pull never types over the user: a block holding what this device has not
  // saved yet keeps it, its own save looks again. Pulls run one at a time, so
  // an older answer never lands after a newer one.
  async pull() {
    if (!this.draftUrl || this.sending || this.saving) return;
    if (this.pulling) {
      this.pullAgain = true;
      return;
    }
    this.pulling = true;
    let draft = null;
    try {
      draft = await getJSON(this.draftUrl);
    } catch {
      draft = null;
    }
    this.pulling = false;
    if (this.pullAgain) {
      this.pullAgain = false;
      void this.pull();
      return;
    }
    if (!draft || !this.ac || this.sending || this.saving) return;
    const picks = (draft.answers || {})[this.getAttribute("message-id")] || [];
    const stored = this.shape(picks);
    const shown = this.answers();
    if (stored === shown) {
      this.saved = stored;
      return;
    }
    if (shown !== this.saved) return;
    this.questions().forEach((q, i) => {
      const option = String(picks[i]?.option ?? -1);
      q.querySelectorAll('input[type="radio"]').forEach((radio) => { radio.checked = radio.value === option; });
      const note = q.querySelector("textarea");
      if (note && note.value !== (picks[i]?.text || "")) {
        note.value = picks[i]?.text || "";
        this.grow(note);
      }
    });
    this.picked = this.readPicked();
    this.saved = stored;
    this.sync();
  }

  async send() {
    if (this.sending || this.sendButton?.disabled) return;
    const body = new URLSearchParams(new FormData(this.form));
    body.set("form", "answers");
    body.set("message_id", this.getAttribute("message-id"));
    window.clearTimeout(this.saveTimer);
    this.sending = true;
    this.fieldset.disabled = true;
    this.sync();
    try {
      const response = await postForm(this.postUrl, body);
      await ensureOk(response, "The answers could not be sent.");
      await this.reload();
    } catch (error) {
      notifyError(error.message || "The answers could not be sent.");
      this.sending = false;
      this.fieldset.disabled = false;
      this.sync();
      this.queueSave();
      if (error.status) void this.reload();
    }
  }

  // The block stays while the server still renders it open: a message of the
  // user the page shows before the server holds it, the one a send writes at
  // once, has not ended it yet.
  async reload() {
    const id = this.getAttribute("message-id");
    try {
      const holder = document.createElement("div");
      holder.innerHTML = await getText(this.getAttribute("fragment-url"));
      const fresh = holder.querySelector(`dc-assistant-choices[message-id="${CSS.escape(id)}"]`);
      if (!fresh || !this.isConnected || fresh.hasAttribute("post-url")) return;
      this.replaceWith(fresh);
    } catch (error) {
      void error;
    }
  }

  watchThread() {
    const bubble = this.closest("[data-assistant-message]");
    if (!bubble?.parentElement) return;
    const check = () => {
      for (let node = bubble.nextElementSibling; node; node = node.nextElementSibling) {
        if (node.getAttribute("data-role") === "user") {
          void this.reload();
          return;
        }
      }
    };
    this.observer = new MutationObserver(check);
    this.observer.observe(bubble.parentElement, { childList: true });
    check();
  }
}

customElements.define("dc-assistant-choices", AssistantChoices);
