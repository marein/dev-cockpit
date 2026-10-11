import { getPlayer } from "@marein/js-scriptune";
import { VolumeSlider } from "@dc/volume-slider";

const music = getPlayer("music");

class SongVolume extends VolumeSlider {
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
