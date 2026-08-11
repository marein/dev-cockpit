// A double tap on a bare modifier key, the machine behind the editor's double
// Shift and the terminal switcher's double Ctrl/Meta. A tap counts only when
// the key goes down and up again with no other key in between and the hold
// stays under TAP_HOLD_MS, so a held modifier never counts. The gesture is two
// such taps in a row, the second one starting within TAP_WINDOW_MS of the
// first tap's keyup, and it fires on the keyup that completes the second tap:
// a second press that is held or interrupted does not trigger. keydown() only
// tracks and never fires, keyup() answers true exactly when the gesture
// completes. The caller filters the events: keydown() takes only a clean
// candidate key, every other keydown goes through reset(), and keyup() sees
// every keyup.
export const TAP_HOLD_MS = 250;
export const TAP_WINDOW_MS = 300;

export class DoubleTap {
  constructor() {
    this.reset();
  }

  reset() {
    this.pending = null;
    this.downAt = 0;
    this.second = false;
    this.armed = null;
    this.armedAt = 0;
  }

  keydown(key) {
    this.second = this.armed === key && Date.now() - this.armedAt < TAP_WINDOW_MS;
    this.pending = key;
    this.downAt = Date.now();
    this.armed = null;
  }

  keyup(key) {
    const clean = this.pending === key && Date.now() - this.downAt < TAP_HOLD_MS;
    this.pending = null;
    if (!clean) {
      this.second = false;
      return false;
    }
    if (this.second) {
      this.reset();
      return true;
    }
    this.armed = key;
    this.armedAt = Date.now();
    return false;
  }
}
