// The stored form numbers the leaves in reading order instead of naming them,
// so a stored layout never carries an id that has to match anything.

const MIN_WIDTH = 160;
const MIN_HEIGHT = 96;
const SHARE = "--editor-group-share";
const EDGE = 0.25;
const MAX_DEPTH = 16;

export const leaf = (id) => ({ id });

export function mapLeaves(node, fn) {
  if (!node.children) return leaf(fn(node.id));
  return { dir: node.dir, sizes: [...node.sizes], children: node.children.map((c) => mapLeaves(c, fn)) };
}

export function leafIds(node, out = []) {
  if (!node.children) out.push(node.id);
  else for (const child of node.children) leafIds(child, out);
  return out;
}

function locate(node, id, parent = null, index = -1) {
  if (!node.children) return node.id === id ? { node, parent, index } : null;
  for (let i = 0; i < node.children.length; i++) {
    const found = locate(node.children[i], id, node, i);
    if (found) return found;
  }
  return null;
}

function normalize(node) {
  if (!node.children) return node;
  const children = [];
  const sizes = [];
  node.children.forEach((child, i) => {
    const flat = normalize(child);
    const share = node.sizes[i];
    if (flat.children && flat.dir === node.dir) {
      flat.children.forEach((inner, j) => {
        children.push(inner);
        sizes.push(share * flat.sizes[j]);
      });
    } else {
      children.push(flat);
      sizes.push(share);
    }
  });
  if (children.length === 1) return children[0];
  const sum = sizes.reduce((a, b) => a + b, 0) || 1;
  return { dir: node.dir, children, sizes: sizes.map((s) => s / sum) };
}

// A parent already split the same way takes the new leaf in, so three groups
// side by side stay one row instead of nesting.
export function splitAt(root, targetId, side, newId) {
  const dir = side === "left" || side === "right" ? "row" : "column";
  const after = side === "right" || side === "down";
  const found = locate(root, targetId);
  if (!found) return root;
  const fresh = leaf(newId);
  const { parent, index } = found;
  if (parent && parent.dir === dir) {
    const half = parent.sizes[index] / 2;
    parent.sizes[index] = half;
    const at = index + (after ? 1 : 0);
    parent.children.splice(at, 0, fresh);
    parent.sizes.splice(at, 0, half);
    return root;
  }
  const split = { dir, sizes: [0.5, 0.5], children: after ? [found.node, fresh] : [fresh, found.node] };
  if (!parent) return split;
  parent.children[index] = split;
  return root;
}

function edgeLeaf(node, dir, last) {
  if (!node.children) return node.id;
  const pick = node.dir === dir && last ? node.children[node.children.length - 1] : node.children[0];
  return edgeLeaf(pick, dir, last);
}

export function removeAt(root, id) {
  const found = locate(root, id);
  if (!found || !found.parent) return { root, heir: null };
  const { parent, index } = found;
  const share = parent.sizes[index];
  parent.children.splice(index, 1);
  parent.sizes.splice(index, 1);
  const next = index > 0 ? index - 1 : 0;
  parent.sizes[next] += share;
  const heir = edgeLeaf(parent.children[next], parent.dir, index > 0);
  return { root: normalize(root), heir };
}

// The leaves as they stand on the screen: by their top edge first, a row
// from the left. Worked out from the shares, a hidden group has a place too.
export function screenOrder(root) {
  const boxes = [];
  const walk = (node, x, y, w, h) => {
    if (!node.children) {
      boxes.push({ id: node.id, x, y });
      return;
    }
    let at = 0;
    node.children.forEach((child, i) => {
      const share = node.sizes[i];
      if (node.dir === "row") walk(child, x + at * w, y, w * share, h);
      else walk(child, x, y + at * h, w, h * share);
      at += share;
    });
  };
  walk(root, 0, 0, 1, 1);
  return boxes.sort((a, b) => (Math.abs(a.y - b.y) > 1e-6 ? a.y - b.y : a.x - b.x)).map((box) => box.id);
}

