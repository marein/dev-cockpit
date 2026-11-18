export const api = globalThis.browser?.runtime?.id ? globalThis.browser : globalThis.chrome;

export const own = new URL(api.runtime.getURL("")).origin;

// Safari has no side panel: the button opens the panel as a popup, which a
// click into the page closes.
export const POPUP = Boolean(api.runtime.getManifest().action?.default_popup);

// From version 142 on Chrome tells the worker when its side panel closes, also
// when another panel takes its place and keeps this page hidden. Firefox and
// older Chrome tell nothing, the panel's port drops when it closes.
export const PANEL_CLOSED = Boolean(api.sidePanel?.onClosed);

// Chrome takes a picture of a tab with no less than every address.
export const EVERY_PAGE = ["<all_urls>"];

export function everyPage() {
  return api.permissions.contains({ origins: EVERY_PAGE }).catch(() => false);
}

// A content script's message carries the tab's address, the extension's own
// pages carry its origin.
export function fromOwnPage(sender) {
  if (!sender || sender.id !== api.runtime.id || typeof sender.url !== "string") return false;
  try {
    return new URL(sender.url).origin === own;
  } catch {
    return false;
  }
}

export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === null || value === undefined || value === false) continue;
    if (key === "dataset") Object.assign(node.dataset, value);
    else if (key === "class") node.className = value;
    else node.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}
