import { swapKeepingFolds } from "@dc/dom";
import { parentDir } from "@dc/editor-format";

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