export function cornerLeaf(node, corner) {
  if (!node.children) return node.id;
  const pick = node.dir === "row" && corner === "end" ? node.children[node.children.length - 1] : node.children[0];
  return cornerLeaf(pick, corner);
}

const round = (n) => Math.round(n * 10000) / 10000;

export function encodeLayout(node, indexOf) {
  if (!node.children) return indexOf(node.id);
  return { split: node.dir, sizes: node.sizes.map(round), children: node.children.map((c) => encodeLayout(c, indexOf)) };
}

function decodeLayout(value, count) {
  const seen = new Set();
  const walk = (v, depth) => {
    if (depth > MAX_DEPTH) return null;
    if (Number.isInteger(v)) {
      if (v < 0 || v >= count || seen.has(v)) return null;
      seen.add(v);
      return leaf(v);
    }
    if (!v || typeof v !== "object" || (v.split !== "row" && v.split !== "column")) return null;
    if (!Array.isArray(v.children) || v.children.length < 2) return null;
    const children = v.children.map((c) => walk(c, depth + 1));
    if (children.some((c) => !c)) return null;
    const valid = Array.isArray(v.sizes) && v.sizes.length === children.length
      && v.sizes.every((s) => typeof s === "number" && Number.isFinite(s) && s > 0);
    return { dir: v.split, children, sizes: valid ? [...v.sizes] : children.map(() => 1) };
  };
  const root = walk(value, 0);
  if (!root || seen.size !== count) return null;
  const order = leafIds(root);
  if (order.some((id, i) => id !== i)) return null;
  return normalize(root);
}

// A set without groups or with a broken layout is one group of everything,
// and an entry no group names joins the first one, so nothing stored is lost.
export function readGroups(saved, count) {
  const specs = Array.isArray(saved.groups) ? saved.groups : [];
  const tree = specs.length ? decodeLayout(saved.layout, specs.length) : null;
  if (!tree) return { specs: [{ members: [...Array(count).keys()], active: null }], tree: null, focus: 0 };
  const taken = new Set();
  const groups = specs.map((spec) => {
    const listed = spec && Array.isArray(spec.tabs) ? spec.tabs : [];
    const members = listed.filter((i) => Number.isInteger(i) && i >= 0 && i < count && !taken.has(i));
    for (const i of members) taken.add(i);
    return { members, active: spec && Number.isInteger(spec.active) ? spec.active : null };
  });
  for (let i = 0; i < count; i++) if (!taken.has(i)) groups[0].members.push(i);
  const focus = Number.isInteger(saved.focus) && saved.focus >= 0 && saved.focus < groups.length ? saved.focus : 0;
  return { specs: groups, tree, focus };
}

function minSize(node, dir) {
  if (!node.children) return dir === "row" ? MIN_WIDTH : MIN_HEIGHT;
  const sizes = node.children.map((c) => minSize(c, dir));
  return node.dir === dir ? sizes.reduce((a, b) => a + b, 0) : Math.max(...sizes);
}

