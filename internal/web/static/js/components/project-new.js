import { getJSON, postJSON, ensureOk } from "@dc/http";
import { notifyError, notifyInfo } from "@dc/toast";
import { relativeTime } from "dc-time";

const slug = (raw) => String(raw).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
const repositoryName = (raw) => {
  const cleaned = String(raw).trim().replace(/\/+$/, "").replace(/\.git$/i, "");
  return cleaned.split(/[/:]/).pop() || "";
};

const SEARCH_DELAY = 200;

function trackNote(branch) {
  const parts = [];
  if (branch.ahead) parts.push(`${branch.ahead} ahead`);
  if (branch.behind) parts.push(`${branch.behind} behind`);
  if (!parts.length) return "";
  return branch.upstream ? `${parts.join(", ")} ${branch.upstream}` : parts.join(", ");
}

function branchPicker(root, { signal, onPick }) {
  const value = root.querySelector("[data-branch-value]");
  const input = root.querySelector("[data-branch-search]");
  const menu = root.querySelector("[data-branch-menu]");
  const listEl = root.querySelector("[data-branch-list]");
  if (!value || !input || !menu || !listEl) {
    return null;
  }
  const source = root.dataset.branchSource || "";
  const resync = root.querySelector("[data-branch-resync]");
  const resyncIcon = root.querySelector("[data-branch-resync-icon]");
  const fetchedEl = root.querySelector("[data-branch-fetched]");
  const field = root.dataset.branchPicker;
  const showsName = field === "branch";
  const fromEl = root.closest("[data-branch-block]")?.querySelector("[data-branch-from]") || null;
  const marksTaken = root.dataset.branchMarksTaken === "true";
  const RESYNC = -1;
  const NONE = -2;
  let entries = [];
  let active = NONE;
  let query = "";
  let open = false;
  let searchSeq = 0;
  let searching = false;
  let searchTimer = 0;

  const pickable = (entry) => entry && !entry.header && !entry.disabled;

  const row = (entry, index) => {
    if (entry.header) {
      const header = document.createElement("h6");
      header.className = "dropdown-header";
      header.textContent = entry.label;
      return header;
    }
    const item = document.createElement("button");
    item.type = "button";
    item.className = "dropdown-item d-flex align-items-center gap-2";
    item.dataset.branchOption = entry.ref;
    if (entry.disabled) {
      item.classList.add("disabled");
      item.setAttribute("aria-disabled", "true");
    }
    if (index === active) {
      item.classList.add("active");
    }
    const icon = document.createElement("i");
    icon.className = `ti ${entry.remote ? "ti-cloud" : "ti-git-branch"} flex-shrink-0`;
    icon.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.className = "text-truncate";
    label.textContent = entry.label;
    item.append(icon, label);
    if (entry.note) {
      const note = document.createElement("span");
      note.className = "text-secondary small ms-auto flex-shrink-0";
      note.dataset.branchNote = "";
      note.textContent = entry.note;
      item.appendChild(note);
    }
    item.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
    if (!entry.disabled) {
      item.addEventListener("click", () => choose(entry), { signal });
    }
    return item;
  };

  const note = (text, tone) => {
    const el = document.createElement("div");
    el.className = `dropdown-item-text small ${tone}`;
    el.textContent = text;
    return el;
  };

  const retryRow = () => {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "dropdown-item d-flex align-items-center gap-2";
    row.dataset.branchRetry = "";
    row.innerHTML = '<i class="ti ti-reload flex-shrink-0" aria-hidden="true"></i>';
    const label = document.createElement("span");
    label.textContent = "Try again";
    row.appendChild(label);
    row.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
    row.addEventListener("click", () => void runSearch(query), { signal });
    return row;
  };

  const busyRow = () => {
    const el = document.createElement("div");
    el.className = "dropdown-item-text small text-secondary d-flex align-items-center gap-2";
    el.dataset.branchSearching = "";
    const spinner = document.createElement("span");
    spinner.className = "spinner-border spinner-border-sm";
    spinner.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.textContent = "Searching…";
    el.append(spinner, label);
    return el;
  };

  let failure = "";
  const render = ({ keepScroll = false } = {}) => {
    const top = keepScroll ? listEl.scrollTop : 0;
    const nodes = entries.map(row);
    if (searching) {
      nodes.unshift(busyRow());
    }
    if (failure) {
      nodes.push(note(failure, "text-danger"), retryRow());
    } else if (!entries.length && !searching) {
      nodes.push(note("No matching branch.", "text-secondary"));
    }
    listEl.replaceChildren(...nodes);
    listEl.scrollTop = top;
    resync?.classList.toggle("active", active === RESYNC);
  };

  const showActive = () => {
    if (active === RESYNC) {
      resync?.scrollIntoView({ block: "nearest" });
      return;
    }
    const item = listEl.querySelector(".dropdown-item.active");
    if (!item) return;
    const head = listEl.getBoundingClientRect().top - menu.getBoundingClientRect().top;
    item.style.scrollMarginTop = `${Math.max(0, Math.round(head))}px`;
    item.scrollIntoView({ block: "nearest" });
  };

  const show = () => {
    open = true;
    menu.classList.add("show");
    input.setAttribute("aria-expanded", "true");
  };
  const hide = () => {
    open = false;
    active = NONE;
    menu.classList.remove("show");
    input.setAttribute("aria-expanded", "false");
  };

  const restore = () => {
    input.value = value.dataset.branchName || value.value;
  };

  const paintFrom = () => {
    if (!fromEl) return;
    const ref = value.value;
    const name = value.dataset.branchName || "";
    const remote = name && ref.endsWith(`/${name}`) ? ref.slice(0, ref.length - name.length - 1) : "";
    fromEl.textContent = remote ? `from ${remote}` : "";
    fromEl.hidden = !remote;
  };

  const choose = (entry) => {
    value.value = entry.ref;
    value.dataset.branchName = showsName ? entry.name : entry.ref;
    value.dataset.branchUpstream = !entry.remote && entry.behind > 0 && entry.ahead === 0 ? entry.upstream : "";
    input.value = value.dataset.branchName;
    paintFrom();
    hide();
    if (onPick) onPick();
  };

  const toEntries = (branches) => {
    const out = [];
    let headed = false;
    for (const branch of branches) {
      if (branch.remote && !headed) {
        out.push({ header: true, label: "On a remote" });
        headed = true;
      }
      const taken = marksTaken ? branch.taken || "" : "";
      out.push({
        ref: branch.ref,
        name: branch.name || branch.ref,
        label: taken ? `${branch.ref} (in ${taken})` : branch.ref,
        note: branch.remote ? `checks out ${branch.name || branch.ref}` : trackNote(branch),
        remote: !!branch.remote,
        upstream: branch.upstream || "",
        ahead: branch.ahead || 0,
        behind: branch.behind || 0,
        disabled: !!taken,
      });
    }
    return out;
  };

  const runSearch = async (text, { keepMark = false } = {}) => {
    const seq = ++searchSeq;
    searching = true;
    failure = "";
    render();
    const params = new URLSearchParams();
    if (text) params.set("q", text);
    if (field === "start") params.set("pick", "start");
    const query = params.toString();
    let data;
    try {
      data = await getJSON(`/projects/${encodeURIComponent(source)}/branches${query ? `?${query}` : ""}`, { signal });
    } catch (err) {
      if (seq !== searchSeq || signal.aborted) return;
      searching = false;
      entries = [];
      failure = err.message || "The branches could not be read.";
      render();
      return;
    }
    if (seq !== searchSeq || signal.aborted) return;
    searching = false;
    entries = toEntries(data.branches || []);
    const first = entries.findIndex(pickable);
    active = keepMark && active === RESYNC ? RESYNC : (first >= 0 ? first : NONE);
    paintFetched(data.fetchedAt || "");
    render();
  };

  const search = (text) => {
    searchSeq += 1;
    searching = true;
    render();
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => void runSearch(text), SEARCH_DELAY);
  };

  const stops = () => {
    const list = resync ? [RESYNC] : [];
    entries.forEach((entry, i) => {
      if (pickable(entry)) list.push(i);
    });
    return list;
  };

  const step = (direction) => {
    const list = stops();
    if (!list.length) return;
    const at = list.indexOf(active);
    active = at < 0
      ? list[direction > 0 ? 0 : list.length - 1]
      : list[(at + direction + list.length) % list.length];
    render({ keepScroll: true });
    showActive();
  };

  input.addEventListener("mousedown", (e) => {
    if (document.activeElement === input) return;
    e.preventDefault();
    input.focus();
  }, { signal });
  const openPlain = () => {
    input.select();
    show();
    query = "";
    void runSearch("");
  };
  input.addEventListener("focus", openPlain, { signal });
  input.addEventListener("click", () => {
    if (!open) openPlain();
  }, { signal });
  input.addEventListener("input", () => {
    show();
    query = input.value.trim();
    search(query);
  }, { signal });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) {
        show();
        void runSearch(query);
        return;
      }
      step(e.key === "ArrowDown" ? 1 : -1);
    } else if (e.key === "Enter") {
      if (!open) return;
      e.preventDefault();
      if (searching) return;
      if (active === RESYNC) {
        void doResync();
        return;
      }
      const chosen = entries[active];
      if (pickable(chosen)) choose(chosen);
    } else if (e.key === "Escape") {
      if (!open) return;
      e.preventDefault();
      restore();
      hide();
    }
  }, { signal });
  root.addEventListener("focusout", () => {
    setTimeout(() => {
      if (root.contains(document.activeElement)) return;
      restore();
      hide();
    }, 0);
  }, { signal });

  const paintFetched = (at) => {
    if (!fetchedEl) return;
    const age = at ? relativeTime(at) : "";
    fetchedEl.textContent = age ? `fetched ${age}` : "never fetched";
    fetchedEl.title = at ? new Date(at).toLocaleString() : "";
  };
  const working = (on) => {
    if (!resync) return;
    resync.disabled = on;
    resync.setAttribute("aria-busy", on ? "true" : "false");
    resyncIcon?.classList.toggle("dc-spin", on);
  };
  const doResync = async () => {
    if (!resync || resync.disabled || !source) return;
    working(true);
    try {
      const response = await postJSON(`/projects/${encodeURIComponent(source)}/fetch`, {});
      await ensureOk(response, "The remotes could not be fetched.");
      const data = await response.json();
      if (!data.fetched) notifyInfo(`"${source}" has no remote to fetch from.`);
      if (!signal.aborted) await runSearch(query, { keepMark: true });
    } catch (err) {
      notifyError(err.message || "The remotes could not be fetched.");
    } finally {
      if (signal.aborted) return;
      working(false);
    }
  };
  if (resync) {
    resync.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
    resync.addEventListener("click", () => void doResync(), { signal });
  }

  return {
    close() {
      hide();
    },
    name() {
      return value.dataset.branchName || value.value;
    },
  };
}

