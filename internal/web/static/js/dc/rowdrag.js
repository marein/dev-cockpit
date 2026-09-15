// The one drag that sorts a list of rows by hand: the terminal strip, across
// or down, and the assistants' column. A press that travels further than
// DRAG_THRESHOLD lifts its row out, the rows it passes slide out of the way by
// the carried row's size, and the drop takes the seat the pointer is over. A
// finger starts a drag only on a row's grip, so a list still scrolls under the
// thumb; a mouse drags from anywhere on the row.
//
// The strip takes the pointer capture on the press and measures from where the
// threshold was crossed, which is what it always did. A list whose row is a
// container with the link inside it cannot: a capture retargets the click that
// follows to the element holding it, so a capture on every press would swallow
// every click that opens something. Such a list says `capture: "drag"` and
// takes it when the drag begins, and then measures from the press point, so the
// row sits under the pointer. The click the release produces is swallowed here.
//
// Geometry is measured against the scroller the list lives in, once at the
// start: everything below is content coordinates, so the list scrolling under
// the drag changes nothing. The list scrolls itself while the carried row hangs
// over an edge.
export const DRAG_THRESHOLD = 6;
export const EDGE_ZONE = 32;
export const EDGE_STEP = 12;
// Dropping a row on another one instead of between two: the zone is a share of
// the target's size and the pointer has to rest in it, so passing over a row on
// the way to a seat never grabs it.
export const GROUP_ZONE_RATIO = 0.3;
export const GROUP_DWELL_MS = 220;

export class RowDrag {
  // host carries the listeners and stays alive across list swaps; options say
  // what a row is and what a drop means:
  //   rowSelector   what a press has to land on (required)
  //   gripSelector  the touch handle inside a row, default [data-tab-grip]
  //   ignoreSelector  parts of a row that are never a drag (buttons)
  //   rows(row)     the seats this row can take, top first (required)
  //   unitRows(row) the rows that travel with it, default the row alone
  //   vertical()    the axis, default down
  //   scroller()    the element the list scrolls in, default the host
  //   blocked()     true while something else owns the list
  //   grouping(row) true where a drop on another row means something
  //   capture       "press" (default) or "drag", see above
  //   classes       { list, row, target } while the drag runs, the strip's own
  //                 names by default
  //   endAnchor(row, others)  what to insert before when the row goes last
  //   onDrop({ row, rows, fromIndex, toIndex, groupRow })  persist it
  //   onEnd()       after every gesture, dragged or not
  constructor(host, options) {
    this.host = host;
    this.o = options;
    this.names = {
      list: "terminal-tabs-strip-dragging",
      row: "terminal-tab-dragging",
      target: "terminal-tab-group-target",
      ...(options.classes || {}),
    };
    this.state = null;
    this.swallow = false;
  }

  wire(signal) {
    // A row holds a link, and a link answers a press with its own native drag
    // image. This one is ours.
    this.host.addEventListener("dragstart", (event) => event.preventDefault(), { signal });
    this.host.addEventListener("pointerdown", (event) => this.onDown(event), { signal });
    this.host.addEventListener("pointermove", (event) => this.onMove(event), { signal });
    // Every way a gesture can end runs the same idempotent close. Chrome drops
    // the capture when the tab goes to the background and the pointerup then
    // lands somewhere else entirely, so a handler on pointerup alone would
    // leave a row stuck under the pointer.
    this.host.addEventListener("pointerup", (event) => this.onUp(event, true), { signal });
    this.host.addEventListener("pointercancel", (event) => this.onUp(event, false), { signal });
    this.host.addEventListener("lostpointercapture", (event) => this.onLostCapture(event), { signal });
    // Capture, and wired before the host's own click handling: the click that
    // ends a drag opens nothing and reaches nobody.
    this.host.addEventListener("click", (event) => this.onClick(event), { signal, capture: true });
  }

  get busy() {
    return this.state !== null;
  }

  vertical() {
    return this.o.vertical ? this.o.vertical() : true;
  }

  axisClient(event) {
    return this.vertical() ? event.clientY : event.clientX;
  }

  frameStart(drag) {
    const rect = drag.scroller.getBoundingClientRect();
    return drag.vertical ? rect.top : rect.left;
  }

  scrolled(drag) {
    return drag.vertical ? drag.scroller.scrollTop : drag.scroller.scrollLeft;
  }

