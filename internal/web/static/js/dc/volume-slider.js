// The audio gain one full row means. scriptune keeps its master gain at a
// tenth of the value it stores, so a jingle at full does not tear anyone's
// ears off, and the assistant's speech goes through the same base: otherwise
// the same percentage would mean two very different loudnesses inside one
// product. Speech arrives peak normalised (measured: peak 0 dB, mean -15 dB),
// so this base puts a full row at -20 dB below that. It is one number, in one
// place, for both controls.
export const GAIN_BASE = 0.1;

// gainFor maps a row's value onto the gain an audio node should carry.
export function gainFor(value) {
  return value * GAIN_BASE;
}

// The volume row every volume control in the cockpit wears: an icon that
// follows the level, a range, and the reading beside it. Where the value comes
// from and what a move does with it is the subclass's business, the look and
// the behaviour are not, so the notification sound and the assistant's voice
// stay the same control to whoever uses them.
export class VolumeSlider extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();

    this.innerHTML = `
      <div class="d-flex align-items-center gap-3">
        <i class="ti fs-2 text-secondary" data-volume-icon></i>
        <input type="range" class="form-range flex-fill" min="0" max="1" step="0.1" aria-label="${this.ariaLabel()}">
        <span class="badge bg-primary-lt text-center" data-volume-output style="width: 3.5rem;"></span>
      </div>`;

    this.icon = this.querySelector("[data-volume-icon]");
    this.range = this.querySelector("input");
    this.output = this.querySelector("[data-volume-output]");
    this.range.value = this.read();
    this.render();

    this.range.addEventListener("input", () => {
      const value = parseFloat(this.range.value);
      if (!Number.isFinite(value)) return;
      this.write(value);
      this.render();
      this.preview(value);
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  render() {
    const volume = this.read();
    this.output.textContent = `${Math.round(volume * 100)}%`;
    const icon = volume === 0 ? "ti-volume-off" : volume > 0.6 ? "ti-volume" : "ti-volume-2";
    this.icon.className = `ti ${icon} fs-2 text-secondary`;
  }

  // What a subclass fills in: where the value lives, what a move does with it,
  // what to play back afterwards, and what the range is called. The defaults
  // are a control that shows full and remembers nothing.
  read() {
    return 1;
  }

  write() {}

  preview() {}

  ariaLabel() {
    return "Volume";
  }
}
