import { getPlayer } from "@marein/js-scriptune";
import { VolumeSlider } from "@dc/volume-slider";

const music = getPlayer("music");

export default class Volume extends VolumeSlider {
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
