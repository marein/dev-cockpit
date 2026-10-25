// Where a conversation stands against its end, the owner's page and a guest's
// alike: whether the reader follows the end, what takes them away from it,
// and the button that brings them back.

// A zoomed page scrolls in fractions of a pixel, and the last one is not
// always reached.
const END_SLACK = 2;
// A wheel's smooth scroll and a finger's fling go on after their last event,
// the scrolls inside this window still belong to the reader.
const STEER_MS = 250;
// A finger resting on the glass trembles, a drag starts past this.
const DRAG_PX = 8;
const UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);
const SCROLL_KEYS = new Set([...UP_KEYS, "ArrowDown", "PageDown", "End", " "]);

// The reader lets go by what they do, never by where the page happens to
// stand: a page that grows under a streaming answer stands off its end
// between two frames, and the browser moves scrollTop on its own when it
// clamps. A follower lets go only once a move of theirs takes them more than
// END_SLACK above the place they stood at when it began, input alone that
// leaves them there, a wheel's tick or a finger short of the scroll, keeps
// them following. Standing at the real end pins again, however the reader got
// there: an overlay scrollbar, an autoscroll or a selection send nothing but
// scrolls. Every change of size is answered by the observer, which runs after
// the layout and before the paint of the same frame, so a follower never sees
// the end below the edge.
export class ChatFollow {
  constructor(scroller, { button = null, signal }) {
    this.scroller = scroller;
    this.button = button;
    this.pinned = true;
    this.readerLeft = false;
    this.ownTop = null;
    this.lastTop = scroller.scrollTop;
    this.lastHeight = scroller.scrollHeight;
    this.from = null;
    this.timer = 0;
    this.steeredAt = -Infinity;
    this.aim = Infinity;
    this.touch = null;
    this.dragging = false;
    const options = { signal, passive: true };
    scroller.addEventListener("wheel", (event) => this.onWheel(event), options);
    scroller.addEventListener("touchstart", (event) => this.onTouchStart(event), options);
    scroller.addEventListener("touchmove", (event) => this.onTouchMove(event), options);
    scroller.addEventListener("touchend", () => this.onTouchEnd(), options);
    scroller.addEventListener("touchcancel", () => this.onTouchEnd(), options);
    scroller.addEventListener("pointerdown", (event) => this.onPointerDown(event), options);
    window.addEventListener("pointerup", () => this.onPointerUp(), options);
    window.addEventListener("pointercancel", () => this.onPointerUp(), options);
    document.addEventListener("keydown", (event) => this.onKeydown(event), options);
    scroller.addEventListener("scroll", () => this.onScroll(), options);
    button?.addEventListener("click", (event) => {
      event.preventDefault();
      this.pin();
    }, { signal });
    this.sizer = new ResizeObserver(() => this.settle());
    this.sizer.observe(scroller);
    for (const child of scroller.children) this.sizer.observe(child);
    signal.addEventListener("abort", () => {
      this.sizer.disconnect();
      window.clearTimeout(this.timer);
    }, { once: true });
  }

  rest() {
    return this.scroller.scrollHeight - this.scroller.scrollTop - this.scroller.clientHeight;
  }

  // A size change alone can bring the reader to the end, a taller scroller
  // fires no scroll.
  settle() {
    if (this.pinned) {
      if (this.holding()) this.later();
      else this.toEnd();
    } else if (!this.steering(performance.now()) && this.rest() <= END_SLACK) this.pin();
    this.paint();
  }

  // A follower's move that has not left the end yet holds the page still, a
  // smooth scroll or a finger the page jumped under would stop or run on from
  // the new end. The end catches up once the move is over.
  holding() {
    return this.from !== null && this.steering(performance.now());
  }

  later() {
    if (this.timer) return;
    this.timer = window.setTimeout(() => {
      this.timer = 0;
      this.settle();
    }, STEER_MS);
  }

  toEnd() {
    this.from = null;
    this.place(() => { this.scroller.scrollTop = this.scroller.scrollHeight; });
  }

  // place runs a move of the page's own and notes where it ended, so the
  // scroll event it fires reads as the page's and not as the reader's.
  place(move) {
    move();
    this.ownTop = this.lastTop = this.scroller.scrollTop;
  }

  // keep runs a change that lays the page out on its way, a box measured at
  // one line before it grows, and puts the scroller back where it stood:
  // WebKit keeps the clamp to that passing layout, and the clamp is nobody's.
  keep(change) {
    const top = this.scroller.scrollTop;
    change();
    if (this.pinned) this.toEnd();
    else if (this.scroller.scrollTop !== top) this.place(() => { this.scroller.scrollTop = top; });
  }

  pin() {
    this.pinned = true;
    this.readerLeft = false;
    this.toEnd();
    this.paint();
  }

  // release lets go of the end. The page lets go too, for a landing on a
  // message or for notes landing under an answer, and only that kind of
  // letting go may be taken back by pinIfNear.
  release(byReader = false) {
    if (byReader) this.readerLeft = true;
    this.from = null;
    if (!this.pinned) return;
    this.pinned = false;
    this.paint();
  }

