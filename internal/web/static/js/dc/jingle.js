import { createThrottledPlay, play } from "@marein/js-scriptune";

export const jingles = {
  arpeggio: `#BPM 168
#TRACK melody
#TYPE sine
#VOLUME 0.9
A4:s D5:s F#5:s A5:s D6:e
#TRACK pad
#TYPE triangle
#VOLUME 0.4
D3+A3:h`,
  doorbell: `#BPM 140
#TRACK melody
#TYPE sine
#VOLUME 0.9
E5:q C5:h
#TRACK pad
#TYPE triangle
#VOLUME 0.3
C3:w`,
  starlight: `#BPM 220
#TYPE triangle
#VOLUME 0.8
C6:s E6:s G6:s C7:e G6:e`,
  retro: `#BPM 240
#TYPE square
#VOLUME 0.5
G3:s C4:s E4:s G4:s C5:q`,
  calm: `#BPM 92
#TRACK melody
#TYPE sine
#VOLUME 0.8
G4:e D5:q
#TRACK pad
#TYPE triangle
#VOLUME 0.3
G3:h`,
};

export const defaultJingle = "arpeggio";

export function currentJingle() {
  const meta = document.querySelector('meta[name="dc-jingle"]');
  const name = meta ? meta.getAttribute("content") : "";
  return jingles[name] ? name : defaultJingle;
}

export function playJingle(name, options) {
  return play(jingles[name] || jingles[defaultJingle], options);
}

const players = {};

export function playNotification() {
  const name = currentJingle();
  players[name] ??= createThrottledPlay(jingles[name]);
  return players[name]("notification");
}
