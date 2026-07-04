import * as scriptune from "@marein/js-scriptune";
import { currentJingle, playJingle } from "@dc/jingle";

class NotifyVolume extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.previewAbort = null;

    this.innerHTML = `
      <div class="d-flex align-items-center gap-3">
        <i class="ti fs-2 text-secondary" data-volume-icon></i>
        <input type="range" class="form-range flex-fill" min="0" max="1" step="0.1" aria-label="Notification volume">
        <span class="badge bg-primary-lt text-center" data-volume-output style="width: 3.5rem;"></span>
      </div>`;

    this.icon = this.querySelector("[data-volume-icon]");
    this.range = this.querySelector("input");
    this.output = this.querySelector("[data-volume-output]");
    this.range.value = scriptune.getMasterVolume();
    this.render();

    this.range.addEventListener("input", () => {
      scriptune.setMasterVolume(parseFloat(this.range.value));
      this.render();
      this.preview();
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.previewAbort?.abort();
    this.ac?.abort();
    this.ac = null;
  }

  render() {
    const volume = scriptune.getMasterVolume();
    this.output.textContent = `${Math.round(volume * 100)}%`;
    const icon = volume === 0 ? "ti-volume-off" : volume > 0.6 ? "ti-volume" : "ti-volume-2";
    this.icon.className = `ti ${icon} fs-2 text-secondary`;
  }

  preview() {
    this.previewAbort?.abort();
    this.previewAbort = new AbortController();
    const picked = document.querySelector('input[name="jingle"]:checked');
    playJingle(picked ? picked.value : currentJingle(), { signal: this.previewAbort.signal }).catch(() => {});
  }
}

customElements.define("dc-notify-volume", NotifyVolume);
