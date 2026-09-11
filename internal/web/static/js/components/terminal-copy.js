import { notifyError } from "@dc/toast";
import { get, set } from "@dc/store";

// The copy sheet. A terminal draws to a canvas, so there is no text in it to
// select; this is where the text lives. It pulls what the terminal has said
// from the server when it opens and puts it into the page as plain text, which
// makes selecting, scrolling and copying the browser's job and, on a phone, the
// system's own: long press, handles, the copy menu everybody already knows.
//
// What it shows is a snapshot from the moment it opened. Copying from a target
// that keeps moving is the one thing nobody wants, so it does not follow the
// live stream.
//
// What the sheet shows is one choice, and it carries both halves of it: which
// source, and how much of it. A coder has two sources and they are not the same
// thing. Its record is what was said, which is what somebody copies an answer
// from. Its screen is what stands there right now, which is the only place a
// prompt somebody is still typing exists, because a draft was never said and is
// in no record. Everything else has one source, its own text, counted in lines.
// The screen carries no amount: a coder draws on the alternate screen, which
// keeps no scrollback, so there is exactly one picture to have.
//
// The first step is what somebody who has never chosen gets: the sheet is
// opened to copy something that just happened, so it opens small and fast, and
// reaching further back is one choice away.
const MESSAGES = [50, 200, 1000, 5000];
const LINES = [500, 2000, 10000, 50000];
const SCREEN = "screen";
// Kept per kind: a choice about a coder says nothing about a shell.
const choiceKey = (kind) => `dc-copy-${kind === "coder" ? "coder" : "terminal"}`;

