import * as store from "@dc/store";

const PREFIX = "dc-assistant-aside:";
// A place leaves with its assistant (keepPlaces, forgetPlace). The cap holds
// a device that never sees the whole list, the places looked at longest ago go
// first.
const KEPT = 50;
// A scroll is written once it rests, at the pace of the list column's own
// (keepCtxScroll in @dc/ctx).
const REST_MS = 150;
// The aside waits invisible while it lands, at most as long as its style
// alone lets it wait (assistant-aside.css).
const WAIT_MS = 2000;
const LANDING = "dc-aside-landing";

const places = new WeakMap();
const landed = new WeakSet();

// assistantPlace keeps where the reader left the aside of an assistant, per
// assistant and per device: the open tab, the project open in Projects with
// its section, its unfolded lists and the scroll its cards had, the scroll,
// the unfolded rows and the older projects. Each list of the aside calls it
// whenever it connects. The first time a list puts its own part back: its
// rows unfold and its tab opens at once, what has to be pulled first (the
// project, the older projects) comes while the aside's body waits invisible,
// then the scroll, so the place shows as a whole. A part that is gone falls
// back quietly, the write that ends the landing drops it and dates the place,
// which is what keeps it among the kept. A list that connects again moved
// with its aside into a fresh surface, and a moved box loses its scroll, the
// open tab's list gives it back. restore lends the list's pulls: older(), and
// project(saved), which answers whether the project stands.
export function assistantPlace(list, restore) {
  const aside = list.closest("[data-assistant-aside]");
  const id = aside?.dataset.assistantAside;
  if (!id) {
    list.closest(".dc-assistant-aside-body")?.classList.remove(LANDING);
    return null;
  }
  let place = places.get(aside);
  if (!place) {
    const saved = recall(id);
    place = { aside, id, saved, top: saved?.scroll || 0, landing: 0, timer: 0, wait: 0, shown: false };
    places.set(aside, place);
    if (saved) place.wait = window.setTimeout(() => show(place), WAIT_MS);
    else show(place);
  }
  wire(place, list);
  if (!landed.has(list)) {
    landed.add(list);
    land(place, list, restore);
  } else if (list.classList.contains("active") && !place.landing) {
    const scroller = list.scroller();
    if (scroller.getClientRects().length) scroller.scrollTop = place.top;
  }
  return { save: () => save(place) };
}

function wire(place, list) {
  const { signal } = list.ac;
  place.aside.addEventListener("shown.bs.tab", (event) => {
    if (event.target.getAttribute("aria-controls") === list.id || list.contains(event.target)) save(place);
  }, { signal });
  list.addEventListener("shown.bs.collapse", () => save(place), { signal });
  list.addEventListener("hidden.bs.collapse", () => save(place), { signal });
  const scroller = list.scroller();
  scroller.addEventListener("scroll", () => {
    if (!list.classList.contains("active")) return;
    place.top = Math.round(scroller.scrollTop);
    later(place);
  }, { passive: true, signal });
}

function later(place) {
  if (!place.timer) place.timer = window.setTimeout(() => save(place), REST_MS);
}

function land(place, list, restore) {
  const { saved } = place;
  if (!saved || place.shown) return;
  place.landing += 1;
  const pane = list.getAttribute("pane");
  unfold(list, saved.folds);
  const shows = saved.tab === pane;
  if (shows && !list.classList.contains("active")) {
    const tab = place.aside.querySelector(`[data-bs-toggle="tab"][aria-controls="${CSS.escape(list.id)}"]`);
    if (tab) window.bootstrap?.Tab.getOrCreateInstance(tab).show();
  }
  let top = saved.scroll;
  const pulls = [];
  if (saved.older.includes(pane)) pulls.push(restore.older());
  if (pane === "projects" && saved.project) {
    pulls.push(restore.project({ ...saved.project, folds: saved.folds }).then((stands) => {
      if (!stands) top = saved.project.list;
    }));
  }
  const settle = () => {
    if (place.shown) return;
    if (shows && list.isConnected && list.classList.contains("active")) list.scroller().scrollTop = top;
    place.landing -= 1;
    later(place);
    if (!place.landing && lists(place).every((each) => landed.has(each))) show(place);
  };
  if (pulls.length) void Promise.allSettled(pulls).then(settle);
  else settle();
}

