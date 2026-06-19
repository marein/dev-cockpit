(() => {
  // Finger travel before a swipe locks into vertical-scroll mode (vs a tap or a
  // horizontal swipe). Once locked, every move reports its incremental delta and
  // session-attach.js turns it into proportional, per-program scroll steps.
  const SCROLL_AXIS_LOCK_PX = 8;
  const DOUBLE_TAP_DELAY_MS = 350;
  const DOUBLE_TAP_MAX_PX = 24;

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
      this.lastY = 0;
      this.axis = null; // null until locked to "v" (scroll) or "h" (ignored)
      this.lastTap = null;
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
      this.lastTap = null;
      this.pendingTap = null;
      this.axis = null;
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
      this.terminal?.addEventListener("dblclick", (event) => this.handleDoubleClick(event), options);
      this.terminal?.addEventListener("touchstart", (event) => this.handleTerminalTouchStart(event), captureOptions);
      this.terminal?.addEventListener("touchmove", (event) => this.handleTerminalTouchMove(event), captureOptions);
      this.terminal?.addEventListener("touchend", (event) => this.handleTerminalTouchEnd(event), captureOptions);
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

    handleDoubleClick(event) {
      event.preventDefault();
      this.toggle();
    }

    registerTap(clientX, clientY) {
      const now = Date.now();
      const previousTap = this.lastTap;
      this.lastTap = { clientX, clientY, time: now };
      if (!previousTap) {
        return false;
      }
      if (now - previousTap.time > DOUBLE_TAP_DELAY_MS) {
        return false;
      }
      if (Math.hypot(clientX - previousTap.clientX, clientY - previousTap.clientY) > DOUBLE_TAP_MAX_PX) {
        return false;
      }
      this.lastTap = null;
      return true;
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
      if (Math.hypot(touch.clientX - this.pendingTap.startX, touch.clientY - this.pendingTap.startY) > DOUBLE_TAP_MAX_PX) {
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
        || Math.hypot(touch.clientX - this.pendingTap.startX, touch.clientY - this.pendingTap.startY) > DOUBLE_TAP_MAX_PX;
      this.pendingTap = null;
      if (moved) {
        this.lastTap = null;
        return;
      }
      if (this.registerTap(touch.clientX, touch.clientY)) {
        event.preventDefault();
        this.toggle();
      }
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
      this.setAttribute("aria-hidden", active ? "false" : "true");
      this.terminal?.classList.toggle("attach-terminal-copy-mode", Boolean(this.mediaQuery?.matches) && !active);
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
      this.lastY = event.clientY;
      this.axis = null;
      this.zone.setPointerCapture(this.pointerID);
      this.consumePointerEvent(event);
    }

    handlePointerMove(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      if (this.axis === null) {
        const dx = event.clientX - this.startX;
        const dy = event.clientY - this.startY;
        if (Math.hypot(dx, dy) < SCROLL_AXIS_LOCK_PX) {
          return; // still within the tap/deadzone
        }
        this.axis = Math.abs(dy) >= Math.abs(dx) ? "v" : "h";
        this.lastY = event.clientY; // reset baseline so the lock doesn't jump
      }
      if (this.axis !== "v") {
        return; // horizontal swipe: not a scroll
      }
      const ddy = event.clientY - this.lastY;
      this.lastY = event.clientY;
      if (ddy !== 0) {
        this.dispatchEvent(new CustomEvent("session-scroll", {
          bubbles: true,
          composed: true,
          detail: { dy: ddy, clientX: event.clientX, clientY: event.clientY },
        }));
      }
    }

    handlePointerUp(event) {
      if (this.pointerID !== event.pointerId) {
        return;
      }
      this.consumePointerEvent(event);
      this.stop();
    }

    stop() {
      if (this.pointerID !== null && this.zone?.hasPointerCapture(this.pointerID)) {
        this.zone.releasePointerCapture(this.pointerID);
      }
      this.pointerID = null;
      this.startX = 0;
      this.startY = 0;
      this.lastY = 0;
      this.axis = null;
    }
  }

  customElements.define("session-scroll-zone", SessionScrollZone);
})();
