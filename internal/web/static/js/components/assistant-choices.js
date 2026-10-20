import { postForm, ensureOk, getText } from "@dc/http";
import { notifyError } from "@dc/toast";
import { growTextarea } from "@dc/dom";

const DRAFT_KEY = "dc-choices:";

// The questions of one answer while the page may still answer them; the
// server renders every state, this only answers an open block. Send waits
// until every question has a pick or words, a second tap takes a pick back,
// and once the answers went out, or a message of the user stands under the
// answer, the block reloads itself and the server renders it over. What is
// picked and typed stays in the tab's session store until then: the thread
// is rebuilt when the page catches up with the server, and a phone coming
// back to the tab is exactly that.
class AssistantChoices extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.form = this.querySelector("[data-choices-form]");
    this.postUrl = this.getAttribute("post-url");
    if (!this.form || !this.postUrl) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.key = DRAFT_KEY + this.getAttribute("message-id");
    this.fieldset = this.form.querySelector("fieldset");
    this.sendButton = this.querySelector("[data-choices-send]");
    this.count = this.querySelector("[data-choices-count]");
    this.sending = false;
    this.restore();
    this.picked = new Map([...this.form.querySelectorAll('input[type="radio"]:checked')].map((radio) => [radio.name, radio.value]));
    this.form.addEventListener("click", (event) => this.onClick(event), { signal });
    this.form.addEventListener("input", (event) => {
      if (event.target.matches("textarea")) this.grow(event.target);
      this.sync();
    }, { signal });
    this.form.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.send();
    }, { signal });
    this.form.querySelectorAll("textarea").forEach((note) => this.grow(note));
    this.watchThread();
    this.sync();
  }

  disconnectedCallback() {
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
    this.save();
  }

  save() {
    const draft = {};
    for (const field of this.form.querySelectorAll("input:checked, textarea")) {
      if (field.value.trim()) draft[field.name] = field.value;
    }
    try {
      if (Object.keys(draft).length) window.sessionStorage.setItem(this.key, JSON.stringify(draft));
      else window.sessionStorage.removeItem(this.key);
    } catch (error) {
      void error;
    }
  }

  restore() {
    let draft = null;
    try {
      draft = JSON.parse(window.sessionStorage.getItem(this.key) || "null");
    } catch (error) {
      void error;
    }
    if (!draft) return;
    for (const field of this.form.querySelectorAll("input[type='radio'], textarea")) {
      if (!(field.name in draft)) continue;
      if (field.type === "radio") field.checked = field.value === draft[field.name];
      else field.value = draft[field.name];
    }
  }

  async send() {
    if (this.sending || this.sendButton?.disabled) return;
    const body = new URLSearchParams(new FormData(this.form));
    body.set("form", "answers");
    body.set("message_id", this.getAttribute("message-id"));
    this.sending = true;
    this.fieldset.disabled = true;
    this.sync();
    try {
      const response = await postForm(this.postUrl, body);
      await ensureOk(response, "The answers could not be sent.");
      try {
        window.sessionStorage.removeItem(this.key);
      } catch (error) {
        void error;
      }
      await this.reload();
    } catch (error) {
      notifyError(error.message || "The answers could not be sent.");
      this.sending = false;
      this.fieldset.disabled = false;
      this.sync();
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