// paint moves the group elements into the new boxes instead of building them,
// a rebuilt element would take its editor's state with it.
export class GroupLayout {
  constructor(host, { onResize, signal }) {
    this.host = host;
    this.onResize = onResize;
    this.splits = new WeakMap();
    let drag = null;
    const end = () => {
      if (!drag) return;
      drag.handle.classList.remove("active");
      drag = null;
      this.onResize();
    };
    host.addEventListener("mousedown", (e) => {
      if (e.target instanceof Element && e.target.closest(".editor-split-handle")) e.preventDefault();
    }, { signal });
    host.addEventListener("pointerdown", (e) => {
      const handle = e.button === 0 && e.target instanceof Element ? e.target.closest(".editor-split-handle") : null;
      const pair = handle && this.pairAt(handle);
      if (!pair) return;
      const row = pair.node.dir === "row";
      const a = pair.a.getBoundingClientRect();
      const b = pair.b.getBoundingClientRect();
      const thick = row ? handle.offsetWidth : handle.offsetHeight;
      drag = {
        ...pair,
        handle,
        row,
        thick,
        start: row ? a.left : a.top,
        room: (row ? b.right - a.left : b.bottom - a.top) - thick,
      };
      handle.classList.add("active");
      handle.setPointerCapture(e.pointerId);
    }, { signal });
    host.addEventListener("pointermove", (e) => {
      if (!drag) return;
      if (e.buttons === 0) {
        end();
        return;
      }
      const { node, index, room } = drag;
      const minA = minSize(node.children[index], node.dir);
      const minB = minSize(node.children[index + 1], node.dir);
      const at = (drag.row ? e.clientX : e.clientY) - drag.start - drag.thick / 2;
      const px = room > minA + minB ? Math.max(minA, Math.min(room - minB, at)) : room / 2;
      this.share(drag, px / room);
    }, { signal });
    for (const type of ["pointerup", "pointercancel", "lostpointercapture"]) host.addEventListener(type, end, { signal });
    host.addEventListener("dblclick", (e) => {
      const handle = e.target instanceof Element ? e.target.closest(".editor-split-handle") : null;
      const pair = handle && this.pairAt(handle);
      if (!pair) return;
      this.share(pair, 0.5);
      this.onResize();
    }, { signal });
  }

  pairAt(handle) {
    const node = this.splits.get(handle.parentElement);
    if (!node) return null;
    const kids = [...handle.parentElement.children].filter((el) => !el.classList.contains("editor-split-handle"));
    const index = kids.indexOf(handle.previousElementSibling);
    if (index < 0 || index + 1 >= kids.length) return null;
    return { node, index, a: kids[index], b: kids[index + 1] };
  }

  share({ node, index, a, b }, fraction) {
    const total = node.sizes[index] + node.sizes[index + 1];
    node.sizes[index] = total * fraction;
    node.sizes[index + 1] = total - node.sizes[index];
    a.style.setProperty(SHARE, String(node.sizes[index]));
    b.style.setProperty(SHARE, String(node.sizes[index + 1]));
  }

  paint(root, elementOf) {
    const build = (node) => {
      if (!node.children) return elementOf(node.id);
      const box = document.createElement("div");
      box.className = "editor-split";
      box.dataset.dir = node.dir;
      node.children.forEach((child, i) => {
        if (i) {
          const handle = document.createElement("div");
          handle.className = "editor-split-handle";
          handle.dataset.editorSplitHandle = node.dir;
          box.appendChild(handle);
        }
        const el = build(child);
        el.style.setProperty(SHARE, String(node.sizes[i]));
        box.appendChild(el);
      });
      this.splits.set(box, node);
      return box;
    };
    const top = build(root);
    top.style.removeProperty(SHARE);
    this.host.replaceChildren(top);
  }
}

export function dropZone(groupEl, headEl, x, y) {
  const box = groupEl.getBoundingClientRect();
  const head = headEl.getBoundingClientRect();
  if (y <= head.bottom) return { zone: "tabs", rect: head };
  const body = { left: box.left, top: head.bottom, width: box.width, height: box.bottom - head.bottom };
  const fx = (x - body.left) / body.width;
  const fy = (y - body.top) / body.height;
  const [side, distance] = [["left", fx], ["right", 1 - fx], ["up", fy], ["down", 1 - fy]]
    .sort((p, q) => p[1] - q[1])[0];
  if (distance >= EDGE) return { zone: "center", rect: body };
  const half = { ...body };
  if (side === "left" || side === "right") half.width = body.width / 2;
  else half.height = body.height / 2;
  if (side === "right") half.left = body.left + body.width / 2;
  if (side === "down") half.top = body.top + body.height / 2;
  return { zone: side, rect: half };
}
