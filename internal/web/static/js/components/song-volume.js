import { getPlayer } from "@marein/js-scriptune";
import { VolumeSlider } from "@dc/volume-slider";

const music = getPlayer("music");

class SongVolume extends VolumeSlider {
  connectedCallback() {
    if (this.ac) return;
    super.connectedCallback();
    // The music plays in one tab of the browser, the volume set in another
    // reaches it through the shared storage, so this row shows that move too.
    addEventListener("storage", () => {
      this.range.value = this.read();
      this.render();
    }, { signal: this.ac.signal });
  }

  read() {
    return music.getVolume();
  }

  write(value) {
    music.setVolume(value);
  }

  ariaLabel() {
    return "Volume";
  }
}

customElements.define("dc-song-volume", SongVolume);
