// Shared project sort, used by the projects list page and the quick nav project
// browser. Both sort their own DOM container but agree on the order through this
// one comparator and the shared "dc-project-sort" key. Sortable elements carry
// data-project-name, and for the non-alpha modes data-project-active
// ("true"/"false") and data-project-used (a unix timestamp).
//
// Modes: "alpha" by name; "active" puts projects with a running session, shell
// or container first (then alphabetical); "recent" by last opened, most recent first.
import { get } from "@dc/store";

export const KEY = "dc-project-sort";
export const MODES = ["alpha", "active", "recent"];

export function mode() {
  const stored = get(KEY, "");
  return MODES.indexOf(stored) >= 0 ? stored : "alpha";
}

function keyOf(node) {
  return {
    name: node.dataset.projectName.toLowerCase(),
    active: node.dataset.projectActive === "true",
    used: Number(node.dataset.projectUsed) || 0,
  };
}

function compareKeys(m, a, b) {
  if (m === "active" && a.active !== b.active) return a.active ? -1 : 1;
  if (m === "recent" && a.used !== b.used) return b.used - a.used;
  return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
}

export function comparator(m) {
  return function (a, b) {
    return compareKeys(m, keyOf(a), keyOf(b));
  };
}

export function mainOf(node, byName) {
  const of = node.dataset.projectWorktreeOf;
  if (!of || of === node.dataset.projectName) return null;
  const main = byName.get(of);
  if (!main || main.dataset.projectWorktreeOf) return null;
  return main;
}

function groupKey(main, members) {
  const key = keyOf(main);
  members.forEach((node) => {
    const k = keyOf(node);
    key.active = key.active || k.active;
    key.used = Math.max(key.used, k.used);
  });
  return key;
}

// Sort the [data-project-name] children of `container` in place by `m`
// (defaults to the stored mode), then re-append them in order.
export function sort(container, m) {
  const current = m || mode();
  const items = Array.from(container.querySelectorAll("[data-project-name]"));
  const byName = new Map(items.map((node) => [node.dataset.projectName, node]));
  const members = new Map();
  const tops = [];
  items.forEach((node) => {
    const main = mainOf(node, byName);
    if (!main) {
      tops.push(node);
      return;
    }
    if (!members.has(main)) members.set(main, []);
    members.get(main).push(node);
  });
  const keys = new Map(tops.map((node) => [node, groupKey(node, members.get(node) || [])]));
  tops.sort((a, b) => compareKeys(current, keys.get(a), keys.get(b)));
  const own = comparator(current);
  tops.forEach((node) => {
    container.appendChild(node);
    (members.get(node) || []).sort(own).forEach((member) => container.appendChild(member));
  });
}
