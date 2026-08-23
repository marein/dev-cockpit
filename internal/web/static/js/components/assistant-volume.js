import { VolumeSlider } from "@dc/volume-slider";

// The assistant's speech volume: the same row the notification sound wears,
// stored per device, and the surface above is told about a move through a
// bubbling event. The value only travels, the audio element it lands on
// belongs to the assistant.
const VOICE_VOLUME_KEY = "dc-assistant-voice-volume";

// On iOS the hardware buttons own an audio element's loudness: the volume
// property takes a value and reads it back, playback ignores it, so no probe
// on this side of the speaker can tell. A platform check is what remains. The
// second half catches iPadOS, which reports itself as a Mac and is told apart
// by its touch screen.
function onIOS() {
  return /iP(hone|ad|od)/.test(navigator.userAgent)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

class AssistantVolume extends VolumeSlider {
  // On iOS a quiet line under the row says where the loudness actually
  // lives, the way the sort dropdown on the start page explains itself.
  connectedCallback() {
    super.connectedCallback();
    if (!this.range || !onIOS()) return;
    const note = document.createElement("div");
    note.className = "text-secondary small mt-1";
    note.textContent = "On iOS the volume follows the hardware buttons.";
    this.append(note);
  }

  read() {
    let stored = null;
    try {
      stored = localStorage.getItem(VOICE_VOLUME_KEY);
    } catch {
      stored = null;
    }
    const percent = Number(stored);
    if (stored === null || !Number.isFinite(percent) || percent < 0 || percent > 100) {
      return 1;
    }
    return percent / 100;
  }

  write(value) {
    try {
      localStorage.setItem(VOICE_VOLUME_KEY, String(Math.round(value * 100)));
    } catch {
      void 0;
    }
    this.dispatchEvent(new CustomEvent("dc-volume-change", { bubbles: true, detail: { value } }));
  }

  ariaLabel() {
    return "Voice volume";
  }
}

customElements.define("dc-assistant-volume", AssistantVolume);