class ProjectNew extends HTMLElement {
  connectedCallback() {
    if (this.abort) {
      return;
    }
    this.abort = new AbortController();
    const signal = this.abort.signal;
    const source = this.querySelector("[data-project-source]");
    if (source) {
      source.addEventListener("change", () => {
        const picked = source.value;
        const url = picked ? `/projects/new?create=${encodeURIComponent(picked)}` : "/projects/new";
        if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
        else window.location.href = url;
      }, { signal });
    }
    const name = this.querySelector("[data-project-name-field]");
    if (!name) {
      return;
    }
    this.typed = name.value.trim() !== "";
    name.addEventListener("input", () => {
      this.typed = name.value.trim() !== "";
    }, { signal });

    const cloneURL = this.querySelector("[data-clone-url]");
    if (cloneURL) {
      const fromURL = () => {
        if (this.typed) {
          return;
        }
        name.value = slug(repositoryName(cloneURL.value));
      };
      cloneURL.addEventListener("input", fromURL, { signal });
      fromURL();
    }

    const created = this.querySelector("[data-branch-new]");
    const modes = this.querySelector("[data-branch-mode]");
    if (!modes) {
      return;
    }
    const mode = () => modes.value || "existing";
    const suggest = () => {
      if (this.typed) {
        return;
      }
      const picked = source && source.selectedOptions[0];
      const project = picked ? picked.dataset.projectName || "" : "";
      const branch = slug(mode() === "new" ? (created ? created.value : "") : pickers.branch ? pickers.branch.name() : "");
      name.value = branch ? `${project}-${branch}` : project;
    };

    const pickers = {};
    for (const root of this.querySelectorAll("[data-branch-picker]")) {
      pickers[root.dataset.branchPicker] = branchPicker(root, {
        signal,
        onPick: () => {
          suggest();
          syncFastForward();
        },
      });
    }

    const ffRow = this.querySelector("[data-branch-ff]");
    const ffInput = this.querySelector("[data-branch-ff-input]");
    const ffLabel = this.querySelector("[data-branch-ff-label]");
    const branchValue = this.querySelector('[data-branch-picker="branch"] [data-branch-value]');
    const syncFastForward = () => {
      if (!ffRow || !ffInput) return;
      const upstream = branchValue?.dataset.branchUpstream || "";
      ffRow.hidden = !upstream;
      ffInput.disabled = !upstream || mode() !== "existing";
      if (ffLabel && upstream) ffLabel.textContent = `Fast-forward to ${upstream} after creating`;
    };

    const apply = (focus) => {
      const active = mode();
      for (const block of this.querySelectorAll("[data-branch-block]")) {
        const on = block.dataset.branchBlock === active;
        block.hidden = !on;
        block.querySelectorAll("input, select").forEach((field) => {
          field.disabled = !on;
        });
        if (!on) {
          const picker = block.querySelector("[data-branch-picker]");
          pickers[picker?.dataset.branchPicker]?.close();
        }
      }
      if (focus && active === "new" && created) {
        created.focus();
      }
      syncFastForward();
      suggest();
    };

    modes.addEventListener("change", () => apply(true), { signal });
    if (created) {
      created.addEventListener("input", suggest, { signal });
    }
    apply(false);
  }

  disconnectedCallback() {
    this.abort?.abort();
    this.abort = null;
  }
}

customElements.define("dc-project-new", ProjectNew);
