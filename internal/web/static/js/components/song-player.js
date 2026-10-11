import { matchesTokens } from "@dc/filter";
import { catalog, onChange, play, playing, stop, title } from "@dc/music";
import { notifyError } from "@dc/toast";

const FILTER_KEY = "dc-song-filter";
const SCROLL_KEY = "dc-song-scroll";

function load(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function start(key) {
  play(key).catch(() => notifyError("Could not load the songs."));
}

function save(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    void 0;
  }
}

class SongPlayer extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.opener = this.querySelector("[data-song-open]");
    this.toggle = this.querySelector("[data-song-toggle]");
    this.skip = this.querySelector("[data-song-next]");
    this.now = this.querySelector("[data-song-now]");
    this.filter = this.querySelector("[data-song-filter]");
    this.count = this.querySelector("[data-song-filter-count]");
    this.clear = this.querySelector("[data-song-filter-clear]");
    this.scroller = this.querySelector("[data-song-scroll]");
    this.list = this.querySelector("[data-song-list]");
    this.empty = this.querySelector("[data-song-empty]");
    this.opener.addEventListener("pointerdown", (event) => { this.touched = event.pointerType === "touch"; }, { signal });
    this.opener.addEventListener("keydown", () => { this.touched = false; }, { signal });
    this.toggle.addEventListener("click", () => (playing() ? stop() : start()), { signal });
    this.skip.addEventListener("click", () => start(), { signal });
    this.list.addEventListener("click", (event) => {
      const item = event.target.closest("[data-song]");
      if (item) start(item.dataset.song);
    }, { signal });
    this.scroller.addEventListener("scroll", () => {
      this.scrollFrame ??= requestAnimationFrame(() => {
        this.scrollFrame = null;
        save(SCROLL_KEY, String(Math.round(this.scroller.scrollTop)));
      });
    }, { passive: true, signal });
    this.filter.addEventListener("input", () => this.setQuery(this.filter.value), { signal });
    this.filter.addEventListener("keydown", (event) => this.filterKey(event), { signal });
    // Bootstrap's dropdown takes Escape on the document while capturing and
    // closes the menu, so a filled filter claims it on the window first.
    window.addEventListener("keydown", (event) => {
      if (event.key !== "Escape" || event.target !== this.filter || this.filter.value === "") return;
      event.preventDefault();
      event.stopPropagation();
      this.setQuery("");
    }, { capture: true, signal });
    this.clear.addEventListener("click", () => {
      this.setQuery("");
      this.filter.focus();
    }, { signal });
    this.addEventListener("shown.bs.dropdown", () => {
      this.restoreFilter();
      this.scroller.scrollTop = Number(load(SCROLL_KEY)) || 0;
      if (!this.touched) this.filter.focus({ preventScroll: true });
    }, { signal });
    this.fill();
    signal.addEventListener("abort", onChange(() => this.render()));
    this.restoreFilter();
    this.render();
  }

  disconnectedCallback() {
    if (this.scrollFrame) cancelAnimationFrame(this.scrollFrame);
    this.scrollFrame = null;
    this.ac?.abort();
    this.ac = null;
  }

  fill() {
    this.list.replaceChildren(...catalog.map((shelf) => {
      const group = document.createElement("div");
      group.dataset.songShelf = "";
      const header = document.createElement("h6");
      header.className = "dropdown-header";
      header.textContent = shelf.style;
      group.append(header);
      for (const [key, name] of Object.entries(shelf.songs)) {
        const item = document.createElement("button");
        item.type = "button";
        item.className = "dropdown-item";
        item.dataset.song = key;
        item.textContent = name;
        group.append(item);
      }
      return group;
    }));
  }

  restoreFilter() {
    this.filter.value = load(FILTER_KEY) ?? "";
    this.applyFilter();
  }

  setQuery(value) {
    if (this.filter.value !== value) this.filter.value = value;
    save(FILTER_KEY, value);
    this.scroller.scrollTop = 0;
    this.applyFilter();
  }

  applyFilter() {
    const query = this.filter.value.trim();
    const items = [...this.list.querySelectorAll("[data-song]")];
    let found = 0;
    for (const item of items) {
      item.hidden = query !== "" && !matchesTokens(item.textContent, query);
      if (!item.hidden) found++;
    }
    for (const group of this.list.querySelectorAll("[data-song-shelf]")) {
      group.hidden = !group.querySelector("[data-song]:not([hidden])");
    }
    this.empty.textContent = `Nothing matches "${query}".`;
    this.empty.hidden = query === "" || found > 0;
    this.count.textContent = query === "" ? "" : `${found} of ${items.length}`;
    this.clear.style.visibility = query === "" ? "hidden" : "";
  }

  filterKey(event) {
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    const first = this.list.querySelector("[data-song]:not([hidden])");
    if (event.key === "ArrowDown" && first) {
      event.preventDefault();
      first.focus();
    } else if (event.key === "Enter" && first) {
      event.preventDefault();
      first.click();
    }
  }

  render() {
    const key = playing();
    const label = key ? `Stop ${title(key)}` : "Play a song";
    this.toggle.querySelector(".ti").className = `ti ${key ? "ti-player-stop" : "ti-player-play"}`;
    this.toggle.title = label;
    this.toggle.setAttribute("aria-label", label);
    this.skip.hidden = !key;
    this.now.textContent = key ? title(key) : "";
    for (const item of this.list.querySelectorAll("[data-song]")) {
      const active = item.dataset.song === key;
      item.classList.toggle("active", active);
      item.toggleAttribute("aria-current", active);
    }
  }
}

customElements.define("dc-song-player", SongPlayer);
