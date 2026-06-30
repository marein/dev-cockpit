(() => {
  // A vertical swipe locks to a direction after this much travel, then repeats a
  // single per-program scroll step (the same step the wheel emits) on a timer —
  // discrete line-wise scrolling, no proportional momentum.
  // Repeat timing: a slow base rate that ramps faster the further the finger
  // travels past the lock threshold (push further = scroll faster).
  const REPEAT_INITIAL_DELAY = 350; // before the first repeat after the lock step
  const REPEAT_DELAY_SLOW = 260;    // ms between steps just past the lock threshold
  const REPEAT_DELAY_FAST = 55;     // ms between steps at full travel
  const REPEAT_RAMP_PX = 80;        // extra travel beyond the lock that reaches FAST
  const SCROLL_GESTURE_MIN_PX = 16; // travel to lock, and to flip after a reversal
  const TAP_MOVE_MAX_PX = 24; // travel that turns a tap into a swipe (no focus)
  const TAP_MAX_MS = 300; // longer than this is a long-press (select), not a dismiss tap

  class SessionScrollZone extends HTMLElement {
    static get observedAttributes() {
      return ["active"];
    }

    connectedCallback() {
      if (this.abortController) {
        return;
      }
      this.abortController = new AbortController();
      this.mediaQuery = window.matchMedia("(pointer: coarse)");
      this.pointerID = null;
      this.startX = 0;
      this.startY = 0;
      this.lastX = 0;
      this.lastY = 0;
      this.axis = null; // null until the gesture clears the deadzone; "v" or "h"
      this.direction = ""; // "" until locked to "up" or "down"
      this.extremeY = 0; // furthest point reached in the current direction
      this.turnY = 0; // where the current direction began (ramp origin)
      this.pendingTap = null;
      this.repeatTimer = null;
      this.render();
      this.zone = this.shadowRoot.querySelector(".zone");
      this.terminal = this.parentElement instanceof HTMLElement ? this.parentElement : null;
      this.addListeners();
      this.syncState();
    }

    disconnectedCallback() {
      this.stop();
      this.abortController?.abort();
      this.abortController = null;
      this.mediaQuery = null;
      this.terminal?.classList.remove("attach-terminal-copy-mode");
      this.terminal = null;
      this.zone = null;
      this.pointerID = null;
      this.pendingTap = null;
      this.direction = "";
      this.repeatTimer = null;
    }

    attributeChangedCallback(name, oldValue, newValue) {
      if (name !== "active" || oldValue === newValue) {
        return;
      }
      if (!this.isConnected) {
        return;
      }
      this.syncState();
      if (!this.hasAttribute("active")) {
        this.stop();
      }
    }

    render() {
      if (this.shadowRoot) {
        return;
      }
      this.attachShadow({ mode: "open" });
      this.shadowRoot.innerHTML = `
        <style>
          :host {
            display: none;
          }
          @media (pointer: coarse) {
            :host {
              position: absolute;
              top: 0;
              left: 0;
              z-index: 4;
              display: none;
              width: 100%;
              height: 100%;
              pointer-events: none;
            }
            :host([active]) {
              display: flex;
              align-items: center;
              justify-content: center;
            }
            .zone {
              width: 50%;
              height: 100%;
              background: transparent;
              pointer-events: auto;
              touch-action: none;
              overscroll-behavior: contain;
              user-select: none;
              -webkit-user-select: none;
            }
            @media (orientation: landscape) {
              .zone {
                width: 60%;
              }
            }
          }
        </style>
        <div class="zone"></div>
      `;
    }

    addListeners() {
      const options = { signal: this.abortController.signal };
      const captureOptions = { capture: true, passive: false, signal: this.abortController.signal };
      this.zone.addEventListener("pointerdown", (event) => this.handlePointerDown(event), options);
      this.zone.addEventListener("pointermove", (event) => this.handlePointerMove(event), options);
      this.zone.addEventListener("pointerup", (event) => this.handlePointerUp(event), options);
      this.zone.addEventListener("pointercancel", () => this.stop(), options);
      this.zone.addEventListener("lostpointercapture", () => this.stop(), options);
      this.zone.addEventListener("click", (event) => event.stopPropagation(), options);
      this.zone.addEventListener("touchstart", this.preventTouchScroll, { passive: false, signal: this.abortController.signal });
      this.zone.addEventListener("touchmove", this.preventTouchScroll, { passive: false, signal: this.abortController.signal });
      this.terminal?.addEventListener("touchstart", (event) => this.handleTerminalTouchStart(event), captureOptions);
      this.terminal?.addEventListener("touchmove", (event) => this.handleTerminalTouchMove(event), captureOptions);
      this.terminal?.addEventListener("touchend", (event) => this.handleTerminalTouchEnd(event), captureOptions);
      this.terminal?.addEventListener("touchcancel", () => this.handleTerminalTouchCancel(), captureOptions);
      for (const button of document.querySelectorAll("[data-session-copy]")) {
        button.addEventListener("click", () => this.toggle(), options);
      }
      this.mediaQuery?.addEventListener("change", () => this.handleMediaQueryChange(), options);
      window.addEventListener("blur", () => this.stop(), options);
      document.addEventListener("visibilitychange", () => {
        if (document.hidden) {
          this.stop();
        }
      }, options);
    }

    preventTouchScroll(event) {
      event.preventDefault();
    }

    focusTerminalInput() {
      if (!this.isActive()) {
        return;
      }
      const anchor = document.getElementById("session-cursor-input");
      if (!anchor) {
        return;
      }
      if (document.activeElement === anchor) {
        anchor.blur();
      } else {
        anchor.focus();
      }
    }

    blurTerminalInput() {
      const active = document.activeElement;
      if (active && (active.id === "session-prompt" || active.id === "session-cursor-input")) {
        active.blur();
      }
    }

    handleTerminalTouchStart(event) {
      if (!this.mediaQuery?.matches) {
        return;
      }
      const touch = event.changedTouches?.[0];
      if (!touch) {
        return;
      }
      this.pendingTap = {
        identifier: touch.identifier,
        startX: touch.clientX,
        startY: touch.clientY,
        startTime: Date.now(),
        moved: false,
      };
    }

    handleTerminalTouchMove(event) {
      if (!this.pendingTap) {
        return;
      }
      const touch = Array.from(event.changedTouches || []).find((entry) => entry.identifier === this.pendingTap.identifier);
      if (!touch) {
        return;
      }
      if (Math.hypot(touch.clientX - this.pendingTap.startX, touch.clientY - this.pendingTap.startY) > TAP_MOVE_MAX_PX) {
        this.pendingTap.moved = true;
      }
    }

    handleTerminalTouchEnd(event) {
      if (!this.mediaQuery?.matches || !this.pendingTap) {
        return;
      }
      const touch = Array.from(event.changedTouches || []).find((entry) => entry.identifier === this.pendingTap.identifier);
      if (!touch) {
        this.pendingTap = null;
        return;
      }
      const moved = this.pendingTap.moved
        || Math.hypot(touch.clientX - this.pendingTap.startX, touch.clientY - this.pendingTap.startY) > TAP_MOVE_MAX_PX;
      const quick = Date.now() - this.pendingTap.startTime < TAP_MAX_MS;
      this.pendingTap = null;
      if (moved) {
        return;
      }
      if (this.isActive()) {
        this.focusTerminalInput();
      } else if (quick) {
        this.clearSelection();
      }
    }

    clearSelection() {
      const selection = window.getSelection ? window.getSelection() : null;
      if (selection && !selection.isCollapsed) {
        selection.removeAllRanges();
      }
    }

    handleTerminalTouchCancel() {
      this.pendingTap = null;
    }

    handleMediaQueryChange() {
      this.syncState();
      if (!this.mediaQuery?.matches) {
        this.stop();
      }
    }

    isActive() {
      return this.hasAttribute("active") && Boolean(this.mediaQuery?.matches);
    }

    setActive(active) {
      this.toggleAttribute("active", active);
      this.syncState();
      if (!active) {
        this.stop();
      }
    }

    toggle() {
      if (!this.mediaQuery?.matches) {
        return;
      }
      this.setActive(!this.hasAttribute("active"));
    }

    syncState() {
      const active = this.isActive();
      const copyMode = Boolean(this.mediaQuery?.matches) && !active;
      this.setAttribute("aria-hidden", active ? "false" : "true");
      this.terminal?.classList.toggle("attach-terminal-copy-mode", copyMode);
      for (const button of document.querySelectorAll("[data-session-copy]")) {
        button.classList.toggle("active", copyMode);
        button.setAttribute("aria-pressed", copyMode ? "true" : "false");
      }
    }

    // Track direction relative to the turning point: while moving one way we
    // extend the extreme; a reversal of SCROLL_GESTURE_MIN_PX from that extreme
    // flips immediately — no need to unwind the whole prior swipe first.
    updateScrollDirection(y) {
      if (this.direction === "down") {
        if (y < this.extremeY) {
          this.extremeY = y; // extend the upward swipe
        } else if (y >= this.extremeY + SCROLL_GESTURE_MIN_PX) {
          this.lockDirection("up", this.extremeY, y, REPEAT_DELAY_SLOW);
        }
      } else if (this.direction === "up") {
        if (y > this.extremeY) {
          this.extremeY = y;
        } else if (y <= this.extremeY - SCROLL_GESTURE_MIN_PX) {
          this.lockDirection("down", this.extremeY, y, REPEAT_DELAY_SLOW);
        }
      }
    }

    // (re)lock to a direction: ramp distance is measured from turnY, and the
    // first step fires immediately so a reversal responds without delay.
    lockDirection(direction, turnY, y, initialDelay) {
      this.direction = direction;
      this.turnY = turnY;
      this.extremeY = y;
      this.startRepeat(initialDelay);
    }

    startRepeat(initialDelay) {
      this.stopRepeat();
      this.fireStep(); // one step immediately on (re)lock
      this.scheduleRepeat(initialDelay);
    }

    scheduleRepeat(delay) {
      this.repeatTimer = window.setTimeout(() => {
        this.fireStep();
        this.scheduleRepeat(this.repeatDelay());
      }, delay);
    }

    // The delay shrinks from SLOW toward FAST as the finger travels further past
    // the lock threshold, so a longer swipe scrolls faster.
    repeatDelay() {
      const over = Math.max(0, Math.abs(this.lastY - this.turnY) - SCROLL_GESTURE_MIN_PX);
      const t = Math.min(1, over / REPEAT_RAMP_PX);
      return REPEAT_DELAY_SLOW - (REPEAT_DELAY_SLOW - REPEAT_DELAY_FAST) * t;
    }

    stopRepeat() {
      if (this.repeatTimer !== null) {
        window.clearTimeout(this.repeatTimer);
        this.repeatTimer = null;
      }
    }

    isGestureEvent(event) {
      return this.isActive()
        && event.pointerType === "touch"
        && event.isPrimary
        && (event.button === undefined || event.button === 0);
    }

    consumePointerEvent(event) {
      event.preventDefault();
      event.stopPropagation();
    }

    handlePointerDown(event) {
      if (!this.isGestureEvent(event)) {
        return;
      }
      this.stop();
      this.pointerID = event.pointerId;
      this.startX = event.clientX;
      this.startY = event.clientY;
      this.lastX = event.clientX;
      this.lastY = event.clientY;
      this.axis = null;
      this.direction = "";
      this.extremeY = event.clientY;
      this.turnY = event.clientY;
      this.zone.setPointerCapture(this.pointerID);
      this.consumePointerEvent(event);
    }

    handlePointerMove(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      const x = event.clientX;
      const y = event.clientY;
      this.lastX = x;
      this.lastY = y;
      if (this.direction === "") {
        if (this.axis === "h") {
          return; // committed to a horizontal swipe: not a scroll
        }
        const dx = x - this.startX;
        const dy = y - this.startY;
        if (Math.hypot(dx, dy) < SCROLL_GESTURE_MIN_PX) {
          return; // still inside the start deadzone
        }
        if (Math.abs(dy) < Math.abs(dx)) {
          this.axis = "h";
          return;
        }
        // Finger up (dy<0) scrolls toward newer output (down); finger down -> up.
        this.lockDirection(dy < 0 ? "down" : "up", this.startY, y, REPEAT_INITIAL_DELAY);
        return;
      }
      this.updateScrollDirection(y);
    }

    handlePointerUp(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      this.stop();
    }

    fireStep(direction = this.direction) {
      if (!direction) {
        return;
      }
      // Drive one per-program scroll step (the same one the wheel emits). The
      // consumer in session-attach.js maps it per program: one line for
      // claude/vim/shell history, a page for copilot's pager.
      this.dispatchEvent(new CustomEvent("session-scroll", {
        bubbles: true,
        composed: true,
        detail: { step: direction, clientX: this.lastX, clientY: this.lastY },
      }));
    }

    stop() {
      this.stopRepeat();
      this.axis = null;
      this.direction = "";
      if (this.pointerID !== null && this.zone?.hasPointerCapture(this.pointerID)) {
        this.zone.releasePointerCapture(this.pointerID);
      }
      this.pointerID = null;
      this.startX = 0;
      this.startY = 0;
      this.lastX = 0;
      this.lastY = 0;
    }
  }

  customElements.define("session-scroll-zone", SessionScrollZone);
})();
