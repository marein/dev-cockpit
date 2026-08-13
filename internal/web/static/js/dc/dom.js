export function qs(selector, root = document) {
  return root.querySelector(selector);
}

export function qsa(selector, root = document) {
  return Array.from(root.querySelectorAll(selector));
}

export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value == null) {
      continue;
    }
    if (key === "class" || key === "className") {
      node.className = value;
    } else if (key === "dataset" && typeof value === "object") {
      Object.assign(node.dataset, value);
    } else if (key === "style" && typeof value === "object") {
      Object.assign(node.style, value);
    } else if (key in node) {
      node[key] = value;
    } else {
      node.setAttribute(key, value);
    }
  }
  for (const child of children.flat()) {
    if (child == null) {
      continue;
    }
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}

export function syncAnimations(root) {
  if (!root || typeof root.getAnimations !== "function") return;
  for (const animation of root.getAnimations({ subtree: true })) {
    if (animation.effect?.getTiming().iterations !== Infinity) continue;
    animation.startTime = 0;
  }
}

export function jumpTextEdge(event, input) {
  if (event.key !== "Home" && event.key !== "End") return false;
  if (!input || event.metaKey || event.ctrlKey || event.altKey || event.isComposing) return false;
  event.preventDefault();
  const to = event.key === "Home" ? 0 : input.value.length;
  if (event.shiftKey) {
    const anchor = input.selectionDirection === "backward" ? input.selectionEnd : input.selectionStart;
    if (to < anchor) input.setSelectionRange(to, anchor, "backward");
    else input.setSelectionRange(anchor, to, "forward");
  } else {
    input.setSelectionRange(to, to);
  }
  input.scrollTop = to === 0 ? 0 : input.scrollHeight;
  return true;
}

const ESCAPE = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

// The one escape in the app, imported by everything that builds markup from a
// value: the toast, the editor, the menus. Element content would not need the
// two quotes, but a second copy of this with a smaller set is how a value ends
// up unquoted inside an attribute one day, so there is no second copy.
export function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, (char) => ESCAPE[char]);
}

// Whether what this window shows counts as seen by somebody, the one test
// behind every "they have read it" in the app: the notification center reads
// its own targets on it, the git dialog reads the question's entry on it. A
// visible but unfocused window is the browser on a second monitor or behind
// another app, and nobody there has seen anything, which is why visibility
// alone is not it.
export function windowSeen() {
  return document.visibilityState === "visible" && document.hasFocus();
}