class TerminalCopy extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.modal = document.getElementById("terminal-copy-modal");
    if (!this.modal) return;
    this.text = this.modal.querySelector("[data-copy-text]");
    this.body = this.modal.querySelector(".modal-body");
    this.amount = this.modal.querySelector("[data-copy-amount]");
    this.waiting = this.modal.querySelector("[data-copy-waiting]");
    this.url = "";
    this.kind = "shell";
    // A coder has a record until one is asked for and does not come back.
    this.hasRecord = true;
    this.choice = "";
    const signal = this.ac.signal;
    // The buttons live in each terminal's control row, so the click says which
    // terminal is meant, exactly like every other control button does.
    document.addEventListener("click", (event) => {
      const button = event.target.closest?.("[data-terminal-copy]");
      if (!button) return;
      event.preventDefault();
      void this.open(button);
    }, { signal });
    // The sheet is laid out while it animates in, so a scroll set before it
    // stands lands in a box that is still empty. Whichever arrives last, the
    // text or the sheet, is what puts the view where it belongs.
    this.modal.addEventListener("shown.bs.modal", () => this.place(), { signal });
    this.amount?.addEventListener("change", () => {
      this.choice = this.amount.value;
      set(choiceKey(this.kind), this.choice);
      void this.load({ keepPlace: true });
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    this.modal = null;
    this.text = null;
  }

  // Which island the button belongs to: the footer names it on a split page,
  // and a page with one terminal has one island.
  island(button) {
    const footer = button.closest("[data-terminal-footer]");
    const id = footer?.getAttribute("data-terminal-footer");
    if (id) {
      return document.querySelector(`terminal-attach[terminal-id="${CSS.escape(id)}"]`);
    }
    return document.querySelector("terminal-attach[active]") || document.querySelector("terminal-attach");
  }

  // What the terminal can answer with decides what the list offers, and the
  // answer itself says it: a coder that came back with a record has both, a
  // coder without one has only its screen, everything else has only its text.
  options(kind, hasRecord) {
    if (kind !== "coder") {
      return [{ items: LINES.map((n) => ({ value: `lines:${n}`, label: `${n} lines` })) }];
    }
    if (!hasRecord) {
      return [{ items: [{ value: SCREEN, label: "Screen" }] }];
    }
    return [
      { group: "Conversation", items: MESSAGES.map((n) => ({ value: `messages:${n}`, label: `${n} messages` })) },
      { items: [{ value: SCREEN, label: "Screen" }] },
    ];
  }

  chosen(groups) {
    const values = groups.flatMap((g) => g.items.map((i) => i.value));
    const stored = get(choiceKey(this.kind), "");
    return values.includes(stored) ? stored : values[0];
  }

  renderAmount(groups) {
    const wanted = groups.flatMap((g) => g.items.map((i) => i.value)).join(",");
    if (this.amount.dataset.built !== wanted) {
      const nodes = [];
      for (const g of groups) {
        const into = g.group ? document.createElement("optgroup") : null;
        if (into) into.label = g.group;
        for (const item of g.items) {
          const option = document.createElement("option");
          option.value = item.value;
          option.textContent = item.label;
          (into || { append: (n) => nodes.push(n) }).append(option);
        }
        if (into) nodes.push(into);
      }
      this.amount.replaceChildren(...nodes);
      this.amount.dataset.built = wanted;
    }
    this.amount.value = this.choice;
    // Nothing to choose is not a choice: one entry is a label, not a control.
    this.amount.hidden = wanted.split(",").length < 2;
  }

  async open(button) {
    const island = this.island(button);
    this.url = island?.getAttribute("copy-url") || "";
    if (!this.url) {
      notifyError({ title: "This terminal has nothing to copy from." });
      return;
    }
    // Which kind this is stands in the url the island carries, and it decides
    // what the first request may ask for, before any answer has come back.
    this.kind = this.url.startsWith("/coders/") ? "coder" : "shell";
    this.hasRecord = true;
    this.choice = this.chosen(this.options(this.kind, true));
    this.text.replaceChildren();
    window.bootstrap?.Modal?.getOrCreateInstance(this.modal)?.show();
    await this.load({ keepPlace: false });
  }

  // The choice carries both halves, which source and how much of it.
  params() {
    const params = new URLSearchParams();
    if (this.choice === SCREEN) {
      params.set("source", SCREEN);
      return params;
    }
    const [unit, amount] = this.choice.split(":");
    params.set(unit === "messages" ? "messages" : "lines", amount);
    return params;
  }

  // A web address in the text is a link, because somebody who finds one here
  // wants to open it, not retype it. The nodes are built rather than a string
  // of markup parsed: everything in here is a program's output, and output is
  // never markup. A trailing bracket or full stop is punctuation around the
  // address, not part of it, so it stays outside the link and inside the text
  // a copy yields, which is unchanged either way.
  render(text) {
    const parts = [];
    const pattern = /https?:\/\/[^\s"'<>`]+/g;
    let at = 0;
    for (const match of text.matchAll(pattern)) {
      let href = match[0];
      const trailing = href.match(/[).,;:!?\]]+$/);
      if (trailing) {
        href = href.slice(0, href.length - trailing[0].length);
      }
      if (!href) {
        continue;
      }
      if (match.index > at) {
        parts.push(document.createTextNode(text.slice(at, match.index)));
      }
      const link = document.createElement("a");
      link.href = href;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.textContent = href;
      parts.push(link);
      at = match.index + href.length;
    }
    if (at < text.length) {
      parts.push(document.createTextNode(text.slice(at)));
    }
    this.text.replaceChildren(...parts);
  }

  // place puts the view where the reading should start. The end is what
  // somebody came for, so that is where it opens; after fetching more, the
  // distance to the end is kept instead, which leaves the lines they are
  // reading exactly where they were.
  place() {
    if (this.fromBottom === null) {
      this.body.scrollTop = this.body.scrollHeight;
      return;
    }
    this.body.scrollTop = Math.max(0, this.body.scrollHeight - this.fromBottom);
  }

  async load({ keepPlace }) {
    this.fromBottom = keepPlace ? this.body.scrollHeight - this.body.scrollTop : null;
    const params = this.params();
    // Reading a long record takes a moment, and an empty sheet looks like an
    // empty terminal. The wait says it is working, and the select is out of
    // reach meanwhile so a second answer cannot overtake the first.
    this.waiting.hidden = false;
    this.text.hidden = true;
    this.amount.disabled = true;
    try {
      const res = await fetch(`${this.url}?${params}`, { headers: { Accept: "application/json" } });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "The terminal could not be read.");
      // Only a request for the record can say whether there is one: an answer
      // from the screen that nobody asked for means the coder keeps none.
      if (data.kind === "coder" && this.choice !== SCREEN && data.screen) {
        this.hasRecord = false;
        this.choice = SCREEN;
      }
      this.renderAmount(this.options(data.kind, this.hasRecord));
      this.render(data.text || "");
      window.requestAnimationFrame(() => this.place());
    } catch (error) {
      this.text.replaceChildren();
      notifyError({ title: "Copy", detail: error.message });
    } finally {
      this.waiting.hidden = true;
      this.text.hidden = false;
      this.amount.disabled = false;
    }
  }
}

customElements.define("terminal-copy", TerminalCopy);
