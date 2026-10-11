// Kick, toms and the low drum are short pitch drops on a triangle. Hats, snare,
// clap, rim and shaker are bursts of scriptune's noise, white noise through a
// band pass at the note's pitch, each stepping down in volume for a decay. The
// snare's triangle body leads by one step, a track plays one note at a time. A
// drum track runs at eight times its tune's BPM, so every hit and every rest
// fills exactly one sixteenth of the lead.
const kit = {
  K: "#TYPE triangle\n#VOLUME 0.6\nA3:s E3:s A2:e E2:e -:e",
  S: "#TYPE triangle\n#VOLUME 0.3\nG3:s\n#TYPE noise\n#VOLUME 0.111\nC8:s\n#VOLUME 0.065\nC8:s\n#VOLUME 0.046\nC8:s\n#VOLUME 0.028\nC8:s\n#VOLUME 0.016\nC8:s\n-:e",
  H: "#TYPE noise\n#VOLUME 0.102\nA8:s\n#VOLUME 0.051\nA8:s\n#VOLUME 0.026\nA8:s\n-:s -:q",
  R: "#TYPE noise\n#VOLUME 0.102\nA8:s\n#VOLUME 0.051\nA8:s\n-:e\n#VOLUME 0.102\nA8:s\n#VOLUME 0.051\nA8:s\n-:e",
  O: "#TYPE noise\n#VOLUME 0.074\nG8:s\n#VOLUME 0.059\nG8:s\n#VOLUME 0.047\nG8:s\n#VOLUME 0.037\nG8:s\n#VOLUME 0.03\nG8:s\n#VOLUME 0.022\nG8:s\n#VOLUME 0.015\nG8:s\n#VOLUME 0.009\nG8:s",
  T: "#TYPE triangle\n#VOLUME 0.5\nD4:s A3:s F3:e D3:e -:e",
  P: "#TYPE noise\n#VOLUME 0.081\nG6:s -:s G6:s -:s G6:s\n#VOLUME 0.04\nG6:s\n#VOLUME 0.02\nG6:s\n-:s",
  B: "#TYPE triangle\n#VOLUME 0.45\nD3:s A2:s D2:e -:q",
  X: "#TYPE noise\n#VOLUME 0.119\nD7:s\n#VOLUME 0.038\nD7:s\n-:dq",
  Z: "#TYPE noise\n#VOLUME 0.029\nE8:s\n#VOLUME 0.06\nE8:s\n#VOLUME 0.029\nE8:s\n-:s -:q",
  ".": "-:h",
};

const pitchNames = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
const letterSteps = { C: 0, D: 2, E: 4, F: 5, G: 7, A: 9, B: 11 };
const accidentalSteps = { "#": 1, b: -1 };

function beat(pattern) {
  return [...pattern.replace(/\s/g, "")].map((step) => kit[step]).join("\n");
}

