import { matchesTokens } from "@dc/filter";
import { catalog, genreOf, mode, next, onChange, play, playing, position, shuffle, stop, title } from "@dc/music";
import { get, getJSON, set, setJSON } from "@dc/store";
import { notifyError } from "@dc/toast";

const FILTER_KEY = "dc-song-filter";
const SCROLL_KEY = "dc-song-scroll";
const OPEN_KEY = "dc-song-open";
const SONGS = catalog.reduce((count, shelf) => count + Object.keys(shelf.songs).length, 0);

function attempt(started) {
  started.catch(() => notifyError("Could not load the songs."));
}

function shelfOf(style) {
  return catalog.find((shelf) => shelf.style === style) ?? null;
}

function tint(element, shelf) {
  element.style.setProperty("--dc-genre-rgb", `var(--tblr-${shelf?.color ?? "secondary"}-rgb)`);
}

function clock(seconds) {
  const whole = Math.max(0, Math.floor(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

class SongPlayer extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.opener = this.querySelector("[data-song-open]");
    this.note = this.querySelector("[data-song-note]");
    this.noteEq = this.querySelector("[data-song-note-eq]");
    this.stage = this.querySelector("[data-song-stage]");
    this.art = this.querySelector("[data-song-art]");
    this.now = this.querySelector("[data-song-now]");
    this.from = this.querySelector("[data-song-from]");
    this.elapsed = this.querySelector("[data-song-elapsed]");
    this.length = this.querySelector("[data-song-length]");
    this.bar = this.querySelector("[data-song-progress]");
    this.toggle = this.querySelector("[data-song-toggle]");
    this.skip = this.querySelector("[data-song-next]");
    this.mode = this.querySelector("[data-song-mode]");
    this.modeLabel = this.querySelector("[data-song-mode-label]");
    this.filter = this.querySelector("[data-song-filter]");
    this.count = this.querySelector("[data-song-filter-count]");
    this.clear = this.querySelector("[data-song-filter-clear]");
    this.scroller = this.querySelector("[data-song-scroll]");
    this.list = this.querySelector("[data-song-list]");
    this.empty = this.querySelector("[data-song-empty]");
    this.artEq = this.equalizer();
    this.art.append(this.artEq);
    this.opener.addEventListener("pointerdown", (event) => { this.touched = event.pointerType === "touch"; }, { signal });
    this.opener.addEventListener("keydown", () => { this.touched = false; }, { signal });
    this.toggle.addEventListener("click", () => (playing() ? stop() : attempt(shuffle())), { signal });
    this.skip.addEventListener("click", () => attempt(next()), { signal });
    this.list.addEventListener("click", (event) => this.pick(event), { signal });
    this.scroller.addEventListener("scroll", () => {
      this.scrollFrame ??= requestAnimationFrame(() => {
        this.scrollFrame = null;
        set(SCROLL_KEY, String(Math.round(this.scroller.scrollTop)));
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
      this.restore();
      this.scroller.scrollTop = Number(get(SCROLL_KEY)) || 0;
      if (!this.touched) this.filter.focus({ preventScroll: true });
      this.follow();
    }, { signal });
    this.addEventListener("hidden.bs.dropdown", () => this.halt(), { signal });
    this.fill();
    signal.addEventListener("abort", onChange(() => this.render()));
    this.restore();
    this.render();
  }

  disconnectedCallback() {
    if (this.scrollFrame) cancelAnimationFrame(this.scrollFrame);
    this.scrollFrame = null;
    this.halt();
    this.artEq?.remove();
    this.artEq = null;
    this.ac?.abort();
    this.ac = null;
  }

  equalizer() {
    const eq = this.noteEq.cloneNode(true);
    eq.removeAttribute("data-song-note-eq");
    return eq;
  }

  pick(event) {
    const genrePlay = event.target.closest("[data-song-genre-play]");
    if (genrePlay) {
      const style = genrePlay.closest("[data-song-shelf]").dataset.songShelf;
      if (playing() && mode() === style) stop();
      else attempt(shuffle(style));
      return;
    }
    const fold = event.target.closest("[data-song-genre]");
    if (fold) {
      this.setOpen(fold.closest("[data-song-shelf]").dataset.songShelf, fold.getAttribute("aria-expanded") !== "true");
      return;
    }
    const item = event.target.closest("[data-song]");
    if (item) attempt(play(item.dataset.song));
  }

  fill() {
    const largest = Math.max(...catalog.map((shelf) => Object.keys(shelf.songs).length));
    this.scroller.style.setProperty("--dc-song-rows", String(catalog.length + largest));
    this.scroller.style.setProperty("--dc-song-genres", String(catalog.length));
    this.list.replaceChildren(...catalog.map((shelf) => {
      const group = document.createElement("div");
      group.dataset.songShelf = shelf.style;
      tint(group, shelf);
      const row = document.createElement("div");
      row.className = "d-flex align-items-center dc-song-genre-row";
      const fold = document.createElement("button");
      fold.type = "button";
      fold.className = "dropdown-item d-flex align-items-center gap-2 flex-fill min-w-0";
      fold.dataset.songGenre = "";
      const chevron = document.createElement("i");
      chevron.className = "ti ti-chevron-right text-secondary";
      chevron.setAttribute("aria-hidden", "true");
      const art = document.createElement("span");
      art.className = "dc-song-art dc-song-art-sm";
      art.setAttribute("aria-hidden", "true");
      art.innerHTML = '<i class="ti"></i>';
      art.firstChild.classList.add(shelf.icon);
      const name = document.createElement("span");
      name.className = "fw-medium dc-trunc";
      name.textContent = shelf.style;
      const eq = this.equalizer();
      eq.dataset.songGenreEq = "";
      const count = document.createElement("span");
      count.className = "ms-auto text-secondary small";
      count.dataset.songCount = "";
      fold.append(chevron, art, name, eq, count);
      const start = document.createElement("button");
      start.type = "button";
      start.className = "btn btn-icon btn-sm btn-ghost-secondary rounded-circle dc-song-genre-play";
      start.dataset.songGenrePlay = "";
      start.innerHTML = '<i class="ti ti-player-play" aria-hidden="true"></i>';
      row.append(fold, start);
      group.append(row);
      Object.entries(shelf.songs).forEach(([key, song], index) => {
        const item = document.createElement("button");
        item.type = "button";
        item.className = "dropdown-item d-flex align-items-center gap-2";
        item.dataset.song = key;
        item.dataset.songNum = String(index + 1);
        const itemEq = this.equalizer();
        itemEq.dataset.songEq = "";
        const label = document.createElement("span");
        label.className = "dc-trunc";
        label.textContent = song;
        item.append(itemEq, label);
        group.append(item);
      });
      return group;
    }));
  }

  restore() {
    const stored = getJSON(OPEN_KEY, []);
    this.open = new Set(Array.isArray(stored) ? stored : []);
    this.filter.value = get(FILTER_KEY);
    this.applyFilter();
  }

  setOpen(style, open) {
    if (open) this.open.add(style);
    else this.open.delete(style);
    setJSON(OPEN_KEY, catalog.map((shelf) => shelf.style).filter((name) => this.open.has(name)));
    this.applyFilter();
  }

  setQuery(value) {
    if (this.filter.value !== value) this.filter.value = value;
    set(FILTER_KEY, value);
    this.scroller.scrollTop = 0;
    this.applyFilter();
  }

  // While a query stands, every genre shows its hits, so the filter finds
  // songs in folded genres too.
  applyFilter() {
    const query = this.filter.value.trim();
    const filtering = query !== "";
    let found = 0;
    for (const group of this.list.querySelectorAll("[data-song-shelf]")) {
      const open = filtering || this.open.has(group.dataset.songShelf);
      let hits = 0;
      for (const item of group.querySelectorAll("[data-song]")) {
        const hit = !filtering || matchesTokens(item.textContent, query);
        item.hidden = !open || !hit;
        if (hit) hits++;
      }
      found += hits;
      group.hidden = hits === 0;
      const fold = group.querySelector("[data-song-genre]");
      fold.disabled = filtering;
      fold.setAttribute("aria-expanded", open ? "true" : "false");
      fold.setAttribute("aria-label", `${group.dataset.songShelf}, ${hits} ${hits === 1 ? "song" : "songs"}`);
      const chevron = fold.querySelector(".ti");
      chevron.classList.toggle("ti-chevron-down", open);
      chevron.classList.toggle("ti-chevron-right", !open);
      fold.querySelector("[data-song-count]").textContent = hits;
    }
    this.empty.textContent = `Nothing matches "${query}".`;
    this.empty.hidden = !filtering || found > 0;
    this.count.textContent = filtering ? `${found} of ${SONGS}` : "";
    this.clear.style.visibility = filtering ? "" : "hidden";
  }

  filterKey(event) {
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.key === "ArrowDown") {
      const first = this.list.querySelector(".dropdown-item:not([hidden]):not(:disabled)");
      if (!first) return;
      event.preventDefault();
      first.focus();
    } else if (event.key === "Enter") {
      const first = this.list.querySelector("[data-song]:not([hidden])");
      if (!first) return;
      event.preventDefault();
      first.click();
    }
  }

  // The position moves on its own, so it follows every frame while the panel
  // is open and stands still while it is closed.
  follow() {
    this.halt();
    const frame = () => {
      this.tick();
      this.positionFrame = requestAnimationFrame(frame);
    };
    this.positionFrame = requestAnimationFrame(frame);
  }

  halt() {
    if (this.positionFrame) cancelAnimationFrame(this.positionFrame);
    this.positionFrame = null;
  }

  tick() {
    const at = position();
    const length = at?.length ?? 0;
    const elapsed = at ? Math.min(at.elapsed, length) : 0;
    const share = length ? (elapsed / length) * 100 : 0;
    this.elapsed.textContent = clock(elapsed);
    this.length.textContent = clock(length);
    this.bar.style.width = `${share}%`;
    this.bar.setAttribute("aria-valuenow", String(Math.round(share)));
  }

  render() {
    const key = playing();
    const genre = key ? genreOf(key) : null;
    const shelf = shelfOf(genre);
    const scope = mode();
    const scopeLabel = scope ?? "All genres";
    this.note.hidden = !!key;
    this.noteEq.hidden = !key;
    this.opener.title = key ? `${title(key)} · ${genre}` : "Songs";
    this.opener.setAttribute("aria-label", key ? `Songs, playing ${title(key)} from ${genre}` : "Songs");
    tint(this.stage, shelf);
    this.art.querySelector(".ti").className = `ti ${shelf?.icon ?? "ti-music"}`;
    this.artEq.hidden = !key;
    this.now.textContent = key ? title(key) : "Nothing playing";
    this.from.textContent = key ? genre : `${SONGS} songs in ${catalog.length} genres`;
    tint(this.mode, shelfOf(scope));
    this.mode.style.visibility = key ? "" : "hidden";
    this.mode.title = `Shuffle ${scope ?? "all genres"}`;
    this.modeLabel.textContent = scopeLabel;
    const toggleLabel = key ? `Stop ${title(key)}` : "Shuffle all genres";
    this.toggle.querySelector(".ti").className = `ti ${key ? "ti-player-stop" : "ti-player-play"}`;
    this.toggle.title = toggleLabel;
    this.toggle.setAttribute("aria-label", toggleLabel);
    const skipLabel = `Next in ${scope ?? "all genres"}`;
    this.skip.disabled = !key;
    this.skip.title = skipLabel;
    this.skip.setAttribute("aria-label", skipLabel);
    for (const group of this.list.querySelectorAll("[data-song-shelf]")) {
      const style = group.dataset.songShelf;
      const running = !!key && scope === style;
      group.querySelector(".dc-song-genre-row").toggleAttribute("data-song-running", running);
      group.querySelector("[data-song-genre-eq]").hidden = genre !== style;
      const start = group.querySelector("[data-song-genre-play]");
      const label = running ? `Stop ${style}` : `Shuffle ${style}`;
      start.querySelector(".ti").className = `ti ${running ? "ti-player-stop" : "ti-player-play"}`;
      start.title = label;
      start.setAttribute("aria-label", label);
    }
    for (const item of this.list.querySelectorAll("[data-song]")) {
      const current = item.dataset.song === key;
      item.toggleAttribute("aria-current", current);
      item.querySelector("[data-song-eq]").hidden = !current;
    }
    this.tick();
  }
}

customElements.define("dc-song-player", SongPlayer);
