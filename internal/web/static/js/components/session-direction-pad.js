import { createRepeater } from "@dc/repeater";

const REPEAT_INITIAL_DELAY = 400;
const REPEAT_INTERVAL = 60;
// Absolute drag distance (px) the pointer must travel from the button centre
// before a direction triggers. Decoupled from the small button size so the
// swipe needs a deliberate reach (pointer capture tracks travel past the edge).
const DIRECTION_DEAD_ZONE_PX = 24;
const DIRECTION_AXIS_LOCK_RATIO = 1.5;
const KEY_DIRECTIONS = {
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
};

class SessionDirectionPad extends HTMLElement {
  connectedCallback() {
    if (this.button) {
      return;
    }
    this.pointerID = null;
    this.activeControl = "";
    this.controls = this.readControls();
    this.abortController = new AbortController();
    this.repeater = this.startRepeater(() => this.fireControl());
    this.style.display = "inline-flex";
    this.style.flex = "0 0 auto";

    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-outline-secondary btn-icon btn-sm";
    button.setAttribute("aria-label", this.getAttribute("label") || "Directional controls");
    button.title = this.getAttribute("title") || "Drag toward a direction";
    button.style.overscrollBehavior = "contain";
    button.style.touchAction = "none";

    const icon = document.createElement("i");
    icon.className = `ti ${this.iconClass()}`;
    icon.setAttribute("aria-hidden", "true");
    button.append(icon);
    this.replaceChildren(button);
    this.button = button;

    this.addButtonListeners();
  }

  disconnectedCallback() {
    this.stop();
    this.abortController?.abort();
    this.abortController = null;
    this.button = null;
  }

  addButtonListeners() {
    const options = { signal: this.abortController.signal };
    this.button.addEventListener("pointerdown", (event) => this.handlePointerDown(event), options);
    this.button.addEventListener("pointermove", (event) => this.handlePointerMove(event), options);
    this.button.addEventListener("pointerup", () => this.stop(), options);
    this.button.addEventListener("pointercancel", () => this.stop(), options);
    this.button.addEventListener("lostpointercapture", () => this.stop(), options);
    this.button.addEventListener("blur", () => this.stop(), options);
    this.button.addEventListener("touchstart", this.preventTouchScroll, { passive: false, signal: this.abortController.signal });
    this.button.addEventListener("touchmove", this.preventTouchScroll, { passive: false, signal: this.abortController.signal });
    this.button.addEventListener("keydown", (event) => this.handleKeyDown(event), options);
    this.button.addEventListener("keyup", (event) => this.handleKeyUp(event), options);
  }

  preventTouchScroll(event) {
    event.preventDefault();
  }

  readControls() {
    return {
      up: this.controlAttribute("up-control", "arrow-up"),
      down: this.controlAttribute("down-control", "arrow-down"),
      left: this.controlAttribute("left-control", "arrow-left"),
      right: this.controlAttribute("right-control", "arrow-right"),
    };
  }

  controlAttribute(name, fallback) {
    if (this.hasAttribute(name)) {
      return (this.getAttribute(name) || "").trim();
    }
    return fallback;
  }

  iconClass() {
    const icon = (this.getAttribute("icon") || "arrows-move").trim();
    if (icon.startsWith("ti-")) {
      return icon;
    }
    return `ti-${icon}`;
  }

  startRepeater(fire) {
    return createRepeater(fire, REPEAT_INITIAL_DELAY, REPEAT_INTERVAL);
  }

  controlFromPoint(clientX, clientY) {
    const rect = this.button.getBoundingClientRect();
    const x = clientX - rect.left - rect.width / 2;
    const y = clientY - rect.top - rect.height / 2;
    const absX = Math.abs(x);
    const absY = Math.abs(y);
    const deadZone = DIRECTION_DEAD_ZONE_PX;
    if (Math.hypot(x, y) < deadZone) {
      return "";
    }
    if (absX > absY * DIRECTION_AXIS_LOCK_RATIO) {
      return x > 0 ? this.controls.right : this.controls.left;
    }
    if (absY > absX * DIRECTION_AXIS_LOCK_RATIO) {
      return y > 0 ? this.controls.down : this.controls.up;
    }
    return "";
  }

  fireControl() {
    if (!this.activeControl) {
      return;
    }
    this.dispatchEvent(new CustomEvent("session-control", {
      bubbles: true,
      detail: { control: this.activeControl },
    }));
  }

  setControl(event) {
    const next = this.controlFromPoint(event.clientX, event.clientY);
    if (next === this.activeControl) {
      return;
    }
    this.activeControl = next;
    this.fireControl();
  }

  stop() {
    this.repeater?.stop();
    this.activeControl = "";
    if (this.pointerID === null || !this.button) {
      this.pointerID = null;
      return;
    }
    if (this.button.hasPointerCapture(this.pointerID)) {
      this.button.releasePointerCapture(this.pointerID);
    }
    this.pointerID = null;
  }

  handlePointerDown(event) {
    if (event.button !== undefined && event.button !== 0) {
      return;
    }
    event.preventDefault();
    this.pointerID = event.pointerId;
    this.button.setPointerCapture(this.pointerID);
    this.activeControl = this.controlFromPoint(event.clientX, event.clientY);
    this.repeater.start();
  }

  handlePointerMove(event) {
    if (this.pointerID !== event.pointerId) {
      return;
    }
    event.preventDefault();
    this.setControl(event);
  }

  handleKeyDown(event) {
    if (event.repeat) {
      return;
    }
    const direction = KEY_DIRECTIONS[event.key];
    const control = this.controls[direction];
    if (!control) {
      return;
    }
    event.preventDefault();
    this.activeControl = control;
    this.repeater.start();
  }

  handleKeyUp(event) {
    const direction = KEY_DIRECTIONS[event.key];
    if (this.controls[direction] === this.activeControl) {
      this.stop();
    }
  }
}

customElements.define("session-direction-pad", SessionDirectionPad);
