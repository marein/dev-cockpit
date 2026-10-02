export function createChart(root, signal) {
  const hidden = new Set();
  let hot = null;
  let pointer = "mouse";

  const frame = () => root.querySelector("[data-cost-frame]");
  const column = (target) => target.closest?.("[data-cost-col]");

  function show(col, pinned) {
    const box = frame();
    const tip = box?.querySelector("[data-cost-tip]");
    const body = root.querySelector(`template[data-cost-tipbody="${CSS.escape(col.dataset.costCol)}"]`);
    if (!tip || !body) return;
    tip.replaceChildren(body.content.cloneNode(true));
    for (const row of tip.querySelectorAll("[data-series]")) row.classList.toggle("off", hidden.has(row.dataset.series));
    tip.classList.toggle("pinned", pinned);
    tip.hidden = false;
    for (const other of box.querySelectorAll(".dc-cost-hot")) other.classList.remove("dc-cost-hot");
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
    const box = frame();
    box?.querySelector("[data-cost-tip]")?.setAttribute("hidden", "");
    for (const col of box?.querySelectorAll(".dc-cost-hot") || []) col.classList.remove("dc-cost-hot");
    hot = null;
  }

  function paint(scope) {
    for (const key of scope.querySelectorAll("[data-cost-legend] [data-series]")) {
      key.setAttribute("aria-pressed", String(!hidden.has(key.dataset.series)));
    }
    const top = parseFloat(scope.querySelector("[data-cost-frame]")?.dataset.top) || 1;
    for (const stack of scope.querySelectorAll(".dc-cost-stack")) {
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
      if (hidden.has(key.dataset.series)) hidden.delete(key.dataset.series);
      else hidden.add(key.dataset.series);
      paint(root);
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
  document.addEventListener("click", (event) => {
    if (hot?.pinned && !root.contains(event.target)) hide();
  }, { signal });

  return {
    prepare: (fresh) => paint(fresh),
    restore(state) {
      if (!state) return;
      const col = root.querySelector(`[data-cost-col="${CSS.escape(state.key)}"]`);
      if (col) show(col, state.pinned);
      else hot = null;
    },
    state: () => hot && { ...hot },
  };
}
