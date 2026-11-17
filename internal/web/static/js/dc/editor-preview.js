import { swapKeepingFolds } from "@dc/dom";
import { parentDir } from "@dc/editor-format";
import { mermaidUrl } from "@dc/editor-filetypes";
import { isDark } from "@dc/theme";

const external = (href) => /^([a-z][a-z\d+.-]*:|\/\/)/i.test(href);

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

let mermaidMod;
function loadMermaid() {
  return (mermaidMod ??= import(mermaidUrl()).then((m) => m.default));
}

let mermaidSeq = 0;
const mermaidGen = new WeakMap();

// Each block renders into its own element, so a later edit's swap discards
// them with the rest of the pane; nothing here outlives that innerHTML
// replace. The generation guards a render that resolves after a newer swap
// already moved on, so two edits in quick succession never both land.
async function renderMermaid(pane) {
  const blocks = [...pane.querySelectorAll("pre > code.language-mermaid")];
  if (!blocks.length) return;
  const gen = (mermaidGen.get(pane) || 0) + 1;
  mermaidGen.set(pane, gen);
  let mermaid;
  try {
    mermaid = await loadMermaid();
  } catch (err) {
    console.warn("mermaid load failed", err);
    return;
  }
  if (mermaidGen.get(pane) !== gen) return;
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    suppressErrorRendering: true,
    theme: isDark() ? "dark" : "default",
  });
  for (const code of blocks) {
    if (mermaidGen.get(pane) !== gen) return;
    const pre = code.closest("pre");
    try {
      const { svg } = await mermaid.render(`dc-mermaid-${++mermaidSeq}`, code.textContent);
      if (mermaidGen.get(pane) !== gen || !pre.isConnected) continue;
      const host = document.createElement("div");
      host.className = "dc-mermaid";
      host.innerHTML = svg;
      pre.replaceWith(host);
    } catch (err) {
      if (mermaidGen.get(pane) !== gen || !pre.isConnected) continue;
      const msg = document.createElement("pre");
      msg.className = "dc-mermaid-error text-danger";
      msg.textContent = err?.message || String(err);
      pre.replaceWith(msg);
    }
  }
}

// The images point into the project before the HTML reaches a live node, a
// browser fetches a src the moment it parses one.
export function showPreview(pane, html, path, hash, rawUrl) {
  const t = document.createElement("template");
  t.innerHTML = html;
  for (const img of t.content.querySelectorAll("img[src]")) {
    const src = img.getAttribute("src");
    if (external(src)) continue;
    const target = projectPath(src, path);
    if (target) img.setAttribute("src", rawUrl(target));
    else img.removeAttribute("src");
  }
  swapKeepingFolds(pane, t.innerHTML, path);
  if (hash) scrollToHeading(pane, hash);
  void renderMermaid(pane);
}

// A heading link must not touch the page's address or history, a link with a
// scheme is left to the browser.
export function wirePreviewLinks(pane, pathOf, follow, opts) {
  pane.addEventListener("click", (e) => {
    const href = e.target.closest("a[href]")?.getAttribute("href");
    if (!href || external(href)) return;
    e.preventDefault();
    const [, hash = ""] = href.split(/#(.*)/s);
    if (href.startsWith("#")) scrollToHeading(pane, hash);
    else follow(projectPath(href, pathOf()), hash);
  }, opts);
}
