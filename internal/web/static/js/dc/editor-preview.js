import { swapKeepingFolds } from "@dc/dom";
import { parentDir } from "@dc/editor-format";
import { mermaidUrl } from "@dc/editor-filetypes";
import { isDark } from "@dc/theme";

const external = (href) => /^([a-z][a-z\d+.-]*:|\/\/)/i.test(href);
const fullAddress = (href) => /^([a-z][a-z\d+.-]*:)?\/\/[^/]/i.test(href);
const linkOf = (a) => a?.getAttribute("href") ?? a?.getAttribute("xlink:href");

// projectPath reads a link of the file at path the way GitHub reads one in a
// README, a leading / from the project root. A link that climbs above the
// root, or does not decode, answers null.
function projectPath(href, path) {
  let target;
  try {
    target = decodeURIComponent(href.replace(/[?#].*$/s, ""));
  } catch {
    return null;
  }
  const parts = target.startsWith("/") ? [] : parentDir(path).split("/").filter(Boolean);
  for (const part of target.split("/")) {
    if (part === "..") {
      if (!parts.length) return null;
      parts.pop();
    } else if (part && part !== ".") {
      parts.push(part);
    }
  }
  return parts.join("/");
}

function scrollToHeading(pane, hash) {
  try {
    pane.querySelector(`#${CSS.escape(decodeURIComponent(hash).replace(/^(user-content-)?/, "user-content-"))}`)?.scrollIntoView();
  } catch {}
}

// The images point into the project before the HTML reaches a live node, a
// browser fetches a src the moment it parses one.
function pointImages(root, path, rawUrl) {
  for (const img of root.querySelectorAll("img[src]")) {
    const src = img.getAttribute("src");
    if (external(src)) continue;
    const target = projectPath(src, path);
    if (target) img.setAttribute("src", rawUrl(target));
    else img.removeAttribute("src");
  }
}

let mermaidMod;
function loadMermaid() {
  return (mermaidMod ??= import(mermaidUrl()).then((m) => m.default));
}

let mermaidSeq = 0;
const drawGen = new WeakMap();
const paneDoc = new WeakMap();
const drawnFrom = new WeakMap();

// A block keeps its source as a hidden <pre> right before its drawing, so a
// redraw always reads what the file says.
const blocksOf = (pane) => [...pane.querySelectorAll("pre > code.language-mermaid")].map((code) => code.parentElement);
const drawingOf = (pre) => (drawnFrom.has(pre.nextElementSibling) ? pre.nextElementSibling : null);
const fits = (drawing, source, dark) => drawnFrom.get(drawing)?.source === source && drawnFrom.get(drawing)?.dark === dark;

function place(pre, drawing) {
  pre.after(drawing);
  pre.hidden = true;
}

async function draw(mermaid, source, { path, rawUrl }) {
  const dark = isDark();
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    suppressErrorRendering: true,
    theme: dark ? "dark" : "default",
  });
  let drawing;
  try {
    const { svg } = await mermaid.render(`dc-mermaid-${++mermaidSeq}`, source);
    const t = document.createElement("template");
    t.innerHTML = svg;
    pointImages(t.content, path, rawUrl);
    // Strict mode drops a link's target, a link to a full address gets the new
    // tab the server gives one in the markdown.
    for (const a of t.content.querySelectorAll("a")) {
      if (!fullAddress(linkOf(a) || "")) continue;
      a.setAttribute("target", "_blank");
      a.setAttribute("rel", "nofollow noopener");
    }
    drawing = document.createElement("div");
    drawing.className = "dc-mermaid";
    drawing.append(t.content);
    // Mermaid fits the drawing into the box up to its own width, which shrinks
    // a wide one past reading on a phone; at its own width the box scrolls.
    const el = drawing.querySelector("svg");
    if (el) el.style.width = el.style.maxWidth;
  } catch (err) {
    drawing = document.createElement("pre");
    drawing.className = "dc-mermaid-error text-danger";
    drawing.textContent = err?.message || String(err);
  }
  drawnFrom.set(drawing, { source, dark });
  return drawing;
}

// The generation drops a pass that a newer swap or theme switch already moved
// on from, so two passes never both land.
async function drawDiagrams(pane) {
  const gen = (drawGen.get(pane) || 0) + 1;
  drawGen.set(pane, gen);
  const todo = blocksOf(pane).filter((pre) => !fits(drawingOf(pre), pre.textContent, isDark()));
  if (!todo.length) return;
  let mermaid;
  try {
    mermaid = await loadMermaid();
  } catch (err) {
    console.warn("mermaid load failed", err);
    return;
  }
  for (const pre of todo) {
    if (drawGen.get(pane) !== gen) return;
    const drawing = await draw(mermaid, pre.textContent, paneDoc.get(pane));
    if (drawGen.get(pane) !== gen || !pre.isConnected) return;
    drawingOf(pre)?.remove();
    place(pre, drawing);
  }
}

// Typing swaps the whole pane. A drawing whose source did not change goes back
// at once, a changed block keeps its old drawing until the new one is drawn,
// so the pane neither flashes the source nor changes its height.
function keepDrawings(pane, old) {
  const dark = isDark();
  const blocks = blocksOf(pane);
  const free = new Set(old.filter(Boolean));
  for (const pre of blocks) {
    const same = [...free].find((drawing) => fits(drawing, pre.textContent, dark));
    if (!same) continue;
    free.delete(same);
    place(pre, same);
  }
  blocks.forEach((pre, i) => {
    if (drawingOf(pre) || !free.has(old[i])) return;
    free.delete(old[i]);
    place(pre, old[i]);
  });
}

export function showPreview(pane, html, path, hash, rawUrl) {
  const t = document.createElement("template");
  t.innerHTML = html;
  pointImages(t.content, path, rawUrl);
  const old = paneDoc.get(pane)?.path === path ? blocksOf(pane).map(drawingOf) : [];
  swapKeepingFolds(pane, t.innerHTML, path);
  paneDoc.set(pane, { path, rawUrl });
  keepDrawings(pane, old);
  if (hash) scrollToHeading(pane, hash);
  void drawDiagrams(pane);
}

// A heading link must not touch the page's address or history, a link with a
// scheme is left to the browser. A diagram's links are SVG ones, their address
// stands in xlink:href.
export function wirePreview(pane, pathOf, follow, opts) {
  pane.addEventListener("click", (e) => {
    const href = linkOf(e.target.closest("a"));
    if (!href || external(href)) return;
    e.preventDefault();
    const [, hash = ""] = href.split(/#(.*)/s);
    if (href.startsWith("#")) scrollToHeading(pane, hash);
    else follow(projectPath(href, pathOf()), hash);
  }, opts);
  document.addEventListener("dc:theme", () => void drawDiagrams(pane), opts);
}
