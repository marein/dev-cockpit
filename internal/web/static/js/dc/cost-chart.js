import { wireRowMenus } from "@dc/contextmenu";

export function createChart(root, signal) {
  const hidden = new Set();
  let hot = null;
  let pointer = "mouse";

  const column = (target) => target.closest?.("[data-cost-col]");

  function show(col, pinned) {
    hide();
    const box = col.closest("[data-cost-frame]");
    const tip = box?.querySelector("[data-cost-tip]");
    const body = root.querySelector(`template[data-cost-tipbody="${CSS.escape(col.dataset.costCol)}"]`);
    if (!tip || !body) return;
    tip.replaceChildren(body.content.cloneNode(true));
    for (const row of tip.querySelectorAll("[data-series]")) row.classList.toggle("off", hidden.has(row.dataset.series));
    tip.classList.toggle("pinned", pinned);
    tip.hidden = false;
    col.classList.add("dc-cost-hot");
    hot = { key: col.dataset.costCol, pinned };
    place(box, col, tip);
  }

  function place(box, col, tip) {
    const f = box.getBoundingClientRect();
    const c = col.getBoundingClientRect();
    const width = tip.offsetWidth;
    let left = c.right - f.left + 8;
    if (left + width > f.width) left = c.left - f.left - 8 - width;
    const min = root.getBoundingClientRect().left - f.left;
    tip.style.left = `${Math.max(min, Math.min(left, f.width - width))}px`;
  }

  function hide() {
    for (const tip of root.querySelectorAll("[data-cost-tip]")) tip.hidden = true;
    for (const col of root.querySelectorAll(".dc-cost-hot")) col.classList.remove("dc-cost-hot");
    hot = null;
  }

  const keysOf = (legend) => [...legend.querySelectorAll("[data-series]")].map((key) => key.dataset.series);

  function toggle(key) {
    if (hidden.has(key.dataset.series)) hidden.delete(key.dataset.series);
    else hidden.add(key.dataset.series);
    paint(root);
  }

  function isolate(key) {
    const own = key.dataset.series;
    const keys = keysOf(key.closest("[data-cost-legend]"));
    const alone = !hidden.has(own) && keys.every((series) => series === own || hidden.has(series));
    for (const series of keys) {
      if (alone || series === own) hidden.delete(series);
      else hidden.add(series);
    }
    paint(root);
  }

  function paint(scope) {
    for (const key of scope.querySelectorAll("[data-cost-legend] [data-series]")) {
      key.setAttribute("aria-pressed", String(!hidden.has(key.dataset.series)));
    }
    for (const stack of scope.querySelectorAll(".dc-cost-stack")) {
      const top = parseFloat(stack.closest("[data-cost-frame]").dataset.top) || 1;
      let sum = 0;
      for (const seg of stack.children) {
        seg.hidden = hidden.has(seg.dataset.series);
        if (!seg.hidden) sum += parseFloat(seg.dataset.v) || 0;
      }
      stack.style.setProperty("--h", String(sum / top * 100));
    }
    for (const row of scope.querySelectorAll("[data-cost-tip] [data-series]")) row.classList.toggle("off", hidden.has(row.dataset.series));
  }

  root.addEventListener("pointerdown", (event) => { pointer = event.pointerType; }, { signal });
  root.addEventListener("pointerover", (event) => {
    if (event.pointerType !== "mouse" || hot?.pinned) return;
    const col = column(event.target);
    if (col && root.contains(col)) show(col, false);
  }, { signal });
  root.addEventListener("pointerout", (event) => {
    if (event.pointerType !== "mouse" || hot?.pinned || !column(event.target)) return;
    if (!event.relatedTarget || !column(event.relatedTarget)) hide();
  }, { signal });
  root.addEventListener("focusin", (event) => {
    const col = column(event.target);
    if (col) show(col, false);
  }, { signal });
  root.addEventListener("focusout", (event) => {
    if (column(event.target) && !column(event.relatedTarget || document.body) && !hot?.pinned) hide();
  }, { signal });
  root.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && hot) hide();
  }, { signal });
  root.addEventListener("click", (event) => {
    const key = event.target.closest("[data-cost-legend] [data-series]");
    if (key) {
      if (event.metaKey || event.ctrlKey || event.shiftKey) toggle(key);
      else isolate(key);
      return;
    }
    const col = column(event.target);
    if (!col) {
      if (hot?.pinned && !event.target.closest("[data-cost-tip]")) hide();
      return;
    }
    const touch = event.detail !== 0 && (pointer === "touch" || pointer === "pen");
    if (touch) {
      if (hot?.pinned && hot.key === col.dataset.costCol) hide();
      else show(col, true);
      return;
    }
    if (col.dataset.drill) window.pe.navigate(col.dataset.drill);
  }, { signal });
  wireRowMenus(root, "[data-cost-legend] [data-series]", (key) => {
    if (!key) return false;
    toggle(key);
    return true;
  }, { signal });
  document.addEventListener("click", (event) => {
    if (hot?.pinned && !root.contains(event.target)) hide();
  }, { signal });

  return {
    prepare: (fresh) => paint(fresh),
    restore(state) {
      hot = null;
      if (!state) return;
      const col = root.querySelector(`[data-cost-col="${CSS.escape(state.key)}"]`);
      if (col) show(col, state.pinned);
    },
    state: () => hot && { ...hot },
  };
}
