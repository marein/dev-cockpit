import { matchesTokens } from "@dc/filter";
import { baseName, parentDir } from "@dc/editor-format";
import { LAYOUT_SAVE_MS } from "@dc/editor-saved";
import { GIT_MARKS } from "@dc/editor-marks";
import * as store from "@dc/store";

// How many changes the commit view builds rows for in one go; the folder rows
// over them ride along uncounted. A working copy with a few hundred changes is
// one screen of scrolling and arrives whole; beyond that a button asks for the
// next batch, because a list of thousands of rows costs more to build and lay
// out than anybody spends reading it. The cap is about the rows alone: the
// counts, the all box and the commit itself always speak for every change.
const COMMIT_ROWS_STEP = 500;

// What every folder row needs is counted once per list into the index and
// moved by the clicks from there on: how many changes sit under it, how many
// of those can be picked and how many are. Answering that by filtering the
// change list instead costs one pass per folder row and another per click,
// which is what made a working copy with ten thousand changes a panel nobody
// could open. The rendered rows are held by key for the same reason. Only
// the first `shown` changes have rows, their folder rows ride along; the
// rest waits behind a button whose numbers count changes, and the picks,
// the summary and the commit always speak for the whole list.
export function changeList({ listEl, filterEl, countEl, clearBtn, picks, kindOf, canOpen, open, active, onPick, onFilter, empty, stateKey, grouped, status, signal }) {
  let changes = [];
  let changesSig = null;
  let sorted = [];
  let query = "";
  let index = emptyIndex();
  let pendingScroll = null;
  let lastScroll = 0;
  let restored = false;
  let stateTimer = 0;
  // How many of the shown changes may carry rows right now; it grows with
  // the button and the walk and survives a re-render, so a refresh never
  // folds a list somebody grew back to its first batch.
  let shown = COMMIT_ROWS_STEP;
  // Which row the keyboard stands on, by key so it survives a rebuild, and
  // the rendered order the arrows walk, every row of it and not only the
  // built ones: walking into the batch behind the button builds it. The
  // walk covers every row, folders and files alike, so the key is typed:
  // "f:" plus the path for a file row, "g:" plus the directory for a
  // folder row.
  let activeKey = "";
  let order = [];
  const orderIndex = new Map();
  let activeRowEl = null;
  let scrollRaf = 0;
  let specList = [];
  let built = 0;
  let builtFiles = 0;
  let moreEl = null;
  const rowEls = new Map();
  const inputs = new Map();
  const picked = new Set();
  listEl.tabIndex = 0;

  function emptyNode(dir, parent) {
    return { dir, parent, dirs: new Map(), files: [], count: 0, selectable: 0, picked: 0 };
  }

  function emptyIndex() {
    const root = emptyNode("", null);
    return { shown: [], byPath: new Map(), nodes: new Map([["", root]]), root, total: 0, pickedAll: 0 };
  }

  // matches is the filter, the shared token match every list in this app
  // narrows with, over the path a row stands for.
  function matches(entry) {
    return query === "" || matchesTokens(entry.path, query) || (!!entry.from && matchesTokens(entry.from, query));
  }

  // dirOf is the folder a row is grouped under: the parent, with the
  // trailing slash of a directory entry out of the way first.
  function dirOf(entry) {
    return parentDir(entry.path.endsWith("/") ? entry.path.slice(0, -1) : entry.path);
  }

  const selectable = (entry) => picks && kindOf(entry) !== "conflict";

  // setChanges takes a fresh list. The sort is the expensive half and
  // belongs to the list, not to a keystroke, so it is done here and the
  // filter only rebuilds the tree over it.
  function setChanges(list) {
    const sig = list.map((entry) => JSON.stringify(entry)).join("\n");
    if (sig === changesSig) return false;
    changesSig = sig;
    changes = list;
    sorted = [...list].sort((a, b) => a.path.localeCompare(b.path));
    buildIndex();
    return true;
  }

  // buildIndex counts what the rows need. byPath and the two totals are
  // about every change there is, because a pick outside the filter is still
  // a pick and must not be pruned or forgotten; the folder tree holds only
  // what the filter shows, which is what makes every folder row, every count
  // and every checkbox under it speak about the same set without a second
  // path through the code.
  function buildIndex() {
    const root = emptyNode("", null);
    const nodes = new Map([["", root]]);
    const byPath = new Map();
    const hits = [];
    let pickedAll = 0;
    for (const entry of sorted) {
      byPath.set(entry.path, entry);
      const canPick = selectable(entry);
      const isPicked = canPick && picked.has(entry.path);
      if (isPicked) pickedAll += 1;
      if (!matches(entry)) continue;
      hits.push(entry);
      let node = root;
      const dir = dirOf(entry);
      if (dir) {
        let prefix = "";
        for (const segment of dir.split("/")) {
          prefix = prefix ? `${prefix}/${segment}` : segment;
          let child = node.dirs.get(segment);
          if (!child) {
            child = emptyNode(prefix, node);
            node.dirs.set(segment, child);
            nodes.set(prefix, child);
          }
          node = child;
        }
      }
      node.files.push(entry);
      for (let n = node; n; n = n.parent) {
        n.count += 1;
        if (canPick) n.selectable += 1;
        if (isPicked) n.picked += 1;
      }
    }
    index = { shown: hits, byPath, nodes, root, total: sorted.length, pickedAll };
  }

  // recountPicks reads the picks back into the folder counts. The clicks
  // move them one by one; this is for the moments a whole set arrives at
  // once, the all box, a draft from another device and a spent commit.
  function recountPicks() {
    for (const node of index.nodes.values()) node.picked = 0;
    for (const entry of index.shown) {
      if (!selectable(entry) || !picked.has(entry.path)) continue;
      for (let n = index.nodes.get(dirOf(entry)); n; n = n.parent) n.picked += 1;
    }
    let all = 0;
    for (const entry of sorted) {
      if (selectable(entry) && picked.has(entry.path)) all += 1;
    }
    index.pickedAll = all;
  }

  function selectedPaths() {
    const paths = [];
    for (const entry of changes) {
      if (selectable(entry) && picked.has(entry.path)) paths.push(entry.path);
    }
    return paths;
  }

  // prunePicks drops picks the list no longer holds.
  function prunePicks() {
    for (const path of picked) {
      if (!index.byPath.has(path)) picked.delete(path);
    }
  }

  // setPick is the one place a single row's pick moves, and it moves every
  // number that counts it: the folders above the row, which is what their
  // checkboxes read, and the whole list's, which is what the commit button
  // and the summary's "picked in all" read. A pick that changed one of the
  // two and not the other is a commit button that stands disabled over
  // work somebody picked.
  function setPick(entry, on) {
    if (on === picked.has(entry.path)) return;
    if (on) picked.add(entry.path);
    else picked.delete(entry.path);
    const delta = on ? 1 : -1;
    index.pickedAll += delta;
    for (let n = index.nodes.get(dirOf(entry)); n; n = n.parent) n.picked += delta;
  }

  function numbersOf(entry) {
    if (entry.binary) return "binary";
    if (entry.added || entry.removed) return `+${entry.added || 0} −${entry.removed || 0}`;
    return "";
  }

  function fileRow(entry, nested, depth) {
    const kind = kindOf(entry);
    const info = GIT_MARKS[kind];
    const key = `f:${entry.path}`;
    const row = document.createElement("div");
    row.className = "editor-commit-row";
    row.tabIndex = -1;
    if (nested) {
      row.classList.add("editor-commit-nested");
      row.style.paddingLeft = `${0.5 + (depth || 1) * 1.1}rem`;
    }
    row.dataset.path = entry.path;
    if (picks) {
      const check = document.createElement("input");
      check.type = "checkbox";
      check.tabIndex = -1;
      check.className = "form-check-input m-0 flex-shrink-0";
      check.setAttribute("aria-label", `Include ${entry.path}`);
      if (kind === "conflict") {
        check.disabled = true;
        check.title = "Resolve the conflict first.";
      } else {
        check.checked = picked.has(entry.path);
        check.addEventListener("change", () => {
          activeKey = key;
          mark();
          setPick(entry, check.checked);
          // Every folder above carries this file in its subtree, so each of
          // their rows may have to move to or from the mixed state.
          for (let n = index.nodes.get(dirOf(entry)); n; n = n.parent) paintGroupRow(n);
          onPick();
        });
      }
      inputs.set(key, check);
      row.appendChild(check);
    }
    const openBtn = document.createElement("button");
    openBtn.type = "button";
    openBtn.tabIndex = -1;
    openBtn.className = "editor-commit-open";
    const isDirEntry = entry.path.endsWith("/");
    const shownPath = isDirEntry ? entry.path.slice(0, -1) : entry.path;
    const line = document.createElement("div");
    line.className = "editor-commit-line";
    const markEl = document.createElement("span");
    markEl.className = `small fw-bold flex-shrink-0 ${info.cls}`;
    markEl.textContent = info.mark;
    const nameEl = document.createElement("span");
    nameEl.className = `text-truncate ${info.cls}`;
    nameEl.textContent = baseName(shownPath) + (isDirEntry ? "/" : "");
    line.append(markEl, nameEl);
    const numbers = numbersOf(entry);
    if (numbers) {
      const counts = document.createElement("span");
      counts.className = "small ms-auto ps-2 flex-shrink-0";
      if (entry.binary) {
        counts.classList.add("text-secondary");
        counts.textContent = numbers;
      } else {
        const plus = document.createElement("span");
        plus.className = "text-green";
        plus.textContent = `+${entry.added || 0}`;
        const minus = document.createElement("span");
        minus.className = "text-red ms-1";
        minus.textContent = `−${entry.removed || 0}`;
        counts.append(plus, minus);
      }
      line.append(counts);
    }
    openBtn.append(line);
    // The full path on a line of its own, wrapping instead of being cut: a
    // name alone does not tell two same named files apart. A file at the
    // root would only repeat its name, and under a group row the folder
    // already stands above the file.
    const renamed = !!entry.from && entry.from !== entry.path;
    if (renamed || (!nested && parentDir(shownPath))) {
      const pathEl = document.createElement("div");
      pathEl.className = "editor-commit-path";
      pathEl.textContent = renamed ? `${entry.from} → ${entry.path}` : entry.path;
      openBtn.append(pathEl);
    }
    openBtn.title = [renamed ? `${entry.from} → ${entry.path}` : entry.path, info.label, numbers].filter(Boolean).join(" · ");
    if (!canOpen(entry)) {
      openBtn.disabled = true;
    } else {
      openBtn.addEventListener("click", () => {
        activeKey = key;
        mark();
        void open(entry);
      });
    }
    row.append(openBtn);
    rowEls.set(key, row);
    return row;
  }

  // A group row is one folder's line over its whole subtree: the checkbox
  // picks and drops everything below it, a mixed pick reads as
  // indeterminate. The folder is only a grouping, what is committed are
  // always the files.
  function groupRow(node, label, depth) {
    const key = `g:${node.dir}`;
    const row = document.createElement("div");
    row.className = "editor-commit-row editor-commit-grouprow";
    row.tabIndex = -1;
    row.dataset.dir = node.dir;
    if (depth) row.style.paddingLeft = `${0.5 + depth * 1.1}rem`;
    if (picks) {
      const check = document.createElement("input");
      check.type = "checkbox";
      check.tabIndex = -1;
      check.className = "form-check-input m-0 flex-shrink-0";
      check.setAttribute("aria-label", `Include everything in ${node.dir}`);
      check.addEventListener("change", () => {
        activeKey = key;
        mark();
        const on = check.checked;
        const before = node.picked;
        const stack = [node];
        while (stack.length > 0) {
          const n = stack.pop();
          for (const entry of n.files) {
            if (!selectable(entry)) continue;
            if (on) picked.add(entry.path);
            else picked.delete(entry.path);
            const input = inputs.get(`f:${entry.path}`);
            if (input && !input.disabled) input.checked = on;
          }
          n.picked = on ? n.selectable : 0;
          paintGroupRow(n);
          for (const child of n.dirs.values()) stack.push(child);
        }
        // The whole list's number moves with the subtree, see setPick.
        index.pickedAll += node.picked - before;
        // A subtree toggle moves rows in both directions, the groups inside
        // it and the ones this folder sits in.
        for (let n = node.parent; n; n = n.parent) {
          n.picked += node.picked - before;
          paintGroupRow(n);
        }
        onPick();
      });
      inputs.set(key, check);
      row.appendChild(check);
    }
    const line = document.createElement("div");
    line.className = "editor-commit-line";
    const icon = document.createElement("i");
    icon.className = "ti ti-folder flex-shrink-0 text-secondary";
    icon.setAttribute("aria-hidden", "true");
    const nameEl = document.createElement("span");
    nameEl.className = "text-truncate small fw-medium";
    nameEl.textContent = label;
    const count = document.createElement("span");
    count.className = "small text-secondary ms-auto ps-2 flex-shrink-0";
    count.textContent = String(node.count);
    line.append(icon, nameEl, count);
    row.title = node.dir;
    row.append(line);
    rowEls.set(key, row);
    paintGroupRow(node);
    return row;
  }

  // paintGroupRow reads a folder's counted pick state back onto its row, so
  // a single file's checkbox never costs a rebuild of the list. A folder
  // that has no row right now, because it is merged into a chain or waits
  // behind the button, is counted all the same and simply has nothing to
  // paint.
  function paintGroupRow(node) {
    if (!node) return;
    const input = inputs.get(`g:${node.dir}`);
    if (!input) return;
    input.disabled = node.selectable === 0;
    input.checked = node.selectable > 0 && node.picked === node.selectable;
    input.indeterminate = node.picked > 0 && node.picked < node.selectable;
  }

  // moreRow is the end of a list that is longer than the panel builds at
  // once. Its number is counted in what the list is showing, changes
  // plainly and matches under a filter, never in tree rows: the folder
  // rows are grouping and adding them in made three thousand hits read as
  // five thousand missing. Built rows plus this rest is the count over the
  // list, and the waiting ones are picked and committed with the rest
  // regardless, because a row nobody can see must not read as a change
  // nobody can commit.
  function moreRow(rest) {
    const row = document.createElement("div");
    row.className = "px-2 py-2 border-top";
    row.dataset.editorCommitRest = String(rest);
    const btn = document.createElement("button");
    btn.type = "button";
    btn.tabIndex = -1;
    btn.className = "btn btn-sm btn-ghost-secondary w-100";
    btn.dataset.editorCommitShowMore = "";
    btn.textContent = `Show ${Math.min(rest, COMMIT_ROWS_STEP)} more`;
    btn.addEventListener("click", () => extend(), { signal });
    const kind = query === "" ? ["change", "changes"] : ["match", "matches"];
    const note = document.createElement("div");
    note.className = "small text-secondary mt-1";
    const tail = picks ? (rest === 1 ? " The box above picks it with the rest." : " The box above picks them with the rest.") : "";
    note.textContent = (rest === 1
      ? `One more ${kind[0]} is not listed here.`
      : `${rest} more ${kind[1]} are not listed here.`) + tail;
    row.append(btn, note);
    return row;
  }

  // specs is the list the panel would render in full: one entry per row,
  // folders first and files after them on every level in the grouped view,
  // the sorted changes in the flat one. Building it is cheap, building its
  // rows is not, which is what the cap is about.
  function specs() {
    const list = [];
    if (!grouped()) {
      for (const entry of index.shown) list.push({ entry, nested: false, depth: 0 });
      return list;
    }
    // Grouped by folder, the way an IDE's directory tree reads: folders
    // first and files after them, on every level. A folder becomes a row of
    // its own as soon as it holds more than one thing; a chain of folders
    // that only hands down to a single subfolder and has no files of its
    // own merges into one row with the joined path as its label. A group's
    // checkbox covers its whole subtree, see groupRow.
    const emit = (node, depth) => {
      for (const segment of [...node.dirs.keys()].sort((a, b) => a.localeCompare(b))) {
        let label = segment;
        let child = node.dirs.get(segment);
        while (child.files.length === 0 && child.dirs.size === 1) {
          const [next] = child.dirs.keys();
          label = `${label}/${next}`;
          child = child.dirs.get(next);
        }
        list.push({ node: child, label, depth });
        emit(child, depth + 1);
      }
      for (const entry of node.files) list.push({ entry, nested: depth > 0, depth });
    };
    emit(index.root, 0);
    return list;
  }

  function render() {
    const scrollTop = listEl.scrollTop;
    rowEls.clear();
    inputs.clear();
    if (index.shown.length === 0) {
      order = [];
      orderIndex.clear();
      specList = [];
      built = 0;
      builtFiles = 0;
      moreEl = null;
      listEl.replaceChildren(empty(query));
      return;
    }
    const list = specs();
    specList = list;
    order = [];
    orderIndex.clear();
    for (let i = 0; i < list.length; i += 1) {
      const key = list[i].entry ? `f:${list[i].entry.path}` : `g:${list[i].node.dir}`;
      orderIndex.set(key, i);
      order.push(key);
    }
    const quota = Math.max(shown, COMMIT_ROWS_STEP);
    const rows = [];
    built = 0;
    builtFiles = 0;
    while (built < list.length && builtFiles < quota) {
      const spec = list[built];
      if (spec.entry) builtFiles += 1;
      rows.push(spec.entry
        ? fileRow(spec.entry, spec.nested, spec.depth)
        : groupRow(spec.node, spec.label, spec.depth));
      built += 1;
    }
    moreEl = built < list.length ? moreRow(index.shown.length - builtFiles) : null;
    if (moreEl) rows.push(moreEl);
    const listHadFocus = listEl.contains(document.activeElement);
    listEl.replaceChildren(...rows);
    if (pendingScroll != null && listEl.clientHeight > 0) {
      listEl.scrollTop = pendingScroll;
      pendingScroll = null;
    } else {
      listEl.scrollTop = scrollTop;
    }
    mark();
    if (listHadFocus && !focusRow()) filterEl.focus();
  }

  // extend builds the next batch behind the rows that already stand,
  // appended and never rebuilt: a full rebuild grows with everything built
  // so far, so a walk that crossed its tenth batch edge would pay for ten
  // thousand rows to gain one, and that once per edge. A batch is
  // COMMIT_ROWS_STEP more changes, the folder rows over them ride along
  // uncounted, and minSpec forces the walk to cover a row the arrows are
  // about to stand on.
  function extend(minSpec = -1) {
    const list = specList;
    if (built >= list.length) return;
    if (moreEl) {
      moreEl.remove();
      moreEl = null;
    }
    const goal = builtFiles + COMMIT_ROWS_STEP;
    const batch = document.createDocumentFragment();
    while (built < list.length && (builtFiles < goal || built <= minSpec)) {
      const spec = list[built];
      if (spec.entry) builtFiles += 1;
      batch.append(spec.entry
        ? fileRow(spec.entry, spec.nested, spec.depth)
        : groupRow(spec.node, spec.label, spec.depth));
      built += 1;
    }
    shown = Math.max(shown, builtFiles);
    if (built < list.length) {
      moreEl = moreRow(index.shown.length - builtFiles);
      batch.append(moreEl);
    }
    listEl.append(batch);
  }

  // ---- the filter and the keyboard walk over its hits ----------------------

  // The filter stands over every list, short or long, and the keyboard owns
  // the rows from there with the real focus: the arrows reach every row,
  // folder rows and file rows alike, in the order they stand on the screen.
  // Nothing here changes what a commit takes: the filter narrows what is
  // shown and what the boxes over it act on, the picks themselves stand.

  function activeIndex() {
    if (!activeKey) return -1;
    const at = orderIndex.get(activeKey);
    return at === undefined ? -1 : at;
  }

  function activeRow() {
    return activeKey ? rowEls.get(activeKey) || null : null;
  }

  function activeInput() {
    return activeKey ? inputs.get(activeKey) || null : null;
  }

  // The mark has to survive a held arrow key, thirty presses a second over
  // a list of thousands of rows, so nothing here may cost what the list is
  // long: the old row is remembered instead of swept for, the index is a
  // map lookup, and the scroll runs once per frame in a rAF rather than
  // once per press, because reading geometry after every press is a forced
  // layout of the whole list and was the freeze itself. The mark moves at
  // once; only following it with the scroller waits for the next frame.
  function mark() {
    if (activeRowEl) activeRowEl.classList.remove("active");
    activeRowEl = null;
    const row = activeRow();
    if (!row) return;
    row.classList.add("active");
    activeRowEl = row;
    if (scrollRaf) return;
    scrollRaf = requestAnimationFrame(() => {
      scrollRaf = 0;
      const marked = activeRowEl;
      if (!marked || !marked.isConnected) return;
      const box = listEl.getBoundingClientRect();
      const rect = marked.getBoundingClientRect();
      if (rect.top < box.top) listEl.scrollTop -= box.top - rect.top;
      else if (rect.bottom > box.bottom) listEl.scrollTop += rect.bottom - box.bottom;
    });
  }

  // focusRow puts the real focus on the marked row, which is what makes the
  // keys what they look like: the focused element is the one they act on.
  // It reports whether there was a row to focus.
  function focusRow() {
    const row = activeRow();
    if (!row) return false;
    row.focus({ preventScroll: true });
    return true;
  }

  // enter is the field's arrow down: into the list at the mark, or at the
  // first row when nothing is marked yet.
  function enter() {
    if (activeKey && focusRow()) return;
    activeKey = "";
    move(1);
  }

  function move(step) {
    if (order.length === 0) return;
    const at = activeIndex();
    // The walk stands still at the bottom instead of wrapping: on a list of
    // thirty thousand changes the wrap from the first row to the last would
    // have to build every row in between in one keystroke, and a held key
    // froze the page for two seconds exactly that way. Up past the first
    // row steps back out into the field, the way it walked in.
    const next = at < 0 ? 0 : at + step;
    if (next < 0) {
      filterEl.focus();
      return;
    }
    if (next >= order.length) return;
    activeKey = order[next];
    // Walking into the batch that was not built appends it, so the arrows
    // never run into a wall the button would have to be clicked for, and
    // the rows that already stand are not built a second time. The order
    // carries one key per spec, so the walk's index is the spec's.
    if (next >= built) extend(next);
    mark();
    focusRow();
  }

  // Enter opens the marked file, which only a file has: on a folder row it
  // does nothing at all, no error, no state, the focus stands.
  async function openActive() {
    const fromList = listEl.contains(document.activeElement);
    // Enter in the field with nothing marked yet takes the first hit: type,
    // Enter, and the top match stands.
    if (!activeKey) {
      const at = specList.findIndex((spec) => spec.entry);
      if (at < 0) return;
      activeKey = order[at];
      mark();
    }
    if (!activeKey.startsWith("f:")) return;
    const entry = index.byPath.get(activeKey.slice(2));
    if (!entry) return;
    if (!canOpen(entry)) {
      status(`"${entry.path}" has nothing to compare.`);
      return;
    }
    await open(entry);
    if (!active()) return;
    if (fromList) focusRow();
    else filterEl.focus();
  }

  // Space is a click on the marked row's checkbox, nothing else: a file row
  // toggles its pick, a folder row its whole subtree, exactly what the
  // mouse gets there, the half checked state included, and like every
  // checkbox it stays where it is: stepping on is the arrows' business.
  function pickActive() {
    const input = activeInput();
    if (input && !input.disabled) input.click();
  }

  function setQuery(next) {
    if (next === query) return;
    query = next;
    buildIndex();
    // A changed filter is a changed list, so the walk starts at its top
    // again instead of keeping a mark somewhere in a list nobody is looking
    // at.
    activeKey = "";
    shown = COMMIT_ROWS_STEP;
    render();
    onFilter();
    scheduleState();
  }

  function persistState() {
    if (!stateKey) return;
    store.setJSON(stateKey, { filter: query, scroll: lastScroll });
  }

  function scheduleState() {
    if (!stateKey) return;
    clearTimeout(stateTimer);
    stateTimer = setTimeout(() => {
      stateTimer = 0;
      persistState();
    }, LAYOUT_SAVE_MS);
  }

  function restoreState() {
    if (!stateKey) return;
    if (!restored) {
      restored = true;
      const saved = store.getJSON(stateKey, null);
      const filter = saved && typeof saved.filter === "string" ? saved.filter.trim() : "";
      if (filter) {
        filterEl.value = filter;
        query = filter;
        buildIndex();
      }
      lastScroll = Math.max(0, Math.round(Number(saved && saved.scroll) || 0));
      if (lastScroll > 0) pendingScroll = lastScroll;
    }
    persistState();
  }

  function forgetState() {
    if (!stateKey) return;
    if (filterEl.value !== "" || query !== "") clear();
    clearTimeout(stateTimer);
    stateTimer = 0;
    lastScroll = 0;
    pendingScroll = null;
    store.remove(stateKey);
  }

  listEl.addEventListener("scroll", () => {
    if (listEl.clientHeight === 0) return;
    lastScroll = Math.round(listEl.scrollTop);
    scheduleState();
  }, { signal, passive: true });
  signal.addEventListener("abort", () => {
    if (!stateTimer) return;
    clearTimeout(stateTimer);
    stateTimer = 0;
    persistState();
  });

  function clear() {
    filterEl.value = "";
    setQuery("");
  }

  function syncFilter() {
    // The count and the clear button keep their place and only appear,
    // because they float over the field's padding: what they must never do
    // is change the width of the box somebody is typing in.
    const asked = query !== "";
    clearBtn.style.visibility = asked ? "" : "hidden";
    countEl.textContent = asked ? `${index.shown.length} of ${index.total}` : "";
  }

  filterEl.addEventListener("input", () => setQuery(filterEl.value.trim()), { signal });
  filterEl.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      enter();
    } else if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      void openActive();
    } else if (e.key === "Escape") {
      // A filter that stands is what Escape means here; the panel's own
      // Escape takes over once the field is empty, so one key leads out
      // step by step.
      if (filterEl.value === "") return;
      e.preventDefault();
      e.stopPropagation();
      clear();
    }
  }, { signal });
  // In the list the keys are the row's: the arrows move the focus itself,
  // so Space is the checkbox convention every browser taught and Enter is
  // the row's diff, and Escape steps back out into the field. The walk
  // moves the real focus so every key acts on the element that visibly
  // holds it.
  listEl.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      move(e.key === "ArrowDown" ? 1 : -1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      void openActive();
    } else if (e.key === " " && !(e.target instanceof HTMLInputElement)) {
      e.preventDefault();
      pickActive();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      filterEl.focus();
    }
  }, { signal });
  clearBtn.addEventListener("click", () => {
    clear();
    filterEl.focus();
  }, { signal });

  return {
    setChanges,
    get changes() { return changes; },
    get index() { return index; },
    get query() { return query; },
    picked,
    recountPicks,
    selectedPaths,
    prunePicks,
    setPick,
    render,
    extend,
    reset() {
      shown = COMMIT_ROWS_STEP;
    },
    restoreState,
    forgetState,
    focusRow,
    focusFilter() {
      filterEl.focus();
    },
    syncFilter,
    holdsFocus() {
      return document.activeElement === filterEl || listEl.contains(document.activeElement);
    },
    inList() {
      return listEl.contains(document.activeElement);
    },
  };
}
