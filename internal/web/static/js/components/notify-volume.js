import * as scriptune from "@marein/js-scriptune";
import { currentJingle, playJingle } from "@dc/jingle";
import { VolumeSlider } from "@dc/volume-slider";

// The notification sound's volume: the shared row, stored as scriptune's
// master volume, and every move plays the picked jingle so the level is
// audible while it is chosen.
class NotifyVolume extends VolumeSlider {
  read() {
    return scriptune.getMasterVolume();
  }

  write(value) {
    scriptune.setMasterVolume(value);
  }

  ariaLabel() {
    return "Notification volume";
  }

  preview() {
    this.previewAbort?.abort();
    this.previewAbort = new AbortController();
    const picked = document.querySelector('input[name="jingle"]:checked');
    playJingle(picked ? picked.value : currentJingle(), { signal: this.previewAbort.signal }).catch(() => {});
  }

  disconnectedCallback() {
    this.previewAbort?.abort();
    super.disconnectedCallback();
  }
}

customElements.define("dc-notify-volume", NotifyVolume);
