import { wireRowMenus } from "@dc/contextmenu";

const GRID_LINES = 3;

// costScale and money mirror costScale and Money in
// internal/web/render, so a scaled axis reads like the server's.
function costScale(peak) {
  if (!(peak > 0)) peak = 1;
  peak = Math.min(peak, Number.MAX_VALUE / 2);
  const step = niceStep(peak / GRID_LINES);
  if (!(step > 0)) return costScale(0);
  const top = Math.ceil(peak / step) * step;
  const grid = [];
  for (let i = 1; i <= GRID_LINES; i++) {
    const v = i * step;
    if (v > top + step / 2) break;
    grid.push(v);
  }
  return { top, grid };
}

function niceStep(raw) {
  const exp = 10 ** Math.floor(Math.log10(raw));
  for (const m of [1, 2, 2.5, 5, 10]) {
    if (raw <= m * exp) return m * exp;
  }
  return 10 * exp;
}

function money(v) {
  if (v <= 0) return "$0.00";
  if (v < 0.005) return "<$0.01";
  if (v < 1000) return `$${v.toFixed(2)}`;
  return `$${Math.round(v).toLocaleString("en-US")}`;
}

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
    tip.querySelector(pinned ? "[data-cost-tiphint]" : "[data-cost-tipdrill]")?.remove();
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
    for (const chart of scope.querySelectorAll("[data-cost-chart]")) paintChart(chart);
    for (const row of scope.querySelectorAll("[data-cost-tip] [data-series]")) row.classList.toggle("off", hidden.has(row.dataset.series));
  }

  // paintChart stacks the shown series in the legend's order and scales the
  // plot to them. A bar folds what is too thin to show its color into one
  // Other, so it needs the plot laid out: unmeasured, nothing folds. With
  // every series shown the scale is the server's, a sum here may land a
  // hair above a nice step and double the axis.
  function paintChart(chart) {
    const frame = chart.querySelector("[data-cost-frame]");
    if (!frame) return;
    const keys = [...chart.querySelectorAll("[data-cost-legend] [data-series]")].map((key) => key.dataset.series);
    const shown = keys.filter((series) => !hidden.has(series));
    const value = (seg) => parseFloat(seg.dataset.v) || 0;
    const stacks = [...chart.querySelectorAll(".dc-cost-stack")].map((stack) => {
      const segs = [...stack.querySelectorAll(".dc-cost-seg[data-series]")];
      const visible = segs.filter((seg) => !hidden.has(seg.dataset.series));
      return { stack, segs, visible, sum: visible.reduce((sum, seg) => sum + value(seg), 0) };
    });
    const peak = Math.max(0, ...stacks.map((s) => s.sum));
    const plot = frame.querySelector(".dc-cost-plot");
    const lines = [...plot.querySelectorAll(".dc-cost-grid:not([data-cost-scaled])")];
    const scaled = [...plot.querySelectorAll(".dc-cost-grid[data-cost-scaled]")];
    let top = parseFloat(frame.dataset.top) || 1;
    if (shown.length === keys.length || !lines.length) {
      for (const line of scaled) line.remove();
      for (const line of lines) line.hidden = false;
    } else {
      const scale = costScale(peak);
      top = scale.top;
      for (const line of lines) line.hidden = true;
      scale.grid.forEach((v, i) => {
        let line = scaled[i];
        if (!line) {
          line = plot.appendChild(lines[0].cloneNode(true));
          line.dataset.costScaled = "";
        }
        line.hidden = false;
        line.style.setProperty("--b", (v / top * 100).toFixed(2));
        line.querySelector("span").textContent = money(v);
      });
      for (const line of scaled.slice(scale.grid.length)) line.remove();
    }
    const any = chart.querySelector("[data-cost-other]");
    const least = any ? parseFloat(getComputedStyle(any).minHeight) || 0 : 0;
    const px = frame.clientHeight / top;
    for (const { stack, segs, visible, sum } of stacks) {
      const thin = visible.filter((seg) => value(seg) * px < least);
      const folded = new Set(thin.length > 1 ? thin : []);
      let rest = 0;
      for (const seg of segs) seg.hidden = hidden.has(seg.dataset.series) || folded.has(seg);
      for (const seg of visible) {
        if (folded.has(seg)) rest += value(seg);
        else seg.style.flexGrow = String(value(seg) / sum);
      }
      const other = stack.querySelector("[data-cost-other]");
      if (other) {
        other.dataset.v = String(rest);
        other.style.flexGrow = String(rest / sum);
        other.hidden = !(rest > 0);
      }
      stack.style.setProperty("--h", String(Math.min(sum / top, 1) * 100));
    }
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

  paint(root);

  return {
    restore(state) {
      paint(root);
      hot = null;
      if (!state) return;
      const col = root.querySelector(`[data-cost-col="${CSS.escape(state.key)}"]`);
      if (col) show(col, state.pinned);
    },
    state: () => hot && { ...hot },
  };
}
