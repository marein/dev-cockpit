import { el } from "@dc/dom";

export const AXIS_LOCK_PX = 12; // travel that commits a gesture to an axis
export const COMMIT_PX = 72; // finger travel that commits a switch on release
export const FLING_VX = 0.5; // px/ms release speed that commits regardless of travel
export const MAX_TX = 56; // the frame follows the finger at most this far
export const FOLLOW = 0.35; // frame travel per finger px
export const VELOCITY_WINDOW_MS = 100; // samples that feed the release velocity
export const VELOCITY_MIN_SPAN_MS = 15; // shorter sample spans give no usable velocity
export const LONG_PRESS_MS = 300;
export const FLING_START_V = 0.35; // px/ms release speed that starts a fling
export const FLING_STOP_V = 0.04; // px/ms, the fling ends below this
export const FLING_TAU_MS = 325; // exponential decay time constant
export const FLING_MAX_V = 4; // px/ms hard ceiling
const PENDING_FAILSAFE_MS = 12000; // drop a stuck pending indicator eventually

export function pushSample(samples, sample) {
  samples.push(sample);
  while (samples.length > 1 && sample.t - samples[0].t > VELOCITY_WINDOW_MS) samples.shift();
}

export function isCurrentPage(url, { ignoreQuery = false } = {}) {
  const target = new URL(url, window.location.href);
  if (target.pathname !== window.location.pathname) return false;
  return ignoreQuery || target.search === window.location.search;
}

// Velocity over the recent sample window, in finger px/ms along "x" or "y". A
// finger that rested before lifting leaves only stale samples, that is no fling.
export function releaseVelocity(samples, now, axis) {
  const recent = samples.filter((sample) => now - sample.t <= VELOCITY_WINDOW_MS);
  if (recent.length < 2) return 0;
  const first = recent[0];
  const last = recent[recent.length - 1];
  const span = last.t - first.t;
  if (span < VELOCITY_MIN_SPAN_MS) return 0;
  return (last[axis] - first[axis]) / span;
}

// SwipeNav turns the reports of a recognizer (phase move, end or cancel, dx the
// finger travel from the start, vx the release velocity) into a switch: it
// slides the frame a damped distance under the finger, shows a pill naming the
// stop you would land on, and navigates through pe.js on release. The pill
// stays as a pending indicator until the new page arrives, and a further swipe
// while one is loading chains from the pending stop.
export class SwipeNav {
  constructor({ frame, host, stops, go = null, pillClass = "", pillData = null, ignoreQuery = false }) {
    this.frame = frame;
    this.host = host;
    this.stops = stops;
    this.go = go;
    this.pillClass = pillClass;
    this.pillData = pillData;
    this.ignoreQuery = ignoreQuery;
    this.gesture = null;
    this.pendingIndex = null;
    this.pill = null;
    this.failsafe = 0;
    this.deadline = 0;
  }

  destroy() {
    this.resetFrame();
    this.removePill();
    clearTimeout(this.failsafe);
    this.frame = null;
    this.gesture = null;
  }

  adopt(from) {
    if (!from) return;
    if (from.pendingIndex !== null && from.pill) {
      this.pendingIndex = from.pendingIndex;
      this.removePill();
      this.pill = from.pill;
      this.host.appendChild(this.pill);
      this.armFailsafe(from.deadline);
      from.pill = null;
      from.pendingIndex = null;
    }
    from.destroy();
  }

  beginGesture() {
    const stops = this.stops();
    if (stops.length < 2) return null;
    let base = this.pendingIndex ?? stops.findIndex((stop) => stop.active);
    if (base < 0 || base >= stops.length) base = 0;
    return { stops, base };
  }

  onSwipe(detail) {
    if (detail.phase === "move") {
      if (detail.begin || !this.gesture) {
        this.gesture = this.beginGesture();
        if (!this.gesture) return;
      }
      this.moveGesture(detail.dx || 0);
      return;
    }
    const gesture = this.gesture;
    this.gesture = null;
    if (!gesture) return;
    if (detail.phase === "end") {
      this.endGesture(gesture, detail.dx || 0, detail.vx || 0);
      return;
    }
    this.resetFrame();
    if (this.pendingIndex === null) this.removePill();
  }

  targetIndex(gesture, dx) {
    const count = gesture.stops.length;
    return (gesture.base + (dx < 0 ? 1 : -1) + count) % count;
  }

  moveGesture(dx) {
    const target = this.targetIndex(this.gesture, dx);
    const tx = Math.max(-MAX_TX, Math.min(MAX_TX, dx * FOLLOW));
    if (this.frame) {
      this.frame.style.transition = "none";
      this.frame.style.transform = tx ? "translateX(" + tx + "px)" : "";
    }
    this.showPill(this.gesture.stops[target], dx < 0 ? 1 : -1, Math.min(1, Math.abs(dx) / COMMIT_PX));
  }

