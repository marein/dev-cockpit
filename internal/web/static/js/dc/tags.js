import * as store from "@dc/store";
import { matchesTokens } from "@dc/filter";

const WIDE = "(min-width: 992px)";

// tagFilter is the tag filter of one project list. Its bar ([data-tag-bar])
// holds a toggle per tag in use, a row's own tags toggle too, and the picks
// are remembered under the list's key, apart from every other list's like its
// text filter. A pick is a tag's id, so a rename keeps it. A row stays when
// it carries every picked tag as a [data-tag]. A pick whose tag is no longer
// in use counts for nothing, so a filter nobody can see never hides a row.
// The picks stand in the filter field too ([data-tag-picked]), each with a
// cross of its own. On a desktop the bar keeps one line, the tags that do not
// fit wait behind +N more, which lists every tag.
export function tagFilter(root, key, changed, signal) {
  const inUse = () => new Set(Array.from(root.querySelectorAll("[data-tag-bar] [data-tag-toggle]"), (toggle) => toggle.dataset.tagToggle));
  const picked = () => {
    const tags = inUse();
    const stored = store.getJSON(key, []);
    return Array.isArray(stored) ? stored.filter((tag) => typeof tag === "string" && tags.has(tag)) : [];
  };
  const toggle = (tag, on) => {
    const keys = picked();
    const has = keys.includes(tag);
    if (has === on) return;
    store.setJSON(key, on ? [...keys, tag] : keys.filter((each) => each !== tag));
    changed();
  };
  root.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest("[data-tag-toggle], [data-tag-unpick]") : null;
    if (!target || !root.contains(target)) return;
    event.preventDefault();
    if (target.dataset.tagUnpick !== undefined) toggle(target.dataset.tagUnpick, false);
    else toggle(target.dataset.tagToggle, !picked().includes(target.dataset.tagToggle));
  }, { signal });
  root.addEventListener("change", (event) => {
    const box = event.target instanceof Element ? event.target.closest("[data-tag-check]") : null;
    if (box && root.contains(box)) toggle(box.dataset.tagCheck, box.checked);
  }, { signal });
  root.addEventListener("input", (event) => {
    const field = event.target instanceof Element ? event.target.closest("[data-tag-menu-filter]") : null;
    if (field && root.contains(field)) filterMenu(field.closest("[data-tag-bar]"), field.value);
  }, { signal });
  root.addEventListener("shown.bs.dropdown", (event) => {
    const bar = event.target instanceof Element ? event.target.closest("[data-tag-bar]") : null;
    const field = bar?.querySelector("[data-tag-menu-filter]");
    if (!field) return;
    field.value = "";
    filterMenu(bar, "");
    if (window.matchMedia("(pointer: fine)").matches) field.focus();
  }, { signal });
  const wide = window.matchMedia(WIDE);
  const refit = () => root.querySelectorAll("[data-tag-bar]").forEach(fit);
  wide.addEventListener("change", refit, { signal });
  const resize = new ResizeObserver(refit);
  const observe = () => {
    resize.disconnect();
    root.querySelectorAll("[data-tag-bar]").forEach((bar) => resize.observe(bar));
  };
  observe();
  signal?.addEventListener("abort", () => resize.disconnect());
  return {
    picked,
    clear: () => store.setJSON(key, []),
    observe,
    // show marks what the filter leaves: the picked tags pressed in the bar,
    // on the rows and in the field, each bar tag with the number of projects
    // left that carry it, dimmed and dead at none, and the count of projects
    // left in the field while anything filters.
    show({ keys, counts, left, total, filtering }) {
      for (const each of root.querySelectorAll("[data-tag-toggle]")) {
        each.setAttribute("aria-pressed", keys.includes(each.dataset.tagToggle) ? "true" : "false");
      }
      for (const bar of root.querySelectorAll("[data-tag-bar]")) {
        for (const each of bar.querySelectorAll("[data-tag-toggle], [data-tag-menu-row]")) {
          const tag = each.dataset.tagToggle ?? each.dataset.tagMenuRow;
          const count = counts.get(tag) || 0;
          const dead = count === 0 && !keys.includes(tag);
          const number = each.querySelector("[data-tag-count]");
          if (number) {
            number.textContent = String(count);
            number.setAttribute("aria-label", `${count} ${count === 1 ? "project" : "projects"}`);
          }
          each.toggleAttribute("data-tag-none", dead);
          const box = each.querySelector("[data-tag-check]");
          if (box) {
            box.checked = keys.includes(tag);
            box.disabled = dead;
          } else {
            each.disabled = dead;
          }
        }
        fit(bar);
      }
      for (const holder of root.querySelectorAll("[data-tag-picked]")) {
        const template = holder.parentElement.querySelector("[data-tag-picked-template]");
        if (!template) continue;
        const chips = keys.map((tag) => {
          const source = root.querySelector(`[data-tag-bar] [data-tag-toggle="${CSS.escape(tag)}"]`);
          const name = source?.querySelector(".dc-tag-name")?.textContent || "";
          const chip = template.content.firstElementChild.cloneNode(true);
          for (const each of source?.classList || []) if (each.startsWith("dc-tag-")) chip.classList.add(each);
          chip.dataset.tagUnpick = tag;
          chip.querySelector(".dc-tag-name").textContent = name;
          chip.title = `Stop filtering by ${name}`;
          chip.setAttribute("aria-label", `Stop filtering by ${name}`);
          return chip;
        });
        holder.replaceChildren(...chips);
        holder.hidden = chips.length === 0;
      }
      for (const count of root.querySelectorAll("[data-filter-count]")) {
        count.textContent = `${left} of ${total}`;
        count.hidden = !filtering;
      }
    },
  };
}