function normalize(sheet) {
  const volumes = [...sheet.matchAll(/#VOLUME\s+([\d.]+)/g)].map((match) => Number(match[1]));
  const loudest = Math.max(...volumes);
  return sheet.replace(/#VOLUME\s+([\d.]+)/g, (_, volume) => `#VOLUME ${Number(volume) / loudest}`);
}

function transpose(notes, steps) {
  return notes.replace(/\b([A-G])([#b]?)([1-7])\b/g, (_, letter, accidental, octave) => {
    const pitch = Number(octave) * 12 + letterSteps[letter] + (accidentalSteps[accidental] ?? 0) + steps;
    return pitchNames[pitch % 12] + Math.floor(pitch / 12);
  });
}

// echoTail plays the last note of a song as a dotted eighth delay. A note has
// no release, so the repeats carry the decay, each quieter and on the other side.
function echoTail(note, volume) {
  return Array.from({ length: 8 }, (_, i) => `#VOLUME ${(volume * 0.6 ** i).toFixed(3)}\n#PAN ${i ? 0.6 * (-1) ** i : 0}\n${note}:e -:s`).join("\n");
}

// hold strikes notes once and lets them ring for whole bars, longer than the
// six beats the longest note value lasts.
function hold(notes, bars, bpm) {
  return `#BPM ${bpm / bars}\n${notes}:w\n#BPM ${bpm}`;
}

// unfold rolls a final chord up from its lowest note, every next note an eighth
// later on a voice of its own, since a track cuts a note off when its next one
// starts. The notes split volume, so the chord is never louder than one note of
// it. The chord starts at bar at of a part bars long, each note rings for a bar
// and its voice rests to the part's end, so it ends with every other track.
function unfold(notes, volume, at, bars) {
  const voice = (i) => `roll${i + 1}`;
  return {
    voices: Object.fromEntries(notes.map((_, i) => [voice(i), `#TYPE sine\n#VOLUME ${volume / notes.length}`])),
    parts: Object.fromEntries(notes.map((note, i) => [voice(i), `${"-:w ".repeat(at)}${"-:e ".repeat(i)}${note}:w ${"-:e ".repeat((bars - at - 1) * 8 - i)}`])),
  };
}

// song renders parts in the order of form into one track per voice plus the
// drums, "name*n" as a loop. Every part segment restarts from the voice's
// setup, so a directive inside a part never leaks into the next one, and a
// voice a part leaves out rests for the part's bars.
function song({ bpm, voices, parts, form }) {
  const order = form.trim().split(/\s+/).map((entry) => {
    const [name, times = "1"] = entry.split("*");
    return { part: parts[name], times: Number(times) };
  });
  const repeat = (times, notes) => (times > 1 ? `#LOOP ${times}\n${notes}\n#ENDLOOP` : notes);
  const tracks = Object.entries(voices).map(([voice, setup]) => [
    `#TRACK ${voice}`,
    ...order.map(({ part, times }) => repeat(times, `#BPM ${bpm}\n#PAN 0\n${setup}\n${part[voice] ?? "-:q ".repeat(part.bars * 4)}`)),
  ].join("\n"));
  const drums = [
    "#TRACK drums",
    `#BPM ${bpm * 8}`,
    "#PAN 0",
    ...order.map(({ part, times }) => repeat(times, beat(part.drums ?? ".".repeat(part.bars * 16)))),
  ].join("\n");
  return normalize([...tracks, drums].join("\n"));
}

export const songs = {
  ambermile: (() => {
    const cells = {
      Bm: "B3:s F#4:s D5:s F#4:s C#5:s F#4:s B4:s F#4:s", G: "G3:s D4:s B4:s D4:s A4:s D4:s G4:s D5:s",
      D: "D4:s A4:s F#5:s A4:s E5:s A4:s D5:s A4:s", A: "A3:s E4:s C#5:s E4:s B4:s E4:s A4:s E5:s",
      Em: "E4:s B4:s G5:s B4:s F#5:s B4:s E5:s B4:s",
    };
    const lows = { Bm: "B2:dh F#2:q", G: "G2:dh D3:q", D: "D2:dh F#2:q", A: "A2:dh C#3:q", Em: "E2:dh B2:q" };
    const chords = { Bm: "B3+D4+F#4:w", G: "G3+B3+D4:w", D: "A3+D4+F#4:w", A: "A3+C#4+E4:w", Em: "G3+B3+E4:w" };
    const pl = (p) => p.split(" ").map((c) => `${cells[c]} ${cells[c]}`).join(" ");
    const bs = (p) => p.split(" ").map((c) => lows[c]).join(" ");
    const pd = (p) => p.split(" ").map((c) => chords[c]).join(" ");
    const prog = "Bm G D A Bm G D A";
    const groove = "B.Z.X.ZZB.Z.X.Z.";
    return song({
      bpm: 100,
      voices: { keys: "#TYPE sine\n#VOLUME 0.5", pluck: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.07", bass: "#TYPE sine\n#VOLUME 0.5" },
      parts: {
        intro: { bars: 8, pluck: `#VOLUME 0.2\n${pl(prog)}`, pad: "-:w ".repeat(4) + pd("Bm G D A") },
        build: {
          bars: 8, pluck: pl(prog), pad: pd(prog), bass: bs(prog),
          drums: "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(4) + "B.Z.Z.Z.B.Z.Z.Z.".repeat(3) + "B.Z.Z.Z.B.ZXZ.ZZ",
        },
        theme: {
          bars: 8,
          keys: "F#5:h E5:q D5:q B4:dh D5:q A5:h F#5:q E5:q C#5:dh -:q F#5:h E5:q D5:q B4:h D5:q G5:q F#5:h E5:q D5:q C#5:h A4:h",
          pluck: pl(prog), pad: pd(prog), bass: bs(prog), drums: groove.repeat(7) + "B.Z.X.ZZB.ZXX.XZ",
        },
        drift: { bars: 8, pluck: pl("Em G Bm A Em G D A"), pad: pd("Em G Bm A Em G D A"), bass: bs("Em G Bm A Em G D A"), drums: groove.repeat(8) },
        break: {
          bars: 8,
          keys: "-:h G5:q F#5:q D5:w -:h F#5:q E5:q C#5:w -:h B5:q A5:q G5:w -:h F#5:q G5:q A5:w",
          pluck: `#VOLUME 0.22\n${pl("Em G Bm A Em G Bm A")}`, pad: pd("Em G Bm A Em G Bm A"),
          drums: ".".repeat(64) + "B.......B.......".repeat(4),
        },
        build2: {
          bars: 4, keys: "-:w -:w B4:q D5:q E5:q F#5:q A5:h G5:q E5:q",
          pluck: pl("Bm G D A"), pad: pd("Bm G D A"), bass: bs("Bm G D A"),
          drums: "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(2) + "B.Z.Z.Z.B.Z.Z.Z.B.Z.ZXZ.BZZZXZZZ",
        },
        outro: {
          bars: 7, keys: "F#5:h E5:q D5:q B4:dh D5:q A5:h F#5:q E5:q C#5:dh -:q F#5:h E5:q D5:q B4:w -:w",
          pluck: `${pl("Bm G D A G")} -:w -:w`, pad: `${pd("Bm G D A G")} B3+D4+F#4:dw -:h`, bass: `${bs("Bm G D A")} -:w -:w -:w`,
          drums: groove.repeat(2) + "B..............." + ".".repeat(64),
        },
      },
      form: "intro build theme*2 drift break build2 theme*2 outro",
    });
  })(),
  goldenhour: (() => {
    const cells = {
      D: "D4:s F#4:s A4:s C#5:s F#4:s A4:s C#5:s E5:s A4:s C#5:s E5:s F#5:s C#5:s E5:s F#5:s A5:s",
      Bm: "B3:s D4:s F#4:s A4:s D4:s F#4:s A4:s B4:s F#4:s A4:s B4:s D5:s A4:s B4:s D5:s F#5:s",
      G: "G3:s B3:s D4:s F#4:s B3:s D4:s F#4:s A4:s D4:s F#4:s A4:s B4:s F#4:s A4:s B4:s D5:s",
      A: "A3:s C#4:s E4:s F#4:s C#4:s E4:s F#4:s A4:s E4:s F#4:s A4:s C#5:s F#4:s A4:s C#5:s E5:s",
    };
    const lows = { D: "D2:dq A2:e -:q D3:q", Bm: "B2:dq F#2:e -:q B2:q", G: "G2:dq D3:e -:q G2:q", A: "A2:dq E2:e -:q A2:q" };
    const chords = { D: "D4+F#4+A4+C#5:w", Bm: "D4+F#4+A4+B4:w", G: "D4+F#4+G4+B4:w", A: "C#4+E4+F#4+A4:w" };
    const pl = (p) => p.split(" ").map((c) => cells[c]).join(" ");
    const bs = (p) => p.split(" ").map((c) => lows[c]).join(" ");
    const pd = (p) => p.split(" ").map((c) => chords[c]).join(" ");
    const prog = "D Bm G A D Bm G A";
    const cruise = "G A D Bm G A D Bm";
    const groove = "B..ZX.Z.B.Z.X.ZB";
    return song({
      bpm: 112,
      voices: { keys: "#TYPE sine\n#VOLUME 0.45", pluck: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.55" },
      parts: {
        intro: { bars: 8, pluck: `#VOLUME 0.2\n${pl(prog)}`, pad: "-:w ".repeat(4) + pd("D Bm G A") },
        build: {
          bars: 8, pluck: pl(prog), pad: pd(prog), bass: bs(prog),
          drums: "Z..Z..Z.Z..Z..Z.".repeat(4) + "B..Z..Z.B..Z..Z.".repeat(3) + "B..Z..Z.B..ZX.ZZ",
        },
        theme: {
          bars: 8,
          keys: "A5:q F#5:e E5:e F#5:h D5:q B4:q D5:q E5:q F#5:dq E5:e D5:h C#5:h E5:h A5:q F#5:e E5:e F#5:q A5:q B5:h A5:q F#5:q G5:q F#5:q E5:q D5:q E5:w",
          pluck: pl(prog), pad: pd(prog), bass: bs(prog), drums: groove.repeat(7) + "B..ZX.Z.B.ZXX.OZ",
        },
        cruise: {
          bars: 8,
          keys: "B4:h D5:q E5:q C#5:h A4:h F#5:dq E5:e D5:q C#5:q D5:w B4:h D5:q G5:q F#5:h E5:h D5:q E5:q F#5:q A5:q F#5:w",
          pluck: pl(cruise), pad: pd(cruise), bass: bs(cruise), drums: "B..ZX.Z.B.Z.X.Z.".repeat(8),
        },
        breakdown: {
          bars: 8, keys: "D5:w F#5:w E5:w C#5:w B4:w D5:w F#5:w E5:w",
          pluck: `#VOLUME 0.2\n${pl("Bm G D A Bm G D A")}`, pad: pd("Bm G D A Bm G D A"), bass: "-:w ".repeat(4) + bs("Bm G D A"),
          drums: ".".repeat(64) + "B.......B.......".repeat(3) + "B...B...BZZZBZZZ",
        },
        lift: {
          bars: 4, keys: "D5:w E5:w F#5:w G5:h E5:h", pluck: pl("G A G A"), pad: pd("G A G A"), bass: bs("G A G A"),
          drums: "Z..Z..Z.Z..Z..Z.Z..Z..Z.ZZZZZZZZZZZZZZZZZZZZZZZZB...B...B.B.BBBB",
        },
        outro: {
          bars: 9,
          keys: "A5:q F#5:e E5:e F#5:h D5:q B4:q D5:q E5:q F#5:dq E5:e D5:h C#5:h E5:h A5:q F#5:e E5:e F#5:h D5:q B4:q D5:q E5:q F#5:dq E5:e D5:dw -:w",
          pluck: `${pl("D Bm G A D Bm")} -:w -:w -:w`, pad: `${pd("D Bm G A D Bm G")} D4+F#4+A4+C#5:dw -:h`, bass: `${bs("D Bm G A D")} ${"-:w ".repeat(4)}`,
          drums: groove.repeat(3) + "B..............." + ".".repeat(80),
        },
      },
      form: "intro build theme*2 cruise*2 breakdown lift theme*2 outro",
    });
  })(),
  saltflats: (() => {
    const cells = {
      "F#m": "F#3:e C#4:e G#4:e A4:e C#5:e A4:e G#4:e C#4:e", D: "D3:e A3:e E4:e F#4:e A4:e F#4:e E4:e C#4:e",
      A: "A3:e E4:e B4:e C#5:e E5:e C#5:e B4:e E4:e", E: "E3:e B3:e F#4:e G#4:e B4:e G#4:e F#4:e B3:e",
    };
    const long = { "F#m": "F#2:w", D: "D2:w", A: "A2:w", E: "E2:w" };
    const moving = { "F#m": "F#2:dh C#3:q", D: "D2:dh F#2:q", A: "A2:dh C#3:q", E: "E2:dh B2:q" };
    const chords = { "F#m": "F#3+C#4+A4:w", D: "D3+A3+F#4:w", A: "A3+E4+C#5:w", E: "E3+B3+G#4:w" };
    const pl = (p) => p.split(" ").map((c) => cells[c]).join(" ");
    const bl = (p) => p.split(" ").map((c) => long[c]).join(" ");
    const bm = (p) => p.split(" ").map((c) => moving[c]).join(" ");
    const pd = (p) => p.split(" ").map((c) => chords[c]).join(" ");
    const prog = "F#m D A E F#m D A E";
    const motif = "C#6:q A5:q -:h -:q F#5:q E5:h C#6:q A5:q -:h -:q G#5:q B5:h C#6:q A5:q -:h -:q F#5:q E5:h E6:q C#6:q -:h -:q B5:q G#5:h";
    return song({
      bpm: 96,
      voices: { piano: "#TYPE sine\n#VOLUME 0.5", pluck: "#TYPE triangle\n#VOLUME 0.28", pad: "#TYPE sawtooth\n#VOLUME 0.07", bass: "#TYPE sine\n#VOLUME 0.45" },
      parts: {
        intro: { bars: 8, pad: pd(prog), pluck: `${"-:w ".repeat(4)}\n#VOLUME 0.2\n${pl("F#m D A E")}` },
        build1: { bars: 8, pad: pd(prog), pluck: `#VOLUME 0.24\n${pl(prog)}`, bass: bl(prog) },
        build2: { bars: 8, piano: motif, pad: pd(prog), pluck: pl(prog), bass: bl(prog), drums: "Z...Z...Z...Z...".repeat(8) },
        build3: { bars: 8, piano: motif, pad: pd(prog), pluck: pl(prog), bass: bm(prog), drums: "B...Z...B...Z.Z.".repeat(7) + "B...Z...B.Z.ZXZZ" },
        theme: {
          bars: 8,
          piano: "C#6:q A5:q B5:h A5:q F#5:q E5:h C#6:q E6:q B5:h G#5:dh -:q C#6:q A5:q B5:h A5:q F#5:q A5:h E6:q C#6:q B5:q A5:q G#5:h B5:h",
          pad: pd(prog), pluck: pl(prog), bass: bm(prog), drums: "B..ZX..ZB.Z.X..Z".repeat(7) + "B..ZX..ZB.Z.XZXZ",
        },
        break: {
          bars: 8, piano: "-:h C#6:h A5:w -:h F#5:h E5:w -:h C#6:h E6:w -:h B5:h G#5:w",
          pad: pd(prog), pluck: `#VOLUME 0.2\n${pl(prog)}`, drums: ".".repeat(112) + "Z.Z.Z.Z.ZZZZZZZZ",
        },
        outro: {
          bars: 6, piano: `C#6:q A5:q B5:h A5:q F#5:q E5:h C#6:q E6:q B5:h G#5:dh -:q C#6:q\n${echoTail("A5", 0.5)}\n-:q`,
          pad: `${pd("F#m D A E")}\n${hold("F#3+C#4+A4", 2, 96)}`, pluck: `${pl("F#m D A E")} -:w -:w`, bass: `${bm("F#m D A")} -:w -:w -:w`,
          drums: "B..ZX..ZB.Z.X..Z".repeat(2) + "B..............." + ".".repeat(48),
        },
      },
      form: "intro build1 build2 build3 theme*2 break theme outro",
    });
  })(),
  lowsun: (() => {
    const cells = {
      Em: "E4:s -:s B4:s E5:s -:s B4:s G4:s -:s E4:s -:s B4:s D5:s -:s B4:s G4:s B3:s",
      C: "C4:s -:s G4:s E5:s -:s G4:s B4:s -:s C4:s -:s G4:s D5:s -:s G4:s E4:s G3:s",
      Am: "A3:s -:s E4:s C5:s -:s E4:s G4:s -:s A3:s -:s E4:s B4:s -:s E4:s C4:s E3:s",
      Bm: "B3:s -:s F#4:s D5:s -:s F#4:s A4:s -:s B3:s -:s F#4:s C#5:s -:s F#4:s D4:s F#3:s",
    };
    const lows = {
      Em: "E2:e -:e E3:s -:s E2:e -:e B2:e D3:e -:e", C: "C3:e -:e C4:s -:s C3:e -:e G2:e B2:e -:e",
      Am: "A2:e -:e A3:s -:s A2:e -:e E3:e G2:e -:e", Bm: "B2:e -:e B3:s -:s B2:e -:e F#2:e A2:e -:e",
    };
    const chords = { Em: "E4+G4+B4+D5:w", C: "C4+E4+G4+B4:w", Am: "C4+E4+G4+A4:w", Bm: "D4+F#4+A4+B4:w" };
    const pl = (p) => p.split(" ").map((c) => cells[c]).join(" ");
    const bs = (p) => p.split(" ").map((c) => lows[c]).join(" ");
    const pd = (p) => p.split(" ").map((c) => chords[c]).join(" ");
    const prog = "Em C Am Bm Em C Am Bm";
    const turn = "C Bm Am Bm C Bm Am Bm";
    const groove = "BZZZXZZBZZBZXZZZ";
    return song({
      bpm: 118,
      voices: { keys: "#TYPE sine\n#VOLUME 0.5", pluck: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE square\n#VOLUME 0.05", bass: "#TYPE triangle\n#VOLUME 0.55" },
      parts: {
        intro: { bars: 8, pluck: `#VOLUME 0.2\n${pl(prog)}`, drums: ".".repeat(64) + "Z.ZZZ.ZZZ.ZZZ.ZZ".repeat(4) },
        build: {
          bars: 8, pluck: pl(prog), pad: pd(prog), bass: bs(prog),
          drums: "ZZZZZZZZZZZZZZZZ".repeat(4) + "BZZZZZZZBZZZZZZZ".repeat(3) + "BZZZXZZZBZZZXZXX",
        },
        theme: {
          bars: 8,
          keys: "E5:dq F#5:e G5:q B5:q A5:dq G5:e E5:h C6:q B5:q A5:q G5:q F#5:dh -:q E5:dq F#5:e G5:q B5:q D6:dq C6:e B5:h A5:q G5:q E5:q C5:q D5:dh -:q",
          pluck: pl(prog), pad: pd(prog), bass: bs(prog), drums: groove.repeat(7) + "BZZZXZZBZZBZXZXX",
        },
        cruise: { bars: 8, pluck: pl(prog), pad: pd(prog), bass: bs(prog), drums: groove.repeat(8) },
        break: {
          bars: 8, keys: "G5:w F#5:w E5:w F#5:h D5:h G5:w A5:w C6:h B5:h F#5:w",
          pluck: `#VOLUME 0.2\n${pl(turn)}`, pad: pd(turn), drums: "Z.ZZZ.ZZZ.ZZZ.ZZ".repeat(4) + ".".repeat(64),
        },
        build2: {
          bars: 8, keys: "E5:w G5:w A5:w B5:w C6:w B5:w A5:w F#5:w",
          pluck: pl(prog), pad: pd(prog), bass: "-:w ".repeat(4) + bs("Em C Am Bm"),
          drums: "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(2) + "ZZZZZZZZZZZZZZZZ".repeat(2) + "BZZZZZZZBZZZZZZZ".repeat(2) + "BZZZXZZZBZZZXZZZBZBZXZBZBZXZXXXX",
        },
        outro: {
          bars: 8,
          keys: `E5:dq F#5:e G5:q B5:q A5:dq G5:e E5:h C6:q B5:q A5:q G5:q F#5:dh -:q E5:dq F#5:e G5:q B5:q A5:dq G5:e\n${echoTail("E5", 0.5)}\n-:w`,
          pluck: `${pl("Em C Am Bm Em")} -:w -:w -:w`, pad: `${pd("Em C Am Bm Em C")} E4+G4+B4+D5:dw -:h`, bass: `${bs("Em C Am Bm")} ${"-:w ".repeat(4)}`,
          drums: groove.repeat(2) + "B..............." + ".".repeat(80),
        },
      },
      form: "intro build theme*2 cruise*2 break build2 theme*2 outro",
    });
  })(),
  paperlanterns: (() => {
    const cells = {
      A: "A3:e E4:e A4:e C#5:e E5:e C#5:e A4:e E4:e", E: "E3:e B3:e E4:e G#4:e B4:e G#4:e E4:e B3:e",
      "F#m": "F#3:e C#4:e F#4:e A4:e C#5:e A4:e F#4:e C#4:e", D: "D3:e A3:e D4:e F#4:e A4:e F#4:e D4:e F#3:e",
      Dm: "D3:e A3:e D4:e F4:e A4:e F4:e D4:e F3:e",
    };
    const lows = { A: "A2:dh E3:q", E: "E2:dh B2:q", "F#m": "F#2:dh C#3:q", D: "D2:dh F#2:q", Dm: "D2:dh F2:q" };
    const chords = { A: "A3+C#4+E4:w", E: "G#3+B3+E4:w", "F#m": "A3+C#4+F#4:w", D: "A3+D4+F#4:w", Dm: "A3+D4+F4:w" };
    const pl = (p) => p.split(" ").map((c) => cells[c]).join(" ");
    const bs = (p) => p.split(" ").map((c) => lows[c]).join(" ");
    const pd = (p) => p.split(" ").map((c) => chords[c]).join(" ");
    const prog = "A E F#m D A E Dm A";
    const bridge = "D Dm A F#m D Dm E E";
    const hook = "E6:e C#6:e A5:e C#6:e E6:e -:e A6:e -:e G#6:e -:e E6:e B5:e G#5:e B5:e E6:e -:e F#6:e -:e C#6:e A5:e F#5:e A5:e C#6:e -:e D6:e -:e A5:e F#5:e D6:e E6:e F#6:e -:e E6:e C#6:e A5:e C#6:e E6:e -:e A6:e -:e G#6:e -:e B6:e -:e G#6:e E6:e B5:e -:e F6:e -:e D6:e A5:e F5:e A5:e D6:e -:e C#6:q E6:q A5:h";
    const groove = "B...X..ZB.Z.X.Z.";
    return song({
      bpm: 104,
      voices: { box: "#TYPE sine\n#VOLUME 0.45", pluck: "#TYPE triangle\n#VOLUME 0.25", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE sine\n#VOLUME 0.5" },
      parts: {
        intro: { bars: 8, box: `#VOLUME 0.3\n${hook}`, pad: "-:w ".repeat(4) + pd("A E Dm A") },
        build: {
          bars: 8, pluck: pl(prog), pad: pd(prog), bass: bs(prog),
          drums: "Z...Z...Z...Z...".repeat(4) + "B...Z...B...Z.Z.".repeat(3) + "B...Z...B.Z.ZXZZ",
        },
        theme: { bars: 8, box: hook, pluck: pl(prog), pad: pd(prog), bass: bs(prog), drums: groove.repeat(7) + "B...X..ZB.Z.XZXZ" },
        bridge: {
          bars: 8,
          box: "F#6:q -:q D6:q -:q F6:q -:q D6:h E6:q -:q C#6:q -:q C#6:q -:q A5:h F#6:q -:q A6:q -:q F6:q -:q D6:q A5:q B5:q E6:q G#6:q -:q B6:dh -:q",
          pluck: pl(bridge), pad: pd(bridge), bass: bs(bridge), drums: "B.......X.......".repeat(7) + "B.......X...X.XZ",
        },
        break: {
          bars: 8, box: "C#6:h A5:h F#5:w A5:h D6:h F#6:w C#6:h A5:h E6:w F6:h D6:h A5:w",
          pluck: `#VOLUME 0.18\n${pl("F#m D A E F#m D Dm Dm")}`, pad: pd("F#m D A E F#m D Dm Dm"),
        },
        lift: {
          bars: 6, box: "B5:w C#6:w D6:w E6:w F#6:w G#6:w", pluck: pl("E E D D E E"), pad: pd("E E D D E E"), bass: bs("E E D D E E"),
          drums: "Z...Z...Z...Z...".repeat(2) + "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(2) + "B.Z.Z.Z.B.Z.Z.Z.B.Z.ZXZ.BZZZXZZZ",
        },
        outro: {
          bars: 9, box: `${hook.replace(/A5:h$/, "A5:w")} -:h`, pad: `${pd("A E F#m D A E Dm")}\n${hold("A3+C#4+E4", 2, 104)}`,
          pluck: `${pl("A E F#m D A E")} -:w -:w -:w`, bass: `${bs("A E F#m D A")} ${"-:w ".repeat(4)}`,
          drums: groove.repeat(3) + "B..............." + ".".repeat(80),
        },
      },
      form: "intro build theme*2 bridge break lift theme*2 outro",
    });
  })(),
  coastaldrive: (() => {
    const arp = {
      G: "G3:s D4:s G4:s B4:s D5:s B4:s G4:s D4:s ", D: "F#3:s D4:s F#4:s A4:s D5:s A4:s F#4:s D4:s ",
      Em: "E3:s B3:s E4:s G4:s B4:s G4:s E4:s B3:s ", C: "C3:s G3:s C4:s E4:s G4:s E4:s C4:s E4:s ",
    };
    const chord = { G: "G3+B3+D4:w ", D: "F#3+A3+D4:w ", Em: "E3+G3+B3:w ", C: "E3+G3+C4:w " };
    const low = { G: "G2:dq D3:e G2:h ", D: "F#2:dq D3:e F#2:h ", Em: "E2:dq B2:e E2:h ", C: "C2:dq G2:e C2:h " };
    const each = (map, chords, times = 1) => chords.split(" ").map((c) => map[c].repeat(times)).join("");
    const turn = "G D Em C G D Em C";
    const groove = "B.Z.X.Z.B.ZBX.Z.";
    const fill = "B.Z.X.Z.B.ZBX.XX";
    const band = { pluck: each(arp, turn, 2), pad: each(chord, turn), bass: each(low, turn) };
    const hook = "B4:e D5:e G5:q F#5:e E5:e D5:q -:e D5:e E5:e F#5:e A5:dq F#5:e G5:q E5:e D5:e B4:q -:e G4:e A4:e B4:e C5:e E5:e D5:h "
      + "B4:e D5:e G5:q A5:e G5:e D5:q -:e F#5:e A5:e D6:e C#6:dq A5:e B5:q G5:e E5:e D5:q B4:q C5:e D5:e E5:e G5:e G4:h";
    return song({
      bpm: 116,
      voices: { lead: "#TYPE sine\n#VOLUME 0.5", pluck: "#TYPE triangle\n#VOLUME 0.32", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5" },
      parts: {
        intro: { bars: 8, pluck: band.pluck },
        build: { bars: 8, pluck: band.pluck, pad: band.pad, bass: "-:w ".repeat(4) + each(low, "G D Em C"), drums: "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(6) + "Z.Z.Z.Z.B.Z.Z.Z.B.Z.Z.Z.B.Z.ZBX." },
        verse: { bars: 8, ...band, lead: "-:h D5:q B4:q A4:h F#4:q D4:q E4:h G4:q B4:q C5:h B4:q A4:q B4:dh -:q A4:h F#4:q A4:q G4:h E4:q G4:q A4:w", drums: groove.repeat(7) + fill },
        hook: { bars: 8, ...band, lead: hook, drums: groove.repeat(7) + fill },
        breakdown: {
          bars: 8,
          pluck: "B3:q E4:q G4:q E4:q C4:q E4:q G4:q E4:q G4:q B4:q D5:q B4:q F#4:q A4:q D5:q A4:q ".repeat(2),
          pad: "E3+G3+B3:w E3+G3+C4:w G3+B3+D4:w F#3+A3+D4:w ".repeat(2),
          bass: "E2:w C2:w G2:w D2:w ".repeat(2),
        },
        rise: { bars: 8, ...band, lead: "G4:w A4:w B4:w C5:w D5:w E5:w F#5:w G5:h -:h", drums: "B.......B.......".repeat(4) + "B.Z.Z.Z.B.Z.Z.Z.".repeat(3) + fill },
        outro: {
          bars: 8,
          lead: "B4:e D5:e G5:q F#5:e E5:e D5:q " + "-:w ".repeat(7),
          pluck: each(arp, "G D Em C G D", 2) + "G3:s D4:s G4:s B4:s D5:s B4:s G4:s D4:s G4:dh -:dh",
          pad: each(chord, "G D Em C G D G") + "-:w",
          bass: each(low, "G D Em C G D") + "G2:h -:h -:w",
          drums: groove.repeat(4) + "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(2) + "B..............." + ".".repeat(16),
        },
      },
      form: "intro build verse hook verse hook breakdown rise hook*2 outro",
    });
  })(),
  willowcreek: (() => {
    const keys = {
      Cm: "C#3:e G#3:e C#4:e E4:e G#4:e E4:e C#4:e G#3:e ", A: "A2:e E3:e A3:e C#4:e E4:e C#4:e A3:e C#4:e ",
      E: "E3:e B3:e E4:e G#4:e B4:e G#4:e E4:e B3:e ", B: "B2:e F#3:e B3:e D#4:e F#4:e D#4:e B3:e D#4:e ",
      Fm: "F#3:e C#4:e F#4:e A4:e C#5:e A4:e F#4:e C#4:e ", G: "G#2:e D#3:e G#3:e C4:e D#4:e C4:e G#3:e D#3:e ",
    };
    const chord = { Cm: "C#4+E4+G#4:w ", A: "C#4+E4+A4:w ", E: "B3+E4+G#4:w ", B: "B3+D#4+F#4:w ", Fm: "C#4+F#4+A4:w ", G: "C4+D#4+G#4:w " };
    const root = { Cm: "C#2:w ", A: "A2:w ", E: "E2:w ", B: "B2:w ", Fm: "F#2:w ", G: "G#2:w " };
    const each = (map, chords) => chords.split(" ").map((c) => map[c]).join("");
    const prog = "Cm A E B Fm Cm A G";
    const turn = "A B Cm Cm A B G G";
    const melody = "E5:q D#5:e C#5:e G#4:h A4:q B4:e C#5:e E5:h G#5:q F#5:e E5:e B4:h D#5:e E5:e F#5:q D#5:h "
      + "C#5:q A4:e B4:e C#5:q E5:q D#5:e C#5:e B4:q G#4:h A4:q C#5:q E5:q A5:q G#5:dq F#5:e C5:h";
    const low = "#TYPE triangle\n" + transpose(melody, -12);
    const soft = "B.....Z.X.....Z.";
    const full = "B.Z.Z.ZBX.Z.Z.Z.";
    const band = { keys: each(keys, prog), pad: each(chord, prog), bass: each(root, prog) };
    return song({
      bpm: 98,
      voices: { lead: "#TYPE sine\n#VOLUME 0.5", keys: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.05", bass: "#TYPE sine\n#VOLUME 0.45" },
      parts: {
        intro: { bars: 8, keys: band.keys },
        verse: { bars: 8, keys: band.keys, lead: low, pad: "-:w ".repeat(4) + each(chord, "Fm Cm A G") },
        hook: { bars: 8, ...band, lead: melody },
        interlude: { bars: 8, ...band, drums: soft.repeat(8) },
        verse2: { bars: 8, ...band, lead: low, drums: soft.repeat(7) + "B.....Z.X...X.ZX" },
        hook2: { bars: 8, ...band, lead: melody, drums: full.repeat(7) + "B.Z.Z.ZBX.Z.X.XX" },
        bridge: {
          bars: 8, keys: each(keys, turn), pad: each(chord, turn),
          bass: "A2:w B2:w C#2:w E2:w A2:w B2:w G#2:w D#2:w", lead: "C#5:w B4:w G#4:w E4:h -:h C#5:w D#5:w C5:w -:w",
        },
        outro: {
          bars: 8,
          keys: each(keys, "Cm A E B Cm") + "E4:h C#4:h -:w -:w",
          pad: each(chord, "Cm A E B Cm Cm Cm") + "-:w",
          bass: "C#2:w A2:w E2:w B2:w C#2:w C#3:w -:w -:w",
          lead: "#TYPE triangle\nE5:q D#5:e C#5:e G#4:h A4:q B4:e C#5:e E5:h " + "-:w ".repeat(6),
          drums: soft.repeat(2) + "B..............." + ".".repeat(80),
        },
      },
      form: "intro verse hook interlude verse2 hook2 bridge hook2 outro",
    });
  })(),
  driftwood: (() => {
    const chop = (dyad, top) => `-:e ${dyad}:e -:e ${dyad}:e -:e ${dyad}:s ${top}:s -:e ${dyad}:e `;
    const pluck = { F: chop("A3+C4", "F4"), Dm: chop("A3+D4", "F4"), Bb: chop("Bb3+D4", "F4"), C: chop("G3+C4", "E4") };
    const chord = { F: "A3+C4+E4:w ", Dm: "A3+D4+F4:w ", Bb: "Bb3+D4+F4:w ", C: "G3+C4+E4:w " };
    const each = (map, chords) => chords.split(" ").map((c) => map[c]).join("");
    const prog = "F Dm Bb C F Dm Bb C";
    const turn = "Bb C Dm F Bb C F C";
    const bassHook = "F2:de C3:s -:e F2:e A2:q G2:e C2:e D2:de A2:s -:e D2:e F2:q E2:e C2:e "
      + "Bb1:de F2:s -:e Bb1:e D2:q C2:e Bb1:e C2:de G2:s -:e C2:e E2:q G2:e Bb2:e ";
    const melody = "-:q A4:e C5:e F5:q E5:e C5:e D5:dq C5:e A4:h -:q F4:e A4:e Bb4:q D5:e F5:e E5:dq D5:e C5:h "
      + "-:q A4:e C5:e F5:q G5:e A5:e F5:dq E5:e D5:h Bb4:q C5:e Bb4:e A4:q G4:q E4:e F4:e G4:e Bb4:e A4:h";
    const groove = "B.Z.X.ZB..Z.X.ZZ";
    const fill = "B.Z.X.ZB..Z.X.XX";
    const band = { pluck: each(pluck, prog), pad: each(chord, prog), bass: bassHook.repeat(2) };
    return song({
      bpm: 108,
      voices: { lead: "#TYPE sine\n#VOLUME 0.45", pluck: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE square\n#VOLUME 0.05", bass: "#TYPE triangle\n#VOLUME 0.6" },
      parts: {
        intro: { bars: 8, pluck: band.pluck, drums: ".".repeat(64) + "Z.Z.Z.Z.Z.Z.Z.Z.".repeat(4) },
        groove: { bars: 8, pluck: band.pluck, bass: band.bass, drums: groove.repeat(7) + fill },
        verse: { bars: 8, ...band, lead: "#TYPE triangle\nC5:w A4:w Bb4:w G4:w C5:w D5:w Bb4:h A4:h G4:w", drums: groove.repeat(7) + fill },
        hook: { bars: 8, ...band, lead: melody, drums: "B.Z.X.ZB..ZHX.Z.".repeat(7) + fill },
        drop: { bars: 8, bass: band.bass, pad: "-:w ".repeat(4) + each(chord, "F Dm Bb C"), drums: groove.repeat(8) },
        bridge: {
          bars: 8, pluck: each(pluck, turn), pad: each(chord, turn),
          bass: "Bb1:w C2:w D2:w F2:w Bb1:w C2:w F2:w C2:w", lead: "F5:w E5:w D5:w C5:w Bb4:w A4:w G4:w -:w",
        },
        outro: {
          bars: 10, lead: `${melody.replace(/A4:h$/, "A4:dw")} -:w`, pad: `${each(chord, prog)}\n${hold("A3+C4+F4", 2, 108)}`,
          pluck: `${each(pluck, "F Dm Bb C F Dm")}${"-:w ".repeat(4)}`, bass: `${bassHook}${"-:w ".repeat(6)}`,
          drums: groove.repeat(2) + "B..............." + ".".repeat(112),
        },
      },
      form: "intro groove verse hook drop verse hook bridge hook outro",
    });
  })(),
  quietcanyon: (() => {
    const chord = {
      Dm: "D4+F4+A4:w C4+F4+A4:w ", Bb: "D4+F4+Bb4:w D4+F4+A4:w ", F: "C4+F4+A4:w C4+E4+A4:w ",
      C: "C4+E4+G4:w D4+E4+G4:w ", Gm: "D4+G4+Bb4:w D4+F4+Bb4:w ", A: "C#4+E4+A4:w C#4+E4+G4:w ",
    };
    const low = { Dm: "D2:w F2:w ", Bb: "Bb1:w D2:w ", F: "F2:w A2:w ", C: "C2:w E2:w ", Gm: "G2:w Bb2:w ", A: "A2:w E2:w " };
    const each = (map, chords) => chords.split(" ").map((c) => map[c]).join("");
    const call = "D5:e F5:e A5:e G5:e F5:q -:q -:w D5:e F5:e Bb5:e A5:e F5:q -:q -:w "
      + "C5:e F5:e A5:e G5:e F5:q -:q -:w E5:e G5:e C6:e Bb5:e G5:q -:q -:w";
    const answer = "-:w A4:e C5:e D5:e C5:e A4:q -:q -:w Bb4:e D5:e F5:e D5:e Bb4:q -:q "
      + "-:w A4:e C5:e F5:e C5:e A4:q -:q -:w G4:e C5:e E5:e D5:e C5:q -:q";
    const hook = "A4:q D5:q E5:q F5:q E5:h D5:q A4:q Bb4:q D5:q F5:q Bb5:q A5:h F5:q D5:q "
      + "G4:q Bb4:q D5:q G5:q F5:h E5:q D5:q C#5:q E5:q A5:q G5:q F5:dh E5:q";
    const half = "B.......X.......";
    const full = "B.Z.Z.Z.X.Z.Z.ZB";
    const fill = "B.Z.Z.Z.X.Z.X.XB";
    const open = { pad: each(chord, "Dm Bb F C"), bass: each(low, "Dm Bb F C") };
    const sung = { pad: each(chord, "Dm Bb Gm A"), bass: each(low, "Dm Bb Gm A"), lead: hook };
    return song({
      bpm: 95,
      voices: {
        lead: "#TYPE sine\n#VOLUME 0.45", call: "#TYPE triangle\n#VOLUME 0.35\n#PAN -0.3", echo: "#TYPE triangle\n#VOLUME 0.22\n#PAN 0.3",
        pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE sine\n#VOLUME 0.5",
      },
      parts: {
        intro: { bars: 8, pad: open.pad },
        call: { bars: 8, pad: open.pad, call, echo: answer },
        call2: { bars: 8, ...open, call, echo: answer, drums: "B...............".repeat(8) },
        hook: { bars: 8, ...sung, drums: half.repeat(8) },
        call3: { bars: 8, ...open, call, echo: answer, drums: full.repeat(7) + fill },
        hook2: { bars: 8, ...sung, echo: transpose(hook, 12), drums: full.repeat(7) + fill },
        breakdown: { bars: 8, pad: open.pad, call },
        outro: {
          bars: 8,
          pad: "D4+F4+A4:w C4+F4+A4:w D4+F4+Bb4:w D4+F4+A4:w D4+F4+A4:w D4+F4+A4:w -:w -:w",
          echo: "-:w A4:e C5:e D5:e C5:e A4:q -:q -:w Bb4:e D5:e F5:e D5:e Bb4:q -:q " + "-:w ".repeat(4),
          bass: "D2:w F2:w Bb1:w A2:w D2:w -:w -:w -:w",
          drums: half.repeat(4) + "B..............." + ".".repeat(48),
        },
      },
      form: "intro call call2 hook call3 hook2 breakdown hook2 outro",
    });
  })(),
  rearview: (() => {
    const arp = {
      Ab: "Ab3:e Eb4:s Ab4:s C5:e Eb4:e Ab4:e C5:s Eb5:s C5:e Eb4:e ",
      Eb: "G3:e Eb4:s G4:s Bb4:e Eb4:e G4:e Bb4:s Eb5:s Bb4:e Eb4:e ",
      Fm: "F3:e C4:s F4:s Ab4:e C4:e F4:e Ab4:s C5:s Ab4:e C4:e ",
      Db: "F3:e Db4:s F4:s Ab4:e Db4:e F4:e Ab4:s Db5:s Ab4:e Db4:e ",
    };
    const chord = { Ab: "Ab3+C4+Eb4:w ", Eb: "G3+Bb3+Eb4:w ", Fm: "Ab3+C4+F4:w ", Db: "Ab3+Db4+F4:w ", Cm: "G3+C4+Eb4:w " };
    const low = {
      Ab: "Ab2:e Eb3:e Ab2:e Eb3:e Ab2:e Eb3:e Ab2:e C3:e ", Eb: "Eb2:e Bb2:e Eb2:e Bb2:e Eb2:e Bb2:e Eb2:e G2:e ",
      Fm: "F2:e C3:e F2:e C3:e F2:e C3:e F2:e Eb3:e ", Db: "Db2:e Ab2:e Db2:e Ab2:e Db2:e Ab2:e Db2:e F2:e ",
    };
    const each = (map, chords) => chords.split(" ").map((c) => map[c]).join("");
    const up = (part) => Object.fromEntries(Object.entries(part).map(([k, v]) => [k, k === "bars" || k === "drums" ? v : transpose(v, 2)]));
    const prog = "Ab Eb Fm Db Ab Eb Fm Db";
    const hook = "Eb5:dq C5:e Eb5:q Ab5:q G5:dq F5:e Eb5:h F5:dq Eb5:e C5:q F5:q Eb5:dq Db5:e C5:h "
      + "Eb5:dq C5:e Eb5:q C6:q Bb5:dq Ab5:e G5:h Ab5:dq G5:e F5:q C5:q Db5:q Eb5:q F5:q Ab4:q";
    const groove = "B.ZZX.ZZB.ZZX.ZZ";
    const band = { pluck: each(arp, prog), pad: each(chord, prog), bass: each(low, prog) };
    const rise = {
      bars: 8, pluck: band.pluck, pad: band.pad, bass: "-:w ".repeat(4) + each(low, "Ab Eb Fm Db"),
      lead: "Ab4:w Bb4:w C5:w Db5:w Eb5:w F5:w G5:w Ab5:h -:h",
      drums: "B...B...B...B...".repeat(4) + "B.Z.B.Z.B.Z.B.Z.".repeat(3) + "B.ZZB.ZZBZZZBZZO",
    };
    const sung = { bars: 8, ...band, lead: hook, drums: groove.repeat(7) + "B.ZZX.ZZB.ZZX.O." };
    const outro = {
      bars: 8,
      lead: "Eb5:dq C5:e Eb5:q Ab5:q G5:dq F5:e Eb5:h " + "-:w ".repeat(6),
      pluck: each(arp, "Ab Eb Fm Db Ab Eb") + "Ab3:e Eb4:e Ab4:e C5:e Eb5:h Ab4:w",
      pad: each(chord, "Ab Eb Fm Db Ab Eb Ab") + "-:w",
      bass: each(low, "Ab Eb Fm Db") + "Ab2:w Eb2:w Ab2:h -:h -:w",
      drums: groove.repeat(4) + "B.Z.Z.Z.B.Z.Z.Z.".repeat(2) + "B..............." + ".".repeat(16),
    };
    return song({
      bpm: 120,
      voices: { lead: "#TYPE sine\n#VOLUME 0.45", pluck: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5" },
      parts: {
        intro: { bars: 8, pad: band.pad, pluck: "-:w ".repeat(4) + each(arp, "Ab Eb Fm Db") },
        verse: {
          bars: 8, ...band, lead: "#TYPE triangle\nC5:w Bb4:w C5:w Db5:w Eb5:w Bb4:w C5:h Bb4:h Ab4:h C5:h",
          drums: "B.Z.X.Z.B.Z.X.Z.".repeat(7) + "B.Z.X.Z.B.Z.X.XX",
        },
        rise,
        hook: sung,
        breakdown: {
          bars: 8,
          lead: "#TYPE triangle\nDb5:w Bb4:w G4:w C5:w Db5:w Eb5:w C5:w -:w",
          pluck: "F4:q Ab4:q Db5:q Ab4:q G4:q Bb4:q Eb5:q Bb4:q G4:q C5:q Eb5:q C5:q F4:q Ab4:q C5:q Ab4:q ".repeat(2),
          pad: each(chord, "Db Eb Cm Fm Db Eb Cm Fm"),
          bass: "Db2:w Eb2:w C2:w F2:w ".repeat(2),
        },
        rise2: up(rise),
        lift: up(sung),
        outro: up(outro),
      },
      form: "intro verse rise hook verse hook breakdown rise2 lift*2 outro",
    });
  })(),
  midnightbloom: (() => {
    const bpm = 122;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars));
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16));
    const prog = ["Am", "F", "C", "G"];
    const pads = { Am: "A3+C4+E4:w ", F: "F3+A3+C4:w ", C: "E3+G3+C4:w ", G: "D3+G3+B3:w " };
    const lo = { Am: "A2", F: "F2", C: "C3", G: "G2" };
    const hi = { Am: "A3", F: "F3", C: "C4", G: "G3" };
    const pad = (n) => prog.map((c) => pads[c]).join("").repeat(n);
    const off = (n) => prog.map((c) => `-:e ${lo[c]}:e `.repeat(4)).join("").repeat(n);
    const rolling = (n) => prog.map((c) => `${lo[c]}:s ${hi[c]}:s `.repeat(8)).join("").repeat(n);
    const hook = "E5:q D5:e C5:e B4:q A4:q -:e A4:e C5:e F5:e E5:dq -:e E5:q D5:e C5:e B4:q G4:q -:e G4:e B4:e D5:e C5:q B4:q ";
    const alone = "A3+E5:q D5:e C5:e B4:q A4:q F3+C4:e A4:e C5:e F5:e E5:dq -:e C3+E5:q D5:e C5:e B4:q G4:q G3+D4:e G4:e B4:e D5:e C5:q B4:q ";
    const groove = "K.O.K.O.K.O.K.O.";
    const busy = "K.OHK.OHK.OHK.OH";
    const fill = "K.O.K.O.K.OHRRRR";
    const lift = "..O...O...O...O.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "H".repeat(16) + "R".repeat(16);
    const ending = unfold(["A3", "E4", "A4", "C5"], 0.45, 4, 8);
    return song({
      bpm,
      voices: { piano: "#TYPE sine\n#VOLUME 0.45", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth\n#VOLUME 0.1", ...ending.voices },
      parts: {
        intro: { bars: 8, pad: pad(2), bass: "-:w ".repeat(4) + off(1), drums: "K...K...K...K...".repeat(4) + groove.repeat(3) + fill },
        groove: { bars: 16, piano: hook.repeat(4), pad: pad(4), bass: off(4), clap: claps(16), drums: (groove.repeat(7) + fill).repeat(2) },
        breakdown: { bars: 12, piano: alone.repeat(3), pad: "-:w ".repeat(8) + pad(1) },
        build: { bars: 8, piano: hook.repeat(2), pad: pad(2), bass: "A2:w F2:w C3:w G2:w ".repeat(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        drop: { bars: 16, piano: hook.repeat(2) + transpose(hook, 12).repeat(2), pad: pad(4), bass: rolling(4), clap: claps(16), drums: (busy.repeat(7) + fill).repeat(2) },
        rise: { bars: 8, piano: alone.repeat(2), pad: pad(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        outro: {
          bars: 8,
          piano: hook + "-:w ".repeat(4),
          ...ending.parts,
          pad: pad(1) + "A3+C4+E4:w A3+C4+E4:w -:w -:w ",
          bass: off(1) + "A2:w " + "-:w ".repeat(3),
          clap: claps(4) + `\n#BPM ${bpm}\n` + "-:w ".repeat(4),
          drums: groove.repeat(6) + "K...K...K...K...K...............",
        },
      },
      form: "intro groove breakdown build drop rise drop outro",
    });
  })(),
  afterglow: (() => {
    const bpm = 120;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars));
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16));
    const prog = ["F", "C", "Dm", "Bb"];
    const low = { F: "F4+A4+C5", C: "E4+G4+C5", Dm: "F4+A4+D5", Bb: "F4+Bb4+D5" };
    const high = { F: "A4+C5+F5", C: "G4+C5+E5", Dm: "A4+D5+F5", Bb: "Bb4+D5+F5" };
    const pads = { F: "F3+A3+C4:w ", C: "E3+G3+C4:w ", Dm: "D3+F3+A3:w ", Bb: "D3+F3+Bb3:w " };
    const root = { F: ["F2", "F3"], C: ["C2", "C3"], Dm: ["D2", "D3"], Bb: ["Bb2", "Bb3"] };
    const pad = (n) => prog.map((c) => pads[c]).join("").repeat(n);
    const off = (n) => prog.map((c) => `-:e ${root[c][0]}:e `.repeat(4)).join("").repeat(n);
    const pulse = (n) => prog.map((c) => `${root[c][0]}:e ${root[c][1]}:e `.repeat(4)).join("").repeat(n);
    const rolling = (n) => prog.map((c) => `${root[c][0]}:s ${root[c][1]}:s `.repeat(8)).join("").repeat(n);
    const hook = prog.map((c) => `${low[c]}:e -:e ${low[c]}:e -:s ${high[c]}:s -:e ${high[c]}:e -:q `).join("");
    const answer = "-:h C6:e A5:e F5:q -:h G5:e E5:e C5:q -:h A5:e F5:e D5:q -:h Bb5:e A5:e F5:e D5:e ";
    const alone = "F4+A4+C5:h A5:q G5:q E4+G4+C5:h G5:q E5:q F4+A4+D5:h F5:q A5:q F4+Bb4+D5:h F5:q D5:q ";
    const groove = "K.O.K.O.K.O.K.O.";
    const busy = "K.OHK.OHK.OHK.OH";
    const fill = "K.O.K.O.K.OHRRRR";
    const lift = "..O...O...O...O.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "H".repeat(16) + "R".repeat(16);
    const ending = unfold(["F4", "A4", "C5", "F5"], 0.32, 4, 8);
    return song({
      bpm,
      voices: { piano: "#TYPE sine\n#VOLUME 0.32", lead: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth\n#VOLUME 0.1", ...ending.voices },
      parts: {
        intro: { bars: 8, pad: pad(2), bass: "-:w ".repeat(4) + off(1), drums: "K...K...K...K...".repeat(4) + groove.repeat(3) + fill },
        groove: { bars: 16, piano: hook.repeat(4), pad: pad(4), bass: pulse(4), clap: claps(16), drums: (groove.repeat(7) + fill).repeat(2) },
        breakdown: { bars: 16, piano: alone.repeat(4), pad: "-:w ".repeat(8) + pad(2), lead: "-:w ".repeat(12) + answer },
        build: { bars: 8, piano: hook.repeat(2), pad: pad(2), bass: "F2:w C3:w D3:w Bb2:w ".repeat(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        drop: { bars: 16, piano: hook.repeat(4), lead: answer.repeat(4), pad: pad(4), bass: rolling(4), clap: claps(16), drums: (busy.repeat(7) + fill).repeat(2) },
        outro: {
          bars: 8,
          piano: hook + "-:w ".repeat(4),
          ...ending.parts,
          pad: pad(1) + "F3+A3+C4:w F3+A3+C4:w -:w -:w ",
          bass: off(1) + "F2:w " + "-:w ".repeat(3),
          clap: claps(4) + `\n#BPM ${bpm}\n` + "-:w ".repeat(4),
          drums: groove.repeat(6) + "K...K...K...K...K...............",
        },
      },
      form: "intro groove breakdown build drop*2 outro",
    });
  })(),
  oceanfloor: (() => {
    const bpm = 123;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars));
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16));
    const prog = ["Dm", "Bb", "Gm", "A"];
    const pads = { Dm: "D3+F3+A3:w ", Bb: "D3+F3+Bb3:w ", Gm: "D3+G3+Bb3:w ", A: "C#3+E3+A3:w " };
    const cells = { Dm: "D2:s D3:s A2:s D3:s ", Bb: "Bb1:s Bb2:s F2:s Bb2:s ", Gm: "G1:s G2:s D2:s G2:s ", A: "A1:s A2:s E2:s A2:s " };
    const pad = (n) => prog.map((c) => pads[c]).join("").repeat(n);
    const rolling = (n) => prog.map((c) => cells[c].repeat(4)).join("").repeat(n);
    const call = "-:h A5:e F5:e D5:q -:w -:h Bb5:e G5:e D5:q -:h -:e C#5:e E5:e A5:e ";
    const answer = "-:h A5:e F5:e D5:q -:h F5:e E5:e D5:q -:h Bb5:e G5:e D5:q -:h C#5:e E5:e A5:q ";
    const alone = "D4+A4:h A5:e F5:e D5:q Bb3+F4:w G3+D4:h Bb5:e G5:e D5:q A3+E4:h C#5:e E5:e A5:q ";
    const groove = "K.O.K.O.K.O.K.O.";
    const busy = "K.OHK.OHK.OHK.OH";
    const fill = "K.O.K.O.K.OHT.TT";
    const lift = "..O...O...O...O.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "H".repeat(16) + "R".repeat(16);
    const ending = unfold(["D4", "A4", "D5", "F5"], 0.45, 4, 8);
    return song({
      bpm,
      voices: { piano: "#TYPE sine\n#VOLUME 0.45", pad: "#TYPE sawtooth\n#VOLUME 0.07", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth\n#VOLUME 0.1", ...ending.voices },
      parts: {
        intro: { bars: 8, pad: pad(2), bass: "-:w ".repeat(4) + rolling(1), drums: "K...K...K...K...".repeat(4) + groove.repeat(3) + fill },
        groove: { bars: 16, piano: call.repeat(4), pad: pad(4), bass: rolling(4), clap: claps(16), drums: (groove.repeat(7) + fill).repeat(2) },
        breakdown: { bars: 12, piano: alone.repeat(3), pad: "-:w ".repeat(8) + pad(1) },
        build: { bars: 8, piano: call.repeat(2), pad: pad(2), bass: "D2:w Bb1:w G1:w A1:w ".repeat(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        drop: { bars: 16, piano: answer.repeat(2) + transpose(answer, 12).repeat(2), pad: pad(4), bass: rolling(4), clap: claps(16), drums: (busy.repeat(7) + fill).repeat(2) },
        rise: { bars: 8, piano: alone.repeat(2), pad: pad(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        outro: {
          bars: 8,
          piano: call + "-:w ".repeat(4),
          ...ending.parts,
          pad: pad(1) + "D3+F3+A3:w D3+F3+A3:w -:w -:w ",
          bass: rolling(1) + "D2:w " + "-:w ".repeat(3),
          clap: claps(4) + `\n#BPM ${bpm}\n` + "-:w ".repeat(4),
          drums: groove.repeat(6) + "K...K...K...K...K...............",
        },
      },
      form: "intro groove breakdown build drop rise drop outro",
    });
  })(),
  citypulse: (() => {
    const bpm = 124;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars));
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16));
    const prog = ["Em", "Em", "C", "D"];
    const pads = { Em: "E3+G3+B3:w ", C: "E3+G3+C4:w ", D: "D3+F#3+A3:w " };
    const lo = { Em: "E2", C: "C2", D: "D2" };
    const hi = { Em: "E3", C: "C3", D: "D3" };
    const pad = (n) => prog.map((c) => pads[c]).join("").repeat(n);
    const pulse = (n) => prog.map((c) => `-:s ${lo[c]}:s ${hi[c]}:s ${lo[c]}:s `.repeat(4)).join("").repeat(n);
    const cell = "E5:s B4:s G5:s ".repeat(21) + "F#5:s ";
    const turn = "B5:s E5:s G5:s ".repeat(21) + "A5:s ";
    const slow = "E5:e B4:e G5:e ".repeat(10) + "E5:e F#5:e ";
    const groove = "K.O.K.O.K.O.K.O.";
    const busy = "KHOHKHOHKHOHKHOH";
    const fill = "K.O.K.O.K.ORRRRR";
    const lift = "..O...O...O...O.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "H".repeat(16) + "R".repeat(16);
    const ending = unfold(["E4", "B4", "E5", "G5"], 0.45, 4, 8);
    return song({
      bpm,
      voices: { piano: "#TYPE sine\n#VOLUME 0.45", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth\n#VOLUME 0.1", ...ending.voices },
      parts: {
        intro: { bars: 8, bass: pulse(2), drums: "K...K...K...K...".repeat(4) + groove.repeat(3) + fill },
        groove: { bars: 16, piano: cell.repeat(4), pad: pad(4), bass: pulse(4), clap: claps(16), drums: (groove.repeat(7) + fill).repeat(2) },
        breakdown: { bars: 12, piano: slow.repeat(3), pad: "-:w ".repeat(4) + pad(2) },
        build: { bars: 8, piano: cell.repeat(2), pad: pad(2), bass: "E2:w C2:w D2:w B1:w ".repeat(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        drop: { bars: 16, piano: cell.repeat(2) + turn.repeat(2), pad: pad(4), bass: pulse(4), clap: claps(16), drums: (busy.repeat(7) + fill).repeat(2) },
        rise: { bars: 8, piano: slow.repeat(2), pad: pad(2), clap: "-:w ".repeat(6) + roll, drums: lift },
        outro: {
          bars: 8,
          piano: cell + "-:w ".repeat(4),
          ...ending.parts,
          pad: pad(1) + "E3+G3+B3:w E3+G3+B3:w -:w -:w ",
          bass: pulse(1) + "E2:w " + "-:w ".repeat(3),
          clap: claps(4) + `\n#BPM ${bpm}\n` + "-:w ".repeat(4),
          drums: groove.repeat(6) + "K...K...K...K...K...............",
        },
      },
      form: "intro groove breakdown build drop rise drop outro",
    });
  })(),
  velvethours: (() => {
    const bpm = 121;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars));
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16));
    const turn = ["Bb", "Bb", "FA", "FA", "Gm", "Gm", "Eb", "Eb"];
    const pads = { Bb: "D3+F3+Bb3:w ", FA: "C3+F3+A3:w ", Gm: "D3+G3+Bb3:w ", Eb: "Eb3+G3+Bb3:w " };
    const arp = { Bb: "Bb3:e D4:e F4:e D4:e ", FA: "A3:e C4:e F4:e C4:e ", Gm: "G3:e Bb3:e D4:e Bb3:e ", Eb: "G3:e Bb3:e Eb4:e G4:e " };
    const lo = { Bb: "Bb2", FA: "A2", Gm: "G2", Eb: "Eb2" };
    const hi = { Bb: "Bb3", FA: "A3", Gm: "G3", Eb: "Eb3" };
    const pad = (n) => turn.map((c) => pads[c]).join("").repeat(n);
    const keys = (n) => turn.map((c) => arp[c].repeat(2)).join("").repeat(n);
    const off = (n) => turn.map((c) => `-:e ${lo[c]}:e `.repeat(4)).join("").repeat(n);
    const rolling = (n) => turn.map((c) => `${lo[c]}:s ${hi[c]}:s `.repeat(8)).join("").repeat(n);
    const line = "D5:h F5:q Bb5:q A5:dh G5:q F5:h C5:q F5:q E5:dh F5:q G5:h Bb5:q D6:q C6:dh Bb5:q G5:h Eb5:q G5:q Bb5:w ";
    const half = "Bb2+D5:h F5:q Bb5:q A5:dh G5:q A2+F5:h C5:q F5:q E5:dh F5:q ";
    const alone = half + "G2+G5:h Bb5:q D6:q C6:dh Bb5:q Eb3+G5:h Eb5:q G5:q Bb2+Bb5:w ";
    const walk = "Bb2:h F2:h Bb2:h F2:h A2:h F2:h A2:h F2:h G2:h D2:h G2:h D2:h Eb2:h Bb2:h Eb2:h F2:h ";
    const groove = "K.O.K.O.K.O.K.O.";
    const busy = "K.OHK.OHK.OHK.OH";
    const fill = "K.O.K.O.K.OHRRRR";
    const lift = "..O...O...O...O.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "H".repeat(16) + "R".repeat(16);
    const ending = unfold(["G4", "Bb4", "D5", "F5"], 0.45, 4, 8);
    return song({
      bpm,
      voices: { piano: "#TYPE sine\n#VOLUME 0.45", keys: "#TYPE triangle\n#VOLUME 0.25", pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth\n#VOLUME 0.1", ...ending.voices },
      parts: {
        intro: { bars: 8, pad: pad(1), keys: keys(1), drums: "K...K...K...K...".repeat(4) + groove.repeat(3) + fill },
        groove: { bars: 16, piano: line.repeat(2), keys: keys(2), pad: pad(2), bass: off(2), clap: claps(16), drums: (groove.repeat(7) + fill).repeat(2) },
        breakdown: { bars: 16, piano: alone.repeat(2), pad: "-:w ".repeat(8) + pad(1) },
        build: { bars: 8, piano: line, keys: keys(1), pad: pad(1), bass: walk, clap: "-:w ".repeat(6) + roll, drums: lift },
        drop: { bars: 16, piano: line + transpose(line, 12), keys: keys(2), pad: pad(2), bass: rolling(2), clap: claps(16), drums: (busy.repeat(7) + fill).repeat(2) },
        rise: {
          bars: 4,
          piano: half,
          pad: "D3+F3+Bb3:w D3+F3+Bb3:w C3+F3+A3:w C3+F3+A3:w ",
          clap: "-:w -:w " + roll,
          drums: "..O...O...O...O." + "H.H.H.H.H.H.H.H." + "H".repeat(16) + "R".repeat(16),
        },
        outro: {
          bars: 8,
          piano: half + "-:w ".repeat(4),
          ...ending.parts,
          keys: ["Bb", "Bb", "FA", "FA"].map((c) => arp[c].repeat(2)).join("") + "-:w ".repeat(4),
          pad: pad(1),
          bass: ["Bb", "Bb", "FA", "FA"].map((c) => `-:e ${lo[c]}:e `.repeat(4)).join("") + "Bb2:w " + "-:w ".repeat(3),
          clap: claps(4) + `\n#BPM ${bpm}\n` + "-:w ".repeat(4),
          drums: groove.repeat(6) + "K...K...K...K...K...............",
        },
      },
      form: "intro groove breakdown build drop rise drop outro",
    });
  })(),
  distantshores: (() => {
    const bpm = 122;
    const chords = ["C#4+E4+G#4", "C#4+E4+A4", "B3+E4+G#4", "B3+D#4+F#4"];
    const roots = [["C#2", "C#3"], ["A1", "A2"], ["E2", "E3"], ["B1", "B2"]];
    const tones = [["C#5", "E5", "G#5", "C#6"], ["C#5", "E5", "A5", "C#6"], ["E5", "G#5", "B5", "E6"], ["F#5", "B5", "D#6", "F#6"]];
    const arp = (t) => tones.map(([a, b, c, d]) => `${a}:e ${b}:e ${c}:e ${d}:e ${c}:e ${b}:e ${d}:e ${c}:e `).join("").repeat(t);
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map(([l, h]) => `${l}:e ${h}:e `.repeat(4)).join("").repeat(t);
    const stab = (t) => chords.map((c) => `-:e ${c}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const hats = "..H...H...H...H.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        arp: "#TYPE triangle\n#VOLUME 0.32", lead: "#TYPE sine\n#VOLUME 0.26", keys: "#TYPE sine\n#VOLUME 0.14",
        pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      parts: {
        intro: { bars: 8, arp: arp(2), pad: pad(2), drums: hats.repeat(8) },
        groove: { bars: 8, arp: arp(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        build: { bars: 8, arp: arp(2), pad: pad(2), lead: "C#5:w D#5:w E5:w F#5:w G#5:w A5:w B5:w C#6:w", drums: hats.repeat(6) + "H.H.H.H.H.H.H.H." + "R".repeat(16) },
        drop: { bars: 8, arp: arp(2), keys: stab(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        groove2: { bars: 8, arp: transpose(arp(2), -12), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(8) },
        brk: { bars: 8, arp: arp(2), keys: pad(2) },
        drop2: {
          bars: 8, arp: arp(2), lead: "G#5:w A5:w B5:w A5:w G#5:w A5:w B5:h D#6:h C#6:w", keys: stab(2), pad: pad(2), bass: bass(2),
          clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill,
        },
        outro: {
          bars: 7, lead: "G#5:w A5:w B5:w A5:w G#5:w A5:w B5:h D#6:h",
          arp: `${arp(2).split(" ").slice(0, 48).join(" ")} -:w`, keys: `${stab(2).split(" ").slice(0, 40).join(" ")} -:w -:w`,
          pad: pad(2).split(" ").slice(0, 7).join(" "), bass: `${bass(1)}-:w -:w -:w`,
          clap: clap(claps.repeat(3) + ".".repeat(64)), drums: four.repeat(3) + "K..............." + ".".repeat(48),
        },
        end: { bars: 2, lead: "C#6:w -:w", pad: hold(chords[0], 2, bpm) },
      },
      form: "intro groove build drop*2 groove2 brk build drop2*2 outro end",
    });
  })(),
  tidalechoes: (() => {
    const bpm = 120;
    const motif = "G5:e A#5:e D6:q C6:e A#5:e -:q G5:e A#5:e D#6:q D6:e A#5:e -:q F5:e A#5:e D6:q C6:e A#5:e -:q F5:e A5:e C6:q A5:h ";
    const echo = (t) => `-:dq ${motif.repeat(t).replace(/A5:h $/, "A5:e ")}`;
    const chords = ["G3+A#3+D4", "G3+A#3+D#4", "F3+A#3+D4", "F3+A3+C4"];
    const roots = ["G2", "Eb2", "Bb1", "F2"];
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const pulse = (t) => roots.map((r) => `${r}:s -:s `.repeat(8)).join("").repeat(t);
    const stab = (t) => chords.map((c) => `-:e ${c}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const hats = "..H...H...H...H.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        piano: "#TYPE sine\n#VOLUME 0.34", echo: "#TYPE triangle\n#VOLUME 0.16", keys: "#TYPE sine\n#VOLUME 0.12",
        pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      parts: {
        intro: { bars: 8, piano: motif.repeat(2), pad: pad(2), drums: hats.repeat(8) },
        groove: { bars: 8, piano: motif.repeat(2), echo: echo(2), pad: pad(2), bass: pulse(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        build: { bars: 8, piano: motif.repeat(2), echo: echo(2), pad: pad(2), bass: "G2:s -:s ".repeat(64), drums: hats.repeat(6) + "H.H.H.H.H.H.H.H." + "R".repeat(16) },
        drop: {
          bars: 8, piano: motif.repeat(2), echo: echo(2), keys: stab(2), pad: pad(2), bass: pulse(2),
          clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill,
        },
        groove2: { bars: 8, piano: transpose(motif.repeat(2), -12), pad: pad(2), bass: pulse(2), clap: clap(claps.repeat(8)), drums: four.repeat(8) },
        brk: { bars: 8, piano: motif.repeat(2), echo: echo(2) },
        drop2: {
          bars: 8, piano: transpose(motif.repeat(2), 12), echo: echo(2), keys: stab(2), pad: pad(2), bass: pulse(2),
          clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill,
        },
        outro: {
          bars: 8, piano: motif.repeat(2), echo: `${echo(1)}${"-:w ".repeat(4)}`,
          keys: `${stab(2).split(" ").slice(0, 48).join(" ")} -:w -:w`, pad: pad(2), bass: `${pulse(1)}${"-:w ".repeat(4)}`,
          clap: clap(claps.repeat(2) + ".".repeat(96)), drums: four.repeat(2) + "K..............." + ".".repeat(80),
        },
        end: { bars: 3, piano: `G5:e A#5:e D6:q C6:e\n${echoTail("A#5", 0.34)}\n-:dh -:e`, pad: hold(chords[0], 3, bpm) },
      },
      form: "intro groove build drop*2 groove2 brk build drop2*2 outro end",
    });
  })(),
  weightless: (() => {
    const bpm = 121;
    const hook = "G5:h F5:q Eb5:q G5:dh Bb5:q C6:h Bb5:q G5:q F5:w G5:h Bb5:q C6:q D6:dh C6:q Bb5:h G5:q F5:q Eb5:w ";
    const chords = ["Eb3+Bb3+G4+D5", "C3+G3+Eb4+Bb4", "Ab2+Eb3+C4+G4", "Bb2+F3+D4+F4"];
    const roots = [["Eb2", "Bb2"], ["C2", "G2"], ["Ab1", "Eb2"], ["Bb1", "F2"]];
    const tones = [["Eb4", "G4", "Bb4", "D5"], ["C4", "Eb4", "G4", "Bb4"], ["Ab3", "C4", "Eb4", "G4"], ["Bb3", "D4", "F4", "C5"]];
    const arp = (t) => tones.map(([a, b, c, d]) => `${a}:e ${b}:e ${c}:e ${d}:e `.repeat(2)).join("").repeat(t);
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map(([r, f]) => `${r}:e ${f}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        piano: "#TYPE sine\n#VOLUME 0.34", arp: "#TYPE triangle\n#VOLUME 0.18", pad: "#TYPE sawtooth\n#VOLUME 0.05",
        bass: "#TYPE sine\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      parts: {
        intro: { bars: 8, arp: arp(2), pad: pad(2), drums: ".......H.......H".repeat(8) },
        groove: { bars: 8, arp: arp(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        build: {
          bars: 8, arp: arp(2), pad: pad(2), piano: "F5:w G5:w Ab5:w Bb5:w C6:w D6:w Eb6:w F6:w",
          drums: "..H...H...H...H.".repeat(6) + "H.H.H.H.H.H.H.H." + "R".repeat(16),
        },
        drop: { bars: 8, piano: hook, arp: arp(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        groove2: { bars: 8, arp: transpose(arp(2), 12), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(8) },
        brk: { bars: 8, piano: hook, arp: arp(2) },
        drop2: { bars: 8, piano: transpose(hook, 12), arp: arp(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        outro: {
          bars: 7, piano: hook.replace(/Eb5:w $/, ""), arp: `${arp(2).split(" ").slice(0, 48).join(" ")} -:w`,
          pad: pad(2).split(" ").slice(0, 7).join(" "), bass: `${bass(1)}-:w -:w -:w`,
          clap: clap(claps.repeat(2) + ".".repeat(80)), drums: four.repeat(2) + "K..............." + ".".repeat(64),
        },
        end: { bars: 2, piano: "Eb5:w -:w", pad: `${chords[0]}:dw -:h` },
      },
      form: "intro groove build drop*2 groove2 brk build drop2*2 outro end",
    });
  })(),
  parallellines: (() => {
    const bpm = 124;
    const upper = "F#5:e D5:e B4:e D5:e F#5:e B5:e A5:e F#5:e G5:e D5:e B4:e D5:e G5:e B5:e A5:e G5:e F#5:e D5:e A4:e D5:e F#5:e A5:e B5:e A5:e E5:e C#5:e A4:e C#5:e E5:e A5:e G5:e E5:e ";
    const lower = "B3:q C#4:q D4:q F#4:q G4:q F#4:q E4:q D4:q F#4:q E4:q D4:q A3:q C#4:q E4:q A4:q G4:q ";
    const chords = ["B3+D4+F#4", "B3+D4+G4", "A3+D4+F#4", "A3+C#4+E4"];
    const rolls = [["B1", "B2", "F#2"], ["G1", "G2", "D2"], ["D2", "D3", "A2"], ["A1", "A2", "E2"]];
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => rolls.map(([r, o, f]) => `${r}:s ${o}:s ${f}:s ${o}:s `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p)}`;
    const claps = "....P.......P...";
    const drive = "K.OHK.OHK.OHK.OH";
    const fill = "K.OHK.OHK.OHKRRR";
    return song({
      bpm,
      voices: {
        upper: "#TYPE sine\n#VOLUME 0.3", lower: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.06",
        bass: "#TYPE triangle\n#VOLUME 0.45", clap: "#TYPE sawtooth",
      },
      parts: {
        intro: { bars: 8, lower: lower.repeat(2), pad: pad(2), drums: "..H...H...H...H.".repeat(8) },
        groove: { bars: 8, lower: lower.repeat(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: drive.repeat(7) + fill },
        build: {
          bars: 8, upper: upper.repeat(2), lower: lower.repeat(2), pad: pad(2), bass: "B1:s B2:s ".repeat(64),
          drums: "..H...H...H...H.".repeat(6) + "H.H.H.H.H.H.H.H." + "R".repeat(16),
        },
        drop: {
          bars: 8, upper: upper.repeat(2), lower: lower.repeat(2), pad: pad(2), bass: bass(2),
          clap: clap(claps.repeat(8)), drums: drive.repeat(7) + fill,
        },
        groove2: { bars: 8, lower: transpose(lower.repeat(2), 12), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: drive.repeat(8) },
        brk: { bars: 8, upper: upper.repeat(2), lower: lower.repeat(2) },
        drop2: {
          bars: 8, upper: transpose(upper.repeat(2), 12), lower: lower.repeat(2), pad: pad(2), bass: bass(2),
          clap: clap(claps.repeat(8)), drums: drive.repeat(7) + fill,
        },
        outro: {
          bars: 8, upper: upper.repeat(2), lower: `${lower.repeat(2).split(" ").slice(0, 28).join(" ")} -:w`,
          pad: pad(2), bass: `${bass(1)}${"-:w ".repeat(4)}`,
          clap: clap(claps.repeat(2) + ".".repeat(96)), drums: drive.repeat(2) + "K..............." + ".".repeat(80),
        },
        end: { bars: 2, upper: `F#5:e D5:e\n${echoTail("B4", 0.3)}\n-:q`, pad: hold(chords[0], 2, bpm) },
      },
      form: "intro groove build drop*2 groove2 brk build drop2*2 outro end",
    });
  })(),
  daybreak: (() => {
    const bpm = 123;
    const hook = "C6:dq B5:e G5:q E5:q D6:dq C6:e B5:q G5:q C6:dq B5:e A5:q E5:q F5:q A5:q C6:q E6:q ";
    const chords = ["E4+G4+C5", "D4+G4+B4", "E4+A4+C5", "F4+A4+C5"];
    const pads = ["C3+G3+E4", "B2+G3+D4", "A2+E3+C4", "F2+C3+A3"];
    const roots = ["C2", "G2", "A2", "F2"];
    const keys = (t) => chords.map((c) => `${c}:e -:e ${c}:e -:s ${c}:s -:e ${c}:e -:q `).join("").repeat(t);
    const pulse = (t) => chords.map((c) => `${c}:s -:s `.repeat(8)).join("").repeat(t);
    const pad = (t) => pads.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map((r) => `-:e ${r}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        piano: "#TYPE sine\n#VOLUME 0.34", keys: "#TYPE sine\n#VOLUME 0.13", lead: "#TYPE triangle\n#VOLUME 0.25",
        pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      parts: {
        intro: { bars: 8, keys: keys(2), pad: pad(2), drums: "..H...H...H...H.".repeat(8) },
        groove: { bars: 8, keys: keys(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        build: {
          bars: 8, keys: keys(1) + pulse(1), lead: "C5:w D5:w E5:w F5:w G5:w A5:w B5:w C6:w", pad: pad(2),
          drums: "..H...H...H...H.".repeat(4) + "H.H.H.H.H.H.H.H.".repeat(2) + "R".repeat(32),
        },
        drop: { bars: 8, piano: hook.repeat(2), keys: keys(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill },
        groove2: { bars: 8, piano: transpose(hook.repeat(2), -12), keys: keys(2), pad: pad(2), bass: bass(2), clap: clap(claps.repeat(8)), drums: four.repeat(8) },
        brk: { bars: 8, piano: hook.repeat(2) },
        drop2: {
          bars: 8, piano: transpose(hook.repeat(2), 12), keys: keys(2), pad: pad(2), bass: bass(2),
          clap: clap(claps.repeat(8)), drums: four.repeat(7) + fill,
        },
        outro: {
          bars: 7, piano: hook.repeat(2).split(" ").slice(0, 28).join(" "), keys: `${keys(2).split(" ").slice(0, 40).join(" ")} -:w -:w`,
          pad: pad(2).split(" ").slice(0, 7).join(" "), bass: `${bass(1)}-:w -:w -:w`,
          clap: clap(claps.repeat(2) + ".".repeat(80)), drums: four.repeat(2) + "K..............." + ".".repeat(64),
        },
        end: { bars: 3, piano: `F5:q A5:q C6:q\n${echoTail("E6", 0.34)}\n-:dh`, pad: `${pads[3]}:dh ${pads[0]}:dw -:dh` },
      },
      form: "intro groove build drop*2 groove2 brk build drop2*2 outro end",
    });
  })(),
};
