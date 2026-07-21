import { el } from "@dc/dom";

let current = null;
let closedAt = 0;
let touchOpenAt = 0;

export function menuJustClosed() {
  return Date.now() - closedAt < 350;
}

// noteTouchOpen marks the menu about to open as opened by a resting finger, so
// it survives that finger's wobble (see the touchmove listener in openMenu).
export function noteTouchOpen() {
  touchOpenAt = Date.now();
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
  const openedAt = Date.now();
  const fromTouch = openedAt - touchOpenAt < 100;
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
  document.addEventListener("wheel", (event) => {
    if (!node.contains(event.target)) closeMenu();
  }, { signal: ac.signal, capture: true, passive: true });
  // Touch events keep targeting the touchstart element for the whole gesture, so
  // the finger still resting on the row after a long-press open reports its
  // wobble as touchmove outside the menu. Only a menu opened by that very finger
  // needs the grace; every other open closes on the first touchmove as before.
  document.addEventListener("touchmove", (event) => {
    if (fromTouch && Date.now() - openedAt < 700) return;
    if (!node.contains(event.target)) closeMenu();
  }, { signal: ac.signal, capture: true, passive: true });
  window.addEventListener("resize", closeMenu, { signal: ac.signal });
  window.addEventListener("blur", closeMenu, { signal: ac.signal });
  document.addEventListener("dc:navigated", closeMenu, { signal: ac.signal });
  signal?.addEventListener("abort", closeMenu, { signal: ac.signal });
  return node;
}

// wireRowMenus gives a container's rows a context menu on right click and on
// touch long press. `openFor(row, x, y)` opens the menu and returns truthy when
// it handled the row.
//
// Three paths, because no single one covers every device:
//   - `contextmenu`, for the mouse and for browsers that raise it on a long
//     press. iOS Safari's carries no coordinates, so a row's rect is the anchor
//     whenever clientX/clientY are 0; anchored at 0,0 the menu would sit in the
//     screen corner and read as "not opening".
//   - touch events, wherever they exist. They are the only reliable path over a
//     row holding a link: iOS hands a long press on a link to its own gesture
//     recognizer, which fires `pointercancel` (killing a pointer-based timer)
//     and, with the callout suppressed, raises no `contextmenu` either. Touch
//     events survive that, and `preventDefault` on `touchend` is what stops the
//     lift from following the link.
//   - pointer events, as the timer path on non-touch pointers.
// The click after an opened menu is swallowed so the row does not also activate.
export function wireRowMenus(container, rowSelector, openFor, { signal } = {}) {
  let press = null;
  let suppressClick = false;
  let pressMenuAt = 0;
  let openedByTouch = false;
  const cancelAny = () => {
    if (!press) return;
    clearTimeout(press.timer);
    press = null;
  };
  // A press is cancelled only by the event family that armed it. That is the
  // whole point over a link: iOS fires pointercancel when its own gesture
  // recognizer claims the long press, and that must not kill the touch timer.
  const cancelPress = (fromTouch) => {
    if (press && press.fromTouch === fromTouch) cancelAny();
  };
  // startPress arms the long-press timer for one gesture. Both event families
  // fire for one finger and pointerdown comes BEFORE touchstart, so the pointer
  // arms first and the touchstart that follows claims the press for the touch
  // family; without that claim the press stays pointer-owned and iOS's
  // pointercancel kills it. The timer therefore reads the owning family at fire
  // time, not the value it was armed with. A touch-owned open goes through
  // noteTouchOpen so the menu tolerates the resting finger's wobble.
  const startPress = (row, x, y, fromTouch) => {
    if (press) {
      if (fromTouch) press.fromTouch = true;
      return;
    }
    const armed = {
      x,
      y,
      fromTouch,
      timer: setTimeout(() => {
        press = null;
        if (armed.fromTouch) noteTouchOpen();
        if (openFor(row, x, y)) {
          suppressClick = true;
          pressMenuAt = Date.now();
          openedByTouch = armed.fromTouch;
        }
      }, 500),
    };
    press = armed;
  };
  const movedTooFar = (x, y) => press && Math.hypot(x - press.x, y - press.y) > 10;

  container.addEventListener("contextmenu", (e) => {
    const row = e.target.closest(rowSelector);
    if (press) {
      cancelAny();
      suppressClick = true;
    }
    if (Date.now() - pressMenuAt < 600) {
      e.preventDefault();
      return;
    }
    let handled;
    if (e.clientX || e.clientY) {
      handled = openFor(row, e.clientX, e.clientY);
    } else if (row) {
      const rect = row.getBoundingClientRect();
      handled = openFor(row, rect.left, rect.bottom + 4);
    }
    if (handled) e.preventDefault();
  }, { signal });

  container.addEventListener("touchstart", (e) => {
    suppressClick = false;
    openedByTouch = false;
    if (e.touches.length !== 1) {
      cancelAny();
      return;
    }
    const touch = e.touches[0];
    startPress(e.target.closest(rowSelector), touch.clientX, touch.clientY, true);
  }, { signal, passive: true });
  container.addEventListener("touchmove", (e) => {
    const touch = e.touches[0];
    if (touch && movedTooFar(touch.clientX, touch.clientY)) cancelPress(true);
  }, { signal, passive: true });
  // Not passive: preventDefault here is what keeps the lift from following the
  // row's link and from synthesizing the click underneath the open menu.
  container.addEventListener("touchend", (e) => {
    cancelPress(true);
    if (!openedByTouch) return;
    openedByTouch = false;
    e.preventDefault();
  }, { signal, passive: false });
  // touchcancel deliberately does NOT cancel the press. iOS ends the touch
  // stream when one of its own recognizers claims the hold (the link drag lift
  // among them), which is precisely the moment the user is long-pressing; a
  // real scroll delivers touchmove past the threshold first. If the stream was
  // cancelled the lift never fires touchend, so the suppressed click below
  // keeps the row's link from being followed.
  // Rows holding links also need the native drag killed at the source: iOS
  // ignores draggable="false" on links, preventing dragstart is the way.
  container.addEventListener("dragstart", (e) => {
    if (e.target.closest?.(rowSelector)) e.preventDefault();
  }, { signal });

  container.addEventListener("pointerdown", (e) => {
    if (e.pointerType !== "touch") {
      suppressClick = false;
      return;
    }
    startPress(e.target.closest(rowSelector), e.clientX, e.clientY, false);
  }, { signal });
  container.addEventListener("pointermove", (e) => {
    if (press && !press.fromTouch && movedTooFar(e.clientX, e.clientY)) cancelPress(false);
  }, { signal });
  container.addEventListener("pointerup", () => cancelPress(false), { signal });
  container.addEventListener("pointercancel", () => cancelPress(false), { signal });

  container.addEventListener("click", (e) => {
    if (!suppressClick) return;
    suppressClick = false;
    e.preventDefault();
    e.stopPropagation();
  }, { signal, capture: true });
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
