import { copyText } from "@dc/dom";
import { get, set } from "@dc/store";
import { openFileLink, webLinks } from "@dc/termlinks";
import { notifyError } from "@dc/toast";

// The copy view of a terminal. A terminal draws to a canvas, so there is no
// text in it to select; this view puts what the terminal has said into the
// page in the terminal's place, which makes selecting, scrolling and copying
// the browser's job and, on a phone, the system's own.
//
// One frame, two faces. A coder's conversation is its record as bubbles, the
// newest page first and older pages on demand. Plain text is a shell's history
// counted in lines, or a coder's screen, the only place a prompt somebody is
// still typing exists; a coder draws on the alternate screen, which keeps no
// scrollback, so its screen carries no amount.
//
// What it shows is a snapshot from the moment it opened, copying from a target
// that keeps moving is the one thing nobody wants.
const LINES_KEY = "dc-copy-lines";
const COPIED_MS = 1500;
const CONVERSATION = "conversation";
const TEXT = "text";

class TerminalCopy extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.terminalId = this.getAttribute("terminal-id") || "";
    this.conversationUrl = this.getAttribute("conversation-url") || "";
    this.textUrl = this.getAttribute("text-url") || "";
    this.heading = this.querySelector("[data-copy-title]");
    this.scroller = this.querySelector("[data-copy-scroll]");
    this.log = this.querySelector("[data-copy-log]");
    this.text = this.querySelector("[data-copy-text]");
    this.waiting = this.querySelector("[data-copy-waiting]");
    this.lines = this.querySelector("[data-copy-lines]");
    this.loading = 0;
    this.face = "";
    this.timers = new Set();
    this.applyFontSize(this.storedFontSize());
    if (this.lines) {
      const stored = get(LINES_KEY, "");
      if ([...this.lines.options].some((o) => o.value === stored)) this.lines.value = stored;
      this.lines.addEventListener("change", () => {
        set(LINES_KEY, this.lines.value);
        void this.show(TEXT, { keepPlace: true });
      }, { signal });
    }
    document.addEventListener("click", (event) => {
      const button = event.target.closest?.("[data-terminal-copy]");
      if (!button || !this.owns(button)) return;
      event.preventDefault();
      if (this.hidden) {
        void this.open();
      } else {
        this.close();
      }
    }, { signal });
    document.addEventListener("terminal-setting-change", (event) => {
      if (event.detail?.setting === "font-size") this.applyFontSize(Number(event.detail.value));
    }, { signal });
    this.addEventListener("click", (event) => this.onClick(event), { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault();
        this.close();
      }
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    for (const timer of this.timers) clearTimeout(timer);
    this.timers.clear();
    this.terminal()?.classList.remove("attach-terminal-behind");
  }

  // A button in a split's footer names its pane, a page with one terminal has
  // one view.
  owns(button) {
    const id = button.closest("[data-terminal-footer]")?.getAttribute("data-terminal-footer");
    return id ? id === this.terminalId : document.querySelectorAll("terminal-copy").length === 1;
  }

  terminal() {
    return this.parentElement?.querySelector("terminal-attach") || null;
  }

  toggles() {
    return [...document.querySelectorAll("[data-terminal-copy]")].filter((button) => this.owns(button));
  }

  storedFontSize() {
    const setting = document.querySelector('terminal-setting-select[setting="font-size"]');
    if (!setting) return 0;
    return parseInt(get(setting.getAttribute("storage-key") || "", ""), 10)
      || parseInt(setting.getAttribute("default-value") || "", 10)
      || 0;
  }

  applyFontSize(size) {
    if (size > 0) this.scroller.style.fontSize = `${size}px`;
  }

  async open() {
    this.hidden = false;
    this.terminal()?.classList.add("attach-terminal-behind");
    for (const button of this.toggles()) {
      button.classList.add("active");
      button.setAttribute("aria-pressed", "true");
    }
    // The text and the bubbles read in the terminal's own font, which the
    // terminal keeps on its selection layer.
    const layer = this.terminal()?.querySelector(".attach-selection");
    if (layer) this.text.style.fontFamily = this.log.style.fontFamily = getComputedStyle(layer).fontFamily;
    this.scroller.focus({ preventScroll: true });
    await this.show(this.conversationUrl ? CONVERSATION : TEXT, { keepPlace: false });
  }

  close() {
    if (this.hidden) return;
    const focused = document.activeElement;
    const giveBack = !focused || focused === document.body || this.contains(focused) || this.toggles().includes(focused);
    this.loading++;
    this.hidden = true;
    this.face = "";
    this.log.replaceChildren();
    this.text.replaceChildren();
    this.waiting.hidden = true;
    this.terminal()?.classList.remove("attach-terminal-behind");
    for (const button of this.toggles()) {
      button.classList.remove("active");
      button.setAttribute("aria-pressed", "false");
    }
    if (giveBack && this.terminalId) {
      document.dispatchEvent(new CustomEvent("dc:activate-pane", { detail: { id: this.terminalId } }));
    }
  }

  setFace(face) {
    this.face = face;
    const conversation = face === CONVERSATION;
    this.heading.textContent = conversation ? "Conversation" : this.conversationUrl ? "Screen" : "History";
    this.log.hidden = !conversation;
    this.text.hidden = conversation;
    for (const button of this.querySelectorAll("[data-copy-face]")) {
      button.hidden = button.getAttribute("data-copy-face") === face;
    }
    this.log.setAttribute("aria-label", this.heading.textContent);
  }

  async show(face, { keepPlace }) {
    const fromBottom = keepPlace ? this.scroller.scrollHeight - this.scroller.scrollTop : null;
    this.setFace(face);
    this.waiting.hidden = false;
    if (this.lines) this.lines.disabled = true;
    const done = face === CONVERSATION ? await this.fetchPage("") : await this.fetchText();
    if (done === undefined || this.hidden || this.face !== face) return;
    this.waiting.hidden = true;
    if (this.lines) this.lines.disabled = false;
    if (!done) return;
    if (face === CONVERSATION) {
      this.text.replaceChildren();
      this.log.replaceChildren(done);
    } else {
      this.log.replaceChildren();
      this.render(done.text, done.links);
    }
    window.requestAnimationFrame(() => {
      this.scroller.scrollTop = fromBottom === null ? this.scroller.scrollHeight : Math.max(0, this.scroller.scrollHeight - fromBottom);
    });
  }

  async fetchPage(before) {
    const ticket = ++this.loading;
    const params = before ? `?${new URLSearchParams({ before })}` : "";
    try {
      const res = await fetch(`${this.conversationUrl}${params}`, { headers: { Accept: "text/html" } });
      const html = await res.text();
      if (!res.ok) throw new Error(html || "The conversation could not be read.");
      if (ticket !== this.loading) return undefined;
      const template = document.createElement("template");
      template.innerHTML = html;
      return template.content;
    } catch (error) {
      if (ticket !== this.loading) return undefined;
      notifyError(error.message);
      return null;
    }
  }

  // A coder's text is its screen, a shell's is its history in as many lines as
  // the reader picked.
  async fetchText() {
    const ticket = ++this.loading;
    const params = new URLSearchParams();
    if (!this.conversationUrl && this.lines) params.set("lines", this.lines.value);
    try {
      const res = await fetch(`${this.textUrl}?${params}`, { headers: { Accept: "application/json" } });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "The terminal could not be read.");
      if (ticket !== this.loading) return undefined;
      return { text: data.text || "", links: data.links || [] };
    } catch (error) {
      if (ticket !== this.loading) return undefined;
      notifyError(error.message);
      return null;
    }
  }

  // A web address in the text is a link, because somebody who finds one here
  // wants to open it, and so is a file of the project the server found. The
  // nodes are built rather than markup parsed, output is never markup, and
  // into a fragment, since tens of thousands of them overflow the arguments
  // of one call.
  render(text, fileLinks) {
    const parts = document.createDocumentFragment();
    let at = 0;
    const links = [...fileLinks, ...webLinks(text).map((link) => ({ ...link, web: true }))];
    for (const link of links.sort((a, b) => a.start - b.start)) {
      if (link.start < at) continue;
      if (link.start > at) parts.append(text.slice(at, link.start));
      const a = document.createElement("a");
      a.href = link.href;
      if (link.web) {
        a.target = "_blank";
        a.rel = "noopener noreferrer";
      } else {
        a.setAttribute("data-file-link", "");
      }
      a.textContent = text.slice(link.start, link.end);
      parts.append(a);
      at = link.end;
    }
    if (at < text.length) parts.append(text.slice(at));
    this.text.replaceChildren(parts);
  }

  async onClick(event) {
    const target = event.target;
    const fileLink = target.closest("a[data-file-link]");
    if (fileLink) {
      event.preventDefault();
      event.stopPropagation();
      openFileLink(fileLink.href, event);
      return;
    }
    if (target.closest("[data-copy-close]")) {
      this.close();
      return;
    }
    const face = target.closest("[data-copy-face]");
    if (face) {
      await this.show(face.getAttribute("data-copy-face"), { keepPlace: false });
      return;
    }
    const more = target.closest("[data-copy-more]");
    if (more) {
      more.disabled = true;
      const page = await this.fetchPage(more.getAttribute("data-copy-more") || "");
      if (!page) {
        more.disabled = false;
        return;
      }
      const fromBottom = this.scroller.scrollHeight - this.scroller.scrollTop;
      more.closest("[data-copy-earlier]")?.replaceWith(page);
      this.scroller.scrollTop = this.scroller.scrollHeight - fromBottom;
      return;
    }
    const one = target.closest("[data-copy-message]");
    if (one) {
      const message = one.closest("[data-message-id]");
      await this.copy(message ? this.source(message) : "", one);
      return;
    }
    const all = target.closest("[data-copy-all]");
    if (all) await this.copy(this.face === CONVERSATION ? this.conversationText() : this.text.textContent, all);
  }

  conversationText() {
    const blocks = [];
    for (const message of this.log.querySelectorAll("[data-message-id]")) {
      const text = this.source(message);
      if (text) blocks.push(`${message.getAttribute("data-role") === "user" ? "user" : "coder"}:\n${text}`);
    }
    return blocks.join("\n\n");
  }

  source(message) {
    return message.querySelector("[data-copy-source]")?.content.textContent || "";
  }

  async copy(text, button) {
    if (!text) return;
    if (!(await copyText(text))) {
      notifyError("Clipboard is not available.");
      return;
    }
    const icon = button.querySelector(".ti");
    icon?.classList.replace("ti-copy", "ti-check");
    button.setAttribute("data-copied", "");
    clearTimeout(button.copiedTimer);
    this.timers.delete(button.copiedTimer);
    button.copiedTimer = setTimeout(() => {
      this.timers.delete(button.copiedTimer);
      icon?.classList.replace("ti-check", "ti-copy");
      button.removeAttribute("data-copied");
    }, COPIED_MS);
    this.timers.add(button.copiedTimer);
  }
}

customElements.define("terminal-copy", TerminalCopy);