// show ends the landing, once every list put its part back or after WAIT_MS.
// What is still out then comes as it arrives without the scroll it was left
// at, the aside is the reader's from here.
function show(place) {
  window.clearTimeout(place.wait);
  place.shown = true;
  place.landing = 0;
  place.aside.querySelector(".dc-assistant-aside-body")?.classList.remove(LANDING);
}

function lists(place) {
  return [...place.aside.querySelectorAll("dc-assistant-list[pane]")];
}

// unfold opens the folds of ids that stand in root, the way a reader's tap
// leaves them: the class Bootstrap reads and the state of the control. The
// rests of the Changes lists are the view's own to unfold, it pulls them.
export function unfold(root, ids) {
  for (const id of ids) {
    const fold = root.querySelector(`#${CSS.escape(id)}[data-assistant-fold]:not([data-assistant-rest])`);
    if (!fold) continue;
    fold.classList.add("show");
    root.querySelector(`[aria-controls="${CSS.escape(id)}"]`)?.setAttribute("aria-expanded", "true");
  }
}

// save writes the place as the aside shows it, while no list still puts its
// own part back. A scroller off the screen (the editor's tab waiting for its
// sheet) has no position to tell and keeps the last one.
function save(place) {
  window.clearTimeout(place.timer);
  place.timer = 0;
  if (place.landing) return;
  const { aside } = place;
  const all = lists(place);
  const active = all.find((list) => list.classList.contains("active"));
  const scroller = active?.scroller?.();
  if (scroller?.isConnected && scroller.getClientRects().length) place.top = Math.round(scroller.scrollTop);
  const view = aside.querySelector('dc-assistant-list[pane="projects"] > [data-assistant-project-view]');
  const opened = view && !view.hidden && view.dataset.project;
  const key = PREFIX + place.id;
  const first = store.get(key, null) === null;
  store.setJSON(key, {
    tab: active?.getAttribute("pane") || "",
    scroll: place.top,
    folds: [...aside.querySelectorAll("[data-assistant-fold][id]:not([data-assistant-rest])")]
      .filter((fold) => fold.classList.contains("show") || aside.querySelector(`[aria-controls="${CSS.escape(fold.id)}"]`)?.getAttribute("aria-expanded") === "true")
      .map((fold) => fold.id),
    older: all.filter(foldsBack).map((list) => list.getAttribute("pane")),
    project: opened ? { name: view.dataset.project, section: view.dataset.section || "", more: view.dataset.more || "", list: Number(view.dataset.listScroll) || 0 } : null,
    at: Date.now(),
  });
  if (first) prune();
}

// foldsBack says a list shows its older rows behind a control that folds them
// again. One that only shows them stays as it is, a place that unfolded it
// would leave the reader no way back.
function foldsBack(list) {
  const all = new URL(list.getAttribute("list-url") || "", window.location.href).searchParams.has("all");
  return all && !!list.querySelector("[data-assistant-older][aria-expanded]");
}

// keepPlaces drops the place of every assistant whose id is not in living.
// Only a list of every assistant that lives may judge, one that misses an
// assistant would take its place.
export function keepPlaces(living) {
  const ids = new Set(living);
  for (const key of store.keys(PREFIX)) {
    if (!ids.has(key.slice(PREFIX.length))) store.remove(key);
  }
}

export function forgetPlace(id) {
  store.remove(PREFIX + id);
}

function prune() {
  const kept = store.keys(PREFIX).map((key) => ({ key, at: Number(store.getJSON(key, null)?.at) || 0 }));
  kept.sort((a, b) => b.at - a.at);
  for (const { key } of kept.slice(KEPT)) store.remove(key);
}

// recall reads a place back as only the cockpit writes it, whatever the
// storage holds.
function recall(id) {
  const saved = store.getJSON(PREFIX + id, null);
  if (!saved || typeof saved !== "object") return null;
  const text = (value) => (typeof value === "string" ? value : "");
  const texts = (value) => (Array.isArray(value) ? value.filter((each) => typeof each === "string" && each) : []);
  const offset = (value) => (Number.isFinite(value) && value > 0 ? value : 0);
  const project = saved.project && typeof saved.project === "object" && text(saved.project.name) ? saved.project : null;
  return {
    tab: text(saved.tab),
    scroll: offset(saved.scroll),
    folds: texts(saved.folds),
    older: texts(saved.older),
    project: project && { name: project.name, section: text(project.section), more: text(project.more), list: offset(project.list) },
  };
}
