import { el } from "@dc/dom";

let current = null;
let closedAt = 0;

export function menuJustClosed() {
  return Date.now() - closedAt < 350;
}

export function closeMenu() {
  if (!current) return;
  const menu = current;
  current = null;
  closedAt = Date.now();
  menu.ac.abort();
  menu.node.remove();
  if (menu.prevFocus?.isConnected) menu.prevFocus.focus({ preventScroll: true });
}

export function openMenu({ x, y, items, signal }) {
  closeMenu();
  const node = el("div", {
    class: "dropdown-menu dc-context-menu show",
    role: "menu",
    tabindex: "-1",
  });
  for (const item of items) {
    if (!item) continue;
    if (item.divider) {
      if (node.lastElementChild && !node.lastElementChild.classList.contains("dropdown-divider")) {
        node.appendChild(el("div", { class: "dropdown-divider", role: "separator" }));
      }
      continue;
    }
    const button = el(
      "button",
      {
        type: "button",
        class: "dropdown-item" + (item.danger ? " text-danger" : ""),
        role: "menuitem",
      },
      item.icon ? el("i", { class: `ti ${item.icon} me-2`, "aria-hidden": "true" }) : null,
      item.label,
    );
    if (item.disabled) button.disabled = true;
    button.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      closeMenu();
      item.action?.();
    });
    node.appendChild(button);
  }
  if (node.lastElementChild?.classList.contains("dropdown-divider")) node.lastElementChild.remove();
  if (!node.childElementCount) return null;
  const ac = new AbortController();
  current = { node, ac, prevFocus: document.activeElement };
  document.body.appendChild(node);
  place(node, x, y);
  node.focus({ preventScroll: true });
  const capture = { signal: ac.signal, capture: true };
  document.addEventListener("pointerdown", (event) => {
    if (!node.contains(event.target)) closeMenu();
  }, capture);
  document.addEventListener("keydown", (event) => onKeydown(event, node), capture);
  for (const type of ["wheel", "touchmove"]) {
    document.addEventListener(type, (event) => {
      if (!node.contains(event.target)) closeMenu();
    }, { signal: ac.signal, capture: true, passive: true });
  }
  window.addEventListener("resize", closeMenu, { signal: ac.signal });
  window.addEventListener("blur", closeMenu, { signal: ac.signal });
  document.addEventListener("dc:navigated", closeMenu, { signal: ac.signal });
  signal?.addEventListener("abort", closeMenu, { signal: ac.signal });
  return node;
}

function onKeydown(event, node) {
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeMenu();
    return;
  }
  if (event.key === "Tab") {
    closeMenu();
    return;
  }
  const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
  if (!step) return;
  event.preventDefault();
  event.stopPropagation();
  const buttons = Array.from(node.querySelectorAll(".dropdown-item:not(:disabled)"));
  if (!buttons.length) return;
  const index = buttons.indexOf(document.activeElement);
  const next = index === -1
    ? (step === 1 ? buttons[0] : buttons[buttons.length - 1])
    : buttons[(index + step + buttons.length) % buttons.length];
  next.focus({ preventScroll: true });
}

function place(node, x, y) {
  const margin = 6;
  const rect = node.getBoundingClientRect();
  const left = Math.max(margin, Math.min(x, window.innerWidth - rect.width - margin));
  const top = Math.max(margin, Math.min(y, window.innerHeight - rect.height - margin));
  node.style.left = `${left}px`;
  node.style.top = `${top}px`;
}
