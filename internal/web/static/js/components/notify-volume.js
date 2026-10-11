import { getPlayer } from "@marein/js-scriptune";
import { currentJingle, playJingle } from "@dc/jingle";
import { VolumeSlider } from "@dc/volume-slider";

const notification = getPlayer("notification");

// The notification sound's volume: the shared row, stored as the notification
// player's volume, and every move plays the picked jingle so the level is
// audible while it is chosen.
class NotifyVolume extends VolumeSlider {
  read() {
    return notification.getVolume();
  }

  write(value) {
    notification.setVolume(value);
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
