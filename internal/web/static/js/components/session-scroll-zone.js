(() => {
  // Proportional swipe scrolling. While the finger is down, travel is streamed
  // as pixel deltas through the session-scroll event, the consumer in
  // session-attach.js converts pixels into per-program scroll steps, so content
  // tracks the finger 1:1 and a gesture can never scroll further than the
  // finger moved. A fast release continues as a fling whose velocity decays
  // exponentially, a touch during the fling catches it.
  // The scroll runs over the network (steps become input POSTs, feedback comes
  // back via the SSE stream), so a fling keeps producing steps for one round
  // trip after a catch. The fling start velocity is therefore capped so that
  // this in-flight glide stays under a fraction of the screen at the measured
  // round trip time, on a fast link the cap never binds, on a slow one the
  // fling slows down instead of overshooting.
  const AXIS_LOCK_PX = 12; // travel to commit to an axis
  const TAP_MOVE_MAX_PX = 24; // travel that turns a tap into a swipe (no focus)
  const TAP_MAX_MS = 300; // longer than this is a long-press (select), not a dismiss tap
  const VELOCITY_WINDOW_MS = 100; // pointer samples that feed the release velocity
  const VELOCITY_MIN_SPAN_MS = 15; // shorter sample spans give no usable velocity
  const FLING_START_VELOCITY = 0.35; // px/ms release speed that starts a fling
  const FLING_STOP_VELOCITY = 0.04; // px/ms, the fling ends below this
  const FLING_DECAY_TAU_MS = 325; // exponential decay time constant
  const FLING_MAX_VELOCITY = 4; // px/ms hard ceiling, even on a LAN
  const FLING_CAP_MIN_VELOCITY = 0.5; // px/ms floor, keeps flings usable on bad links
  const FLING_OVERSHOOT_FRACTION = 0.4; // of the zone height in flight at the rtt
  const RTT_DEFAULT_MS = 150; // assumed round trip before the first measurement
  const RTT_SAMPLE_MIN_MS = 30;
  const RTT_SAMPLE_MAX_MS = 2000;
  const RTT_EWMA_WEIGHT = 0.3;

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
      this.samples = []; // recent {t, y} pointer positions for the release velocity
      this.flingVelocity = 0;
      this.flingFrame = null;
      this.flingLastTime = 0;
      this.suppressTap = false;
      this.rtt = RTT_DEFAULT_MS;
      this.pendingTap = null;
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
      this.samples = [];
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
      this.zone.addEventListener("lostpointercapture", () => this.cancelDrag(), options);
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
      // Every input POST reports its round trip (session-input.js), the EWMA
      // feeds the fling velocity cap.
      document.addEventListener("session-input-latency", (event) => {
        const ms = Number(event.detail?.ms);
        if (!Number.isFinite(ms)) {
          return;
        }
        const sample = Math.min(RTT_SAMPLE_MAX_MS, Math.max(RTT_SAMPLE_MIN_MS, ms));
        this.rtt = this.rtt * (1 - RTT_EWMA_WEIGHT) + sample * RTT_EWMA_WEIGHT;
      }, options);
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
      // A touch anywhere on the terminal catches a running fling, and that
      // touch must not double as a focus or dismiss tap.
      if (this.flingFrame !== null) {
        this.stopFling();
        this.suppressTap = true;
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
        this.suppressTap = false;
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
      if (this.suppressTap) {
        this.suppressTap = false;
        return;
      }
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
      this.suppressTap = false;
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
      if (this.flingFrame !== null) {
        this.stopFling();
        this.suppressTap = true;
      }
      this.cancelDrag();
      this.pointerID = event.pointerId;
      this.startX = event.clientX;
      this.startY = event.clientY;
      this.lastX = event.clientX;
      this.lastY = event.clientY;
      this.axis = null;
      this.samples = [{ t: event.timeStamp, y: event.clientY }];
      try {
        this.zone.setPointerCapture(this.pointerID);
      } catch (captureError) {
        void captureError;
      }
      this.consumePointerEvent(event);
    }

    handlePointerMove(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      const x = event.clientX;
      const y = event.clientY;
      const dy = y - this.lastY;
      this.lastX = x;
      this.lastY = y;
      this.recordSample(event.timeStamp, y);
      if (this.axis === "h") {
        return; // committed to a horizontal swipe: not a scroll
      }
      if (this.axis === null) {
        const totalX = x - this.startX;
        const totalY = y - this.startY;
        if (Math.hypot(totalX, totalY) < AXIS_LOCK_PX) {
          return; // still inside the start deadzone
        }
        if (Math.abs(totalY) < Math.abs(totalX)) {
          this.axis = "h";
          return;
        }
        this.axis = "v";
        this.emitScroll(totalY, true); // include the deadzone travel
        return;
      }
      if (dy !== 0) {
        this.emitScroll(dy, false);
      }
    }

    handlePointerUp(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      const vertical = this.axis === "v";
      const velocity = vertical ? this.releaseVelocity(event.timeStamp) : 0;
      this.cancelDrag();
      if (vertical) {
        this.maybeFling(velocity);
      }
    }

    recordSample(t, y) {
      this.samples.push({ t, y });
      while (this.samples.length > 1 && t - this.samples[0].t > VELOCITY_WINDOW_MS) {
        this.samples.shift();
      }
    }

    // Velocity over the recent sample window, in finger px/ms. A finger that
    // rested before lifting leaves only stale samples, that is not a fling.
    releaseVelocity(now) {
      const recent = this.samples.filter((sample) => now - sample.t <= VELOCITY_WINDOW_MS);
      if (recent.length < 2) {
        return 0;
      }
      const first = recent[0];
      const last = recent[recent.length - 1];
      const span = last.t - first.t;
      if (span < VELOCITY_MIN_SPAN_MS) {
        return 0;
      }
      return (last.y - first.y) / span;
    }

    // The glide after a catch is roughly velocity times round trip, capping the
    // start velocity bounds it to a fraction of the visible screen.
    flingVelocityCap() {
      const height = this.zone?.clientHeight || 600;
      const cap = (FLING_OVERSHOOT_FRACTION * height) / Math.max(this.rtt, 1);
      return Math.min(FLING_MAX_VELOCITY, Math.max(FLING_CAP_MIN_VELOCITY, cap));
    }

    maybeFling(velocity) {
      if (Math.abs(velocity) < FLING_START_VELOCITY) {
        return;
      }
      const cap = this.flingVelocityCap();
      this.flingVelocity = Math.max(-cap, Math.min(cap, velocity));
      this.flingLastTime = performance.now();
      this.flingFrame = window.requestAnimationFrame((now) => this.flingStep(now));
    }

    flingStep(now) {
      const dt = Math.max(0, now - this.flingLastTime);
      this.flingLastTime = now;
      const decay = Math.exp(-dt / FLING_DECAY_TAU_MS);
      const dy = this.flingVelocity * FLING_DECAY_TAU_MS * (1 - decay);
      this.flingVelocity *= decay;
      if (dy !== 0) {
        this.emitScroll(dy, false);
      }
      if (Math.abs(this.flingVelocity) < FLING_STOP_VELOCITY) {
        this.flingFrame = null;
        return;
      }
      this.flingFrame = window.requestAnimationFrame((next) => this.flingStep(next));
    }

    stopFling() {
      if (this.flingFrame !== null) {
        window.cancelAnimationFrame(this.flingFrame);
        this.flingFrame = null;
      }
      this.flingVelocity = 0;
    }

    // dy is finger travel in px, finger up (negative) scrolls toward newer
    // output. begin marks the first delta of a gesture so the consumer resets
    // its pixel accumulator.
    emitScroll(dy, begin) {
      this.dispatchEvent(new CustomEvent("session-scroll", {
        bubbles: true,
        composed: true,
        detail: { dy, begin, clientX: this.lastX, clientY: this.lastY },
      }));
    }

    // Ends the drag but keeps lastX/lastY, a fling started on release still
    // anchors its events at the lift-off point.
    cancelDrag() {
      if (this.pointerID !== null && this.zone?.hasPointerCapture(this.pointerID)) {
        this.zone.releasePointerCapture(this.pointerID);
      }
      this.pointerID = null;
      this.axis = null;
      this.samples = [];
    }

    stop() {
      this.stopFling();
      this.cancelDrag();
      this.suppressTap = false;
    }
  }

  customElements.define("session-scroll-zone", SessionScrollZone);
})();