  contentPos(drag, client) {
    return client - this.frameStart(drag) + this.scrolled(drag);
  }

  onDown(event) {
    if (event.button !== 0 || this.o.blocked?.()) return;
    if (this.o.ignoreSelector && event.target.closest(this.o.ignoreSelector)) return;
    if (event.pointerType === "touch" && !event.target.closest(this.o.gripSelector || "[data-tab-grip]")) return;
    const row = event.target.closest(this.o.rowSelector);
    if (!row) return;
    this.state = {
      row,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      lastClient: this.axisClient(event),
      active: false,
      raf: 0,
    };
    if (this.o.capture !== "drag") this.grab();
  }

  grab() {
    try {
      this.state.row.setPointerCapture(this.state.pointerId);
    } catch (error) {
      void error;
    }
  }

  onMove(event) {
    const drag = this.state;
    if (!drag || event.pointerId !== drag.pointerId) return;
    if (!drag.active) {
      // Self healing: a button that was released where nothing heard it.
      if (!(event.buttons & 1)) {
        this.state = null;
        return;
      }
      if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < DRAG_THRESHOLD) return;
      this.begin(event);
    }
    event.preventDefault();
    drag.lastClient = this.axisClient(event);
    this.update();
  }

  onUp(event, commit) {
    const drag = this.state;
    if (!drag || (event.pointerId !== undefined && event.pointerId !== drag.pointerId)) return;
    this.end(commit);
  }

  // A capture that goes away while the drag holds it ends the drag where it is:
  // Chrome drops it when the tab goes to the background, and the pointerup then
  // lands somewhere else entirely. Only the drag's own capture counts. A touch
  // pointer is implicitly captured by whatever it went down on, so taking the
  // capture for the row releases the grip's and fires this before the row has
  // moved a pixel; that one is not the end of anything.
  onLostCapture(event) {
    const drag = this.state;
    if (!drag || !drag.active || event.target !== drag.row) return;
    this.end(true);
  }

  onClick(event) {
    if (!this.swallow) return;
    this.swallow = false;
    event.preventDefault();
    event.stopPropagation();
  }

  begin(event) {
    const drag = this.state;
    drag.active = true;
    drag.vertical = this.vertical();
    drag.scroller = this.o.scroller ? this.o.scroller() : this.host;
    drag.list = drag.row.parentNode;
    drag.rows = this.o.rows(drag.row);
    drag.units = drag.rows.map((row) => (this.o.unitRows ? this.o.unitRows(row) : [row]));
    drag.fromIndex = drag.rows.indexOf(drag.row);
    drag.toIndex = drag.fromIndex;
    drag.grouping = Boolean(this.o.grouping?.(drag.row));
    drag.groupTarget = -1;
    drag.groupPending = -1;
    drag.groupSince = 0;
    if (this.o.capture === "drag") this.grab();
    const size = (rect) => (drag.vertical ? rect.height : rect.width);
    const start = (rect) => (drag.vertical ? rect.top : rect.left);
    const frameStart = this.frameStart(drag);
    const scrolled = this.scrolled(drag);
    drag.sizes = drag.units.map((rows) => rows.reduce((sum, row) => sum + size(row.getBoundingClientRect()), 0));
    drag.size = drag.sizes[drag.fromIndex];
    drag.centers = drag.units.map((rows, i) => start(rows[0].getBoundingClientRect()) + drag.sizes[i] / 2 - frameStart + scrolled);
    // A list that captures on the drag measures from where the press was, not
    // from where the threshold was crossed, so the row sits under the pointer
    // and the seat it takes is the one the pointer is over; it catches up by
    // those few pixels once, which is what makes the drop predictable. The
    // strip keeps its own anchor, the event that crossed the threshold.
    drag.startContent = this.o.capture === "drag"
      ? (drag.vertical ? drag.startY : drag.startX) - frameStart + scrolled
      : this.contentPos(drag, this.axisClient(event));
    drag.list.classList.add(this.names.list);
    for (const row of drag.units[drag.fromIndex]) row.classList.add(this.names.row);
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  groupCandidate(drag, draggedCenter) {
    if (!drag.grouping) return -1;
    for (let i = 0; i < drag.rows.length; i += 1) {
      if (i === drag.fromIndex) continue;
      let shift = 0;
      if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.size;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.size;
      const visualCenter = drag.centers[i] + shift;
      if (Math.abs(draggedCenter - visualCenter) < drag.sizes[i] * GROUP_ZONE_RATIO) {
        return i;
      }
    }
    return -1;
  }

  update() {
    const drag = this.state;
    if (!drag || !drag.active) return;
    const delta = this.contentPos(drag, drag.lastClient) - drag.startContent;
    const draggedCenter = drag.centers[drag.fromIndex] + delta;
    let toIndex = 0;
    for (let i = 0; i < drag.centers.length; i += 1) {
      if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
    }
    drag.toIndex = toIndex;
    const candidate = this.groupCandidate(drag, draggedCenter);
    if (candidate === -1) {
      drag.groupPending = -1;
      drag.groupTarget = -1;
    } else if (candidate !== drag.groupPending) {
      drag.groupPending = candidate;
      drag.groupSince = Date.now();
      drag.groupTarget = -1;
    } else if (drag.groupTarget === -1 && Date.now() - drag.groupSince >= GROUP_DWELL_MS) {
      drag.groupTarget = candidate;
    }
    if (drag.grouping) {
      drag.rows.forEach((row, i) => row.classList.toggle(this.names.target, i === drag.groupTarget));
    }
    const move = drag.vertical ? "translateY" : "translateX";
    drag.units.forEach((rows, i) => {
      let shift = 0;
      if (i === drag.fromIndex) shift = delta;
      else if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.size;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.size;
      const transform = shift ? move + "(" + shift + "px)" : "";
      for (const row of rows) row.style.transform = transform;
    });
  }

  tickEdgeScroll() {
    const drag = this.state;
    if (!drag || !drag.active) return;
    const rect = drag.scroller.getBoundingClientRect();
    const lower = drag.vertical ? rect.top : rect.left;
    const upper = drag.vertical ? rect.bottom : rect.right;
    let delta = 0;
    if (drag.lastClient < lower + EDGE_ZONE) delta = -EDGE_STEP;
    else if (drag.lastClient > upper - EDGE_ZONE) delta = EDGE_STEP;
    if (delta) {
      const max = drag.vertical
        ? drag.scroller.scrollHeight - drag.scroller.clientHeight
        : drag.scroller.scrollWidth - drag.scroller.clientWidth;
      const current = this.scrolled(drag);
      const next = Math.max(0, Math.min(current + delta, max));
      if (next !== current) {
        if (drag.vertical) drag.scroller.scrollTop = next;
        else drag.scroller.scrollLeft = next;
        this.update();
      }
    }
    // The dwell is time, not movement: a pointer resting on a row has to reach
    // the group target without another event arriving.
    if (drag.groupPending >= 0 && drag.groupTarget === -1
      && Date.now() - drag.groupSince >= GROUP_DWELL_MS) {
      this.update();
    }
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  cancel() {
    this.end(false);
  }

  // Idempotent, and it never releases the capture itself: after the browser has
  // taken it away that throws and swallows everything behind it.
  end(commit) {
    const drag = this.state;
    this.state = null;
    if (!drag) return;
    if (!drag.active) {
      this.o.onEnd?.();
      return;
    }
    window.cancelAnimationFrame(drag.raf);
    drag.list.classList.remove(this.names.list);
    for (const row of drag.units.flat()) {
      row.style.transform = "";
      row.classList.remove(this.names.row, this.names.target);
    }
    // A click is what a press without a drag is; this one moved, so the link
    // under it must not open. A touch drag has no click to swallow, and the
    // flag lasts one task so the next tap lands.
    this.swallow = true;
    window.setTimeout(() => { this.swallow = false; }, 0);
    const groupRow = commit && drag.groupTarget >= 0 ? drag.rows[drag.groupTarget] : null;
    const moved = commit && !groupRow && drag.toIndex !== drag.fromIndex;
    if (moved) {
      const others = drag.rows.filter((row) => row !== drag.row);
      const anchor = others[drag.toIndex] || this.o.endAnchor?.(drag.row, others) || null;
      for (const row of drag.units[drag.fromIndex]) drag.list.insertBefore(row, anchor);
    }
    if (groupRow || moved) {
      this.o.onDrop?.({
        row: drag.row,
        rows: drag.rows,
        fromIndex: drag.fromIndex,
        toIndex: drag.toIndex,
        groupRow,
      });
    }
    this.o.onEnd?.();
  }
}