  pinIfNear(distance) {
    if (!this.pinned && !this.readerLeft && this.rest() < distance) this.pin();
  }

  // carry hands what the reader had to the surface that replaces this one,
  // adopt takes it over; the place itself is the surface's to restore, and a
  // place the new surface no longer holds lands at the end.
  carry(place) {
    return { pinned: this.pinned, readerLeft: this.readerLeft, place };
  }

  adopt(carried, restore) {
    if (carried.pinned || !carried.place) {
      this.pin();
      return;
    }
    this.release();
    if (!restore(carried.place)) {
      this.pin();
      return;
    }
    this.readerLeft = carried.readerLeft;
    this.paint();
  }

  paint() {
    if (this.button) this.button.hidden = this.pinned;
  }

  // The end a move of the reader aims at is the one that stood when it began:
  // a wheel's or a key's smooth scroll runs to that place, and an answer
  // growing meanwhile has moved the end on by the time it arrives.
  steer() {
    this.steeredAt = performance.now();
    this.ownTop = null;
    this.aim = this.scroller.scrollHeight - this.scroller.clientHeight - END_SLACK;
  }

  // hold notes where a follower stood as a move begins that may take them up,
  // the place their scrolls are measured from. That is the last place a
  // scroll or the page's own move read, not scrollTop: Chromium applies a
  // wheel's scroll before the main thread hears of the wheel.
  hold() {
    if (this.pinned && this.from === null) this.from = this.lastTop;
  }

  onWheel(event) {
    if (event.ctrlKey || !event.deltaY) return;
    this.steer();
    if (event.deltaY < 0) this.hold();
  }

  onTouchStart(event) {
    const touch = event.touches[0];
    this.touch = touch ? { x: touch.clientX, y: touch.clientY } : null;
    this.steer();
  }

  onTouchMove(event) {
    const touch = event.touches[0];
    if (!touch || !this.touch) return;
    this.steer();
    const dx = touch.clientX - this.touch.x;
    const dy = touch.clientY - this.touch.y;
    if (dy > DRAG_PX && dy >= Math.abs(dx)) this.hold();
  }

  onTouchEnd() {
    this.touch = null;
    this.steer();
  }

  // A press on the scrollbar arrives at the scroller itself, outside its
  // client area. An overlay scrollbar takes no room and reports nothing, its
  // drag reads in onScroll as a move nobody claimed.
  onPointerDown(event) {
    if (event.target !== this.scroller || event.offsetX < this.scroller.clientWidth) return;
    this.dragging = true;
    this.steer();
    this.hold();
  }

  onPointerUp() {
    if (!this.dragging) return;
    this.dragging = false;
    this.steer();
    if (this.pinned || this.rest() <= END_SLACK) this.pin();
  }

  onKeydown(event) {
    if (!SCROLL_KEYS.has(event.key) || event.ctrlKey || event.metaKey || event.altKey) return;
    const target = event.target;
    if (target !== document.body && !this.scroller.contains(target)) return;
    if (target.isContentEditable || target.closest?.("input, textarea, select")) return;
    this.steer();
    const up = UP_KEYS.has(event.key) || (event.key === " " && event.shiftKey);
    if (up) this.hold();
  }

  steering(now) {
    return !!this.touch || this.dragging || now - this.steeredAt < STEER_MS;
  }

  // A scroll that reads back the page's own last move is the page's, and a
  // clamp to a shorter end is nobody's. Any other is the reader's, by the
  // input before it or by a means this does not see, a scrollbar without a
  // gutter, find in page, an autoscroll. A follower it takes more than
  // END_SLACK above where they stood lets go, a smaller move up holds the page
  // still while it may go on. Away from the end, one right after the reader's
  // input that stands at the end it aimed for pins, and outside the reader's
  // input the real end pins, whoever moved there.
  onScroll() {
    const top = this.scroller.scrollTop;
    const height = this.scroller.scrollHeight;
    const end = this.rest() <= END_SLACK;
    const now = performance.now();
    const steering = this.steering(now);
    const own = this.ownTop !== null && Math.abs(top - this.ownTop) < 1;
    const clamped = end && height < this.lastHeight;
    const up = top < this.lastTop - 0.5 && !clamped;
    if (own || clamped) {
      if (!this.pinned && !steering && end) this.pin();
    } else if (this.pinned) {
      const from = this.from ?? this.lastTop;
      if (!end && top < from - END_SLACK) {
        this.release(true);
      } else if (up || steering) {
        if (up) this.from = from;
        this.steeredAt = now;
      }
    } else if (steering) {
      this.steeredAt = now;
      if (end || (!up && top >= this.aim)) this.pin();
      else this.release(true);
    } else if (end) {
      this.pin();
    } else if (up) {
      this.release(true);
    }
    this.lastTop = top;
    this.lastHeight = height;
    this.paint();
  }
}