// carries reports whether a row carries every tag in keys.
function carries(row, keys) {
  return keys.every((tag) => row.querySelector(`[data-tag="${CSS.escape(tag)}"]`));
}

// narrow runs a list's filter over its rows. A project is a main row with its
// worktrees, a worktree carries no tags and stands with its main: a row is
// left when it passes the text and its main carries every picked tag, and a
// project is left when any of its rows is. Each tag counts the projects left
// that carry it, which is what a pick of it would leave.
export function narrow(rows, { mainOf, matches, keys }) {
  const hits = new Set();
  const left = new Set();
  const projects = new Set();
  for (const row of rows) {
    const main = mainOf(row) || row;
    projects.add(main);
    if (matches(row) && carries(main, keys)) {
      hits.add(row);
      left.add(main);
    }
  }
  const counts = new Map();
  for (const main of left) {
    for (const tag of new Set(Array.from(main.querySelectorAll("[data-tag]"), (node) => node.dataset.tag))) {
      counts.set(tag, (counts.get(tag) || 0) + 1);
    }
  }
  return { hits, counts, left: left.size, total: projects.size };
}

// fit keeps a desktop bar on one line: the tags that fit stand, the rest wait
// behind +N more. A picked tag counts no wider than one that is not, so a
// pick moves nothing. On a phone the line scrolls instead.
function fit(bar) {
  const line = bar.querySelector("[data-tag-bar-tags]");
  const more = bar.querySelector("[data-tag-more]");
  if (!line || !more) return;
  const chips = Array.from(line.querySelectorAll("[data-tag-toggle]"));
  chips.forEach((chip) => { chip.hidden = false; });
  more.hidden = true;
  if (!window.matchMedia(WIDE).matches || bar.hidden || line.scrollWidth <= line.clientWidth) return;
  const widths = chips.map((chip) => chip.getBoundingClientRect().width);
  more.hidden = false;
  const gap = parseFloat(getComputedStyle(line).columnGap) || 0;
  const label = more.querySelector("[data-tag-more-label]");
  let room = line.clientWidth;
  let used = 0;
  let shown = 0;
  while (shown < chips.length && used + (shown ? gap : 0) + widths[shown] <= room) {
    used += (shown ? gap : 0) + widths[shown];
    shown += 1;
  }
  chips.slice(shown).forEach((chip) => { chip.hidden = true; });
  if (label) label.textContent = `+${chips.length - shown} more`;
  room = line.clientWidth;
  while (shown > 0 && used > room) {
    shown -= 1;
    used -= widths[shown] + (shown ? gap : 0);
    chips[shown].hidden = true;
    if (label) label.textContent = `+${chips.length - shown} more`;
  }
}

function filterMenu(bar, query) {
  if (!bar) return;
  let shown = 0;
  for (const row of bar.querySelectorAll("[data-tag-menu-row]")) {
    row.hidden = query.trim() !== "" && !matchesTokens(row.dataset.tagMenuName || "", query);
    if (!row.hidden) shown += 1;
  }
  const none = bar.querySelector("[data-tag-menu-none]");
  if (none) none.hidden = shown > 0;
}

// swapBar puts a fresh render's bar in place of the standing one when the
// tags in use changed, and leaves it alone otherwise, so a refresh does not
// replace the buttons under a finger for nothing. The counts a filter
// narrowed are no change, the totals are.
export function swapBar(bar, fresh) {
  const shape = (node) => `${node.hidden}|${Array.from(node.querySelectorAll("[data-tag-bar-tags] [data-tag-toggle]"), (toggle) => `${toggle.className}:${toggle.dataset.tagToggle}:${toggle.dataset.tagTotal}:${toggle.querySelector(".dc-tag-name")?.textContent}`).join("|")}`;
  if (!bar || !fresh || shape(bar) === shape(fresh)) return false;
  bar.replaceWith(fresh);
  return true;
}
