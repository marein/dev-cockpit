import { getJSON } from "@dc/http";

const OWN_DELAY = 200;
const PUBLIC_DELAY = 700;
const MIN_PUBLIC = 3;

class RepoSearch extends HTMLElement {
  connectedCallback() {
    if (this.abort) {
      return;
    }
    this.abort = new AbortController();
    const signal = this.abort.signal;
    const value = this.querySelector("[data-repo-value]");
    const input = this.querySelector("[data-repo-search]");
    const menu = this.querySelector("[data-repo-menu]");
    const listEl = this.querySelector("[data-repo-list]");
    const addressLine = this.querySelector("[data-repo-address-line]");
    const addressEl = addressLine?.querySelector("[data-repo-address]") || null;
    if (!value || !input || !menu || !listEl) {
      return;
    }
    const host = this.dataset.repoHost || "";
    const own = { entries: [], failure: "", retry: false, searching: false, seq: 0, timer: 0, request: null };
    const pub = { entries: [], term: "", searching: false, seq: 0, timer: 0, request: null };
    let open = false;
    let active = "";
    let term = "";

    const visible = () => {
      const mine = new Set(own.entries.map((e) => e.url));
      return { mine: own.entries, theirs: pub.entries.filter((e) => !mine.has(e.url)) };
    };
    const options = () => {
      const { mine, theirs } = visible();
      return [...mine, ...theirs];
    };

    const row = (entry) => {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "dropdown-item d-flex align-items-center gap-2";
      item.dataset.repoOption = entry.url;
      item.title = entry.url;
      if (entry.url === active) {
        item.classList.add("active");
      }
      const icon = document.createElement("i");
      icon.className = `ti ${/^ti-[a-z0-9-]+$/.test(entry.icon || "") ? entry.icon : "ti-git-fork"} flex-shrink-0`;
      icon.setAttribute("aria-hidden", "true");
      const label = document.createElement("span");
      label.className = "text-truncate";
      label.textContent = entry.path;
      item.append(icon, label);
      if (entry.hint) {
        const hint = document.createElement("span");
        hint.className = "text-secondary small ms-auto flex-shrink-0";
        hint.dataset.repoHint = "";
        hint.textContent = entry.hint;
        item.appendChild(hint);
      }
      item.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
      item.addEventListener("click", () => choose(entry), { signal });
      return item;
    };

    const note = (text, tone) => {
      const el = document.createElement("div");
      el.className = `dropdown-item-text small ${tone}`;
      el.dataset.repoNote = "";
      el.textContent = text;
      return el;
    };

    const busy = (text, mark) => {
      const el = document.createElement("div");
      el.className = "dropdown-item-text small text-secondary d-flex align-items-center gap-2";
      el.dataset[mark] = "";
      const spinner = document.createElement("span");
      spinner.className = "spinner-border spinner-border-sm";
      spinner.setAttribute("aria-hidden", "true");
      const label = document.createElement("span");
      label.textContent = text;
      el.append(spinner, label);
      return el;
    };

    const retryRow = () => {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "dropdown-item d-flex align-items-center gap-2";
      item.dataset.repoRetry = "";
      const icon = document.createElement("i");
      icon.className = "ti ti-reload flex-shrink-0";
      icon.setAttribute("aria-hidden", "true");
      const label = document.createElement("span");
      label.textContent = "Try again";
      item.append(icon, label);
      item.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
      item.addEventListener("click", () => void runOwn(), { signal });
      return item;
    };

    const render = () => {
      const top = listEl.scrollTop;
      const { mine, theirs } = visible();
      const nodes = [];
      if (own.searching) {
        nodes.push(busy("Searching…", "repoSearching"));
      }
      nodes.push(...mine.map(row));
      if (own.failure) {
        nodes.push(note(own.failure, "text-danger"));
        if (own.retry) nodes.push(retryRow());
      } else if (!own.searching && !mine.length) {
        nodes.push(note("No repository of yours matches.", "text-secondary"));
      }
      if (theirs.length) {
        const header = document.createElement("h6");
        header.className = "dropdown-header";
        header.dataset.repoPublic = "";
        header.textContent = "Public";
        nodes.push(header, ...theirs.map(row));
      }
      if (pub.searching) {
        nodes.push(busy("Searching public repositories…", "repoPublicSearching"));
      }
      listEl.replaceChildren(...nodes);
      listEl.scrollTop = top;
    };

    const mark = () => {
      const all = options();
      if (!all.some((e) => e.url === active)) {
        active = all[0]?.url || "";
      }
    };

    const show = () => {
      open = true;
      menu.classList.add("show");
      input.setAttribute("aria-expanded", "true");
    };
    const cancel = () => {
      clearTimeout(own.timer);
      clearTimeout(pub.timer);
      own.seq += 1;
      pub.seq += 1;
      own.request?.abort();
      pub.request?.abort();
      own.searching = false;
      pub.searching = false;
    };
    const hide = () => {
      cancel();
      open = false;
      active = "";
      menu.classList.remove("show");
      input.setAttribute("aria-expanded", "false");
    };

    const restore = () => {
      input.value = value.dataset.repoPath || "";
    };

    const address = (scope) => {
      const params = new URLSearchParams({ host });
      if (term) params.set("q", term);
      if (scope) params.set("scope", scope);
      return `/projects/new/repositories?${params}`;
    };

    const runOwn = async () => {
      const seq = ++own.seq;
      own.request?.abort();
      const request = new AbortController();
      own.request = request;
      own.searching = true;
      own.failure = "";
      render();
      let data;
      try {
        data = await getJSON(address(""), { signal: request.signal });
      } catch (err) {
        if (seq !== own.seq || signal.aborted) return;
        own.searching = false;
        own.entries = [];
        own.failure = err.message || "The repositories could not be searched.";
        own.retry = !(err.status >= 400 && err.status < 500);
        render();
        return;
      }
      if (seq !== own.seq || signal.aborted) return;
      own.searching = false;
      own.entries = data.repositories || [];
      own.failure = data.failure || "";
      own.retry = true;
      mark();
      render();
    };

    // A public search asks the forge's own search, which GitHub limits to a
    // few a minute: it waits until the typing pauses, and a failed or limited
    // one leaves the section out without a word.
    const runPublic = async () => {
      const seq = ++pub.seq;
      pub.request?.abort();
      const request = new AbortController();
      pub.request = request;
      const asked = term;
      pub.searching = true;
      render();
      let data;
      try {
        data = await getJSON(address("public"), { signal: request.signal });
      } catch {
        if (seq !== pub.seq || signal.aborted) return;
        pub.searching = false;
        pub.entries = [];
        pub.term = "";
        render();
        return;
      }
      if (seq !== pub.seq || signal.aborted) return;
      pub.searching = false;
      pub.entries = data.repositories || [];
      pub.term = asked;
      mark();
      render();
    };

    const schedule = (now) => {
      cancel();
      own.searching = true;
      own.timer = setTimeout(() => void runOwn(), now ? 0 : OWN_DELAY);
      if ([...term].length < MIN_PUBLIC) {
        pub.entries = [];
        pub.term = "";
      } else if (pub.term !== term) {
        pub.searching = true;
        pub.timer = setTimeout(() => void runPublic(), PUBLIC_DELAY);
      }
      render();
    };

    const step = (direction) => {
      const all = options();
      if (!all.length) return;
      const at = all.findIndex((e) => e.url === active);
      const next = at < 0 ? (direction > 0 ? 0 : all.length - 1) : (at + direction + all.length) % all.length;
      active = all[next].url;
      render();
      listEl.querySelector(".dropdown-item.active")?.scrollIntoView({ block: "nearest" });
    };

    const paintAddress = () => {
      if (!addressLine || !addressEl) return;
      addressEl.textContent = value.value;
      addressLine.hidden = !value.value;
    };

    const choose = (entry) => {
      value.value = entry.url;
      value.dataset.repoPath = entry.path;
      input.value = entry.path;
      paintAddress();
      hide();
      value.dispatchEvent(new Event("input", { bubbles: true }));
    };

    const openAll = () => {
      term = "";
      show();
      schedule(true);
    };

    input.addEventListener("mousedown", (e) => {
      if (document.activeElement === input) return;
      e.preventDefault();
      input.focus();
    }, { signal });
    input.addEventListener("focus", () => {
      input.select();
      openAll();
    }, { signal });
    input.addEventListener("click", () => {
      if (!open) openAll();
    }, { signal });
    input.addEventListener("input", () => {
      term = input.value.trim();
      show();
      schedule(false);
    }, { signal });
    input.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!open) {
          openAll();
          return;
        }
        step(e.key === "ArrowDown" ? 1 : -1);
      } else if (e.key === "Enter") {
        if (!open) return;
        e.preventDefault();
        if (own.searching) return;
        const chosen = options().find((entry) => entry.url === active);
        if (chosen) choose(chosen);
      } else if (e.key === "Escape") {
        if (!open) return;
        e.preventDefault();
        e.stopPropagation();
        restore();
        hide();
      }
    }, { signal });
    this.addEventListener("focusout", () => {
      setTimeout(() => {
        if (this.contains(document.activeElement)) return;
        restore();
        hide();
      }, 0);
    }, { signal });
    signal.addEventListener("abort", cancel);

    if (document.activeElement === input) {
      input.select();
      openAll();
    }
  }

  disconnectedCallback() {
    this.abort?.abort();
    this.abort = null;
  }
}

customElements.define("dc-repo-search", RepoSearch);
