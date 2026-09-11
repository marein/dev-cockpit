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
const CARD = { selector: "[data-project-name]", name: "projectName", active: "projectActive", used: "projectUsed", worktreeOf: "projectWorktreeOf" };
export const INDEX = { selector: "[data-index-project]", name: "indexProject", active: "indexActive", used: "indexUsed", worktreeOf: "indexWorktreeOf" };

export function mode() {
  const stored = get(KEY, "");
  return MODES.indexOf(stored) >= 0 ? stored : "alpha";
}

function keyOf(node, f = CARD) {
  return {
    name: node.dataset[f.name].toLowerCase(),
    active: node.dataset[f.active] === "true",
    used: Number(node.dataset[f.used]) || 0,
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

export function mainOf(node, byName, f = CARD) {
  const of = node.dataset[f.worktreeOf];
  if (!of || of === node.dataset[f.name]) return null;
  const main = byName.get(of);
  if (!main || main.dataset[f.worktreeOf]) return null;
  return main;
}

function groupKey(main, members, f = CARD) {
  const key = keyOf(main, f);
  members.forEach((node) => {
    const k = keyOf(node, f);
    key.active = key.active || k.active;
    key.used = Math.max(key.used, k.used);
  });
  return key;
}

// Sort the [data-project-name] children of `container` in place by `m`
// (defaults to the stored mode), then re-append them in order.
export function sort(container, m, f = CARD) {
  const current = m || mode();
  const items = Array.from(container.querySelectorAll(f.selector));
  const byName = new Map(items.map((node) => [node.dataset[f.name], node]));
  const members = new Map();
  const tops = [];
  items.forEach((node) => {
    const main = mainOf(node, byName, f);
    if (!main) {
      tops.push(node);
      return;
    }
    if (!members.has(main)) members.set(main, []);
    members.get(main).push(node);
  });
  const keys = new Map(tops.map((node) => [node, groupKey(node, members.get(node) || [], f)]));
  tops.sort((a, b) => compareKeys(current, keys.get(a), keys.get(b)));
  const own = (a, b) => compareKeys(current, keyOf(a, f), keyOf(b, f));
  tops.forEach((node) => {
    container.appendChild(node);
    (members.get(node) || []).sort(own).forEach((member) => container.appendChild(member));
  });
}
