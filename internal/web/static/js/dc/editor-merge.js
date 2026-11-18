// A side keeps at least this much while the divider is dragged or the box
// narrows: its gutters and a few characters of code.
const MIN_SIDE_PX = 120;

// tieMergeView wires what the editor adds to a side by side view of
// @codemirror/merge: the two sides scrolling together sideways and the
// divider between them. Aborting the controller it returns takes all of it
// down, the view's DOM goes with the view.
export function tieMergeView(view, { onMoved, share, onShare, onDivider }) {
  const ac = new AbortController();
  syncSideways(view, onMoved, ac.signal);
  wireDivider(view, share, onShare, onDivider, ac.signal);
  return ac;
}

// The two sides of a merge view already share the vertical axis: the outer
// .cm-mergeView is their scroller and the editors grow to their full height,
// see fillsTheBox in the editor. The horizontal axis is the one they do not
// share, every editor keeps its own .cm-scroller for it, so with wrapping off
// the left column stands at one place and the right one at another and the
// two halves of a line are no longer next to each other. This ties them
// together on that axis alone and leaves the vertical scroller exactly where
// the package put it.
//
// Writing scrollLeft raises a scroll event of its own, and answering that
// one would send the value straight back. Comparing the two values instead
// of guarding is not enough either: a side whose longest line is shorter
// clamps what it is given, keeps answering with its own end and pulls its
// neighbour back there, which is a comparison that refuses to scroll past
// the shorter file's width. So a write that really moved the other side
// marks it, and that one event is spent instead of answered.
function syncSideways(view, onMoved, signal) {
  const sides = [view.a.scrollDOM, view.b.scrollDOM];
  const echo = new Set();
  view.dom.addEventListener("scroll", () => onMoved(), { signal, passive: true });
  for (const [index, from] of sides.entries()) {
    const to = sides[index === 0 ? 1 : 0];
    from.addEventListener("scroll", () => {
      onMoved();
      if (echo.delete(from)) return;
      const was = to.scrollLeft;
      if (was === from.scrollLeft) return;
      to.scrollLeft = from.scrollLeft;
      if (to.scrollLeft !== was) echo.add(to);
    }, { signal, passive: true });
  }
}

// The divider shares the width between the two sides, dragged with a mouse or
// a finger, and a double click evens it. The share is the left side's part of
// the room both have, so it reads the same on every width. It belongs to the
// tab, which hands it in with every build and gets every move back through
// onShare, because the end of a drag is not guaranteed to arrive: Chrome
// drops the pointer capture when the tab goes to the background. The pointer
// down stays here, the surface's swipe must not start under it. onDivider
// hears where the divider's middle stands from the view's left edge, in
// pixels, and 0 once the view is gone, so what stands above the two sides can
// follow it.
function wireDivider(view, initial, onShare, onDivider, signal) {
  const left = view.a.dom.parentNode;
  const right = view.b.dom.parentNode;
  const box = left.parentNode;
  const divider = document.createElement("div");
  divider.className = "editor-merge-divider";
  divider.dataset.editorMergeDivider = "";
  divider.setAttribute("role", "separator");
  divider.setAttribute("aria-orientation", "vertical");
  divider.title = "Drag to resize, double click to even";
  right.before(divider);
  let share = initial > 0 && initial < 1 ? initial : 0.5;
  let dragging = false;
  let placed = -1;
  const apply = (want) => {
    const room = box.clientWidth - divider.offsetWidth;
    const min = room > 0 ? Math.min(MIN_SIDE_PX, room / 2) / room : 0;
    const used = Math.min(Math.max(want, min), 1 - min);
    left.style.flexGrow = String(used);
    right.style.flexGrow = String(1 - used);
    divider.setAttribute("aria-valuenow", String(Math.round(used * 100)));
    const middle = room > 0 ? Math.round(used * room + divider.offsetWidth / 2) : 0;
    if (middle !== placed) onDivider(middle);
    placed = middle;
    return used;
  };
  const endDrag = () => {
    dragging = false;
    divider.classList.remove("active");
  };
  apply(share);
  divider.addEventListener("mousedown", (e) => e.preventDefault(), { signal });
  divider.addEventListener("pointerdown", (e) => {
    e.stopPropagation();
    if (e.pointerType === "mouse" && e.button !== 0) return;
    dragging = true;
    divider.classList.add("active");
    try {
      divider.setPointerCapture(e.pointerId);
    } catch (error) {
      void error;
    }
  }, { signal });
  divider.addEventListener("pointermove", (e) => {
    if (!dragging) return;
    if (e.buttons === 0) {
      endDrag();
      return;
    }
    const rect = box.getBoundingClientRect();
    const room = rect.width - divider.offsetWidth;
    if (room <= 0) return;
    share = apply((e.clientX - rect.left - divider.offsetWidth / 2) / room);
    onShare(share);
  }, { signal });
  divider.addEventListener("pointerup", endDrag, { signal });
  divider.addEventListener("pointercancel", endDrag, { signal });
  divider.addEventListener("lostpointercapture", endDrag, { signal });
  divider.addEventListener("dblclick", () => {
    share = apply(0.5);
    onShare(share);
  }, { signal });
  const watch = new ResizeObserver(() => apply(share));
  watch.observe(box);
  signal.addEventListener("abort", () => {
    watch.disconnect();
    onDivider(0);
  });
}