  endGesture(gesture, dx, vx) {
    const target = this.targetIndex(gesture, dx);
    const fling = Math.abs(vx) > FLING_VX && Math.sign(vx) === Math.sign(dx);
    const commit = Math.abs(dx) > COMMIT_PX || fling;
    this.resetFrame();
    if (!commit) {
      if (this.pendingIndex === null) this.removePill();
      return;
    }
    const stop = gesture.stops[target];
    if (this.go) {
      this.removePill();
      this.go(stop);
      return;
    }
    this.pendingIndex = target;
    this.showPill(stop, dx < 0 ? 1 : -1, 1, true);
    this.armFailsafe();
    if (!stop.url || isCurrentPage(stop.url, { ignoreQuery: this.ignoreQuery })) {
      // Swiped back onto the page already showing: abort the in-flight load.
      window.pe?.abortController?.abort();
      this.pendingIndex = null;
      this.removePill();
      return;
    }
    if (window.app?.navigate) Promise.resolve(window.app.navigate(stop.url)).catch(() => {});
    else window.location.href = stop.url;
  }

  armFailsafe(deadline = performance.now() + PENDING_FAILSAFE_MS) {
    clearTimeout(this.failsafe);
    this.deadline = deadline;
    this.failsafe = setTimeout(() => {
      this.pendingIndex = null;
      this.removePill();
    }, Math.max(0, deadline - performance.now()));
  }

  resetFrame() {
    const frame = this.frame;
    if (!frame) return;
    if (!frame.style.transform) {
      frame.style.transition = "";
      return;
    }
    frame.style.transition = "transform 0.18s ease";
    frame.style.transform = "";
    setTimeout(() => {
      frame.style.transition = "";
    }, 200);
  }

  showPill(stop, dir, progress, pending) {
    if (!this.pill) {
      this.pill = el("div", { class: ("dc-swipe-pill " + this.pillClass).trim(), dataset: this.pillData });
      this.host.appendChild(this.pill);
    }
    const key = stop.id + ":" + dir;
    if (this.pill.dataset.key !== key) {
      this.pill.dataset.key = key;
      this.pill.replaceChildren(...[
        dir < 0 ? el("i", { class: "ti ti-chevron-left", "aria-hidden": "true" }) : null,
        stop.icon ? stop.icon.cloneNode(true) : null,
        el("span", { class: "dc-swipe-pill-name text-truncate" }, stop.name),
        dir > 0 ? el("i", { class: "ti ti-chevron-right", "aria-hidden": "true" }) : null,
      ].filter(Boolean));
    }
    this.pill.style.opacity = String(0.35 + 0.65 * progress);
    this.pill.classList.toggle("dc-swipe-pill-pending", Boolean(pending));
  }

  removePill() {
    this.pill?.remove();
    this.pill = null;
  }
}

export function swipeBlockedAt(target, boundary) {
  for (let node = target instanceof Element ? target : target?.parentElement; node && node !== boundary; node = node.parentElement) {
    if (node.matches("input, textarea, select, [contenteditable]:not([contenteditable='false'])")) return true;
    if (node.scrollWidth > node.clientWidth + 1) {
      const overflow = getComputedStyle(node).overflowX;
      if (overflow === "auto" || overflow === "scroll") return true;
    }
  }
  return false;
}

function selectionStands() {
  const selection = window.getSelection ? window.getSelection() : null;
  return Boolean(selection && selection.rangeCount && !selection.isCollapsed);
}

export function watchSwipe(surface, { signal, startable, onSwipe }) {
  let gesture = null;
  const abort = () => {
    const horizontal = gesture?.axis === "h";
    gesture = null;
    if (horizontal) onSwipe({ phase: "cancel" });
  };
  const touchOf = (event) => gesture && Array.from(event.changedTouches || []).find((touch) => touch.identifier === gesture.id);
  surface.addEventListener("touchstart", (event) => {
    if (gesture) {
      abort();
      return;
    }
    if (event.touches.length !== 1 || selectionStands() || !startable(event.target)) return;
    const touch = event.touches[0];
    gesture = {
      id: touch.identifier,
      x: touch.clientX,
      y: touch.clientY,
      t: event.timeStamp,
      axis: null,
      samples: [{ t: event.timeStamp, x: touch.clientX, y: touch.clientY }],
    };
  }, { signal, passive: true });
  surface.addEventListener("touchmove", (event) => {
    const touch = touchOf(event);
    if (!touch) return;
    if (event.touches.length !== 1 || selectionStands()) {
      abort();
      return;
    }
    const dx = touch.clientX - gesture.x;
    const dy = touch.clientY - gesture.y;
    pushSample(gesture.samples, { t: event.timeStamp, x: touch.clientX, y: touch.clientY });
    if (gesture.axis === null) {
      if (Math.hypot(dx, dy) < AXIS_LOCK_PX) return;
      if (event.timeStamp - gesture.t > LONG_PRESS_MS || Math.abs(dy) >= Math.abs(dx)) {
        gesture = null;
        return;
      }
      gesture.axis = "h";
      if (event.cancelable) event.preventDefault();
      onSwipe({ phase: "move", dx, begin: true });
      return;
    }
    if (event.cancelable) event.preventDefault();
    onSwipe({ phase: "move", dx });
  }, { signal, passive: false });
  surface.addEventListener("touchend", (event) => {
    const touch = touchOf(event);
    if (!touch) return;
    const done = gesture;
    gesture = null;
    if (done.axis !== "h") return;
    onSwipe({ phase: "end", dx: touch.clientX - done.x, vx: releaseVelocity(done.samples, event.timeStamp, "x") });
  }, { signal });
  surface.addEventListener("touchcancel", abort, { signal });
  surface.addEventListener("contextmenu", abort, { signal });
  document.addEventListener("selectionchange", () => {
    if (gesture && selectionStands()) abort();
  }, { signal });
}
