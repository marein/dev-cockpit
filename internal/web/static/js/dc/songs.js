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

// A panned song gives every hit its own pan, the hats, rim and open hat a
// little to the right, the shaker as far to the left, the rest in the center.
const hitPans = { H: 0.2, R: 0.2, O: 0.2, Z: -0.2 };

const pitchNames = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
const letterSteps = { C: 0, D: 2, E: 4, F: 5, G: 7, A: 9, B: 11 };
const accidentalSteps = { "#": 1, b: -1 };

function beat(pattern, pans) {
  return [...pattern.replace(/\s/g, "")].map((step) => (pans && step !== "." ? `#PAN ${pans[step] ?? 0}\n${kit[step]}` : kit[step])).join("\n");
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
// The notes sit low to high from left to right.
function unfold(notes, volume, at, bars) {
  const voice = (i) => `roll${i + 1}`;
  const sides = [-0.4, -0.2, 0.2, 0.4];
  return {
    voices: Object.fromEntries(notes.map((_, i) => [voice(i), `#TYPE sine\n#VOLUME ${volume / notes.length}`])),
    pans: Object.fromEntries(notes.map((_, i) => [voice(i), sides[i]])),
    parts: Object.fromEntries(notes.map((note, i) => [voice(i), `${"-:w ".repeat(at)}${"-:e ".repeat(i)}${note}:w ${"-:e ".repeat((bars - at - 1) * 8 - i)}`])),
  };
}

// song renders parts in the order of form into one track per voice plus the
// drums, "name*n" as a loop. Every part segment restarts from the voice's
// setup, so a directive inside a part never leaks into the next one, and a
// voice a part leaves out rests for the part's bars. pans sets where a voice
// sits at the start of every segment, a song that gives them also pans its
// hits by hitPans. lowEnd rebuilds its bass and its kick, see boost.
function song({ bpm, voices, pans, lowEnd, parts, form }) {
  const order = form.trim().split(/\s+/).map((entry) => {
    const [name, times = "1"] = entry.split("*");
    return { part: parts[name], times: Number(times) };
  });
  const repeat = (times, notes) => (times > 1 ? `#LOOP ${times}\n${notes}\n#ENDLOOP` : notes);
  const tracks = Object.entries(voices).map(([voice, setup]) => [
    `#TRACK ${voice}`,
    ...order.map(({ part, times }) => repeat(times, `#BPM ${bpm}\n#PAN ${pans?.[voice] ?? 0}\n${setup}\n${part[voice] ?? "-:q ".repeat(part.bars * 4)}`)),
  ].join("\n"));
  const drums = [
    "#TRACK drums",
    `#BPM ${bpm * 8}`,
    "#PAN 0",
    ...order.map(({ part, times }) => repeat(times, beat(part.drums ?? ".".repeat(part.bars * 16), pans && hitPans))),
  ].join("\n");
  const sheet = normalize([...tracks, drums].join("\n"));
  return lowEnd ? boost(sheet, bpm, lowEnd) : sheet;
}

// A note has no envelope, so a fade, a riser or a crash is a run of strokes
// stepping evenly in volume.
function ramp(cells, from, to) {
  return cells.map((cell, i) => `#VOLUME ${(from + ((to - from) * i) / (cells.length - 1)).toFixed(3)}\n${cell}`).join("\n");
}

// Rounded to two decimals as in scriptune's table, so swell counts the cycles
// scriptune really plays.
function hertz(note) {
  const [, letter, accidental, octave] = note.match(/^([A-G])([#b]?)(\d)$/);
  const pitch = Number(octave) * 12 + letterSteps[letter] + (accidentalSteps[accidental] ?? 0);
  return Math.round(440 * 2 ** ((pitch - 57) / 12) * 100) / 100;
}

function ease(levels, at) {
  const x = at * (levels.length - 1);
  const i = Math.min(Math.floor(x), levels.length - 2);
  return levels[i] + ((levels[i + 1] - levels[i]) * (1 - Math.cos(Math.PI * (x - i)))) / 2;
}

// swell sounds a note for beats while its volume eases through levels and,
// if given, its pan through pans. A note has no envelope, so the swell is a
// run of steps of about step seconds. Every step lasts whole cycles of the
// pitch, it stops at zero right where the next one starts at zero, so the
// volume moves without a click. scriptune reads a BPM as a whole number, so a
// step ends as near as it gets to where all steps so far should end, errors
// never add up. A rest fills the part of a cycle left at the end, the run
// keeps to the beat.
function swell(note, beats, bpm, levels, pans, step = 0.08) {
  const hz = hertz(note);
  const seconds = (beats * 60) / bpm;
  const cycles = Math.floor(hz * seconds + 1e-6);
  const count = Math.max(1, Math.round(cycles / Math.max(1, Math.round(hz * step))));
  const cells = [];
  let time = 0;
  let rate;
  for (let i = 0; i < count; i++) {
    const at = count > 1 ? i / (count - 1) : 0;
    const end = Math.min(Math.floor((cycles * (i + 1)) / count) / hz, seconds);
    const next = (i < count - 1 ? Math.round : Math.ceil)(240 / (end - time));
    if (next !== rate) cells.push(`#BPM ${(rate = next)}`);
    if (pans) cells.push(`#PAN ${ease(pans, at).toFixed(2)}`);
    cells.push(`#VOLUME ${ease(levels, at).toFixed(3)}\n${note}:w`);
    time += 240 / rate;
  }
  if (seconds - time > 1e-6) cells.push(`#BPM ${Math.round(240 / (seconds - time))}\n-:w`);
  return `${cells.join("\n")}\n#BPM ${bpm}`;
}

// tone sounds a note for seconds in steps of about step seconds, each at the
// level that level gives for the seconds since the note started. A step
// lasts whole cycles like a step of swell, so the wave stops at zero where
// the next one starts. scriptune reads a BPM as a whole number and misses
// the end of a cycle by a little, so of the cycle ends within two of where a
// step should end it takes the one a whole BPM gets closest to, as a whole
// note or a dotted whole, whose BPM lands finer. The last step ends on the
// last cycle, or on the one before if that saves more than 2 microseconds a
// cycle it leaves out, since a rest fills what is left at the end and the
// note keeps to the beat.
function tone(note, seconds, bpm, level, step) {
  const hz = hertz(note);
  const cycles = Math.floor(hz * seconds + 1e-6);
  const count = Math.max(1, Math.round(cycles / Math.max(1, Math.round(hz * step))));
  const cells = [];
  let time = 0;
  let done = 0;
  let rate;
  for (let i = 0; i < count && done < cycles; i++) {
    const target = Math.round((cycles * (i + 1)) / count);
    const from = Math.max(done + 1, i < count - 1 ? target - 2 : cycles - 1);
    const to = Math.max(from, i < count - 1 ? Math.min(cycles - 1, target + 2) : cycles);
    let best;
    for (let end = from; end <= to; end++) {
      for (const [value, beats] of [["w", 4], ["dw", 6]]) {
        const next = Math.round((60 * beats) / (end / hz - time));
        const miss = Math.abs(time + (60 * beats) / next - end / hz) + (i < count - 1 ? 0 : (cycles - end) * 2e-6);
        if (!best || miss < best.miss) best = { end, value, length: (60 * beats) / next, next, miss };
      }
    }
    if (best.next !== rate) cells.push(`#BPM ${(rate = best.next)}`);
    cells.push(`#VOLUME ${Math.min(1, level(time)).toFixed(3)}\n${note}:${best.value}`);
    time += best.length;
    done = best.end;
  }
  if (seconds - time > 1e-6) cells.push(`#BPM ${Math.round(240 / (seconds - time))}\n-:w`);
  return `${cells.join("\n")}\n#BPM ${bpm}`;
}

// A rest starts an oscillator just like a note, so rests take as few cells
// as they can.
const values = [[24, "dw"], [16, "w"], [12, "dh"], [8, "h"], [6, "dq"], [4, "q"], [3, "de"], [2, "e"], [1, "s"]];

function rests(n) {
  const cells = [];
  for (const [steps, value] of values) for (; n >= steps; n -= steps) cells.push(`-:${value}`);
  return cells.join(" ");
}

// The drops a kick falls through, each step a note, the whole cycles it lasts
// and its level. The hard kick of Dark Techno drops over two octaves to its
// tail, the soft kick starts lower and rings longer, like the low drum it
// replaces, the tom of a fill drops an octave.
const drops = {
  hard: [["D4", 2, 0.8], ["G3", 2, 1], ["D3", 2, 1], ["A2", 2, 0.95], ["E2", 2, 0.9], ["B1", 2, 0.8], ["G1", 3, 0.6], ["G1", 3, 0.3]],
  soft: [["G3", 2, 0.6], ["D3", 2, 0.9], ["A2", 2, 1], ["E2", 2, 0.95], ["B1", 3, 0.85], ["G1", 3, 0.7], ["G1", 4, 0.45], ["G1", 4, 0.2]],
  tom: [["D4", 4, 0.8], ["A3", 4, 0.9], ["F3", 5, 0.8], ["D3", 6, 0.6], ["D3", 6, 0.3]],
};

// land moves a kick's drop from G1 to tail, at level.
function land(drop, tail, level) {
  const shift = Math.round(12 * Math.log2(hertz(tail) / hertz("G1")));
  return drop.map(([note, cycles, volume]) => [transpose(note, shift), cycles, volume * level]);
}

// thump strikes the drop shape gives a letter of grid, one letter a
// sixteenth, after a burst of noise at punch. Every step of a drop ends where
// its wave is zero, so neither the drop nor the tail clicks. The rest after a
// hit ends where the next sixteenth should start, so errors never add up, and
// a hit stops short where the next one starts.
function thump(bpm, grid, shape, punch) {
  const sixteenth = 15 / bpm;
  const hits = [...grid].flatMap((letter, i) => (shape[letter] ? [i] : []));
  const cells = [rests(hits[0] ?? grid.length)];
  let time = (hits[0] ?? 0) * sixteenth;
  const until = (end, cell) => {
    if (end - time < 1e-6) return;
    const rate = Math.round(240 / (end - time));
    cells.push(`#BPM ${rate}\n${cell}:w`);
    time += 240 / rate;
  };
  hits.forEach((at, i) => {
    const next = hits[i + 1] ?? grid.length;
    cells.push(`#TYPE noise\n#VOLUME ${punch.toFixed(3)}`);
    until(time + 0.004, "A6");
    cells.push("#TYPE triangle");
    for (const [note, cycles, level] of shape[grid[at]]) {
      if (time + cycles / hertz(note) > next * sixteenth) break;
      cells.push(`#VOLUME ${level.toFixed(3)}`);
      until(time + cycles / hertz(note), note);
    }
    const filled = Math.min(next, Math.ceil(time / sixteenth - 1e-6));
    until(filled * sixteenth, "-");
    cells.push(`#BPM ${bpm}\n${rests(next - filled)}`);
    time += (next - filled) * sixteenth;
  });
  return cells;
}

function forge(bpm, tail = "G1") {
  const sixteenth = 15 / bpm;
  const kick = (grid, level = 0.6) => thump(bpm, grid.replace(/\s/g, ""), { x: land(drops.hard, tail, level) }, 0.25 * level).join("\n");
  const volume = (level, i) => (typeof level === "function" ? level(i) : level);
  const fade = (from, to, length) => (i) => from + ((to - from) * i) / length;
  const strokes = (grid, strike) => {
    const steps = [...grid.replace(/\s/g, "")];
    const cells = [];
    for (let i = 0; i < steps.length; ) {
      const rest = steps[i] === ".";
      let n = 1;
      while (steps[i + n] === (rest ? "." : "~")) n++;
      cells.push(rest ? rests(n) : strike(steps[i], n, i));
      i += n;
    }
    return cells.join("\n");
  };
  const play = (grid, notes, level, decay, pans) => strokes(grid, (key, n, i) =>
    swell(notes[key], n / 4, bpm, decay.map((d) => d * volume(level, i)), pans, (n * sixteenth) / decay.length));
  const fall = (grid, notes, level, steps) => strokes(grid, (key, n, i) => steps.map(([shift, step]) => {
    const loud = step * volume(level, i);
    return swell(transpose(notes[key], shift), n / 4 / steps.length, bpm, [loud, loud], null, 1);
  }).join("\n"));
  const only = (grid, slot) => grid.replace(/./g, (step, i) => (i % 4 === slot ? step : "."));
  // Wrapped around, so the echo of a loop's last strokes opens the next round.
  const later = (grid, n) => (grid.slice(-n) + grid.slice(0, -n)).replace(/^~+/, (holds) => ".".repeat(holds.length));
  const drone = (notes, level) => notes.split(" ").reduce((runs, note) => {
    if (runs.at(-1)?.note === note) runs.at(-1).bars++;
    else runs.push({ note, bars: 1 });
    return runs;
  }, []).map(({ note, bars }) => swell(note, bars * 4, bpm, [0.2, 1, 0.45].map((l) => l * level), [-0.3, 0.3], 0.12)).join("\n");
  const whine = (notes, levels) => notes.split(" ").map((note, i, all) => swell(note, 8, bpm, [ease(levels, i / all.length), ease(levels, (i + 1) / all.length)], [0.4, -0.4], 0.05)).join("\n");
  const riser = (bars, from = "C3") => `${ramp(Array.from({ length: bars * 8 - 1 }, (_, i) => `${transpose(from, i)}:e`), 0.003, 0.06)}\n-:e`;
  const impact = (bars) => `${ramp(Array(16).fill("D8:q"), 0.06, 0.003)}\n${rests((bars - 4) * 16)}`;
  const four = (bars) => "x...".repeat(bars * 4);
  return { kick, volume, fade, play, fall, only, later, drone, whine, riser, impact, four };
}

// Kept apart from rests, which also takes dotted values, so the Deep Dubstep
// sheets stay byte for byte as they were composed.
function rest(sixteenths) {
  const cells = [];
  for (const [value, length] of [["w", 16], ["h", 8], ["q", 4], ["e", 2], ["s", 1]]) {
    for (; sixteenths >= length; sixteenths -= length) cells.push(`-:${value}`);
  }
  return cells.join(" ");
}

function depth(bpm, { roots, lows, tones }) {
  const bars = (p) => p.split(" ");
  const runs = (p) => bars(p).reduce((all, root) => {
    if (all.at(-1)?.root === root) all.at(-1).bars++;
    else all.push({ root, bars: 1 });
    return all;
  }, []);
  const waves = (n, low, high) => Array.from({ length: n + 1 }, (_, i) => (i % 2 ? high : low));
  // The sub is a sine too deep for a phone speaker, the growl an octave up
  // carries it there. A root held for half a wave hands the next one the
  // wave where it left it.
  const sub =(p, low, high, { notes = roots, per = 1, step = 0.08 / per } = {}) => {
    let halves = 0;
    return runs(p).map(({ root, bars }) => {
      const levels = halves % 2 ? waves(bars * per, high, low) : waves(bars * per, low, high);
      halves += bars * per;
      return swell(notes[root], bars * 4, bpm, levels, null, step);
    }).join("\n");
  };
  const growl = (p, high, per, step) => sub(p, 0, high, { notes: lows, per, step });
  const pass = (notes, high, levels, { pans, beats = 8 } = {}) => bars(notes)
    .map((note) => (note === "-" ? rest(beats * 4) : swell(note, beats, bpm, levels.map((level) => level * high), pans))).join("\n");
  const near = (p, high, { pans = [-0.5, 0.5], beats = 8 } = {}) => pass(
    bars(p).filter((_, i) => i % (beats / 4) === 0).map((root) => tones[root]).join(" "), high, [0, 1, 0], { pans, beats },
  );
  const far = (notes, high, { pans = [0.5, -0.5], beats = 8 } = {}) => `${rest(beats * 2)}\n${pass(notes, high, [0, 1, 0], { pans, beats })}\n${rest(beats * 2)}`;
  const pulse = (p, levels, { rhythm = "x.x.x.x.x.x.x.x.", notes = lows } = {}) => {
    const grid = bars(p).flatMap((root) => [...rhythm].map((step) => (step === "x" ? notes[root] : null)));
    const plucks = grid.filter(Boolean).length;
    let plucked = 0;
    const cells = [];
    for (const note of grid) {
      if (!note) {
        if (typeof cells.at(-1) === "number") cells[cells.length - 1]++;
        else cells.push(1);
        continue;
      }
      const level = ease(levels, plucks > 1 ? plucked++ / (plucks - 1) : 0);
      cells.push(swell(note, 0.25, bpm, [level, level / 4], null, 0.025));
    }
    return cells.map((cell) => (typeof cell === "number" ? rest(cell) : cell)).join("\n");
  };
  // The echo comes a dotted eighth late and cuts the two bars of rest the
  // notes end on by as much, so it ends with them.
  const ring = [1, 0.35, 0.1, 0];
  const ping = (notes, high) => pass(notes, high, ring);
  const echo = (notes, high) => `-:de\n${pass(notes.replace(/ -$/, ""), high, ring)}\n-:w -:h -:q -:s`;
  return { sub, growl, pass, near, far, pulse, ping, echo };
}

// pluck strikes a note for beats like a string played gently, with nothing
// but volume steps, as swell builds its swells. Every step lasts whole cycles
// of the pitch, so it stops at zero right where the next one starts at zero.
// A whole BPM misses the cycles of a long step on a high note, so each step
// takes, from about five percent more or fewer cycles, the count, note value
// and BPM that come closest to whole cycles, and its end aims at where all
// steps so far should end. Every step is an oscillator, so a quiet voice gets
// by with longer steps and a higher floor.
function pluck(note, beats, bpm, level, attack, decay, step = 0.2, floor = 36) {
  const hz = hertz(note);
  const seconds = (beats * 60) / bpm;
  const lengths = { w: 4, dw: 6, h: 2, dh: 3, q: 1, dq: 1.5, e: 0.5, de: 0.75, s: 0.25 };
  const at = (t) => (t < attack ? (level * t) / attack : decay ? level * 1e-3 ** ((t - attack) / decay) : level);
  const rises = attack > 0 ? Math.min(4, Math.max(1, Math.round(attack / 0.015))) : 0;
  const fall = decay ? Math.max(0.025, (step * decay) / Math.log(1e3)) : 0.25;
  const ends = Array.from({ length: rises }, (_, i) => Math.min((attack * (i + 1)) / rises, seconds));
  for (let end = attack; end < seconds && at(end) > level * 10 ** (-floor / 20); ) ends.push((end = Math.min(end + fall, seconds)));
  const fit = (length, cycles) => {
    let best;
    const spread = Math.round(2 + cycles / 20);
    for (const n of cycles ? Array.from({ length: 2 * spread + 1 }, (_, i) => cycles - spread + i).filter((n) => n > 0) : [0]) {
      for (const [value, units] of Object.entries(lengths)) {
        const rate = Math.round(60 * units * (n ? hz / n : 1 / length));
        const time = (60 * units) / rate;
        const miss = n ? Math.abs(time * hz - n) : Math.abs(time - length);
        if (rate > 0 && (!n || time < seconds - elapsed + 1e-9) && !(best?.miss <= miss)) best = { rate, value, time, miss };
      }
    }
    return best;
  };
  const cells = [];
  let elapsed = 0;
  let rate;
  for (const [i, end] of ends.entries()) {
    const cell = end - elapsed >= 1 / hz && fit(end - elapsed, Math.max(1, Math.round((end - elapsed) * hz)));
    if (!cell) break;
    if (cell.rate !== rate) cells.push(`#BPM ${(rate = cell.rate)}`);
    cells.push(`#VOLUME ${(i === rises - 1 ? level : at(elapsed + cell.time / 2)).toFixed(4)}\n${note}:${cell.value}`);
    elapsed += cell.time;
  }
  if (seconds - elapsed > 1e-6) {
    const rest = fit(seconds - elapsed);
    cells.push(`#BPM ${rest.rate}\n-:${rest.value}`);
  }
  return `${cells.join("\n")}\n#BPM ${bpm}`;
}

const quarters = { w: 4, h: 2, q: 1, e: 0.5, s: 0.25, dw: 6, dh: 3, dq: 1.5, de: 0.75 };

// A rest of a length no note value has takes one cell at a whole BPM, since
// every rest starts an oscillator like a note.
function span(note, beats, bpm) {
  const exact = Object.keys(quarters).find((value) => quarters[value] === beats);
  if (exact) return `${note}:${exact}`;
  const value = Object.keys(quarters).find((key) => Number.isInteger((bpm * quarters[key]) / beats)) ?? "w";
  return `#BPM ${Math.round((bpm * quarters[value]) / beats)}\n${note}:${value}\n#BPM ${bpm}`;
}

const picking = {
  strum: ["0:w", "1:w"], swell: ["0:w", "1:w"], roll: ["0:dq 0:dq 0:q", "-:e 1:dq 1:dq 1:e"],
  rock: ["0:h 0:h", "-:q 1:h 1:q"], toll: ["0:w", "-:h 1:h"], pedal: ["0:w", "-:q 1:q 1:q 1:q"],
  cross: ["0:dq 0:dq 0:q", "-:q 1:q 1:q 1:q"],
};
const touches = {
  strum: { attack: 0.03, decay: 4.5 }, swell: { attack: 0.9, decay: 8 }, roll: { attack: 0.025, decay: 3.5 },
  rock: { attack: 0.025, decay: 4 }, toll: { attack: 0.035, decay: 6 }, pedal: { attack: 0.025, decay: 3 },
  cross: { attack: 0.02, decay: 3 },
};

// scriptune has no reverb, so Space Ambient builds the room from the
// material: echo taps, a bloom, slow washes and a shimmer.
function space(bpm, chords) {
  const dyads = { ...chords, "-": ["-", "-"] };
  const strike = (note, beats, level, attack, decay, step, floor) => (note === "-" ? span("-", beats, bpm)
    : pluck(note, beats, bpm, level, attack, decay, step, floor));
  const breathe = (line, contour) => {
    const notes = line.split(/[\s|]+/).filter(Boolean).map((cell) => ({ note: cell.split(":")[0], beats: quarters[cell.split(":")[1]] }));
    const total = notes.reduce((sum, { beats }) => sum + beats, 0);
    let at = 0;
    return notes.map(({ note, beats }, i) => {
      const accent = at % 1 ? 0.82 : at % 2 ? 0.92 : 1;
      const level = ease(contour, (at + beats / 2) / total) * accent * (1 + 0.05 * Math.sin(i * 2.7 + at));
      at += beats;
      return { note, beats, level };
    });
  };
  const play = (notes, { level, attack, decay, pick = 1, step, floor }) => notes
    .map((n) => strike(n.note, n.beats, level * n.level ** pick, attack * (1.6 - 0.6 * Math.min(n.level, 1)), decay, step, floor))
    .join("\n");
  const melody = (line, contour, { edge = 0.12, ...touch } = {}) => {
    const notes = breathe(line, contour);
    const long = notes.map((n) => (n.beats < 1 ? { ...n, note: "-" } : n));
    const lead = { level: 0.27, attack: 0.04, decay: 6, ...touch };
    const tap = { attack: touch.attack ?? 0.03, decay: lead.decay };
    return {
      lead: play(notes, lead),
      ...(edge && { edge: play(notes, { level: edge, attack: 0.008, decay: 1.5, pick: 1.5, step: 0.6, floor: 18 }) }),
      bloom: play(long, { level: 0.13, attack: 0.3, decay: 9, step: 0.5, floor: 30 }),
      tap1: play(notes, { level: 0.17, ...tap, step: 0.4, floor: 30 }),
      tap2: play(notes, { level: 0.08, ...tap, step: 0.6, floor: 24 }),
      tap3: play(notes, { level: 0.035, ...tap, step: 0.8, floor: 18 }),
    };
  };
  const guitar = (progression, pattern, contour) => {
    const bars = progression.split(" ");
    const touch = touches[pattern];
    const [low, high] = [0, 1].map((string) => breathe(bars.map((chord) => (chord === "-" ? "-:w"
      : picking[pattern][string].replace(/\d/g, (i) => dyads[chord][i]))).join(" "), contour));
    const wash = (lane, string) => {
      const notes = bars.filter((_, i) => i % 2 === lane).map((chord) => ({ note: dyads[chord][string], beats: 8, level: ease(contour, 0.5) }));
      return play(notes, { level: 0.085, attack: 0.5, decay: 10, step: 0.5, floor: 30 });
    };
    const top = (chord) => (chord === "-" ? "-:w" : `${transpose(dyads[chord][1], 12)}:w`);
    return {
      low: play(low, { level: 0.2, step: 0.3, ...touch }), high: play(high, { level: 0.2, step: 0.3, ...touch }),
      ...(pattern !== "swell" && {
        pickLow: play(low, { level: 0.09, attack: 0.006, decay: 1.6, pick: 1.5, step: 0.6, floor: 18 }),
        pickHigh: play(high, { level: 0.09, attack: 0.006, decay: 1.6, pick: 1.5, step: 0.6, floor: 18 }),
      }),
      echoLow: play(low, { ...touch, level: 0.085, step: 0.6, floor: 24 }), echoHigh: play(high, { ...touch, level: 0.085, step: 0.6, floor: 24 }),
      washLowA: wash(0, 0), washHighA: wash(0, 1), washLowB: wash(1, 0), washHighB: wash(1, 1),
      shimmer: play(breathe(bars.map(top).join(" "), contour), { level: 0.035, attack: 0.9, decay: 8, step: 0.6, floor: 24 }),
    };
  };
  const bell = (line, contour, level = 0.05) => ({ bell: play(breathe(line, contour), { level, attack: 0.004, decay: 2.5, step: 0.6, floor: 24 }) });
  const coda = (chord, note, pattern = "strum", touch = {}) => ({
    bars: 4, hum: [0.6, 0.3, 0], ...guitar(`${chord} - - -`, pattern, [0.5, 0.45]), ...melody(`${note}:w -:w -:w -:e`, [0.45, 0.4], touch),
    washLowB: "-:w -:w -:w", washHighB: "-:w -:w -:w",
  });
  const record = ({ root, parts, form, echo = 0.75, drone = 0.1, buzz = 0.02, voices = {} }) => {
    const order = form.split(" ").map((name) => ({ ...parts[name] }));
    // A swell rests through the part of a cycle left at its end, a gap a loud
    // drone ticks at, so each part's note lasts the whole cycles up to where
    // the song's clock is nearest one.
    const undertone = (place, sound) => {
      let clock = 0;
      let droned = 0;
      return order.map((part) => {
        const note = place(part.root ?? root);
        clock += (part.bars * 240) / bpm;
        const beats = (Math.round(hertz(note) * (clock - droned)) / hertz(note)) * (bpm / 60);
        droned += (beats * 60) / bpm;
        return sound(note, beats, part);
      });
    };
    const same = (note) => note;
    const drones = undertone(same, (note, beats, part) => swell(note, beats, bpm, part.hum.map((level) => level * drone), null, 0.2));
    const buzzes = undertone(same, (note, beats, part) => swell(note, beats, bpm, part.hum.map((level) => level * buzz), null, 0.6));
    order.forEach((part, i) => Object.assign(part, { drone: drones[i], buzz: buzzes[i] }));
    const all = {
      lead: "#TYPE sine\n#PAN 0.1", edge: "#TYPE sawtooth\n#PAN 0.1", bloom: "#TYPE sine\n#PAN -0.35",
      tap1: "#TYPE sine\n#PAN 0.6", tap2: "#TYPE sine\n#PAN -0.6", tap3: "#TYPE sine\n#PAN 0.4",
      low: "#TYPE sine\n#PAN -0.2", high: "#TYPE sine\n#PAN 0.15", pickLow: "#TYPE sawtooth\n#PAN -0.2", pickHigh: "#TYPE sawtooth\n#PAN 0.15",
      echoLow: "#TYPE sine\n#PAN 0.5", echoHigh: "#TYPE sine\n#PAN -0.5",
      washLowA: "#TYPE sine\n#PAN -0.6", washHighA: "#TYPE sine\n#PAN -0.45", washLowB: "#TYPE sine\n#PAN 0.6", washHighB: "#TYPE sine\n#PAN 0.45",
      shimmer: "#TYPE sine\n#PAN 0.25", bell: "#TYPE sine\n#PAN -0.3", drone: "#TYPE sine", buzz: "#TYPE sawtooth", ...voices,
    };
    const used = Object.fromEntries(Object.entries(all).filter(([voice]) => order.some((part) => part[voice] !== undefined)));
    const strum = `#BPM ${Math.round(60 / 0.035)}\n-:q\n#BPM ${bpm}`;
    const delays = {
      high: strum, pickHigh: strum, echoLow: rests(echo * 4), echoHigh: `${rests(echo * 4)}\n${strum}`, washLowB: "-:w", washHighB: "-:w",
      bloom: "-:s", tap1: rests(echo * 4), tap2: rests(echo * 8), tap3: rests(echo * 12),
    };
    const [first] = order;
    for (const [voice, rest] of Object.entries(delays)) if (used[voice]) first[voice] = `${rest}\n${first[voice] ?? rests(first.bars * 16)}`;
    const sheet = song({ bpm, voices: used, parts: { ...order }, form: order.map((_, i) => i).join(" ") });
    // The deep undertone replaces the drone and the buzz after normalize, so
    // every other voice keeps its level. A phone speaker cannot play the sub,
    // the growl an octave up carries it there.
    const octave = (n) => (note) => note.replace(/\d$/, n);
    const layer = (n, level, step) => undertone(octave(n), (note, beats, part) => {
      const seconds = (beats * 60) / bpm;
      return tone(note, seconds, bpm, (t) => Math.min(1, ease(part.hum, Math.min(1, t / seconds))) * level, step);
    });
    const write = (voice, type, notes) => [`#TRACK ${voice}`, ...notes.map((cells) => `#BPM ${bpm}\n#PAN 0\n#TYPE ${type}\n${cells}`)].join("\n");
    // parseSheet carries the volume a track ends on into the next one, so the
    // growl ends on what the buzz left and every voice after it plays as it did.
    const tracks = sheet.split(/\n(?=#TRACK )/);
    const left = tracks.find((text) => text.startsWith("#TRACK buzz\n")).match(/#VOLUME [\d.e-]+/g).at(-1);
    return tracks.flatMap((text) => (text.startsWith("#TRACK drone\n") ? [write("sub", "sine", layer(1, 0.33, 0.25)), write("body", "sine", layer(2, 0.24, 0.35))]
      : text.startsWith("#TRACK buzz\n") ? [`${write("growl", "sawtooth", layer(2, 0.07, 0.25))}\n${left}`] : [text])).join("\n");
  };
  return { breathe, play, melody, guitar, bell, coda, record };
}

const durations = { ...quarters, ds: 0.375 };

// A score of Classic Rock may write triplets, t and x, which scriptune lacks,
// so they only place notes and never reach a cell.
const lengths = { ...durations, t: 1 / 3, x: 1 / 6 };

const semitone = (note) => {
  const [, letter, accidental, octave] = note.match(/^([A-G])([#b]?)(\d)$/);
  return Number(octave) * 12 + letterSteps[letter] + (accidentalSteps[accidental] ?? 0);
};
const named = (pitch) => pitchNames[pitch % 12] + Math.floor(pitch / 12);

// scriptune reads a BPM as a whole number, so this times cells the same way.
function seconds(cells) {
  let rate = 60;
  let total = 0;
  for (const line of cells.split("\n")) {
    if (line.startsWith("#BPM")) rate = parseInt(line.slice(5));
    else if (!line.startsWith("#")) for (const cell of line.split(" ").filter(Boolean)) total += (durations[cell.split(":")[1]] * 60) / rate;
  }
  return total;
}

// Every rest starts an oscillator like a note, so a rest takes as few cells
// as it can.
function wait(time) {
  const cells = [];
  let left = time;
  if (left >= 1) {
    cells.push("#BPM 60");
    for (const [value, beats] of [["dw", 6], ["w", 4], ["dh", 3], ["h", 2], ["q", 1]]) for (; left >= beats; left -= beats) cells.push(`-:${value}`);
  }
  if (left > 5e-4) cells.push(fit(left).cell.replace("note", "-"));
  return cells.join("\n");
}

// A tone lasts whole cycles of hz, so it stops at zero right where the next
// one starts at zero. A whole BPM rarely lands on a cycle end, so of the
// cycle counts near seconds and the values of scriptune it takes the cell
// closest to seconds among those within miss of a cycle end.
function fit(seconds, hz = 0, cap = Infinity, miss = 1e-3) {
  let best;
  const consider = (rate, value, cycles) => {
    const time = (60 * durations[value]) / rate;
    if (rate < 1 || time > cap + 1e-9) return;
    const off = cycles ? Math.abs(time * hz - cycles) : 0;
    const score = off > miss ? 1 + off : Math.abs(time - seconds);
    if (!(best?.score <= score)) best = { score, time, cell: `#BPM ${rate}\nnote:${value}` };
  };
  if (!hz) {
    for (const [value, beats] of Object.entries(durations)) consider(Math.round((60 * beats) / seconds), value, 0);
    return best;
  }
  const slack = Math.max(0.004, seconds * 0.08);
  const high = Math.floor(Math.min(seconds + slack, cap) * hz + 1e-9);
  const low = Math.max(1, Math.min(high, Math.ceil((seconds - slack) * hz)));
  for (let cycles = low; cycles <= high; cycles++) {
    for (const [value, beats] of Object.entries(durations)) consider(Math.round((60 * beats * hz) / cycles), value, cycles);
  }
  return best;
}

// A browser starts and stops an oscillator on a sample, so two steps can
// miss each other by one and the wave jumps by its level times how far it
// moves in a sample. edge caps the level for a jump of jump at 44.1 kHz, so
// the highest notes play a little softer.
const edge = (wave, hz, jump) => jump / ({ sine: Math.sin((2 * Math.PI * hz) / 44100), triangle: (4 * hz) / 44100, sawtooth: (2 * hz) / 44100 }[wave] ?? 0);

const slopes = { sine: 2 * Math.PI, triangle: 4, sawtooth: 2 };

// The miss of a whole BPM grows with the square of a step's length times its
// pitch, so a long step splits into even ones. Every step aims at where all
// steps so far should end, so errors never add up. A step lands within miss
// of a cycle end, so the wave jumps there by at most slip.
const renderer = ({ jump, slip }) => (run, room, pan) => {
  const cells = pan === undefined ? [] : [`#PAN ${pan.toFixed(2)}`];
  const steps = run.flatMap(([wave, note, length, level]) => {
    if (wave === "noise") return [[wave, note, length, level, 0, 0]];
    const hz = hertz(note);
    const loud = Math.min(level, edge(wave, hz, jump), 1);
    const miss = Math.max(0.0015, Math.min(0.05, slip / (slopes[wave] * Math.max(loud, 1e-3))));
    const count = Math.max(1, Math.ceil(length / Math.sqrt((2400 * miss) / hz) - 1e-9));
    return Array.from({ length: count }, () => [wave, note, length / count, loud, hz, miss]);
  });
  let time = 0;
  let end = 0;
  let type;
  let volume;
  for (const [wave, note, length, loud, hz, miss] of steps) {
    end += length;
    const target = Math.min(end, room) - time;
    if (target < (hz ? 0.6 / hz : 0.001)) continue;
    const cell = fit(target, hz, room - time, miss);
    if (!cell) continue;
    if (wave !== type) cells.push(`#TYPE ${(type = wave)}`);
    if (loud.toFixed(4) !== volume) cells.push(`#VOLUME ${(volume = loud.toFixed(4))}`);
    cells.push(cell.cell.replace("note", note));
    time += cell.time;
  }
  return cells.join("\n");
};
const sound = renderer({ jump: 0.028, slip: 0.003 });

// Every start is measured from the start of the part, so a voice never
// drifts from the others. A note ends on a whole cycle a little before the
// next one should start, so a sound may start up to early seconds before its
// time instead of after a rest.
function lay(events, total, early = 0.003) {
  const sorted = events.filter((event) => event.at < total).sort((a, b) => a.at - b.at);
  const cells = [];
  let time = 0;
  const rest = (until, gap) => {
    if (until - time <= gap) return;
    const cell = wait(until - time);
    cells.push(cell);
    time += seconds(cell);
  };
  sorted.forEach(({ at, sound, length = Infinity }, i) => {
    rest(at, early);
    const room = Math.min(sorted[i + 1]?.at ?? total, total, at + length) - time;
    if (room < 0.004) return;
    const cell = sound(room);
    cells.push(cell);
    time += seconds(cell);
  });
  rest(total, 5e-4);
  return cells.join("\n");
}

// shade varies a song like a player would, the same on every run, so the
// song never changes.
const shade = (seed, i) => {
  const x = Math.sin(seed * 78.233 + i * 12.9898) * 43758.5453;
  return x - Math.floor(x);
};
const human = (seed, i, spread) => (shade(seed, i) - 0.5) * 2 * spread;
// A hit moved a little before the part starts reads the contour at its start.
const shape = (contour, time, total) => ease(contour, Math.min(1, Math.max(0, time / total)));

const play = (segments, { wave = "sawtooth", level, length, picked = true, pick = 1.2, fall = 3, step = 0.2, vibrato = 0 }) => (room) => {
  const total = Math.min(room, length);
  const pieces = [];
  let t = 0;
  for (const [note, size] of segments) {
    const end = size ? Math.min(total, t + size) : total;
    if (end - t > 1e-4) pieces.push([note, t, end]);
    t = end;
  }
  if (!pieces.length) return "";
  if (vibrato === 2) {
    const [note, from, to] = pieces.pop();
    const low = transpose(note, -1);
    let s = Math.min(to, from + 0.14);
    pieces.push([note, from, s]);
    for (let down = true; s < to - 1e-4; down = !down) {
      const e = Math.min(to, s + (down ? 0.05 : 0.12));
      pieces.push([down ? low : note, s, e]);
      s = e;
    }
  }
  const at = (time) => level * 10 ** ((-fall * time) / 20) * (vibrato === 1 && time > 0.15 ? 1 + 0.14 * Math.sin(2 * Math.PI * 5.5 * (time - 0.15)) : 1);
  const run = [];
  for (const [note, from, to] of pieces) {
    let s = from;
    if (picked && from === 0) {
      s = Math.min(to, 0.018);
      run.push([wave, note, s, level * pick]);
    }
    const count = Math.max(1, Math.round((to - s) / (vibrato === 1 ? 0.045 : step)));
    for (let i = 0; i < count; i++) {
      const a = s + ((to - s) * i) / count;
      const b = s + ((to - s) * (i + 1)) / count;
      if (b - a > 1e-4) run.push([wave, note, b - a, at((a + b) / 2)]);
    }
  }
  // A note that ends before its room does dies away instead of stopping dead.
  if (room > total + 0.06) {
    const [note] = pieces.at(-1);
    run.push([wave, note, 0.025, at(total) * 0.45], [wave, note, 0.03, at(total) * 0.18]);
  }
  return sound(run, room);
};

// A track plays one note at a time, so the body and the burst of the snare
// sound on voices of their own.
const drumset = (tail) => {
  const tone = (wave, note, cycles, level) => [wave, note, cycles / hertz(note), level];
  const noise = (band, run) => run.map(([length, level]) => ["noise", band, length, level]);
  const tom = (note, pan) => [["tom", [...noise("A5", [[0.004, 0.25]]), tone("triangle", transpose(note, 12), 1, 0.85),
    tone("sine", transpose(note, 5), 2, 1), tone("sine", note, 6, 0.85), tone("sine", note, 10, 0.45), tone("sine", note, 14, 0.18)], pan]];
  return {
    K: [["kick", [...noise("D6", [[0.003, 0.3]]), tone("triangle", transpose(tail, 19), 1, 0.8), tone("triangle", transpose(tail, 12), 2, 0.9),
      tone("sine", transpose(tail, 5), 2, 1), tone("sine", tail, 4, 0.9), tone("sine", tail, 5, 0.55), tone("sine", tail, 6, 0.25)]]],
    S: [["shell", [tone("triangle", "A3", 1, 0.6), tone("sine", "E3", 3, 0.65), tone("sine", "E3", 4, 0.3)]],
      ["snare", [...noise("F6", [[0.012, 0.42]]), ...noise("C7", [[0.025, 0.3], [0.035, 0.19], [0.05, 0.11], [0.07, 0.05], [0.09, 0.02]])]]],
    G: [["shell", [tone("sine", "E3", 2, 0.3)]], ["snare", noise("C7", [[0.015, 0.16], [0.03, 0.06]])]],
    H: [["hat", noise("B7", [[0.008, 0.16], [0.022, 0.065]])]],
    O: [["hat", noise("A7", [[0.02, 0.13], [0.05, 0.1], [0.08, 0.065], [0.1, 0.03]])]],
    C: [["crash", [...noise("A7", [[0.02, 0.28]]), ...noise("C8", [[0.06, 0.22], [0.12, 0.155]]), ...noise("B7", [[0.2, 0.1], [0.3, 0.058]]),
      ...noise("A7", [[0.45, 0.028], [0.6, 0.012]])]]],
    Y: [["ride", [tone("sine", "C#7", 20, 0.07), ...noise("D8", [[0.04, 0.09], [0.1, 0.05], [0.15, 0.02]])]]],
    T: tom("D3", -0.25),
    M: tom("A2", 0.05),
    F: tom("E2", 0.35),
  };
};

function stage(bpm, { bend = 0.05, slide = 0.025, echo = 0.75, tail = "G1" } = {}) {
  const beat = 60 / bpm;
  const hits = drumset(tail);
  const onBeat = (at) => Math.abs(at - Math.round(at)) < 1e-6;

  const read = (line) => {
    let at = 0;
    return line.split(/[\s|]+/).filter(Boolean).flatMap((cell) => {
      const [, note, value, tail] = cell.match(/^(.+):([a-z]+)(.*)$/);
      const beats = lengths[value];
      at += beats;
      return note === "-" ? [] : [{ mark: note + tail, at: at - beats, beats }];
    });
  };
  // scriptune knows no pitch between semitones, so a bend or a slide climbs
  // in them.
  const path = (notes) => {
    const [first, ...moves] = notes.split(/([>/])/);
    const segments = [[first, 0]];
    for (let i = 0; i < moves.length; i += 2) {
      const [move, to] = [moves[i], moves[i + 1]];
      const from = segments.at(-1)[0];
      const size = move === ">" ? bend : slide;
      const way = Math.sign(semitone(to) - semitone(from));
      if (!way) continue;
      segments.at(-1)[1] = size;
      for (let pitch = semitone(from) + way; pitch !== semitone(to); pitch += way) segments.push([named(pitch), size]);
      segments.push([to, 0]);
    }
    return segments;
  };
  const shift = (segments, steps) => segments.map(([note, size]) => [transpose(note, steps), size]);

  const lead = (cells, { level = 1, contour = [1, 1], late = 0.01, ring = 0.25, wave = "sawtooth", share = 0.28, pick = 1.2, fall = 2.5, seed = 2 } = {}) => (put, bars, total) => {
    const notes = read(cells).map((note) => ({ ...note, parts: note.mark.match(/^(_?)([^~]+)(~*)$/) }));
    notes.forEach(({ parts: [, mark, run, shake], at, beats }, n) => {
      const before = notes[n - 1];
      const slur = beats < 0.3 && !onBeat(at * 2) && before?.at + before?.beats > at - 1e-6 && before.parts[2].split(/[>/]/).at(-1) !== run.split(/[>/]/)[0];
      const legato = mark === "_" || slur;
      const time = at * beat + late + human(seed, n, 0.004);
      const loud = 0.4 * level * (onBeat(at) ? 1 : 0.92) * (legato ? 0.9 : 1) * shape(contour, time, total);
      const length = (beats + ring) * beat;
      const segments = path(run);
      put("lead", time, play(segments, { wave, level: loud, length, picked: !legato, pick, fall, vibrato: shake.length }), length);
      put("echo", time + echo * beat, play(segments, { wave, level: loud * share, length, picked: false, fall, step: 0.25 }), length);
    });
  };
  // A phone speaker cannot play the triangle of the bass, the overtones of
  // the sawtooth an octave up carry the line there.
  const thump = (put, time, segments, level, length, mute) => {
    const span = mute ? Math.min(length, 0.2) : length;
    put("bass", time, play(segments, { wave: "triangle", level: 0.85 * level, length: span, pick: 1.1, fall: mute ? 30 : 5, step: 0.25 }), span);
    put("growl", time, play(shift(segments, 12), { level: 0.16 * level, length: span, pick: 1.3, fall: mute ? 40 : 12 }), span);
  };
  const bassline = (cells, { level = 1, contour = [1, 1], late = 0.004, seed = 5 } = {}) => (put, bars, total) => {
    read(cells).forEach(({ mark, at, beats }, n) => {
      const [, notes, mute, ring] = mark.match(/^([^,~]+)(,?)(~?)$/);
      const time = at * beat + late + human(seed, n, 0.003);
      const loud = level * (onBeat(at) ? 1 : 0.9) * shape(contour, time, total);
      thump(put, time, path(notes), loud, (beats + (ring ? 0.5 : 0.1)) * beat, !!mute);
    });
  };
  // Every string of a power chord sounds on a voice of its own, so each one
  // stops at zero.
  const riff = (cells, { level = 1, contour = [1, 1], late = 0.004, seed = 4 } = {}) => (put, bars, total) => {
    read(cells).forEach(({ mark, at, beats }, n) => {
      const [, notes, chord, mute, ring] = mark.match(/^([^\^,~]+)(\^?)(,?)(~?)$/);
      const time = at * beat + late + human(seed, n, 0.003);
      const loud = level * (onBeat(at) ? 1 : 0.9) * shape(contour, time, total) * (1 + human(seed + 1, n, 0.04));
      const length = (beats + (ring ? 0.5 : 0)) * beat;
      const span = mute ? Math.min(length, 0.17) : length;
      const segments = path(notes);
      const strings = chord ? [["rh1", 0, 0.24], ["rh5", 7, 0.18], ["rh8", 12, 0.13]] : [["rh1", 0, 0.27]];
      strings.forEach(([voice, steps, share], k) => {
        const touch = { level: loud * share, length: span, pick: 1.15, fall: mute ? 40 : 3, step: mute ? 0.05 : 0.2 };
        put(voice, time + k * 0.006, play(shift(segments, steps), touch), span);
      });
      thump(put, time, shift(segments, -12), loud, length, !!mute);
    });
  };
  // The notes of the acoustic take turns on lanes voices, so each one rings
  // on under the next ones.
  const fingers = (progression, voicing, patterns, { level = 1, contour = [1, 1], lanes = 3, seed = 7 } = {}) => (put, bars, total) => {
    const chords = progression.split(" ");
    const all = [patterns].flat().map((pattern) => pattern.replace(/\s/g, ""));
    let n = 0;
    for (let bar = 0; bar < bars; bar++) {
      const chord = voicing[chords[bar % chords.length]];
      const pattern = all[bar % all.length];
      [...pattern].forEach((place, i) => {
        if (place === "." || !chord) return;
        const at = bar * 4 + (i * 4) / pattern.length;
        const time = at * beat + 0.012 + human(seed, n, 0.006);
        const loud = 0.4 * level * (onBeat(at) ? 1 : 0.85) * shape(contour, time, total) * (1 + human(seed + 1, n, 0.08));
        const note = chord[Number(place)];
        put(`a${(n++ % lanes) + 1}`, time, play([[note, 0]], { wave: "triangle", level: loud, length: 2.4, pick: 1.4, fall: 12, step: 0.25 }));
        put("nail", time, (room) => sound([["sawtooth", note, 0.012, loud * 0.35], ["sawtooth", note, 0.03, loud * 0.12]], room));
      });
    }
  };
  const drums = (grids, { level = 1, contour = [1, 1], late = { snare: 0.02, hat: 0.008, tom: 0.01 }, seed = 3 } = {}) => (put, bars, total) => {
    for (const [lane, grid] of Object.entries(grids)) {
      const rows = grid.replace(/\s/g, "").split("|");
      let n = 0;
      for (let bar = 0; bar < bars; bar++) {
        const row = rows[bar % rows.length];
        [...row].forEach((step, i) => {
          if (step === ".") return;
          const beats = bar * 4 + (i * 4) / row.length;
          const at = beats * beat + (late[lane] ?? 0) + (lane === "kick" ? 0 : human(seed, n, 0.003));
          const loud = level * (step === step.toUpperCase() ? 1 : 0.5) * shape(contour, at, total) * (1 - 0.08 * shade(seed + 1, n++));
          for (const [voice, run, pan] of hits[step.toUpperCase()]) {
            put(voice, at, (room) => sound(run.map(([wave, note, length, volume]) => [wave, note, length, volume * loud]), room, pan));
          }
        });
      }
    }
  };

  // The drums are voices of their own, so the kit track of song stays empty.
  const part = (bars, ...layers) => {
    const lanes = {};
    const total = bars * 4 * beat;
    const put = (voice, at, sound, length) => (lanes[voice] ??= []).push({ at: Math.max(0, at), sound, length });
    for (const layer of layers.flat()) layer(put, bars, total);
    return { bars, drums: "", ...Object.fromEntries(Object.entries(lanes).map(([voice, events]) => [voice, lay(events, total)])) };
  };
  // The first voice sets a volume of 1 before it plays, so normalize leaves
  // every volume as written and edge holds.
  const record = ({ voices, parts, form }) => {
    const used = Object.fromEntries(Object.entries(voices).filter(([voice]) => Object.values(parts).some((p) => p[voice] !== undefined)));
    const filled = Object.fromEntries(Object.entries(parts).map(([name, p]) => [name, {
      ...Object.fromEntries(Object.keys(used).map((voice) => [voice, wait(p.bars * 4 * beat)])), ...p,
    }]));
    const [first] = Object.keys(used);
    return song({ bpm, voices: { ...used, [first]: `${used[first]}\n#VOLUME 1` }, parts: filled, form });
  };
  return { lead, bassline, riff, fingers, drums, part, record };
}

const drummer = (...bars) => {
  const all = bars.flat(Infinity);
  const lanes = [...new Set(all.flatMap((bar) => Object.keys(bar)))];
  return Object.fromEntries(lanes.map((lane) => [lane, all.map((bar) => bar[lane] ?? "....").join("|")]));
};
const times = (n, bar) => Array(n).fill(bar);
const crash = (bar) => ({ ...bar, cym: "C..." });

const rock = { kick: "K.....K.K.......", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.H." };
const rock2 = { kick: "K.....KK..K.....", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.O." };
const ride = { ...rock, hat: undefined, ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
const ride2 = { kick: "K.....KK..K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
const toms = { kick: "K.....K.K.......", snare: "....S...........", hat: "H.H.H.H.H.H.....", tom: "............TTFF" };
const down = { kick: "K.......K.......", snare: "....S...S.GG....", hat: "H.H.H.H.........", tom: "............MMFF" };
const triplets = { kick: "K..K..K..K..", tom: ".TT.MM.FF.FF" };
const thunder = { kick: "K.....K.....K.....K.....", tom: "..TTTT..MMMM..FFFF..FFFF", cym: "C..." };

const band = {
  kick: "#PAN 0", bass: "#PAN 0", growl: "#PAN 0", lead: "#PAN 0.2", echo: "#PAN -0.5",
  rh1: "#PAN -0.3", rh5: "#PAN 0.3", rh8: "#PAN -0.1", shell: "#PAN 0.05", snare: "#PAN 0.05",
  hat: "#PAN -0.3", tom: "#PAN 0", crash: "#PAN -0.4", ride: "#PAN 0.35",
};
const strings = { a1: "#PAN -0.35", a2: "#PAN -0.1", a3: "#PAN 0.15", a4: "#PAN 0.35", a5: "#PAN -0.2", a6: "#PAN 0.25", nail: "#PAN 0" };

// The party shelves keep their highest notes softer than Classic Rock, so a
// step may miss its cycle end by more and still stays clean.
const blare = renderer({ jump: 0.02, slip: 0.008 });

const move = (note, steps) => named(semitone(note) + steps);

// A deep loud sine that stops off a cycle end clicks louder than any other
// voice, so every step of the sub moves by up to two cycles to where a whole
// BPM lands closest to one. The last step ends 5 ms early, so a voice that
// comes in a little early never cuts it.
const settle = (run) => {
  const pieces = run.flatMap(([wave, note, length, level]) => {
    const count = Math.ceil(length / (0.95 * Math.sqrt((2400 * 0.0015) / hertz(note))));
    return Array.from({ length: count }, () => [wave, note, length / count, level]);
  });
  const out = [];
  let goal = 0;
  let time = 0;
  pieces.forEach(([wave, note, length, level], i) => {
    const hz = hertz(note);
    const last = i === pieces.length - 1;
    goal += length - (last ? 0.005 : 0);
    let best = null;
    for (let shift = -2; shift <= 2; shift++) {
      const cell = goal - time + shift / hz >= 1 / hz && fit(goal - time + shift / hz, hz, last ? goal - time : Infinity, 0.0015);
      if (!cell) continue;
      const rank = (cell.score >= 1 ? cell.score - 1 : 0) + Math.abs(time + cell.time - goal) * 1e-3;
      if (!best || rank < best.rank) best = { rank, time: cell.time };
    }
    if (!best) return;
    out.push([wave, note, best.time, level]);
    time += best.time;
  });
  return out;
};

// A note has no envelope, so ring builds it from steps. A real string loses
// its overtones first, so a struck note darkens through waves, and duck
// pulls it under every kick as a compressor keyed by the kick does, so the
// kick comes through and the note pumps.
const ring = (segments, { level, length, waves = [["sine", 0]], onset = [], fall = 0, step = 0.2, shake = 0, floor = 36, duck = null }) => (room) => {
  const total = Math.min(room, length, fall ? floor / fall : Infinity);
  const pieces = [];
  let t = 0;
  for (const [note, size] of segments) {
    const end = size ? Math.min(total, t + size) : total;
    if (end - t > 1e-4) pieces.push([note, t, end]);
    t = end;
  }
  if (!pieces.length || level < 1e-4) return "";
  const cuts = new Set([total]);
  let start = 0;
  for (const [size] of onset) cuts.add(Math.min(total, (start += size)));
  let change = 0;
  for (const [, size] of waves.slice(0, -1)) cuts.add(Math.min(total, (change += size)));
  for (const [, from] of pieces) cuts.add(from);
  const pace = (x) => (shake && x >= 0.2 ? 0.048 : step * (1 + x));
  for (let x = start + pace(start); x < total - pace(x) / 3; x += pace(x)) cuts.add(x);
  const near = duck ? duck.kicks.filter((kick) => kick > duck.start - 0.07 && kick < duck.start + total) : [];
  for (const kick of near) for (const after of [0, 0.07]) cuts.add(kick - duck.start + after);
  const times = [0, ...[...cuts].filter((x) => x > 1e-6 && x <= total).sort((a, b) => a - b)];
  const dip = (time) => (near.some((kick) => duck.start + time > kick && duck.start + time < kick + 0.07) ? 1 - duck.depth : 1);
  const waveAt = (time) => {
    let sum = 0;
    for (const [wave, size] of waves) if (time < (sum += size) - 1e-9 || !size) return wave;
    return waves.at(-1)[0];
  };
  const noteAt = (time) => (pieces.find(([, from, to]) => time >= from - 1e-9 && time < to - 1e-9) ?? pieces.at(-1))[0];
  const at = (time) => level * 10 ** ((-fall * time) / 20) * (shake && time > 0.2 ? 1 + shake * Math.sin(2 * Math.PI * 5.2 * (time - 0.2)) : 1);
  const run = [];
  for (let i = 1; i < times.length; i++) {
    const [a, b] = [times[i - 1], times[i]];
    let into = 0;
    const lead = onset.find(([size]) => a < (into += size) - 1e-9);
    run.push([waveAt(a), noteAt(a), b - a, (lead ? level * lead[1] : at((a + b) / 2)) * dip((a + b) / 2)]);
  }
  if (room > total + 0.06) {
    const [wave, note] = run.at(-1);
    run.push([wave, note, 0.03, at(total) * 0.35]);
  }
  return blare(run, room);
};

// A track plays one note at a time, so every layer of an instrument sounds
// on a voice of its own.
const instrument = (...layers) => (segments, level, length, { voice = "", slur = false, shake = false, duck = null } = {}) => layers
  .filter(({ strike }) => !(strike && slur))
  .map(({ voice: name, shift = 0, share = 1, onset, slur: soft = onset, vibrato = 0, strike, ...shape }) => [name.replace("#", voice), ring(
    segments.map(([note, size]) => [move(note, shift), size]),
    { ...shape, onset: slur ? soft : onset, shake: shake ? vibrato : 0, level: level * share, length, duck },
  )]);

const struck = (wave, note, cycles, level) => [wave, note, cycles / hertz(note), level];
const hiss = (band, run) => run.map(([length, level]) => ["noise", band, length, level]);

// A track plays one note at a time, so the body and the burst of the snare
// sound on voices of their own.
const breaks = (tail = "A1") => ({
  K: [["kick", [...hiss("D6", [[0.004, 0.5]]), struck("triangle", move(tail, 24), 2, 0.95), struck("triangle", move(tail, 12), 2, 0.9),
    struck("sine", move(tail, 5), 2, 1), struck("sine", tail, 4, 0.85)]]],
  S: [["shell", [struck("triangle", "D4", 1, 0.5), struck("sine", "G3", 5, 0.5)]],
    ["snare", [...hiss("F6", [[0.008, 0.45]]), ...hiss("C7", [[0.03, 0.3], [0.05, 0.12], [0.07, 0.035]])]]],
  G: [["snare", hiss("C7", [[0.016, 0.1]])]],
  R: [["rim", [struck("triangle", "F5", 3, 0.3), ...hiss("D7", [[0.005, 0.14]])]]],
  H: [["hat", hiss("E8", [[0.008, 0.14]])]],
  O: [["hat", hiss("A7", [[0.03, 0.11], [0.08, 0.05], [0.1, 0.015]])]],
  T: [["shaker", hiss("D8", [[0.022, 0.05]])]],
  Y: [["ride", [struck("sine", "D#7", 24, 0.05), ...hiss("D8", [[0.05, 0.07], [0.12, 0.035]])]]],
  C: [["crash", [...hiss("A7", [[0.02, 0.22]]), ...hiss("C8", [[0.06, 0.17], [0.12, 0.11]]), ...hiss("B7", [[0.2, 0.065], [0.3, 0.03]]),
    ...hiss("A7", [[0.45, 0.012]])]]],
});

const tune = (melody) => {
  const notes = [];
  let at = 0;
  let open = null;
  const step = (mark, beats) => {
    if (mark === ".") {
      if (open) open.beats += beats;
    } else if (mark === "-") open = null;
    else notes.push((open = { mark, at, beats }));
    at += beats;
  };
  for (const cell of melody.split(/[\s|]+/).filter(Boolean)) {
    const [mark, count = "1"] = cell.split("*");
    for (let i = 0; i < Number(count); i++) {
      if (mark.includes("/")) for (const half of mark.split("/")) step(half, 0.25);
      else step(mark, 0.5);
    }
  }
  return notes;
};

const pause = (bars) => Array(bars).fill("-*8").join(" | ");

// The bass of a drop goes as loud as a step may before a step that misses
// its cycle end clicks, and its kick hits a little harder, so it keeps its
// lead over the bass.
const bang = { level: 1.18, tone: { body: 1, harm: 0, bite: 0.09, depth: 1 }, gains: { kick: 1.2 } };

function arena(bpm, { bend = 0.03, tail = "A1" } = {}) {
  const beat = 60 / bpm;
  const hits = breaks(tail);

  const read = (line) => {
    let at = 0;
    return line.split(/[\s|]+/).filter(Boolean).flatMap((cell) => {
      const [, note, value, tail] = cell.match(/^(.+?):([a-z]+)(.*)$/);
      const beats = durations[value];
      at += beats;
      return note === "-" ? [] : [{ mark: note + tail, at: at - beats, beats }];
    });
  };
  const span = (line) => line.split(/[\s|]+/).filter(Boolean).reduce((sum, cell) => sum + durations[cell.match(/:([a-z]+)/)[1]], 0);
  const path = (notes, shift = 0) => {
    const [first, ...moves] = notes.split(">").map((note) => move(note, shift));
    const segments = [[first, 0]];
    for (const to of moves) {
      const from = segments.at(-1)[0];
      const way = Math.sign(semitone(to) - semitone(from));
      if (!way) continue;
      segments.at(-1)[1] = bend;
      for (let pitch = semitone(from) + way; pitch !== semitone(to); pitch += way) segments.push([named(pitch), bend]);
      segments.push([to, 0]);
    }
    return segments;
  };

  // A phone cannot play the sub and a small speaker plays only the overtones
  // of the body, so the bass layers a sine, a triangle and a sawtooth. Every
  // kick ducks the sub and the body, so the two never mask each other and the
  // bass pumps back in after it.
  const thump = (segments, level, length, start, kicks, { sub = 1, body = 0.4, harm = 0.3, bite = 0.09, fall = 3, depth = 0.85 } = {}) => [
    ["sub", -12, sub, [["sine", 0]], fall / 4, depth],
    ["body", 0, body, [["triangle", 0]], fall, depth * 0.8],
    ["harm", 12, harm, [["sawtooth", bite], ["triangle", 0]], fall * 4, 0],
  ].filter(([, , share]) => share > 0).map(([voice, shift, share, waves, drop, deep]) => [voice, (room) => {
    const total = Math.min(room, length);
    const pieces = [];
    let t = 0;
    for (const [note, size] of segments) {
      const end = size ? Math.min(total, t + size) : total;
      if (end - t > 1e-4) pieces.push([move(note, shift), t, end]);
      t = end;
    }
    if (!pieces.length || level < 1e-4) return "";
    const near = deep ? kicks.filter((kick) => kick > start - 0.2 && kick < start + total) : [];
    const cuts = new Set([total, ...pieces.map(([, from]) => from), waves[0][1]]);
    for (const kick of near) for (const after of [0, 0.04, 0.085, 0.14]) cuts.add(kick - start + after);
    for (let x = 0.25; x < total - 0.1; x += 0.25) cuts.add(x);
    const times = [0, ...[...cuts].filter((x) => x > 1e-6 && x <= total).sort((a, b) => a - b)];
    const duck = (time) => 1 - deep * Math.max(0, ...near.map((kick) => (start + time < kick ? 0 : Math.exp(-Math.max(0, start + time - kick - 0.025) / 0.045))));
    const noteAt = (time) => (pieces.find(([, from, to]) => time >= from - 1e-9 && time < to - 1e-9) ?? pieces.at(-1))[0];
    const run = [];
    for (let i = 1; i < times.length; i++) {
      const [a, b] = [times[i - 1], times[i]];
      const wave = waves.length > 1 && a < waves[0][1] - 1e-9 ? waves[0][0] : waves.at(-1)[0];
      run.push([wave, noteAt(a), b - a, share * level * (wave === "sawtooth" ? 1.25 : 1) * 10 ** ((-drop * a) / 20) * duck((a + b) / 2)]);
    }
    if (room > total + 0.06) {
      const [wave, note, , loud] = run.at(-1);
      run.push([wave, note, 0.03, loud * 0.35]);
    }
    return blare(run, room);
  }]);

  const roll = (figure, progression, chords, { level = 1, contour = [1, 1], late = 0.002, tone = {}, seed = 5 } = {}) => (put, bars, total, mix) => {
    const names = progression.trim().split(/\s+/);
    const notes = read(figure);
    const length = span(figure);
    const degrees = { R: 0, 2: 2, 3: 3, 4: 5, 5: 7, 6: 9, 7: 10, 8: 12, L: -5 };
    let n = 0;
    for (let from = 0; from < bars * 4 - 1e-9; from += length) {
      for (const { mark, at: into, beats } of notes) {
        const at = from + into;
        if (at >= bars * 4 - 1e-9) break;
        const bar = Math.floor(at / 4 + 1e-9);
        const [root] = chords[names[bar % names.length]];
        const [next] = chords[names[(bar + 1) % names.length]];
        const [, run, marks] = mark.match(/^([^',]+)([',]*)$/);
        const note = (degree) => (degree === "N" ? next : /^[A-G]/.test(degree) ? degree : move(root, degrees[degree]));
        const time = at * beat + late + human(seed, n, 0.002);
        const loud = level * (marks.includes("'") ? 0.55 : 1) * shape(contour, time, total) * (1 + human(seed + 1, n++, 0.03));
        const hold = (marks.includes(",") ? Math.min(beats, 0.35) : beats + 0.05) * beat;
        for (const [voice, sound] of thump(path(run.split(">").map(note).join(">")), loud, hold, time, mix.kicks, tone)) put(voice, time, sound, hold);
      }
    }
  };

  // Every kick goes into the mix, so the bass can duck under it.
  const drums = (grids, { level = 1, contour = [1, 1], late = { snare: 0.006, hat: 0.003 }, gains = {}, soft = 0.5, swing = 0, seed = 3 } = {}) => (put, bars, total, mix) => {
    for (const [lane, grid] of Object.entries(grids)) {
      const rows = grid.replace(/\s/g, "").split("|");
      let n = 0;
      for (let bar = 0; bar < bars; bar++) {
        const row = rows[bar % rows.length];
        [...row].forEach((step, i) => {
          if (step === ".") return;
          const sixteenth = (i * 16) / row.length;
          const shuffle = Number.isInteger(sixteenth) && sixteenth % 2 ? (swing * beat) / 4 : 0;
          const at = (bar * 4 + (i * 4) / row.length) * beat + shuffle + (late[lane] ?? 0) + (lane === "kick" ? 0 : human(seed, n, 0.002));
          const loud = level * (gains[lane] ?? 1) * (step === step.toUpperCase() ? 1 : soft) * shape(contour, at, total) * (1 - 0.06 * shade(seed + 1, n++));
          if (step.toUpperCase() === "K") mix.kicks.push(at);
          for (const [name, run] of hits[step.toUpperCase()]) put(name, at, (room) => blare(run.map(([wave, note, length, volume]) => [wave, note, length, volume * loud]), room));
        });
      }
    }
  };

  const slam = (figure, progression, chords, { level = 0.8, ...options } = {}) => roll(figure, progression, chords, { ...options, level: level * bang.level, tone: bang.tone });
  const pound = (grids, options = {}) => drums(grids, { ...options, gains: { ...options.gains, ...bang.gains } });

  const sing = (play, melody, { voice = "lead", level = 1, contour = [1, 1], late = 0.008, ring = 0.25, delay = 0, shift = 0, pump = 0, seed = 2 } = {}) => (put, bars, total, mix) => {
    tune(melody).forEach(({ mark, at, beats }, n) => {
      const [, slurred, run, marks] = mark.match(/^(_?)([^~!']+)([~!']*)$/);
      const time = (at + delay) * beat + late + human(seed, n, 0.006);
      const loud = level * shape(contour, time, total) * (beats >= 1 ? 1 : 0.9) * (marks.includes("!") ? 1.15 : 1) * (marks.includes("'") ? 0.5 : 1)
        * (1 + human(seed + 1, n, 0.04));
      const length = (beats + ring) * beat;
      const duck = pump ? { kicks: mix.kicks, start: time, depth: pump } : null;
      for (const [name, sound] of play(path(run, shift), loud, length, { voice, slur: slurred === "_", shake: marks.includes("~"), duck })) put(name, time, sound, length);
    });
  };

  const stab = (play, riff, chords, { level = 1, contour = [1, 1], hold = 4, strum = 0.004, late = 0.004, pump = 0, prefix = "s", seed = 7 } = {}) => (put, bars, total, mix) => {
    tune(riff).forEach(({ mark, at, beats }, n) => {
      const [, name, marks] = mark.match(/^(.+?)([!']*)$/);
      const time = at * beat + late + human(seed, n, 0.003);
      const loud = level * shape(contour, time, total) * (marks.includes("!") ? 1.15 : 1) * (marks.includes("'") ? 0.55 : 1) * (1 + human(seed + 1, n, 0.04));
      const length = Math.min(beats, hold) * beat;
      chords[name].slice(1).forEach((note, k) => {
        const duck = pump ? { kicks: mix.kicks, start: time + k * strum, depth: pump } : null;
        for (const [voice, sound] of play([[note, 0]], loud, length, { voice: `${prefix}${k + 1}`, duck })) put(voice, time + k * strum, sound, length);
      });
    });
  };

  const pad = (progression, chords, { wave = "sawtooth", level = 1, contour = [1, 1], swell = 0.6, fall = 1.5, shift = 0, pump = 0, prefix = "p" } = {}) => (put, bars, total, mix) => {
    const names = progression.trim().split(/\s+/);
    for (let bar = 0; bar < bars;) {
      const name = names[bar % names.length];
      let run = 1;
      while (bar + run < bars && names[(bar + run) % names.length] === name) run++;
      const time = bar * 4 * beat;
      const length = run * 4 * beat;
      const loud = level * shape(contour, time, total);
      if (name !== "-") chords[name].slice(1).forEach((note, k) => {
        const onset = [[swell / 3, 0.25], [swell / 3, 0.55], [swell / 3, 0.85]];
        const duck = pump ? { kicks: mix.kicks, start: time, depth: pump } : null;
        put(`${prefix}${k + 1}`, time, ring([[move(note, shift), 0]], { level: loud, length, waves: [[wave, 0]], onset, fall, step: 0.3, floor: 40, duck }), length);
      });
      bar += run;
    }
  };

  const rise = (at, beats, { level = 0.1, voice = "fx" } = {}) => (put) => {
    const bands = ["C6", "F6", "A#6", "D#7", "G#7", "C#8", "F#8"];
    const count = Math.max(1, Math.round((beats * beat) / 0.12));
    const run = Array.from({ length: count }, (_, i) => ["noise", bands[Math.floor((i * bands.length) / count)], (beats * beat) / count, level * ((i + 1) / count) ** 1.6]);
    put(voice, at * beat, (room) => blare(run, room));
  };
  const fall = (at, beats, { level = 0.08, voice = "wash" } = {}) => (put) => {
    const bands = ["F#8", "C#8", "G#7", "D#7", "A#6", "F6", "C6"];
    const count = Math.max(1, Math.round((beats * beat) / 0.12));
    const run = Array.from({ length: count }, (_, i) => ["noise", bands[Math.floor((i * bands.length) / count)], (beats * beat) / count, level * (1 - i / count) ** 2]);
    put(voice, at * beat, (room) => blare(run, room));
  };
  const sweep = (at, beats, from, to, { wave = "triangle", level = 0.08, voice = "siren" } = {}) => (put) => {
    const count = Math.abs(semitone(to) - semitone(from)) + 1;
    const way = Math.sign(semitone(to) - semitone(from));
    const run = Array.from({ length: count }, (_, i) => [wave, named(semitone(from) + i * way), (beats * beat) / count, level * (0.15 + 0.85 * ((i + 1) / count) ** 1.5)]);
    put(voice, at * beat, (room) => blare(run, room));
  };

  // The drums are voices of their own, so the kit track of song stays empty.
  const part = (bars, ...layers) => {
    const lanes = {};
    const mix = { kicks: [] };
    const total = bars * 4 * beat;
    const put = (voice, at, sound, length) => (lanes[voice] ??= []).push({ at: Math.max(0, at), sound, length });
    for (const layer of layers.flat()) layer(put, bars, total, mix);
    return { bars, drums: "", ...Object.fromEntries(Object.entries(lanes).map(([voice, events]) => [voice, lay(events, total, 0.012)])) };
  };
  // The first voice sets a volume of 1 before it plays, so normalize leaves
  // every volume as written and edge holds.
  const record = ({ voices, parts, form }) => {
    const used = Object.fromEntries(Object.entries(voices).filter(([voice]) => Object.values(parts).some((p) => p[voice] !== undefined)));
    const filled = Object.fromEntries(Object.entries(parts).map(([name, p]) => [name, {
      ...Object.fromEntries(Object.keys(used).map((voice) => [voice, wait(p.bars * 4 * beat)])), ...p,
    }]));
    const [first] = Object.keys(used);
    return song({ bpm, voices: { ...used, [first]: `${used[first]}\n#VOLUME 1` }, parts: filled, form });
  };
  return { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record };
}

const desk = {
  kick: "#PAN 0", sub: "#PAN 0", body: "#PAN 0", harm: "#PAN 0", shell: "#PAN 0.05", snare: "#PAN 0.05", rim: "#PAN -0.15",
  hat: "#PAN 0.3", shaker: "#PAN -0.35", ride: "#PAN 0.35", crash: "#PAN -0.3",
  lead: "#PAN -0.12", leado: "#PAN 0.4", echo: "#PAN -0.5",
  s1: "#PAN -0.45", s2: "#PAN -0.15", s3: "#PAN 0.15", s4: "#PAN 0.45",
  p1: "#PAN -0.6", p2: "#PAN 0.6", p3: "#PAN -0.3", p4: "#PAN 0.3",
  k1: "#PAN -0.3", k2: "#PAN -0.1", k3: "#PAN 0.1", k4: "#PAN 0.3",
  fx: "#PAN 0", wash: "#PAN 0", siren: "#PAN 0",
};
const hall = { ...desk, vox: "#PAN 0.08", b1: "#PAN -0.45", b2: "#PAN 0.45", b3: "#PAN 0" };

const anthem = instrument(
  { voice: "#", waves: [["sawtooth", 0]], onset: [[0.012, 1.2]], slur: [[0.01, 1]], fall: 1.5, step: 0.3, vibrato: 0.06 },
  { voice: "#o", shift: 12, share: 0.5, waves: [["sawtooth", 0]], onset: [[0.012, 1.2]], slur: [[0.01, 1]], fall: 2.5, step: 0.35, floor: 24 },
);
const spark = instrument(
  { voice: "#", waves: [["sawtooth", 0.06], ["triangle", 0]], onset: [[0.01, 1.2]], fall: 8, step: 0.2, floor: 27 },
  { voice: "#o", shift: 12, share: 0.4, waves: [["sawtooth", 0.04], ["triangle", 0]], onset: [[0.01, 1.2]], fall: 11, step: 0.2, floor: 24 },
);
const bell = instrument({ voice: "#", waves: [["sine", 0]], onset: [[0.01, 1.2]], fall: 4, step: 0.35, floor: 30 },
  { voice: "#o", shift: 24, share: 0.2, waves: [["sine", 0]], fall: 20, step: 0.15, floor: 18, strike: true });
const rave = instrument({ voice: "#", waves: [["sawtooth", 0]], onset: [[0.012, 1.25]], fall: 7, step: 0.25, floor: 24 });
const gate = instrument({ voice: "#", waves: [["sawtooth", 0]], fall: 0 });
const grand = instrument({ voice: "#", waves: [["triangle", 0.015], ["sine", 0]], onset: [[0.01, 1.2]], fall: 5, step: 0.35, floor: 30 });
const mist = instrument({ voice: "#", waves: [["triangle", 0]], onset: [[0.02, 0.8]], slur: [[0.02, 0.9]], fall: 1.5, step: 0.35, floor: 30 });
const soar = instrument(
  { voice: "#", waves: [["sawtooth", 0]], onset: [[0.012, 1.2]], slur: [[0.012, 1]], fall: 1, step: 0.3, vibrato: 0.05 },
  { voice: "#o", shift: 12, share: 0.45, waves: [["sawtooth", 0]], onset: [[0.012, 1.2]], slur: [[0.012, 1]], fall: 2, step: 0.35, floor: 24 },
);
const vox = instrument({ voice: "#", waves: [["triangle", 0]], onset: [[0.03, 0.45], [0.03, 0.8]], slur: [[0.03, 0.9]], fall: 1, step: 0.3, vibrato: 0.07, floor: 30 });
const keys = instrument({ voice: "#", waves: [["triangle", 0.02], ["sine", 0]], onset: [[0.01, 1.2]], fall: 4, step: 0.35, floor: 30 });
const gated = instrument({ voice: "#", waves: [["sawtooth", 0]], onset: [[0.006, 1.3]], fall: 10, step: 0.2, floor: 20 });
const brass = instrument({ voice: "#", waves: [["triangle", 0.04], ["sawtooth", 0]], onset: [[0.02, 0.5], [0.02, 0.85], [0.03, 1.15]], fall: 2.5, step: 0.25, floor: 24 });

const climb = (bars) => drummer(
  times(bars - 4, { kick: "K...K...K...K...", hat: "..h...h...h...h." }),
  times(2, { kick: "K...K...K...K...", snare: "S...S...S...S...", hat: "..h...h...h...h." }),
  { kick: "K...K...K...K...", snare: "S.S.S.S.S.S.S.S.", hat: "..O...O...O...O." },
  { kick: "K...K...K.......", snare: "S.S.S.S.S.S.....", roll: ".G.G.G.G.G.G...." },
);
const rolled = { roll: 3 };

const pulse = (progression, bars, ...cells) => {
  const names = progression.trim().split(/\s+/);
  return Array.from({ length: bars }, (_, bar) => cells[bar % cells.length].replaceAll("x", names[bar % names.length])).join(" | ");
};

// Party Dubstep writes its bass and its chops on a grid of sixteenths, so a
// riff can split one into two thirty seconds.
const grid = (line) => {
  const notes = [];
  let at = 0;
  let open = null;
  const step = (mark, beats) => {
    if (mark === ".") {
      if (open) open.beats += beats;
    } else if (mark === "-") open = null;
    else notes.push((open = { mark, at, beats }));
    at += beats;
  };
  for (const cell of line.split(/[\s|]+/).filter(Boolean)) {
    const [mark, count = "1"] = cell.split("*");
    for (let i = 0; i < Number(count); i++) {
      if (mark.includes("/")) for (const half of mark.split("/")) step(half, 0.125);
      else step(mark, 0.25);
    }
  }
  return notes;
};

const measure = (line) => line.split(/[\s|]+/).filter(Boolean).reduce((sum, cell) => sum + 0.25 * Number(cell.split("*")[1] ?? 1), 0);
const hush = (bars) => Array(bars).fill("-*16").join(" | ");

// scriptune plays equal temperament only, so the thirds and the seventh
// among the partials land a little off the sawtooth's own and beat against
// it, which makes the bass shimmer and roughen.
const partials = { 1: 0, 2: 12, 3: 19, 4: 24, 5: 28, 6: 31, 7: 34, 8: 36, 10: 40, 12: 43, 16: 48, 20: 52 };
const over = (note, partial) => move(note, partials[partial]);

// The body of the bass sits from E2 up, so its sub an octave down goes as
// deep as a kick and no deeper.
const fold = (note) => move("E2", (((semitone(note) - semitone("E2")) % 12) + 12) % 12);

// scriptune has no filter, so a filter that opens or a voice that talks is
// a wave stepping through the partials of the bass note, the higher ones
// softer.
const glide = (wave, note, list, seconds, level, tilt = 0.5) => {
  const count = Math.max(1, Math.min(list.length, Math.floor(seconds / 0.03)));
  return Array.from({ length: count }, (_, i) => {
    const partial = list[count > 1 ? Math.round((i * (list.length - 1)) / (count - 1)) : 0];
    return [wave, over(note, partial), seconds / count, level * (4 / partial) ** tilt];
  });
};

// scriptune knows no pitch between semitones, so a slide climbs in them,
// two at a time where one would last less than 25 ms.
const slide = (wave, from, to, seconds, level, fade = 1) => {
  const way = Math.sign(semitone(to) - semitone(from));
  const distance = Math.abs(semitone(to) - semitone(from));
  const stride = seconds / (distance + 1) < 0.025 ? 2 : 1;
  const count = Math.floor(distance / stride) + 1;
  return Array.from({ length: count }, (_, i) => [wave, move(from, way * stride * i), seconds / count, level * (1 + ((fade - 1) * i) / Math.max(1, count - 1))]);
};

// duck pulls the bass under every hit as a compressor keyed by the drums
// does, so the drums come through and the bass pumps back in after them.
const duck = (run, start, { kicks, snares }, depth) => {
  const total = run.reduce((sum, [, , length]) => sum + length, 0);
  const smooth = depth === 1;
  const near = depth ? [...kicks, ...(smooth ? [] : snares)].filter((hit) => hit > start - (smooth ? 0.2 : 0.07) && hit < start + total) : [];
  if (!near.length) return run;
  const cuts = near.flatMap((hit) => (smooth ? [0, 0.04, 0.085, 0.14] : [0, 0.07]).map((after) => hit - start + after));
  const gain = smooth
    ? (time) => 1 - Math.max(0, ...near.map((hit) => (start + time < hit ? 0 : Math.exp(-Math.max(0, start + time - hit - 0.025) / 0.045))))
    : (time) => (near.some((hit) => start + time > hit && start + time < hit + 0.07) ? 1 - depth : 1);
  const out = [];
  let t = 0;
  for (const [wave, note, length, level] of run) {
    const marks = [t, ...cuts.filter((x) => x > t + 1e-3 && x < t + length - 1e-3).sort((a, b) => a - b), t + length];
    for (let i = 1; i < marks.length; i++) out.push([wave, note, marks[i] - marks[i - 1], level * gain((marks[i - 1] + marks[i]) / 2)]);
    t += length;
  }
  return out;
};

// A phone cannot play the sub, so the bass of a drop lives in the mids, a
// sawtooth body whose overtones a phone plays. mid2 plays the body again in
// step, since that doubles the level a voice may have.
const snarl = (beat, { sub = 1, body = 1, octave = 0.8, form = 0.75, high = 0.35 } = {}) => {
  const bottom = (note, seconds, level, share = 1) => ["sub", [["sine", move(note, -12), seconds, sub * share * level]], 1];
  const double = (run) => ["oct", run.map(([wave, note, length, level]) => [wave, move(note, 12), length, (level * octave) / body]), 0.75];
  const twin = (run) => ["mid2", run, 0.75];
  const flutter = (seconds) => Math.max(1, Math.round(seconds / (beat / 8)));
  return {
    talk: (first, second = null, { scoop = true, tilt = 0.5 } = {}) => (note, seconds, level) => {
      const mid = scoop && seconds > 0.2
        ? [["sawtooth", move(note, -1), 0.03, body * level], ["sawtooth", note, seconds - 0.03, body * level]]
        : [["sawtooth", note, seconds, body * level]];
      return [
        bottom(note, seconds, level), ["mid", mid, 0.75], twin(mid), double(mid),
        ["f1", glide("triangle", note, first, seconds, form * level, tilt), 0.75],
        ...(second ? [["f2", glide("sawtooth", note, second, seconds, high * level, tilt), 0.75]] : []),
      ];
    },
    wobble: (rate, { peak = 6 } = {}) => (note, seconds, level) => {
      const count = Math.max(1, Math.round(seconds / (rate * beat)));
      const length = seconds / count;
      const cycle = length < 0.25
        ? [["triangle", 0.4, 3, 0.2], ["sawtooth", 1, peak, 0.65]]
        : [["triangle", 0.4, 3, 0.2], ["sawtooth", 1, peak, 0.65], ["sawtooth", 0.65, 4, 0.35]];
      const mid = [];
      const f1 = [];
      for (let i = 0; i < count; i++) {
        for (const [wave, open, partial, peaked] of cycle) {
          mid.push([wave, note, length / cycle.length, body * open * level]);
          f1.push(["triangle", over(note, partial), length / cycle.length, form * peaked * level]);
        }
      }
      return [bottom(note, seconds, level), ["mid", mid, 0.75], twin(mid), double(mid), ["f1", f1, 0.75]];
    },
    growl: ({ rough = 0.3 } = {}) => (note, seconds, level) => {
      const count = flutter(seconds);
      const length = seconds / count;
      const mid = Array.from({ length: count }, (_, i) => ["sawtooth", note, length, body * (i % 2 ? 0.45 : 1) * level]);
      const f1 = Array.from({ length: count }, (_, i) => ["triangle", over(note, i % 2 ? 6 : 4), length, form * (i % 2 ? 0.75 : 0.9) * level]);
      return [bottom(note, seconds, level), ["mid", mid, 0.75], twin(mid), double(mid), ["f1", f1, 0.75], ["grit", [["sawtooth", over(note, 7), seconds, rough * level]], 0.75]];
    },
    screech: ({ lift = 36, bend = -5 } = {}) => (note, seconds, level, n) => {
      const scream = move(note, lift);
      const count = flutter(seconds * 0.67);
      const length = (seconds * 0.67) / count;
      const trill = Array.from({ length: count }, (_, i) => ["sawtooth", move(scream, i % 2), length, 0.34 * level]);
      const pan = n % 2 ? 0.25 : -0.25;
      return [
        bottom(note, seconds, level, 0.6),
        ["mid", [["sawtooth", note, seconds, body * 0.5 * level]], 0.75],
        ["top", [...trill, ...slide("sawtooth", scream, move(scream, bend), seconds * 0.33, 0.34 * level, 0.6)], 0, pan],
        ["top2", [["triangle", over(note, 12), seconds, 0.16 * level]], 0, -pan],
      ];
    },
    chug: ({ fifth = 7 } = {}) => (note, seconds, level) => {
      const strike = Math.min(0.03, seconds / 2);
      const hit = (pitch, share) => (seconds < 0.08 ? [["sawtooth", pitch, seconds, share * level]]
        : [["sawtooth", pitch, strike, share * level], ["sawtooth", pitch, seconds - strike, share * 0.78 * level]]);
      return [bottom(note, seconds, level), ["mid", hit(note, body * 1.1), 0.75], ["mid2", hit(note, body * 1.1), 0.75], ["grit", hit(move(note, fifth), 0.6), 0.75], ["oct", hit(move(note, 12), 0.65), 0.75]];
    },
    dive: () => (note, seconds, level) => [
      ["mid", slide("sawtooth", move(note, 12), note, seconds, body * level, 0.85)],
      ["f1", glide("triangle", note, [8, 6, 5, 4, 3, 2], seconds, form * level)],
    ],
    rise: () => (note, seconds, level) => [
      bottom(note, seconds, level, 0.5),
      ["mid", slide("sawtooth", note, move(note, 12), seconds, body * 0.7 * level, 1.3)],
      ["f1", glide("triangle", note, [2, 3, 4, 5, 6, 8], seconds, form * level)],
    ],
    bwaa: () => (note, seconds, level) => {
      const mid = [["triangle", note, seconds / 3, body * 0.8 * level], ["sawtooth", note, (seconds * 2) / 3, body * level]];
      return [bottom(note, seconds, level), ["mid", mid, 0.75], twin(mid), double(mid), ["f1", glide("triangle", note, [2, 3, 4, 5, 6, 8, 10], seconds, form * level, -0.3), 0.75]];
    },
    zap: () => (note, seconds, level, n) => {
      const time = Math.min(seconds, 0.13);
      return [["zap", Array.from({ length: 9 }, (_, i) => ["sawtooth", move(note, 48 - 3 * i), time / 9, 0.3 * level * (1 - i / 14)]), 0, n % 2 ? 0.4 : -0.4]];
    },
  };
};

// A track plays one note at a time, so the clap of the snare sounds on a
// voice of its own.
const slams = (tail) => ({
  K: [["kick", [...hiss("D6", [[0.005, 0.75]]), struck("triangle", move(tail, 31), 2, 1), struck("triangle", move(tail, 24), 2, 1), struck("triangle", move(tail, 12), 3, 0.95),
    struck("sine", move(tail, 5), 3, 1), struck("sine", tail, 5, 0.95), struck("sine", tail, 3, 0.45)]]],
  S: [["shell", [struck("triangle", "D4", 1, 0.6), struck("sine", "A3", 8, 0.6), struck("sine", "F3", 6, 0.3)]],
    ["snare", [...hiss("F6", [[0.01, 0.7]]), ...hiss("C7", [[0.04, 0.5], [0.06, 0.25], [0.1, 0.1], [0.15, 0.035]])]],
    ["clap", hiss("A6", [[0.007, 0.45], [0.009, 0], [0.007, 0.45], [0.009, 0], [0.03, 0.4], [0.08, 0.12]])]],
  L: [["clap", hiss("A6", [[0.007, 0.3], [0.009, 0], [0.007, 0.3], [0.03, 0.25], [0.08, 0.08]])]],
  N: [["snare", [...hiss("F6", [[0.006, 0.42]]), ...hiss("C7", [[0.03, 0.24], [0.08, 0.06]])]]],
  G: [["snare", hiss("C7", [[0.016, 0.1]])]],
  H: [["hat", hiss("E8", [[0.008, 0.14]])]],
  O: [["hat", hiss("A7", [[0.03, 0.11], [0.08, 0.05], [0.1, 0.015]])]],
  T: [["shaker", hiss("D8", [[0.022, 0.05]])]],
  Y: [["ride", [struck("sine", "D#7", 24, 0.05), ...hiss("D8", [[0.05, 0.07], [0.12, 0.035]])]]],
  C: [["crash", [...hiss("A7", [[0.02, 0.22]]), ...hiss("C8", [[0.06, 0.17], [0.12, 0.11]]), ...hiss("B7", [[0.2, 0.065], [0.3, 0.03]]),
    ...hiss("A7", [[0.45, 0.012]])]]],
});

function rig(bpm, { tail = "F1", ...settings } = {}) {
  const beat = 60 / bpm;
  const tools = arena(bpm);
  const hits = slams(tail);
  const sounds = snarl(beat, settings);

  // Every kick and every full snare goes into the mix, so the bass can duck
  // under them.
  const drums = (grids, { level = 1, contour = [1, 1], late = { snare: 0.004, hat: 0.003 }, gains = {}, soft = 0.5, seed = 3 } = {}) => (put, bars, total, mix) => {
    for (const [lane, lanes] of Object.entries(grids)) {
      const rows = lanes.replace(/\s/g, "").split("|");
      let n = 0;
      for (let bar = 0; bar < bars; bar++) {
        const row = rows[bar % rows.length];
        [...row].forEach((step, i) => {
          if (step === ".") return;
          const at = (bar * 4 + (i * 4) / row.length) * beat + (late[lane] ?? 0) + (lane === "kick" ? 0 : human(seed, n, 0.002));
          const loud = level * (gains[lane] ?? 1) * (step === step.toUpperCase() ? 1 : soft) * shape(contour, at, total) * (1 - 0.06 * shade(seed + 1, n++));
          if (step.toUpperCase() === "K") mix.kicks.push(at);
          if (step === "S") (mix.snares ??= []).push(at);
          for (const [name, run] of hits[step.toUpperCase()]) put(name, at, (room) => blare(run.map(([wave, note, length, volume]) => [wave, note, length, volume * loud]), room));
        });
      }
    }
  };

  const riff = (line, root, palette, { level = 1, contour = [1, 1], late = 0.002, seed = 5 } = {}) => (put, bars, total, mix) => {
    const notes = grid(line);
    const length = measure(line);
    let n = 0;
    for (let from = 0; from < bars * 4 - 1e-9; from += length) {
      for (const { mark, at: into, beats } of notes) {
        const at = from + into;
        if (at >= bars * 4 - 1e-9) break;
        const [, name, shift = "0", marks] = mark.match(/^([A-Z])([+-]\d+)?([!']*)$/);
        const time = at * beat + late + human(seed, n, 0.002);
        const seconds = beats * beat;
        const loud = level * shape(contour, time, total) * (marks.includes("!") ? 1.12 : 1) * (marks.includes("'") ? 0.6 : 1);
        for (const [voice, run, depth = 0, pan] of palette[name](fold(move(root, Number(shift))), seconds, loud, n)) {
          put(voice, time, (room) => blare((voice === "sub" ? settle : (steps) => steps)(duck(run, time, { kicks: mix.kicks, snares: mix.snares ?? [] }, depth)), room, pan), seconds);
        }
        n++;
      }
    }
  };

  const call = (play, line, { voice = "chop", level = 1, contour = [1, 1], late = 0.006, delay = 0, shift = 0, pump = 0, seed = 2 } = {}) => (put, bars, total, mix) => {
    const notes = grid(line);
    const length = measure(line);
    const all = [];
    for (let from = 0; from < bars * 4 - 1e-9; from += length) all.push(...notes.map((note) => ({ ...note, at: note.at + from })).filter(({ at }) => at < bars * 4 - 1e-9));
    all.forEach(({ mark, at, beats }, n) => {
      const [, scoop, note, marks] = mark.match(/^(\^?)([A-G][#b]?\d)([~!']*)$/);
      const time = (at + delay) * beat + late + human(seed, n, 0.003);
      const pitch = move(note, shift);
      const segments = scoop ? [[move(pitch, -2), 0.03], [move(pitch, -1), 0.025], [pitch, 0]] : [[pitch, 0]];
      const loud = level * shape(contour, time, total) * (marks.includes("!") ? 1.15 : 1) * (marks.includes("'") ? 0.55 : 1) * (1 + human(seed + 1, n, 0.04));
      const length = beats * beat;
      const duck = pump ? { kicks: mix.kicks, start: time, depth: pump } : null;
      for (const [name, sound] of play(segments, loud, length, { voice, shake: marks.includes("~"), duck })) put(name, time, sound, length + 0.08);
    });
  };

  const sweet = (chords, loop) => {
    const bass = (level = 0.6, contour = [1, 1], upto = Infinity) => (put, bars, total, mix) => {
      const names = loop.trim().split(/\s+/);
      for (let bar = 0; bar < Math.min(bars, upto); bar++) {
        const [root] = chords[names[bar % names.length]];
        const time = bar * 4 * beat + 0.002;
        const loud = level * shape(contour, time, total);
        put("sub", time, (room) => blare(settle(duck([["sine", move(root, -12), 4 * beat, 1.2 * loud]], time, { kicks: mix.kicks, snares: [] }, 1)), room), 4 * beat);
        put("body", time, (room) => blare([["triangle", root, 2 * beat, 0.8 * loud], ["triangle", root, 2 * beat, 0.6 * loud]], room), 4 * beat);
      }
    };
    return {
      bass,
      sing: (line, level = 0.62, contour) => call(chop, line, { level, contour, pump: 0.3 }),
      echo: (line, level = 0.15, contour) => call(chop, line, { voice: "echo", level, contour, delay: 0.75, pump: 0.3, seed: 4 }),
      pump: (bars, cells, level = 0.5, contour, hold = 1.5) => tools.stab(glow, pulse(loop, bars, ...cells), chords, { level, contour, pump: 0.8, hold }),
      piano: (bars, cells, level = 0.3, contour) => tools.stab(grand, pulse(loop, bars, ...cells), chords, { level, contour, prefix: "k" }),
      lift: (bars, hook, note, siren, { level = 0.32, low = 0.6, contour = [0.3, 1] } = {}) => [
        tools.stab(gate, `${pulse(loop, bars - 4, "x - x - x - x -")} | ${pulse(loop, 3, "x/- x/- x/- x/- x/- x/- x/- x/-")} | -*8`, chords, { level, contour }),
        call(chop, `${hook.split(" | ").slice(0, 2).join(" | ")} | ${hush(bars - 2)}`, { level: 0.55 }),
        call(stammer, `${hush(bars - 2)} | ${stutter(note, 7, 2)}`, { level: 0.4, contour: [0.5, 1] }),
        ...(low ? [bass(low, [0.5, 0.9], bars - 2)] : []),
        drums(windup(bars), { contour: [0.6, 1] }), tools.rise(bars * 2, bars * 2 - 1), tools.sweep(bars * 2, bars * 2 - 1, ...siren),
      ],
    };
  };

  return { ...tools, sounds, drums, riff, call, sweet };
}

const booth = {
  kick: "#PAN 0", sub: "#PAN 0", body: "#PAN 0", mid: "#PAN 0", mid2: "#PAN 0", oct: "#PAN 0", f1: "#PAN -0.08", f2: "#PAN 0.1", grit: "#PAN 0.06",
  top: "#PAN -0.25", top2: "#PAN 0.25", zap: "#PAN 0.4",
  shell: "#PAN 0.04", snare: "#PAN 0.04", clap: "#PAN -0.06", hat: "#PAN 0.3", shaker: "#PAN -0.35", ride: "#PAN 0.35", crash: "#PAN -0.3",
  chop: "#PAN 0.08", chopv: "#PAN 0.08", echo: "#PAN -0.45", echov: "#PAN -0.45",
  s1: "#PAN -0.45", s2: "#PAN 0.05", s3: "#PAN 0.45",
  p1: "#PAN -0.6", p2: "#PAN 0.6", p3: "#PAN 0", k1: "#PAN -0.3", k2: "#PAN 0", k3: "#PAN 0.3",
  fx: "#PAN 0", wash: "#PAN 0", siren: "#PAN 0",
};

const glow = instrument({ voice: "#", waves: [["sawtooth", 0]], onset: [[0.012, 0.75], [0.012, 1.1]], fall: 2.5, step: 0.3, floor: 30 });
const chop = instrument(
  { voice: "#", waves: [["sawtooth", 0.03], ["triangle", 0]], onset: [[0.03, 1.15]], fall: 6, step: 0.25, floor: 30, vibrato: 0.06 },
  { voice: "#v", shift: 12, share: 0.35, waves: [["sine", 0]], fall: 16, step: 0.2, floor: 18 },
);
const stammer = instrument({ voice: "#", waves: [["sawtooth", 0.02], ["triangle", 0]], fall: 0 });

const windup = (bars) => drummer(
  times(bars - 5, { kick: "K...K...K...K...", hat: "..h...h...h...h." }),
  times(2, { kick: "K...K...K...K...", snare: "N...N...N...N...", hat: "..h...h...h...h." }),
  { kick: "K...K...K...K...", snare: "N.N.N.N.N.N.N.N." },
  { kick: "K...K...K...K...", snare: "NNNNNNNNNNNNNNNN" },
  { snare: "NNNNNNNNNNNNNNNNNNNNNNNN........" },
);

const stutter = (note, beats, bars) => {
  const cells = Array.from({ length: bars * 16 }, (_, i) => (i < beats * 4 ? `${move(note, Math.floor(i / 4))}/${move(note, Math.floor(i / 4))}` : "-"));
  return Array.from({ length: bars }, (_, bar) => cells.slice(bar * 16, bar * 16 + 16).join(" ")).join(" | ");
};

// unroll reads a track the way parseSheet of scriptune does, loops unrolled,
// and gives its notes, each with the lowest pitch of its cell or null for a
// rest, where it starts and how long it lasts, in sixteenths of the tempo the
// track starts with.
function unroll(text) {
  const sixteenths = Object.fromEntries(values.map(([n, value]) => [value, n]));
  let bpm, tempo;
  const loops = [{ count: 1, cells: [] }];
  for (const row of text.split("\n").map((l) => l.replace(/;.*/, "").trim()).filter(Boolean)) {
    const [command, ...args] = row.split(/\s+/);
    if (command === "#BPM") {
      bpm = parseInt(args[0]);
      tempo ??= bpm;
    } else if (command === "#LOOP") loops.push({ count: parseInt(args[0]), cells: [] });
    else if (command === "#ENDLOOP") {
      const { count, cells } = loops.pop();
      loops.at(-1).cells.push(...Array(count).fill(cells).flat());
    } else if (!command.startsWith("#")) {
      for (const cell of [command, ...args]) {
        const [pitch, value] = cell.split(":");
        const length = (sixteenths[value] * tempo) / bpm;
        if (!Number.isInteger(length)) throw new Error(`${cell} is off the grid of sixteenths`);
        loops.at(-1).cells.push({ pitch: pitch === "-" ? null : pitch.split("+").sort((a, b) => hertz(a) - hertz(b))[0], length });
      }
    }
  }
  let start = 0;
  return loops[0].cells.map(({ pitch, length }) => ({ pitch, start: (start += length) - length, length }));
}

// The hits of the kit as a panned song writes them, in lines with the
// volumes left open, since normalize scaled them.
const blank = (row) => row.replace(/^#VOLUME .*/, "#VOLUME");
const kitRows = Object.keys(kit).map((letter) => [letter, beat(letter, hitPans).split("\n").map(blank)]);

// strip reads a drum track back into the letters of its hits, loops unrolled,
// one letter a sixteenth, and writes it again with the hits of letters left
// out, a rest in their place.
function strip(text, letters) {
  const rows = text.split("\n");
  const kept = rows.slice(0, 3);
  const loops = [{ count: 1, letters: [] }];
  for (let i = 3; i < rows.length; ) {
    const row = rows[i];
    if (row.startsWith("#LOOP ")) {
      loops.push({ count: parseInt(row.slice(6)), letters: [] });
      kept.push(row);
      i++;
    } else if (row === "#ENDLOOP") {
      const { count, letters } = loops.pop();
      loops.at(-1).letters.push(...Array(count).fill(letters).flat());
      kept.push(row);
      i++;
    } else {
      const found = kitRows.filter(([, lines]) => lines.every((l, k) => blank(rows[i + k] ?? "") === l));
      if (found.length !== 1) throw new Error(`no single hit of the kit fits "${row}"`);
      const [letter, lines] = found[0];
      kept.push(...(letters.includes(letter) ? ["-:h"] : rows.slice(i, i + lines.length)));
      loops.at(-1).letters.push(letter);
      i += lines.length;
    }
  }
  return { text: kept.join("\n"), grid: loops[0].letters.join("") };
}

// boost gives a song the low end of Deep Dubstep and Dark Techno. Every voice
// but the bass and the drums stays note for note and at its volume, so the
// melody keeps its level, and nothing boost writes goes through normalize,
// every new volume stays at or below 1 instead. The bass becomes three
// voices, the way Deep Dubstep layers its sub, growl and near: the sub, a sine
// in the lowest octave you feel more than hear, the bass line itself on whole
// cycles, and the grit, a short saw stroke on its notes that carries the bass
// on a phone speaker. Kick and low drum leave the drums for a kick of their
// own, built like the kick of Dark Techno. A song gives the settings, see the
// presets below.
function boost(sheet, bpm, { tail, kick, sub, bass, grit }) {
  const all = sheet.split(/\n(?=#TRACK )/).map((text) => ({ name: text.slice("#TRACK ".length, text.indexOf("\n")), text }));
  const find = (name) => all.find((track) => track.name === name).text;
  const sixteenth = 15 / bpm;
  const notes = unroll(find("bass"));
  const drums = strip(find("drums"), "KBT");
  const length = drums.grid.length;
  if (notes.at(-1).start + notes.at(-1).length !== length) throw new Error("bass and drums differ in length");
  const kicks = [...drums.grid].map((letter) => "KB".includes(letter));
  const octave = (note, n) => note.replace(/\d$/, n);

  // run collects the cells of a voice and joins the rests in between, so a
  // run of them starts as few oscillators as it can.
  const run = () => {
    const cells = [];
    let gap = 0;
    return {
      rest: (n) => (gap += n),
      add: (cell) => {
        if (gap) cells.push(rests(gap));
        gap = 0;
        cells.push(cell);
      },
      done: () => [...cells, gap ? rests(gap) : ""].filter(Boolean).join("\n"),
    };
  };

  // The sub takes the pitch the bass sounds at the start of every beat, or
  // its first one in the beat, into the lowest octave. It sounds wherever the
  // bass sounds or rests for at most bridge sixteenths between two notes, a
  // pitch held over beats as one note. Every kick ducks it, then it swells
  // back through duck, a level a sixteenth, as if the kick pushed it away.
  const sounding = Array(length).fill(null);
  for (const { pitch, start, length: n } of notes) if (pitch) sounding.fill(pitch, start, start + n);
  const gate = sounding.map(Boolean);
  for (let i = 0; i < length; ) {
    let j = i;
    while (j < length && !sounding[j]) j++;
    if (i > 0 && j > i && j < length && j - i <= sub.bridge) gate.fill(true, i, j);
    i = Math.max(j, i + 1);
  }
  const roots = Array.from({ length: Math.ceil(length / 4) }, (_, b) => sounding[4 * b] ?? sounding.slice(4 * b, 4 * b + 4).find(Boolean));
  const last = [];
  kicks.forEach((on, i) => (last[i] = on ? i : (last[i - 1] ?? -Infinity)));
  const pump = (at) => {
    const since = at - last[Math.min(length - 1, Math.floor(at))];
    if (!(since < sub.duck.length - 1)) return sub.duck.at(-1);
    const i = Math.floor(since);
    return sub.duck[i] + (sub.duck[i + 1] - sub.duck[i]) * (since - i);
  };
  const deep = run();
  for (let i = 0, held = null; i < length; ) {
    if (!gate[i]) {
      deep.rest(1);
      i++;
      continue;
    }
    const pitch = (held = roots[Math.floor(i / 4)] ?? held);
    let j = i + 1;
    while (j < length && gate[j] && octave(roots[Math.floor(j / 4)] ?? pitch, 1) === octave(pitch, 1)) j++;
    const from = i;
    const step = kicks.slice(from, j).some(Boolean) ? sub.step : sub.calm;
    deep.add(tone(octave(pitch, 1), (j - from) * sixteenth, bpm, (t) => sub.level * pump(from + t / sixteenth), step));
    i = j;
  }

  // The bass plays its line on whole cycles, every note from its full level
  // down towards sustain. accent sets a level by sixteenth of the beat, ring
  // cuts a note short and rests for the rest of it. A note above 150 Hz, the
  // top of a line that leaps an octave, gets softer the higher it is, a loud
  // high triangle clicks from the rounding to whole samples alone.
  const body = run();
  for (const { pitch, start, length: n } of notes) {
    if (!pitch) {
      body.rest(n);
      continue;
    }
    const ring = Math.min(n, bass.ring ?? n);
    const level = bass.level * (bass.accent?.[start % 4] ?? 1) * Math.min(1, 150 / hertz(pitch));
    body.add(tone(pitch, ring * sixteenth, bpm, (t) => level * (bass.sustain + (1 - bass.sustain) * Math.exp(-t / bass.decay)), bass.step));
    body.rest(n - ring);
  }

  // The grit strikes the notes of the bass that start where its mask has an
  // x, a saw in the octave above the sub that fades out within ring
  // sixteenths.
  const saw = run();
  for (const { pitch, start, length: n } of notes) {
    if (!pitch || grit.mask[start % grit.mask.length] !== "x") {
      saw.rest(n);
      continue;
    }
    const ring = Math.min(n, grit.ring);
    saw.add(tone(octave(pitch, 2), ring * sixteenth, bpm, (t) => grit.level * Math.exp(-t / grit.decay), grit.step));
    saw.rest(n - ring);
  }

  // The kick plays where the drums had a kick or a low drum, the tom where
  // they had a tom.
  const shape = {
    K: land(drops[kick.drop], tail, kick.level),
    T: drops.tom.map(([note, cycles, level]) => [note, cycles, level * kick.level * 0.8]),
  };
  shape.B = shape.K;
  const hits = thump(bpm, drums.grid, shape, kick.punch * kick.level).filter(Boolean).join("\n");

  // parseSheet carries the type and the volume a track ends on into the next
  // one until that sets its own, so the new voices end on what the bass left
  // and every voice after them plays as it did.
  const left = ["#TYPE", "#VOLUME"].map((command) => find("bass").split("\n").filter((row) => row.startsWith(command)).at(-1)).filter(Boolean);
  const voice = (name, type, notes) => `#TRACK ${name}\n#BPM ${bpm}\n#PAN 0\n#TYPE ${type}\n${notes}`;
  return all.map(({ name, text }) => {
    if (name === "bass") return [voice("sub", "sine", deep.done()), voice("bass", bass.type, body.done()), voice("grit", "sawtooth", saw.done()), ...left].join("\n");
    if (name === "drums") return `${drums.text}\n${voice("kick", "triangle", hits)}`;
    return text;
  }).join("\n");
}

// Chill Electronic stays laid back. Its soft kick lands where the low drum
// was, the sub follows the bass beat by beat and dips a little under the
// kick, the bass sustains on a triangle and the grit touches every note.
const chill = {
  kick: { drop: "soft", level: 0.85, punch: 0.1 },
  sub: { level: 0.85, bridge: 1, duck: [0.55, 0.8, 0.95, 1], step: 0.1, calm: 0.12 },
  bass: { type: "triangle", level: 1, sustain: 0.7, decay: 0.3, step: 0.1 },
  grit: { level: 0.1, decay: 0.07, ring: 1, mask: "x", step: 0.05 },
};

// Melodic House pumps. A hard kick on every beat, the sub bridges the rests
// between offbeat and rolling notes and ducks deep under every kick, the
// bass plucks short and loudest on the offbeat, the grit strikes the and of
// every beat.
const house = {
  kick: { drop: "hard", level: 1, punch: 0.25 },
  sub: { level: 0.9, bridge: 3, duck: [0.15, 0.45, 0.75, 0.92, 1], step: 0.07, calm: 0.12 },
  bass: { type: "triangle", level: 1, sustain: 0.35, decay: 0.12, ring: 4, accent: [0.7, 0.85, 1, 0.9], step: 0.09 },
  grit: { level: 0.12, decay: 0.05, ring: 1, mask: "..x.", step: 0.04 },
};

// tuned gives a preset with some of its settings changed. A song tunes its
// preset and adds the tail its kick drops to, the root or the fifth of its
// key.
const tuned = (preset, changes) => Object.fromEntries(Object.entries(preset).map(([key, value]) => [key, typeof value === "object" ? { ...value, ...changes[key] } : value]));

const scores = {
  ambermile: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: { ...chill, tail: "F#1" },
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
  },
  goldenhour: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "A1" }, { bass: { sustain: 0.5, decay: 0.2 }, grit: { level: 0.13 } }),
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
  },
  saltflats: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "F#1" }, { sub: { level: 0.9 }, grit: { level: 0.07 } }),
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
  },
  lowsun: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "E1" }, { sub: { bridge: 2 }, bass: { sustain: 0.45, decay: 0.15 }, grit: { level: 0.13 } }),
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
  },
  paperlanterns: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "A1" }, { grit: { level: 0.08 } }),
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
  },
  coastaldrive: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "G1" }, { kick: { level: 0.9, punch: 0.15 }, bass: { sustain: 0.55, decay: 0.2 }, grit: { level: 0.12 } }),
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
  },
  willowcreek: () => {
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
      pans: { keys: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "G#1" }, { sub: { level: 0.9 }, grit: { level: 0.07 } }),
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
  },
  driftwood: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "F1" }, { kick: { level: 0.9, punch: 0.15 }, bass: { sustain: 0.45, decay: 0.15 }, grit: { level: 0.15 } }),
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
  },
  quietcanyon: () => {
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
      pans: { pad: 0.25 },
      lowEnd: tuned({ ...chill, tail: "A1" }, { kick: { level: 0.75 }, sub: { level: 0.9 }, grit: { level: 0.06 } }),
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
  },
  rearview: () => {
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
      pans: { pluck: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...chill, tail: "Ab1" }, { kick: { level: 0.9, punch: 0.15 }, sub: { bridge: 2 }, bass: { sustain: 0.5, decay: 0.15 }, grit: { level: 0.12, mask: "x..." } }),
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
  },
  midnightbloom: () => {
    const bpm = 122;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars), hitPans);
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16), hitPans);
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
      pans: { pad: 0.3, ...ending.pans },
      lowEnd: { ...house, tail: "A1" },
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
  },
  afterglow: () => {
    const bpm = 120;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars), hitPans);
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16), hitPans);
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
      pans: { lead: 0.25, pad: -0.3, ...ending.pans },
      lowEnd: { ...house, tail: "F1" },
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
  },
  oceanfloor: () => {
    const bpm = 123;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars), hitPans);
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16), hitPans);
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
      pans: { pad: 0.3, ...ending.pans },
      lowEnd: tuned({ ...house, tail: "D1" }, { sub: { level: 0.85 } }),
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
  },
  citypulse: () => {
    const bpm = 124;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars), hitPans);
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16), hitPans);
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
      pans: { pad: 0.3, ...ending.pans },
      lowEnd: { ...house, tail: "G1" },
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
  },
  velvethours: () => {
    const bpm = 121;
    const claps = (bars) => `\n#BPM ${bpm * 8}\n` + beat("....P.......P...".repeat(bars), hitPans);
    const roll = `\n#BPM ${bpm * 8}\n` + beat("P.".repeat(8) + "P".repeat(16), hitPans);
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
      pans: { keys: -0.3, pad: 0.3, ...ending.pans },
      lowEnd: tuned({ ...house, tail: "F1" }, { bass: { sustain: 0.45, decay: 0.18 } }),
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
  },
  distantshores: () => {
    const bpm = 122;
    const chords = ["C#4+E4+G#4", "C#4+E4+A4", "B3+E4+G#4", "B3+D#4+F#4"];
    const roots = [["C#2", "C#3"], ["A1", "A2"], ["E2", "E3"], ["B1", "B2"]];
    const tones = [["C#5", "E5", "G#5", "C#6"], ["C#5", "E5", "A5", "C#6"], ["E5", "G#5", "B5", "E6"], ["F#5", "B5", "D#6", "F#6"]];
    const arp = (t) => tones.map(([a, b, c, d]) => `${a}:e ${b}:e ${c}:e ${d}:e ${c}:e ${b}:e ${d}:e ${c}:e `).join("").repeat(t);
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map(([l, h]) => `${l}:e ${h}:e `.repeat(4)).join("").repeat(t);
    const stab = (t) => chords.map((c) => `-:e ${c}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p, hitPans)}`;
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
      pans: { arp: -0.3, keys: 0.35, pad: 0.2 },
      lowEnd: { ...house, tail: "G#1" },
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
  },
  tidalechoes: () => {
    const bpm = 120;
    const motif = "G5:e A#5:e D6:q C6:e A#5:e -:q G5:e A#5:e D#6:q D6:e A#5:e -:q F5:e A#5:e D6:q C6:e A#5:e -:q F5:e A5:e C6:q A5:h ";
    const echo = (t) => `-:dq ${motif.repeat(t).replace(/A5:h $/, "A5:e ")}`;
    const chords = ["G3+A#3+D4", "G3+A#3+D#4", "F3+A#3+D4", "F3+A3+C4"];
    const roots = ["G2", "Eb2", "Bb1", "F2"];
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const pulse = (t) => roots.map((r) => `${r}:s -:s `.repeat(8)).join("").repeat(t);
    const stab = (t) => chords.map((c) => `-:e ${c}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p, hitPans)}`;
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
      pans: { echo: 0.35, keys: -0.3, pad: -0.2 },
      lowEnd: { ...house, tail: "G1" },
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
  },
  weightless: () => {
    const bpm = 121;
    const hook = "G5:h F5:q Eb5:q G5:dh Bb5:q C6:h Bb5:q G5:q F5:w G5:h Bb5:q C6:q D6:dh C6:q Bb5:h G5:q F5:q Eb5:w ";
    const chords = ["Eb3+Bb3+G4+D5", "C3+G3+Eb4+Bb4", "Ab2+Eb3+C4+G4", "Bb2+F3+D4+F4"];
    const roots = [["Eb2", "Bb2"], ["C2", "G2"], ["Ab1", "Eb2"], ["Bb1", "F2"]];
    const tones = [["Eb4", "G4", "Bb4", "D5"], ["C4", "Eb4", "G4", "Bb4"], ["Ab3", "C4", "Eb4", "G4"], ["Bb3", "D4", "F4", "C5"]];
    const arp = (t) => tones.map(([a, b, c, d]) => `${a}:e ${b}:e ${c}:e ${d}:e `.repeat(2)).join("").repeat(t);
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map(([r, f]) => `${r}:e ${f}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p, hitPans)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        piano: "#TYPE sine\n#VOLUME 0.34", arp: "#TYPE triangle\n#VOLUME 0.18", pad: "#TYPE sawtooth\n#VOLUME 0.05",
        bass: "#TYPE sine\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      pans: { arp: -0.3, pad: 0.3 },
      lowEnd: tuned({ ...house, tail: "Bb1" }, { kick: { level: 0.9 }, sub: { duck: [0.3, 0.6, 0.85, 1] } }),
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
  },
  parallellines: () => {
    const bpm = 124;
    const upper = "F#5:e D5:e B4:e D5:e F#5:e B5:e A5:e F#5:e G5:e D5:e B4:e D5:e G5:e B5:e A5:e G5:e F#5:e D5:e A4:e D5:e F#5:e A5:e B5:e A5:e E5:e C#5:e A4:e C#5:e E5:e A5:e G5:e E5:e ";
    const lower = "B3:q C#4:q D4:q F#4:q G4:q F#4:q E4:q D4:q F#4:q E4:q D4:q A3:q C#4:q E4:q A4:q G4:q ";
    const chords = ["B3+D4+F#4", "B3+D4+G4", "A3+D4+F#4", "A3+C#4+E4"];
    const rolls = [["B1", "B2", "F#2"], ["G1", "G2", "D2"], ["D2", "D3", "A2"], ["A1", "A2", "E2"]];
    const pad = (t) => chords.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => rolls.map(([r, o, f]) => `${r}:s ${o}:s ${f}:s ${o}:s `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p, hitPans)}`;
    const claps = "....P.......P...";
    const drive = "K.OHK.OHK.OHK.OH";
    const fill = "K.OHK.OHK.OHKRRR";
    return song({
      bpm,
      voices: {
        upper: "#TYPE sine\n#VOLUME 0.3", lower: "#TYPE triangle\n#VOLUME 0.3", pad: "#TYPE sawtooth\n#VOLUME 0.06",
        bass: "#TYPE triangle\n#VOLUME 0.45", clap: "#TYPE sawtooth",
      },
      pans: { lower: -0.3, pad: 0.3 },
      lowEnd: { ...house, tail: "F#1" },
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
  },
  daybreak: () => {
    const bpm = 123;
    const hook = "C6:dq B5:e G5:q E5:q D6:dq C6:e B5:q G5:q C6:dq B5:e A5:q E5:q F5:q A5:q C6:q E6:q ";
    const chords = ["E4+G4+C5", "D4+G4+B4", "E4+A4+C5", "F4+A4+C5"];
    const pads = ["C3+G3+E4", "B2+G3+D4", "A2+E3+C4", "F2+C3+A3"];
    const roots = ["C2", "G2", "A2", "F2"];
    const keys = (t) => chords.map((c) => `${c}:e -:e ${c}:e -:s ${c}:s -:e ${c}:e -:q `).join("").repeat(t);
    const pulse = (t) => chords.map((c) => `${c}:s -:s `.repeat(8)).join("").repeat(t);
    const pad = (t) => pads.map((c) => `${c}:w `).join("").repeat(t);
    const bass = (t) => roots.map((r) => `-:e ${r}:e `.repeat(4)).join("").repeat(t);
    const clap = (p) => `#BPM ${bpm * 8}\n${beat(p, hitPans)}`;
    const claps = "....P.......P...";
    const four = "K.O.K.O.K.O.K.O.";
    const fill = "K.O.K.O.K.O.KRRR";
    return song({
      bpm,
      voices: {
        piano: "#TYPE sine\n#VOLUME 0.34", keys: "#TYPE sine\n#VOLUME 0.13", lead: "#TYPE triangle\n#VOLUME 0.25",
        pad: "#TYPE sawtooth\n#VOLUME 0.06", bass: "#TYPE triangle\n#VOLUME 0.5", clap: "#TYPE sawtooth",
      },
      pans: { keys: -0.3, pad: 0.3 },
      lowEnd: { ...house, tail: "G1" },
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
  },
  basalt: () => {
    const bpm = 126;
    const { kick, volume, fade, play, only, later, drone, whine, riser, impact, four } = forge(bpm);
    const low = { g: "G2", f: "F2", a: "Ab2", b: "Bb2", d: "D3", G: "G3" };
    const duck = [0, 0.55, 0.8, 1];
    const bass = (grid, level) => play(grid, low, (i) => volume(level, i) * duck[i % 4], [1, 0.35]);
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const grit = (grid, level) => play(only(grid, 3), high, level, [1, 0.35]);
    const plain = ".ggg".repeat(16);
    const turn = ".ggg.ggg.ggg.ggg" + ".ggg.ggg.ggg.ggG" + ".ggg.ggg.ggg.ggb" + ".ggg.ggg.gfg.agf";
    const tones = { f: "F4", g: "G4", a: "Ab4", b: "Bb4", d: "D5", e: "Eb5", G: "G5" };
    const seed = "...g............" + "................";
    const hooks = [
      "...g..g........." + "...g..a.........",
      "...g..g...d~...." + "...g..a..g......",
      "...g..g...d~...." + "...g..a..g....f.",
      "...g..g...e~...." + "G..g..a..g....f.",
      "...g..g...e~..d." + "G..g..a..g..b.G.",
    ];
    const pluck = [1, 0.45, 0.2];
    const ring = [1, 0.7, 0.5, 0.35, 0.22, 0.12];
    const last = [1, 0.6, 0.4, 0.25, 0.15, 0.08, 0.04, 0.02];
    const hook = (grid, level, decay = pluck) => play(grid, tones, level, decay);
    const up = { ...Object.fromEntries(Object.entries(tones).map(([key, note]) => [key, transpose(note, 12)])), G: "G5" };
    const echo = (grid, level, decay = pluck, notes = tones) => play(later(grid, 3), notes, level, decay);
    const tick = "..H...H...H...H.";
    const clap = "..H.P.H...H.P.H.";
    const shake = "..H.P.HZ..H.P.HZ";
    const open = "Z.O.P.OZ..O.P.OZ";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", hook: "#TYPE sawtooth\n#PAN -0.15",
        echo: "#TYPE triangle\n#PAN 0.45", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        intro: {
          bars: 8, kick: kick(four(8)), hook: `${rests(64)}\n${hook(seed.repeat(2), fade(0.04, 0.11, 64))}`,
          drums: ".".repeat(32) + "..Z...Z...Z...Z.".repeat(2) + tick.repeat(3) + "..H...H...H.RRRR",
        },
        groove: {
          bars: 16, kick: kick(four(16)), bass: bass(plain.repeat(3) + turn, 0.5), hook: hook(hooks[0].repeat(8), 0.15),
          drums: (clap.repeat(7) + "..H.P.H...H.P.RR").repeat(2),
        },
        layer: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(turn.repeat(4), 0.5),
          hook: hook(hooks[1].repeat(4) + hooks[2].repeat(4), 0.17), echo: echo(hooks[1].repeat(4) + hooks[2].repeat(4), 0.09),
          drone: drone("D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3 D3", 0.03),
          drums: shake.repeat(15) + "..H.P.HZ..H.PRRR",
        },
        peak: {
          bars: 16, kick: kick(four(15) + "x...x...x...x.x."), bass: bass(turn.repeat(4), 0.52), grit: grit(turn.repeat(4), 0.08),
          hook: hook(hooks[3].repeat(8), 0.19), echo: echo(hooks[3].repeat(8), 0.1),
          drone: drone("D3 D3 D3 D3 Eb3 Eb3 D3 D3 D3 D3 D3 D3 Eb3 Eb3 D3 D3", 0.035),
          drums: open.repeat(15) + "Z.O.P.OZXXXXXXXX",
        },
        breakdown: {
          bars: 8, bass: swell("G1", 32, bpm, [0.4, 0.15, 0.35, 0.12, 0.3, 0.1, 0.25]),
          hook: hook(hooks[2].repeat(4), 0.17, ring), echo: echo(hooks[2].repeat(4), 0.11, ring),
          drone: drone("D3 D3 D3 D3 Eb3 Eb3 Eb3 Eb3", 0.04), whine: whine("Ab5 Ab5 Ab5 Ab5", [0, 0.04]),
          drums: ".".repeat(64) + "..Z...Z...Z...Z.".repeat(3) + "..Z...Z...Z.Z.ZZ",
        },
        build: {
          bars: 8, kick: kick(four(6) + ".".repeat(32)), bass: bass("..g.".repeat(24) + ".".repeat(32), fade(0.25, 0.6, 96)),
          hook: hook(hooks[3].repeat(4), 0.17), whine: whine("Ab5 A5 Bb5 B5", [0.025, 0.05]), rise: riser(8),
          drums: clap.repeat(4) + "P.H.P.H.P.H.P.H.".repeat(2) + "P.P.P.P.P.P.P.P." + "XXXXXXXXXXXX....",
        },
        return: {
          bars: 24, kick: kick(four(24)), bass: bass(turn.repeat(6), 0.52), grit: grit(turn.repeat(6), 0.09),
          hook: hook(hooks[4].repeat(8) + hooks[3].repeat(4), 0.19), echo: echo(hooks[4].repeat(8) + hooks[3].repeat(4), 0.08, pluck, up),
          drone: drone("D3 D3 D3 D3 Eb3 Eb3 D3 D3 ".repeat(3).trim(), 0.035), rise: impact(24),
          drums: (open.repeat(7) + "Z.O.P.OZ..O.PRRR").repeat(3),
        },
        outro: {
          bars: 16, kick: kick(four(14) + "x" + ".".repeat(31)), bass: bass(turn.repeat(2) + ".".repeat(128), 0.5),
          hook: `${hook(hooks[1].repeat(4) + hooks[0].repeat(2) + seed, fade(0.17, 0.06, 224))}\n${hook("...g" + "~".repeat(25) + "...", 0.08, last)}`,
          echo: echo(hooks[1].repeat(4) + ".".repeat(128), fade(0.09, 0.03, 128)), drone: `${drone("D3 D3 D3 D3 D3 D3 D3 D3", 0.03)}\n${rests(128)}`,
          drums: clap.repeat(8) + tick.repeat(4) + "..Z...Z...Z...Z.".repeat(2) + ".".repeat(32),
        },
      },
      form: "intro groove layer peak breakdown build return outro",
    });
  },
  obsidian: () => {
    const bpm = 128;
    const { kick, volume, fade, play, later, drone, whine, riser, impact, four } = forge(bpm, "A1");
    const low = { a: "A2", c: "C3", g: "G2", A: "A3" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const bass = (grid, level) => play(grid, low, level, [1, 0.75, 0.5, 0.25]);
    const grit = (grid, level) => play(grid, high, level, [1, 0.4]);
    const pump = "..a~".repeat(16);
    const shove = "..a~".repeat(7) + "..A~" + "..a~".repeat(6) + "..c~..g~";
    const tones = { a: "A4", c: "C5", e: "E5", f: "F5" };
    const thirds = (bars, cycle, held = 0) => Array.from({ length: bars * 16 }, (_, i) => {
      const stroke = cycle[Math.floor(i / 3) % cycle.length];
      return i % 3 === 0 ? stroke : i % 3 <= held && stroke !== "." ? "~" : ".";
    }).join("");
    const pluck = [1, 0.4, 0.15];
    const ring = [1, 0.6, 0.35, 0.2, 0.1];
    const hook = (grid, level, decay = pluck) => play(grid, tones, (i) => volume(level, i) * (i % 4 === 2 ? 1 : 0.6), decay);
    const up = Object.fromEntries(Object.entries(tones).map(([key, note]) => [key, transpose(note, 12)]));
    const ghost = (grid, level, decay = pluck) => play(later(grid, 2), up, level, decay);
    const swing = "E3 E3 E3 E3 F3 F3 E3 E3";
    const hats = "..H...H...H...H.";
    const rim = "..H..XH...H..XH.";
    const full = "..H.PXH...H.PXHO";
    const fill = "..H..XH.X.X.XXXX";
    const roll = "X.X.X.X.XXXXXXXX";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", hook: "#TYPE triangle\n#PAN -0.2",
        ghost: "#TYPE sine\n#PAN 0.5", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        intro: { bars: 16, kick: kick(four(16)), drums: ".".repeat(64) + hats.repeat(8) + rim.repeat(3) + fill },
        bass: {
          bars: 8, kick: kick(four(8)), bass: bass(pump.repeat(2), fade(0.3, 0.5, 128)), hook: hook(thirds(8, "a..."), fade(0.1, 0.2, 128)),
          drums: rim.repeat(7) + fill,
        },
        groove: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(shove.repeat(4), 0.5),
          hook: hook(thirds(8, "a.a.") + thirds(8, "a.ae"), 0.22), drums: rim.repeat(15) + "..H..XH.X.X.XXXX",
        },
        cut: {
          bars: 4, hook: hook(thirds(4, "a.ae", 1), 0.22, ring), ghost: ghost(thirds(4, "a.ae", 1), 0.06, ring),
          drone: drone("E3 E3 E3 E3", 0.04), whine: whine("E5 F5", [0, 0.04]), drums: ".".repeat(32) + "X...X...X...X..." + roll,
        },
        peak: {
          bars: 16, kick: kick(four(15) + "x...x...x...x.x."), bass: bass(shove.repeat(4), 0.52), grit: grit(shove.repeat(4), 0.07),
          hook: hook(thirds(16, "acae"), 0.24), ghost: ghost(thirds(16, "acae"), 0.06), drone: drone(`${swing} ${swing}`, 0.03),
          drums: full.repeat(15) + "..H.PXH.X.X.XXXX",
        },
        breakdown: {
          bars: 12, bass: swell("A2", 48, bpm, [0.35, 0.1, 0.3, 0.08, 0.25, 0.06, 0.2]),
          hook: hook(thirds(12, "acaf", 2), 0.2, ring), ghost: ghost(thirds(12, "acaf", 2), 0.06, ring),
          drone: drone("E3 E3 E3 E3 F3 F3 F3 F3 E3 E3 E3 E3", 0.04), whine: whine("C6 C6 C6 B5 B5 B5", [0, 0.045, 0.02]),
          drums: ".".repeat(128) + hats.repeat(3) + "..H...H...H.XXXX",
        },
        build: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass("..a.".repeat(8) + ".".repeat(32), fade(0.3, 0.55, 32)),
          hook: hook(thirds(4, "acaf"), fade(0.15, 0.26, 64)), rise: riser(4, "E3"),
          drums: "P...P...P...P..." + "P.P.P.P.P.P.P.P." + roll + "XXXXXXXXXXXX....",
        },
        return: {
          bars: 16, kick: kick(four(16)), bass: bass(shove.repeat(4), 0.52), grit: grit(shove.repeat(4), 0.08),
          hook: hook(thirds(16, "ecaf"), 0.24), ghost: ghost(thirds(16, "ecaf"), 0.07), drone: drone(`${swing} ${swing}`, 0.035),
          rise: impact(16), drums: full.repeat(15) + "..H.PXH.X.X.XXXX",
        },
        outro: {
          bars: 16, kick: kick(four(15) + "x..............."), bass: bass(shove.repeat(2) + ".".repeat(128), 0.5),
          hook: hook(thirds(8, "a.ae") + thirds(4, "a.a.") + thirds(4, "a..."), fade(0.22, 0.05, 256)),
          drums: rim.repeat(8) + hats.repeat(4) + ".".repeat(64),
        },
      },
      form: "intro bass groove cut peak breakdown build return outro",
    });
  },
  cinder: () => {
    const bpm = 130;
    const { kick, volume, fade, play, fall, only, drone, riser, impact, four } = forge(bpm, "F1");
    const low = { f: "F2", a: "Ab2", b: "Bb2", c: "C3", e: "Eb3", F: "F3" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const accents = "..x...x...x....x";
    const accent = (i) => accents[i % 16] === "x";
    const acid = (grid, level) => play(grid, low, (i) => volume(level, i) * (accent(i) ? 1 : 0.5), [1, 0.3]);
    const body = (grid, level) => play(only(grid, 1), low, level, [1, 0.4]);
    const scream = (grid, level) => play(grid.replace(/./g, (step, i) => (accent(i) ? step : ".")), high, level, [1, 0.3]);
    const bars = {
      T: "..F...e~..F....e", U: ".fF..fe~.fF..cfe", A: ".fFf.fe~.fFf.cfe",
      B: ".fFf.fe~.fFF.bFe", C: ".fFf.fa~.fFf.cbF", D: ".fFf.fe~.F.F.FcF",
    };
    const line = (letters) => [...letters].map((bar) => bars[bar]).join("");
    const shot = [[0, 1], [-5, 0.75], [-12, 0.55], [-19, 0.4]];
    const zap = (grid, top, level) => fall(grid, { z: top }, level, shot);
    const zaps = [
      "......z~........" + "................",
      "......z~........" + "......z~....z~..",
      "...z~.z~........" + "......z~....z~..",
    ];
    const glow = "C3 C3 C3 C3 Db3 Db3 C3 C3";
    const hats = "..H...H...H...H.";
    const run = ".HZH.HZH.HZH.HZH";
    const clap = ".HZHPHZH.HZHPHZH";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", acid: "#TYPE sawtooth\n#PAN 0.1", body: "#TYPE triangle", scream: "#TYPE sawtooth\n#PAN -0.3",
        zap: "#TYPE sawtooth\n#PAN -0.35", drone: "#TYPE sawtooth", rise: "#TYPE noise",
      },
      parts: {
        intro: {
          bars: 8, kick: kick(four(8)), acid: `${rests(64)}\n${acid(line("TTTT"), fade(0.14, 0.24, 64))}`,
          drums: ".".repeat(32) + hats.repeat(5) + "..H...H.X.X.XXXX",
        },
        acid: {
          bars: 16, kick: kick(four(16)), acid: acid(line("UUUUAAABAAABAAAB"), 0.26), body: body(line("UUUUAAABAAABAAAB"), 0.45),
          drums: run.repeat(15) + ".HZH.HZH.X.XXXXX",
        },
        drive: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), acid: acid(line("AAAB".repeat(4)), 0.28), body: body(line("AAAB".repeat(4)), 0.45),
          zap: zap(zaps[0].repeat(4) + zaps[1].repeat(4), "C6", 0.07), drone: drone("C3 ".repeat(16).trim(), 0.03),
          drums: clap.repeat(15) + ".HZHPHZHXXXXXXXX",
        },
        gap: { bars: 2, acid: acid(line("AD"), 0.3), zap: zap(zaps[1], "C6", 0.07), drums: "X...X...X.X.X.X." + "XXXXXXXXXXXXXXXX" },
        peak: {
          bars: 16, kick: kick(four(16)), acid: acid(line("ACAD".repeat(4)), 0.3), body: body(line("ACAD".repeat(4)), 0.47),
          zap: zap(zaps[2].repeat(8), "Eb6", 0.075), drone: drone(`${glow} ${glow}`, 0.03), drums: clap.repeat(15) + ".HZHPHZH.X.XXXXX",
        },
        tool: {
          bars: 8, kick: kick(four(7) + "x...x...x......."), body: body(line("AAAAAAAA"), 0.5), acid: acid(line("TTTTTTTT"), 0.22),
          drums: run.repeat(7) + ".HZH.HZHXXXXXXXX",
        },
        gap2: { bars: 2, zap: zap(zaps[2], "F6", 0.075), rise: riser(2, "F4"), drums: "X.X.X.X.X.X.X.X." + "XXXXXXXXXXXXXXXX" },
        blaze: {
          bars: 24, kick: kick(four(24)), acid: acid(line("ACAD".repeat(6)), 0.3), scream: scream(line("ACAD".repeat(6)), 0.07),
          body: body(line("ACAD".repeat(6)), 0.47), zap: zap(zaps[2].repeat(12), "F6", 0.075), drone: drone(`${glow} ${glow} ${glow}`, 0.035),
          rise: impact(24), drums: (clap.repeat(7) + ".HZHPHZH.X.XXXXX").repeat(3),
        },
        outro: {
          bars: 16, kick: kick(four(15) + "x..............."), acid: acid(line("AAAAUUUUTTTT") + ".".repeat(64), fade(0.28, 0.12, 192)),
          body: body(line("AAAAUUUU") + ".".repeat(128), 0.45), drums: run.repeat(8) + hats.repeat(4) + ".".repeat(64),
        },
      },
      form: "intro acid drive gap peak tool gap2 blaze outro",
    });
  },
  graphite: () => {
    const bpm = 125;
    const { kick, fade, play, fall, later, drone, whine, riser, impact, four } = forge(bpm, "A1");
    const low = { d: "D2", f: "F2", g: "G2" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const suck = [0.15, 0.4, 0.7, 0.9, 1, 0.8];
    const bass = (grid, level) => play(grid, low, level, suck);
    const grit = (grid, level) => play(grid, high, level, [0.2, 0.6, 1, 0.6]);
    const pump = ".d~~".repeat(16);
    const walk = ".d~~".repeat(12) + ".d~~.d~~.f~~.g~~";
    const tones = { a: "A4", c: "C5", d: "D5", e: "E5", f: "F5" };
    const over = Object.fromEntries(Object.entries(tones).map(([key, note]) => [key, transpose(note, 19)]));
    const under = Object.fromEntries(Object.entries(tones).map(([key, note]) => [key, transpose(note, -12)]));
    const toll = [1, 0.7, 0.5, 0.35, 0.25, 0.17, 0.11, 0.07];
    const bells = [
      "......a~~~~~...." + ".".repeat(48),
      "......a~~~~~...." + ".".repeat(16) + "......a~~~~~...." + "..........d~~~~~",
      "......a~~~~~...." + "...c~~~~~......." + "......a~~~~~...." + "..........d~~~~~",
      "......a~~~~~...." + "...c~~~~~......." + "......a~~~~~..f~" + "~~~~......e~~~~~",
    ];
    const bell = (grid, level) => play(grid, tones, level, toll);
    const glint = Array.from({ length: 16 }, (_, i) => 0.75 ** i);
    const shine = (grid, level) => play(grid, over, level, glint);
    const answer = (grid, level) => play(later(grid, 32), under, level, toll);
    const sway = "D3 D3 D3 D3 C3 C3 D3 D3";
    const slap = [[0, 1], [-3, 0.55], [-7, 0.25]];
    const skins = { d: "D4", a: "A3" };
    const hands = (pattern, level = 0.35) => ({
      drums: pattern.replace(/B/g, "."),
      conga: fall(pattern.replace(/./g, (step, i) => (step !== "B" ? "." : i % 2 ? "d" : "a")), skins, level, slap),
    });
    const tick = "..Z...Z...Z...Z.";
    const conga = "..Z..BZ...Z.B.Z.";
    const deep = "..Z.XBZ...Z.XBZB";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", bell: "#TYPE triangle\n#PAN -0.25",
        shine: "#TYPE sine\n#PAN -0.4", answer: "#TYPE triangle\n#PAN 0.45", conga: "#TYPE triangle\n#PAN 0.3", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        haze: {
          bars: 8, drone: drone("D3 D3 D3 D3 D3 D3 D3 D3", 0.045), bell: bell(bells[0] + bells[1], fade(0.08, 0.2, 128)),
          shine: shine(bells[0] + bells[1], fade(0.02, 0.05, 128)),
        },
        floor: {
          bars: 8, kick: kick(four(8)), bell: bell(bells[1].repeat(2), 0.2), shine: shine(bells[1].repeat(2), 0.05),
          drone: drone("D3 D3 D3 D3 C3 C3 C3 C3", 0.035), ...hands(".".repeat(64) + tick.repeat(3) + "..Z...Z...Z.B.BB"),
        },
        pump: {
          bars: 16, kick: kick(four(16)), bass: `${bass(pump, fade(0.25, 0.5, 64))}\n${bass(pump.repeat(2) + walk, 0.5)}`,
          grit: `${grit(pump, fade(0, 0.045, 64))}\n${grit(pump.repeat(2) + walk, 0.045)}`,
          bell: bell(bells[1].repeat(4), 0.2), shine: shine(bells[1].repeat(4), 0.05), ...hands(conga.repeat(15) + "..Z..BZ...Z.B.BB"),
        },
        deep: {
          bars: 24, kick: kick(four(24)), bass: bass(walk.repeat(6), 0.5), grit: grit(walk.repeat(6), 0.06),
          bell: bell(bells[2].repeat(6), 0.21), shine: shine(bells[2].repeat(6), 0.05), answer: answer(bells[2].repeat(6), 0.08),
          drone: drone(`${sway} ${sway} ${sway}`, 0.03), ...hands((deep.repeat(7) + "..Z.XBZ.B.B.BBBB").repeat(3)),
        },
        breakdown: {
          bars: 16, bass: swell("D2", 64, bpm, [0.4, 0.15, 0.35, 0.12, 0.3, 0.1, 0.25, 0.08, 0.2]),
          bell: bell(bells[3].repeat(4), 0.21), shine: shine(bells[3].repeat(4), 0.06), answer: answer(bells[3].repeat(4), 0.1),
          drone: drone("D3 D3 D3 D3 C3 C3 C3 C3 Bb2 Bb2 Bb2 Bb2 C3 C3 C3 C3", 0.04), whine: whine("A5 A5 A5 A5 Bb5 Bb5 A5 A5", [0, 0.035, 0.02]),
          drums: ".".repeat(192) + tick.repeat(3) + "..Z...Z...Z.ZZZZ",
        },
        build: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass(".d~~".repeat(8) + ".".repeat(32), fade(0.25, 0.5, 32)),
          bell: bell(bells[3], 0.2), rise: riser(4, "D3"),
          ...hands("..Z.B.Z...Z.B.Z." + "B.B.B.B.B.B.B.B." + "BBBBBBBBBBBBBBBB" + "XXXXXXXX........"),
        },
        return: {
          bars: 16, kick: kick(four(16)), bass: bass(walk.repeat(4), 0.52), grit: grit(walk.repeat(4), 0.07),
          bell: bell(bells[3].repeat(4), 0.22), shine: shine(bells[3].repeat(4), 0.06), answer: answer(bells[3].repeat(4), 0.09),
          drone: drone(`${sway} ${sway}`, 0.035), rise: impact(16), ...hands((deep.repeat(7) + "..Z.XBZ.B.B.BBBB").repeat(2)),
        },
        settle: {
          bars: 8, kick: kick(four(8)), bass: bass(pump + ".".repeat(64), 0.45), grit: grit(pump + ".".repeat(64), 0.04),
          bell: bell(bells[1].repeat(2), 0.18),
          drone: drone("D3 D3 D3 D3 D3 D3 D3 D3", 0.035), ...hands(conga.repeat(4) + tick.repeat(4)),
        },
        dusk: {
          bars: 8, drone: swell("D3", 32, bpm, [0.03, 0.045, 0.03, 0], [-0.3, 0.3], 0.12),
          bell: bell(bells[1] + bells[0], fade(0.18, 0.06, 128)), shine: shine(bells[1] + bells[0], fade(0.05, 0.015, 128)),
        },
      },
      form: "haze floor pump deep breakdown build return settle dusk",
    });
  },
  magnetite: () => {
    const bpm = 129;
    const { kick, volume, fade, play, only, drone, whine, riser, impact, four } = forge(bpm, "B1");
    const low = { e: "E2", f: "F2", d: "D2", E: "E3", F: "F3", D: "D3" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const bass = (grid, level) => play(grid, low, (i) => volume(level, i) * (i % 4 === 3 ? 0.75 : 1), [1, 0.35]);
    const grit = (grid, level) => play(only(grid, 3), high, level, [1, 0.35]);
    const bounce = "..eE".repeat(15) + "..fF";
    const lurch = "..eE".repeat(7) + "..fF" + "..eE".repeat(6) + "..dD..fF";
    const root = { e: "E4", f: "F4", d: "D4" };
    const fifth = { e: "B4", f: "C5", d: "A4" };
    const stabs = [
      "......e........." + "................",
      "......e........." + "......e....e....",
      "......e..e......" + "......e....f....",
      "...e..e..e......" + "......e....f..f.",
      "...e..e..e....e." + "..e...e....f..f.",
    ];
    const held = "......e~~~~~...." + "......e~~~~f~~~~";
    const chop = [1, 0.45, 0.15];
    const ring = [1, 0.7, 0.5, 0.35, 0.25, 0.15, 0.08];
    const stab = (grid, level, decay = chop) => play(grid, root, level, decay);
    const five = (grid, level, decay = chop) => play(grid, fifth, level, decay);
    const tilt = "B2 B2 B2 B2 C3 C3 B2 B2";
    const hats = "..H...H...H...H.";
    const crack = "..H.X.H...H.X.H.";
    const open = "..O.X.O...O.X.O.";
    const drive = "..O.X.OH..O.X.OH";
    const roll = "X.X.X.X.XXXXXXXX";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", stab: "#TYPE sawtooth\n#PAN -0.2",
        five: "#TYPE triangle\n#PAN 0.3", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        intro: {
          bars: 8, kick: kick(four(8)), stab: `${rests(64)}\n${stab(stabs[0].repeat(2), fade(0.08, 0.15, 64))}`,
          five: `${rests(64)}\n${five(stabs[0].repeat(2), fade(0.06, 0.12, 64))}`, drums: ".".repeat(32) + hats.repeat(5) + "..H...H.X.X.XXXX",
        },
        groove: {
          bars: 16, kick: kick(four(16)), bass: bass(bounce.repeat(2) + lurch.repeat(2), 0.5),
          stab: stab(stabs[1].repeat(8), 0.15), five: five(stabs[1].repeat(8), 0.12), drums: crack.repeat(15) + "..H.X.H.X.X.XXXX",
        },
        break1: {
          bars: 8, bass: swell("E2", 32, bpm, [0.35, 0.12, 0.3, 0.1, 0.25]), stab: stab(held.repeat(4), 0.15, ring),
          five: five(held.repeat(4), 0.12, ring), drone: drone("B2 B2 B2 B2 C3 C3 C3 C3", 0.04), drums: ".".repeat(96) + hats.repeat(2),
        },
        build1: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass("..e.".repeat(8) + ".".repeat(32), fade(0.3, 0.55, 32)),
          stab: stab(stabs[2].repeat(2), 0.15), five: five(stabs[2].repeat(2), 0.12), rise: riser(4, "B3"),
          drums: "X...X...X...X..." + "X.X.X.X.X.X.X.X." + roll + "XXXXXXXXXXXX....",
        },
        drive: {
          bars: 16, kick: kick(four(16)), bass: bass(lurch.repeat(4), 0.52),
          stab: stab(stabs[2].repeat(4) + stabs[3].repeat(4), 0.16), five: five(stabs[2].repeat(4) + stabs[3].repeat(4), 0.12),
          drums: open.repeat(15) + "..O.X.O.X.X.XXXX",
        },
        peak: {
          bars: 16, kick: kick(four(15) + "x...x...x...x.x."), bass: bass(lurch.repeat(4), 0.52), grit: grit(lurch.repeat(4), 0.08),
          stab: stab(stabs[3].repeat(8), 0.17), five: five(stabs[3].repeat(8), 0.13), drone: drone(`${tilt} ${tilt}`, 0.035),
          drums: drive.repeat(15) + "..O.X.OHXXXXXXXX",
        },
        break2: {
          bars: 16, bass: swell("E2", 64, bpm, [0.4, 0.12, 0.35, 0.1, 0.3, 0.08, 0.25, 0.06, 0.2]),
          stab: stab(held.repeat(8), 0.15, ring), five: five(held.repeat(8), 0.12, ring),
          drone: drone("B2 B2 B2 B2 C3 C3 C3 C3 B2 B2 B2 B2 C3 C3 C3 C3", 0.04), whine: whine("B5 B5 C6 C6 B5 B5 C6 C6", [0, 0.04, 0.025]),
          drums: ".".repeat(192) + hats.repeat(3) + "..H...H...H.HHHH",
        },
        build2: {
          bars: 8, kick: kick(four(6) + ".".repeat(32)), bass: bass("..e.".repeat(24) + ".".repeat(32), fade(0.25, 0.6, 96)),
          stab: stab(stabs[4].repeat(4), fade(0.1, 0.18, 128)), five: five(stabs[4].repeat(4), fade(0.08, 0.14, 128)),
          whine: whine("C6 C#6 D6 D#6", [0.025, 0.05]), rise: riser(8, "E3"),
          drums: crack.repeat(4) + "X.H.X.H.X.H.X.H.".repeat(2) + roll + "XXXXXXXXXXXX....",
        },
        final: {
          bars: 16, kick: kick(four(16)), bass: bass(lurch.repeat(4), 0.52), grit: grit(lurch.repeat(4), 0.09),
          stab: stab(stabs[4].repeat(8), 0.17), five: five(stabs[4].repeat(8), 0.13), drone: drone(`${tilt} ${tilt}`, 0.035),
          rise: impact(16), drums: drive.repeat(15) + "..O.X.OHX.X.XXXX",
        },
        outro: {
          bars: 8, kick: kick(four(7) + "x..............."), bass: bass(bounce + ".".repeat(64), 0.5),
          stab: stab(stabs[1].repeat(2) + stabs[0].repeat(2), fade(0.15, 0.05, 128)),
          five: five(stabs[1].repeat(2) + stabs[0].repeat(2), fade(0.12, 0.04, 128)), drums: crack.repeat(4) + hats.repeat(2) + ".".repeat(32),
        },
      },
      form: "intro groove break1 build1 drive peak break2 build2 final outro",
    });
  },
  slate: () => {
    const bpm = 127;
    const { kick, fade, play, only, drone, whine, riser, impact, four } = forge(bpm, "G1");
    const low = { c: "C3", b: "Bb2", g: "G2", e: "Eb3" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const bass = (grid, level, decay = [1, 0.45]) => play(grid, low, level, decay);
    const grit = (grid, level) => play(only(grid, 1), high, level, [1, 0.4]);
    const off = "..c..c.c..c..c.c";
    const slip = off.repeat(3) + "..c..c.c..e..b.g";
    const long = [1, 0.8, 0.6, 0.45, 0.3, 0.2];
    const left = { g: "G4", f: "F4" };
    const right = { c: "C5", d: "D5", e: "Eb5" };
    const calls = [
      ["...g~...........".repeat(2), ".".repeat(32)],
      ["...g~...........".repeat(2), "........c~......".repeat(2)],
      ["...g~.....g~...." + "...g~...........", "........c~......" + "........c~...c~."],
      ["...g~.....g~...." + "...g~.....f~....", "........e~......" + "........d~...c~."],
      ["...g~.....g~..g~" + "...g~.....f~....", "........e~....c~" + "........d~...c~."],
    ];
    const ping = [1, 0.6, 0.35, 0.2, 0.1];
    const ring = [1, 0.75, 0.55, 0.4, 0.28, 0.18, 0.1];
    const linger = (grid) => grid.replace(/~(\.{1,5})/g, (_, dots) => "~".repeat(dots.length + 1));
    const call = (n, times, side, level, decay = ping, shape = (grid) => grid) =>
      play(shape(calls[n][side]).repeat(times), side ? right : left, level, decay);
    const shade = "G3 G3 G3 G3 Ab3 Ab3 G3 G3";
    const hats = "..H...H...H...H.";
    const broken = "..H...P...H..P.H";
    const layer = "Z.H..XP.Z.H..P.H";
    const fill = "..H...P.X.X.XXXX";
    const roll = "X.X.X.X.XXXXXXXX";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", left: "#TYPE triangle\n#PAN -0.6",
        right: "#TYPE triangle\n#PAN 0.6", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        alone: {
          bars: 8, left: `${call(0, 2, 0, fade(0.08, 0.18, 64))}\n${call(1, 2, 0, 0.18)}`, right: `${rests(64)}\n${call(1, 2, 1, 0.16)}`,
          drone: drone("G3 G3 G3 G3 G3 G3 G3 G3", 0.03),
        },
        floor: { bars: 8, kick: kick(four(8)), left: call(1, 4, 0, 0.18), right: call(1, 4, 1, 0.16), drums: hats.repeat(7) + fill },
        groove: {
          bars: 16, kick: kick(four(16)), bass: bass(slip.repeat(4), 0.5), left: call(2, 8, 0, 0.18), right: call(2, 8, 1, 0.16),
          drums: broken.repeat(15) + fill,
        },
        layer: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(slip.repeat(4), 0.5), grit: grit(slip.repeat(4), 0.08),
          left: call(3, 8, 0, 0.19), right: call(3, 8, 1, 0.17), drone: drone(`${shade} ${shade}`, 0.03),
          drums: layer.repeat(15) + "Z.H..XP.XXXXXXXX",
        },
        half: {
          bars: 8, kick: kick("x.........x.....".repeat(8)), bass: bass("..c~~~~~~~..b~~~..c~~~~~~~..g~~~".repeat(4), 0.42, long),
          left: call(3, 4, 0, 0.18, ring, linger), right: call(3, 4, 1, 0.16, ring, linger), drone: drone(shade, 0.035),
          whine: whine("G5 G5 Ab5 Ab5", [0, 0.04]), drums: "....Z...X...Z.Z.".repeat(7) + "....Z...X.X.XXXX",
        },
        build: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass(off.repeat(2) + ".".repeat(32), fade(0.3, 0.55, 32)),
          left: call(3, 2, 0, 0.18), right: call(3, 2, 1, 0.16), rise: riser(4, "G3"),
          drums: "P...P...P...P..." + "P.P.P.P.P.P.P.P." + roll + "XXXXXXXXXXXX....",
        },
        return: {
          bars: 16, kick: kick(four(16)), bass: bass(slip.repeat(4), 0.52), grit: grit(slip.repeat(4), 0.08),
          left: call(3, 8, 0, 0.2), right: call(3, 8, 1, 0.18), drone: drone(`${shade} ${shade}`, 0.035),
          drums: layer.repeat(15) + "Z.H..XP.X.X.XXXX",
        },
        cut: {
          bars: 2, left: call(4, 1, 0, 0.2, ring, linger), right: call(4, 1, 1, 0.18, ring, linger),
          drums: ".".repeat(16) + "X...X...X.X.XXXX",
        },
        again: {
          bars: 16, kick: kick(four(16)), bass: bass(slip.repeat(4), 0.52), grit: grit(slip.repeat(4), 0.09),
          left: call(4, 8, 0, 0.2), right: call(4, 8, 1, 0.18), drone: drone(`${shade} ${shade}`, 0.035), rise: impact(16),
          drums: layer.repeat(15) + fill,
        },
        outro: {
          bars: 8, kick: kick(four(7) + "x..............."), bass: bass(slip + ".".repeat(64), 0.5),
          left: `${call(2, 2, 0, fade(0.18, 0.1, 64))}\n${call(0, 2, 0, fade(0.1, 0.03, 64))}`, right: `${call(2, 2, 1, fade(0.16, 0.08, 64))}\n${rests(64)}`,
          drums: hats.repeat(6) + ".".repeat(32),
        },
      },
      form: "alone floor groove layer half build return cut again outro",
    });
  },
  caldera: () => {
    const bpm = 130;
    const { kick, fade, play, drone, whine, riser, impact, four } = forge(bpm, "B1");
    const low = { b: "B2", f: "F#2", d: "D3", a: "A2" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const threes = (bars, cycle) => Array.from({ length: bars * 16 }, (_, i) => {
      const at = i - (i % 3);
      if (at % 4 === 0) return ".";
      return i % 3 === 0 ? cycle[(at / 3) % cycle.length] : i % 3 === 1 ? "~" : ".";
    }).join("");
    const bass = (grid, level) => play(grid, low, level, [1, 0.6, 0.3]);
    const grit = (grid, level) => play(grid, high, level, [1, 0.3]);
    const tones = { b: "B3", d: "D4", f: "F#4", B: "B4", a: "A3", g: "G3", e: "E4" };
    const arp = "bBfdbBfdbBfdbBfa";
    const lift = "gBfdgBfdbBfdbBfe";
    const masks = [
      "x...............", "x.......x.......", "x...x...x...x...", "x.x.x.x.x.x.x.x.",
      "x.xxx.xxx.xxx.xx", "xxxxxxxxxxxxxxxx", "x~x~x~x~x~x~x~x~",
    ];
    const seq = (bars, mask, notes = arp) => Array.from({ length: bars * 16 }, (_, i) => (mask[i % 16] === "x" ? notes[i % 16] : mask[i % 16] === "~" ? "~" : ".")).join("");
    const sequence = (grid, level, decay = [1, 0.3]) => play(grid, tones, level, decay);
    const heat = "F#3 F#3 F#3 F#3 G3 G3 F#3 F#3";
    const h1 = "..H...H...H...H.";
    const h2 = "..H...HZ..H...HZ";
    const h3 = ".ZH.P.HZ.ZH.P.HZ";
    const h4 = ".ZH.PZHZ.ZHHPZHZ";
    const fill = "..H...H.X.X.XXXX";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", seq: "#TYPE sawtooth\n#PAN 0.2",
        drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        vent: { bars: 8, kick: kick(four(8)), seq: sequence(seq(8, masks[0]), fade(0.06, 0.12, 128)), drums: ".".repeat(112) + fill },
        smoke: {
          bars: 8, kick: kick(four(8)), bass: bass(threes(8, "b"), fade(0.3, 0.5, 128)), seq: sequence(seq(8, masks[1]), 0.12),
          drums: h1.repeat(7) + fill,
        },
        ash: {
          bars: 8, kick: kick(four(8)), bass: bass(threes(8, "bbbf"), 0.5), seq: sequence(seq(8, masks[2]), 0.12),
          drums: h1.repeat(7) + fill,
        },
        heat: {
          bars: 8, kick: kick(four(8)), bass: bass(threes(8, "bbbf"), 0.5), seq: sequence(seq(8, masks[3]), 0.12),
          drone: drone(heat, 0.03), drums: h2.repeat(7) + fill,
        },
        glow: {
          bars: 8, kick: kick(four(8)), bass: bass(threes(8, "bbdbbf"), 0.5), seq: sequence(seq(8, masks[4]), 0.12),
          drone: drone(heat, 0.03), drums: h3.repeat(7) + fill,
        },
        flow: {
          bars: 8, kick: kick(four(8)), bass: bass(threes(8, "bbdbbf"), 0.5), grit: grit(threes(8, "bbdbbf"), 0.06),
          seq: sequence(seq(8, masks[5]), 0.12), drone: drone(heat, 0.035), drums: h3.repeat(7) + fill,
        },
        surge: {
          bars: 8, kick: kick(four(7) + "x...x...x.x.x.x."), bass: bass(threes(8, "bbdbbf"), 0.52), grit: grit(threes(8, "bbdbbf"), 0.07),
          seq: sequence(seq(8, masks[5]), 0.13), drone: drone(heat, 0.035), rise: riser(8, "B2"), drums: h4.repeat(7) + "X".repeat(16),
        },
        erupt: {
          bars: 16, kick: kick(four(16)), bass: bass(threes(16, "bbdbbf"), 0.52), grit: grit(threes(16, "bbdbbf"), 0.08),
          seq: sequence(seq(16, masks[5]), 0.13), drone: drone(`${heat} ${heat}`, 0.035), rise: impact(16), drums: h4.repeat(15) + fill,
        },
        crater: {
          bars: 8, bass: swell("B2", 32, bpm, [0.35, 0.1, 0.3, 0.08, 0.25]), seq: sequence(seq(8, masks[6]), 0.11, [1, 0.6, 0.35, 0.2]),
          drone: drone("F#3 F#3 F#3 F#3 G3 G3 G3 G3", 0.04), whine: whine("D6 D6 C#6 C#6", [0, 0.04]),
          drums: ".".repeat(64) + h1.repeat(3) + "..H...H...H.HHHH",
        },
        reload: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass(threes(2, "b") + ".".repeat(32), fade(0.3, 0.55, 32)),
          seq: sequence(seq(4, masks[5]), fade(0.06, 0.13, 64)), rise: riser(4, "F#3"),
          drums: "P...P...P...P..." + "P.P.P.P.P.P.P.P." + "X.X.X.X.XXXXXXXX" + "XXXXXXXXXXXX....",
        },
        blast: {
          bars: 16, kick: kick(four(16)), bass: bass(threes(16, "bbabbf"), 0.52), grit: grit(threes(16, "bbabbf"), 0.08),
          seq: sequence(seq(8, masks[5], lift) + seq(8, masks[5]), 0.13), drone: drone(`${heat} ${heat}`, 0.035), rise: impact(16),
          drums: h4.repeat(15) + "..H.PZH.XXXXXXXX",
        },
        stop: { bars: 1, kick: kick("x..............."), drums: "X..............." },
      },
      form: "vent smoke ash heat glow flow surge erupt crater reload blast stop",
    });
  },
  faultline: () => {
    const bpm = 128;
    const { kick, fade, play, drone, whine, riser, impact, four } = forge(bpm, "F#1");
    const low = { f: "F#2", e: "E2", d: "D2", c: "C#2" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const breath = [0.8, 1, 0.3, 0.75, 0.95, 1, 0.3, 0.75, 0.95, 1, 0.3, 0.65];
    const hold = (notes) => [...notes].map((note) => `..${note}${"~".repeat(11)}..`).join("");
    const bass = (grid, level) => play(grid, low, level, breath);
    const grit = (grid, level) => play(grid, high, level, breath);
    const fault = hold("ffed");
    const shear = hold("fedc");
    const tones = { s: "C#5", S: "F#5" };
    const masks = ["..s...s.........", "..s..ss...s.....", "..s..ss..ss..s..", "s.s..ss..ss..sSs", "..s~~~....s~~~.."];
    const turn = (mask, n) => mask.slice(16 - n) + mask.slice(0, 16 - n);
    const stutter = (mask, shifts, level, decay = [1, 0.25]) => play(shifts.map((n) => turn(mask, n)).join(""), tones, level, decay);
    const steps = (pattern) => [...pattern].map(Number);
    const ring = [1, 0.6, 0.35, 0.2, 0.1];
    const deep = "C#3 C#3 C#3 C#3 D3 D3 C#3 C#3";
    const hats = "..H...H...H...H.";
    const claps = "..H.P..H..H.P..H";
    const drive = "..O.P..H..O.P.RH";
    const roll = "X.X.X.X.XXXXXXXX";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", stutter: "#TYPE sawtooth\n#PAN 0.25",
        drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        intro: {
          bars: 8, kick: kick(four(8)), stutter: `${rests(64)}\n${stutter(masks[0], steps("0000"), fade(0.06, 0.12, 64))}`,
          drums: ".".repeat(32) + hats.repeat(5) + "..H...H.R.R.RRRR",
        },
        groove: {
          bars: 16, kick: kick(four(16)), bass: bass(fault.repeat(4), 0.5), grit: grit(fault.repeat(4), 0.04), stutter: stutter(masks[1], steps("0000000011111111"), 0.13),
          drums: claps.repeat(15) + "..H.P..HR.R.RRRR",
        },
        stumble: {
          bars: 8, kick: kick(("x...x...x...x..." + "x...x...x..x..x.").repeat(4)), bass: bass(fault.repeat(2), 0.5),
          stutter: stutter(masks[2], steps("00112233"), 0.13), drums: (claps + "..H.P..H..HR..RH").repeat(4),
        },
        drive: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(shear.repeat(4), 0.52), grit: grit(shear.repeat(4), 0.06),
          stutter: stutter(masks[2], steps("0000111122221111"), 0.14), drone: drone(`${deep} ${deep}`, 0.03),
          drums: drive.repeat(15) + "..O.P..HX.X.XXXX",
        },
        breakdown: {
          bars: 8, bass: swell("F#2", 32, bpm, [0.35, 0.1, 0.3, 0.08, 0.25]), stutter: stutter(masks[4], steps("00000000"), 0.13, ring),
          drone: drone("C#3 C#3 C#3 C#3 D3 D3 D3 D3", 0.04), whine: whine("A5 A5 A#5 A#5", [0, 0.04]),
        },
        build: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass("..f.".repeat(8) + ".".repeat(32), fade(0.3, 0.55, 32)),
          stutter: `${stutter(masks[3], steps("000"), fade(0.1, 0.16, 48))}\n${rests(16)}`, rise: `${riser(3, "C#3")}\n${rests(16)}`,
          drums: "X...X...X...X..." + roll + "X".repeat(16) + ".".repeat(16),
        },
        fall: {
          bars: 8, bass: swell("F#2", 32, bpm, [0.2, 0.35, 0.1, 0.3, 0.08, 0.25]), stutter: stutter(masks[4], steps("11112222"), 0.13, ring),
          drone: drone("D3 D3 D3 D3 C#3 C#3 C#3 C#3", 0.04), whine: whine("A#5 A#5 B5 B5", [0.04, 0.02, 0.045]),
          drums: ".".repeat(96) + hats.repeat(2),
        },
        build2: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass("..f.".repeat(8) + ".".repeat(32), fade(0.3, 0.55, 32)),
          stutter: stutter(masks[3], steps("0000"), fade(0.1, 0.16, 64)), rise: riser(4, "F#3"),
          drums: "P...P...P...P..." + "P.P.P.P.P.P.P.P." + roll + "XXXXXXXXXXXX....",
        },
        return: {
          bars: 24, kick: kick(four(24)), bass: bass(shear.repeat(6), 0.52), grit: grit(shear.repeat(6), 0.07),
          stutter: stutter(masks[3], steps("000011112222333322221111"), 0.15), drone: drone(`${deep} ${deep} ${deep}`, 0.035),
          rise: impact(24), drums: (drive.repeat(7) + "..O.P..HX.X.XXXX").repeat(3),
        },
        outro: {
          bars: 12, kick: kick(four(11) + "x..............."), bass: bass(fault.repeat(2) + ".".repeat(64), 0.5),
          stutter: stutter(masks[1], steps("111100000000"), fade(0.13, 0.03, 192)), drums: claps.repeat(8) + ".".repeat(64),
        },
      },
      form: "intro groove stumble drive breakdown build fall build2 return outro",
    });
  },
  anthracite: () => {
    const bpm = 125;
    const { kick, volume, fade, play, only, later, drone, whine, riser, impact, four } = forge(bpm, "F1");
    const low = { b: "Bb2", a: "Ab2", g: "Gb2", f: "F2" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const press = [0.25, 0.7, 0.85, 1];
    const bass = (grid, level) => play(grid, low, (i) => volume(level, i) * press[i % 4], [1, 0.5]);
    const grit = (grid, level) => play(only(grid, 2), high, level, [1, 0.35]);
    const roll = "bbbb".repeat(16);
    const sink = "bbbb".repeat(15) + "bagf";
    const tones = { b: "Bb3", a: "A3", d: "Db4", g: "Gb3" };
    const up = Object.fromEntries(Object.entries(tones).map(([key, note]) => [key, transpose(note, 12)]));
    const bloom = [0.2, 0.5, 0.8, 1, 0.9, 0.65];
    const horns = [
      "........b~~~~~~~" + ".".repeat(48),
      "........b~~~~~~~" + "a~~~~~~~........" + ".".repeat(32),
      "........b~~~~~~~" + "a~~~~~~~........" + "........b~~~~~~~" + "...d~~~~~~~.....",
      "........b~~~~~~~" + "a~~~~~~~........" + "........b~~~~~~~" + "g~~~~~~~...d~~~~",
    ];
    const horn = (grid, level) => play(grid, tones, level, bloom);
    const shadow = (grid, level) => play(later(grid, 4), up, level, bloom);
    const smoulder = "F3 F3 F3 F3 Gb3 Gb3 F3 F3";
    const shaker = "..Z...Z...Z...Z.";
    const heavy = "..Z.P.Z...Z.P.PZ";
    const smoke = "Z.Z.P.Z.Z.Z.P.PZ";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", horn: "#TYPE sawtooth\n#PAN -0.15",
        shadow: "#TYPE triangle\n#PAN 0.45", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        alone: { bars: 8, bass: bass(roll.repeat(2), fade(0.1, 0.45, 128)) },
        floor: {
          bars: 16, kick: kick(four(16)), bass: bass(roll.repeat(3) + sink, 0.45),
          drums: ".".repeat(128) + shaker.repeat(7) + "..Z...Z...Z.P.PP",
        },
        smoulder: {
          bars: 16, kick: kick(four(16)), bass: bass(sink.repeat(4), 0.45), horn: horn(horns[0].repeat(2) + horns[1].repeat(2), 0.12),
          drums: heavy.repeat(15) + "..Z.P.Z.P.P.PPPP",
        },
        burn: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(sink.repeat(4), 0.47), horn: horn(horns[1].repeat(4), 0.13),
          drone: drone(`${smoulder} ${smoulder}`, 0.03), drums: heavy.repeat(15) + "..Z.P.Z.XXXXXXXX",
        },
        deeper: {
          bars: 16, kick: kick(four(16)), bass: bass(sink.repeat(4), 0.48), grit: grit(sink.repeat(4), 0.06),
          horn: horn(horns[2].repeat(4), 0.13), drone: drone(`${smoulder} ${smoulder}`, 0.03), drums: smoke.repeat(15) + "Z.Z.P.Z.P.P.PPPP",
        },
        breakdown: {
          bars: 16, bass: bass(roll.repeat(4), fade(0.3, 0.12, 256)), horn: horn(horns[2].repeat(4), 0.13),
          shadow: shadow(horns[2].repeat(4), 0.05), drone: drone("F3 F3 F3 F3 Gb3 Gb3 Gb3 Gb3 F3 F3 F3 F3 E3 E3 E3 E3", 0.04),
          whine: whine("Db6 Db6 C6 C6 Db6 Db6 D6 D6", [0, 0.04, 0.02]), drums: ".".repeat(192) + shaker.repeat(4),
        },
        build: {
          bars: 8, kick: kick(four(6) + ".".repeat(32)), bass: bass(roll.repeat(2), fade(0.12, 0.5, 128)), horn: horn(horns[3].repeat(2), 0.13),
          rise: riser(8, "F2"), drums: heavy.repeat(4) + "P.Z.P.Z.P.Z.P.Z.".repeat(2) + "X.X.X.X.XXXXXXXX" + "XXXXXXXXXXXX....",
        },
        blaze: {
          bars: 24, kick: kick(four(24)), bass: bass(sink.repeat(6), 0.5), grit: grit(sink.repeat(6), 0.07), horn: horn(horns[3].repeat(6), 0.14),
          shadow: shadow(horns[3].repeat(6), 0.05), drone: drone(`${smoulder} ${smoulder} ${smoulder}`, 0.035), rise: impact(24),
          drums: (smoke.repeat(7) + "Z.Z.P.Z.X.X.XXXX").repeat(3),
        },
        embers: {
          bars: 16, kick: kick(four(8) + ".".repeat(128)), bass: bass(sink.repeat(2) + roll.repeat(2), fade(0.45, 0.05, 256)),
          horn: `${horn(horns[1].repeat(2), 0.12)}\n${rests(128)}`, drums: heavy.repeat(8) + ".".repeat(128),
        },
      },
      form: "alone floor smoulder burn deeper breakdown build blaze embers",
    });
  },
  tephra: () => {
    const bpm = 127;
    const { kick, volume, fade, play, fall, only, drone, whine, riser, impact, four } = forge(bpm, "G#1");
    const low = { c: "C#3", g: "G#2", b: "B2", e: "E3" };
    const high = Object.fromEntries(Object.entries(low).map(([key, note]) => [key, transpose(note, 12)]));
    const bass = (grid, level) => play(grid, low, (i) => volume(level, i) * (i % 4 === 3 ? 1 : 0.8), [1, 0.4]);
    const grit = (grid, level) => play(only(grid, 3), high, level, [1, 0.35]);
    const skip = ".c.c".repeat(16);
    const hop = ".c.c".repeat(12) + ".c.c.c.c.e.c.b.g";
    const thud = [[0, 1], [-5, 0.6], [-12, 0.3]];
    const pitch = { c: "C#4", g: "G#3", e: "E4", b: "B3" };
    const tom = (grid, level) => fall(grid, pitch, level, thud);
    const figures = [
      "......g.......g." + "......g...g...g.",
      "..c...g..c....g." + "..c...g...g..cg.",
      "..c..eg..c..b.g." + "..c..eg...g.ecg.",
    ];
    const tones = { g: "G#5", c: "C#6", b: "B5", e: "E6" };
    const blow = Array.from({ length: 20 }, (_, i) => ease([0.5, 1, 0.85, 0.6, 0.35], i / 19));
    const calls = [
      "........g~~~~~.." + ".".repeat(48),
      "........g~~~~~.." + "c~~~~~~~~~......" + ".".repeat(32),
      "........g~~~~~.." + "c~~~~~~~~~......" + "........g~~~~~.." + "b~~~~~e~~~~~~~..",
    ];
    const whistle = (grid, level) => play(grid, tones, level, blow);
    const glow = "G#3 G#3 G#3 G#3 A3 A3 G#3 G#3";
    const shake = "..Z...Z...Z...Z.";
    const tribe = "..Z.X.ZZ..Z.X.ZZ";
    const stomp = "..O.X.ZZ..O.PXZZ";
    return song({
      bpm,
      voices: {
        kick: "#TYPE triangle", bass: "#TYPE triangle", grit: "#TYPE sawtooth", tom: "#TYPE triangle\n#PAN 0.2",
        whistle: "#TYPE sine\n#PAN -0.35", drone: "#TYPE sawtooth", whine: "#TYPE sine", rise: "#TYPE noise",
      },
      parts: {
        skins: { bars: 8, tom: tom(figures[0].repeat(2) + figures[1].repeat(2), fade(0.25, 0.4, 128)), drums: shake.repeat(8) },
        floor: { bars: 8, kick: kick(four(8)), tom: tom(figures[1].repeat(4), 0.4), drums: shake.repeat(7) + "..Z...Z.X.X.XXXX" },
        groove: {
          bars: 16, kick: kick(four(16)), bass: bass(skip.repeat(3) + hop, 0.48), tom: tom(figures[1].repeat(8), 0.4),
          whistle: whistle(calls[0].repeat(4), 0.06), drums: tribe.repeat(15) + "..Z.X.ZZX.X.XXXX",
        },
        layer: {
          bars: 16, kick: kick(four(15) + "x...x...x......."), bass: bass(hop.repeat(4), 0.5), grit: grit(hop.repeat(4), 0.07),
          tom: tom(figures[2].repeat(8), 0.42), whistle: whistle(calls[1].repeat(4), 0.065), drone: drone(`${glow} ${glow}`, 0.03),
          drums: tribe.repeat(15) + "..Z.X.ZZX.X.XXXX",
        },
        rite: { bars: 8, kick: kick(four(8)), tom: tom(figures[2].repeat(4), 0.45), drums: stomp.repeat(7) + "..O.X.ZZX.X.XXXX" },
        breakdown: {
          bars: 8, bass: swell("C#3", 32, bpm, [0.3, 0.1, 0.25, 0.08, 0.2]), whistle: whistle(calls[2].repeat(2), 0.065),
          tom: tom(figures[0].repeat(2) + ".".repeat(64), fade(0.3, 0.1, 64)), drone: drone("G#3 G#3 G#3 G#3 A3 A3 A3 A3", 0.04),
          whine: whine("E5 E5 D#5 D#5", [0, 0.035]),
        },
        build: {
          bars: 4, kick: kick(four(2) + ".".repeat(32)), bass: bass(".c.c".repeat(8) + ".".repeat(32), fade(0.3, 0.55, 32)),
          tom: tom("..c...g..c....g." + "..c.c.g.c.c.g.g." + "c.c.c.c.g.g.g.g." + "ccccggggccccgggg", fade(0.3, 0.5, 64)), rise: riser(4, "G#3"),
          drums: shake.repeat(2) + "X.X.X.X.XXXXXXXX" + "XXXXXXXXXXXX....",
        },
        peak: {
          bars: 24, kick: kick(four(24)), bass: bass(hop.repeat(6), 0.52), grit: grit(hop.repeat(6), 0.08), tom: tom(figures[2].repeat(12), 0.45),
          whistle: whistle(calls[2].repeat(6), 0.07), drone: drone(`${glow} ${glow} ${glow}`, 0.035), rise: impact(24),
          drums: (stomp.repeat(7) + "..O.X.ZZX.X.XXXX").repeat(3),
        },
        outro: {
          bars: 8, kick: kick(four(7) + "x..............."), tom: tom(figures[1].repeat(2) + figures[0].repeat(2), fade(0.4, 0.15, 128)),
          drums: tribe.repeat(4) + shake.repeat(2) + ".".repeat(32),
        },
      },
      form: "skins floor groove layer rite breakdown build peak outro",
    });
  },
  undertow: () => {
    const bpm = 96;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { E: "E1", F: "F1", G: "G1" }, lows: { E: "E2", F: "F2", G: "G2" }, tones: { E: "B3", F: "A3", G: "D4" },
    });
    const dark = "E E E E F F E E";
    const turn = "E E G G F F E E";
    const melody = "- - - - B4 - - - - - C5 -";
    const half = "K.......S.......";
    const shuffle = "K..Z..Z.S..Z..Z.";
    const ticks = "K.HZ..ZHS.HZ..ZH";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN 0.4",
      },
      parts: {
        intro: {
          bars: 8, sub: swell("E1", 32, bpm, [0, 0.2, 0.08, 0.3, 0.12, 0.4, 0.15, 0.5, 0.25]),
          near: `${"-:w ".repeat(4)}\n${near("E E E E", 0.16)}`, far: far("- - E4", 0.08),
        },
        rise: {
          bars: 8, sub: sub("E E E E E E E E", 0.25, 0.55), growl: growl("E E E E E E E E", 0.15),
          near: near("E E E E E E E E", 0.26), far: far("B4 - E4", 0.1), pulse: pulse("E E E E E E E E", [0, 0.15]),
          drums: ".".repeat(64) + half.repeat(3) + "K.......S...K.K.",
        },
        groove: {
          bars: 16, sub: sub(`${dark} ${dark}`, 0.3, 0.6), growl: growl(`${dark} ${dark}`, 0.2), near: near(`${dark} ${dark}`, 0.28),
          far: far("B4 - C5 B4 - C5 B4", 0.13), pulse: pulse(`${dark} ${dark}`, [0.14, 0.18, 0.14]),
          drums: half.repeat(7) + "K.......S...S.SS" + shuffle.repeat(7) + "K..Z..Z.S..Z.KZK",
        },
        breakdown: {
          bars: 8, sub: swell("E1", 32, bpm, [0.6, 0.2, 0.55, 0.15, 0.5, 0.15, 0.45, 0.2, 0.3]),
          far: far("E4 - B4", 0.08), ping: ping("B4 C5 B4 -", 0.25), echo: echo("B4 C5 B4 -", 0.1),
          drums: ".".repeat(96) + "K.......K.......K.......K...K.K.",
        },
        groove2: {
          bars: 24, sub: sub(`${turn} ${turn} ${turn}`, 0.3, 0.6),
          growl: `${growl(turn, 0.25)}\n${growl(turn, 0.25, 2)}\n${growl(turn, 0.25, 4)}`,
          near: near(`${turn} ${turn} ${turn}`, 0.3), far: far("B4 D5 C5 B4 B4 D5 C5 B4 B4 D5 C5", 0.14),
          pulse: pulse(`${turn} ${turn} ${turn}`, [0.14, 0.2, 0.16]), ping: ping(melody, 0.2), echo: echo(melody, 0.08),
          drums: shuffle.repeat(7) + "K..Z..Z.S..Z.KZK" + ticks.repeat(15) + "K.HZ..ZHS.S.SSSS",
        },
        outro: {
          bars: 8, sub: swell("E1", 32, bpm, [0.3, 0.55, 0.25, 0.45, 0.15, 0.3, 0.05, 0.15, 0]),
          growl: swell("E2", 32, bpm, [0, 0.2, 0, 0.12, 0, 0.06, 0, 0, 0]), near: `${near("E E E E E E", 0.16)}\n-:w -:w`,
          pulse: pulse("E E E E E E E E", [0.14, 0]), ping: ping("- - - B4", 0.16),
          drums: half.repeat(4) + "K...............".repeat(2) + ".".repeat(32),
        },
      },
      form: "intro rise groove breakdown groove2 outro",
    });
  },
  fathom: () => {
    const bpm = 88;
    const { sub, growl, near, far, pulse, ping } = depth(bpm, {
      roots: { D: "D1", F: "F1", C: "C1" }, lows: { D: "D2", F: "F2", C: "C2" }, tones: { D: "A3", F: "C4", C: "G3" },
    });
    const slow = { per: 0.5 };
    const wide = { pans: [-0.7, 0.7], beats: 16 };
    const back = { pans: [0.7, -0.7], beats: 16 };
    const down = "D D D D F F F F";
    const cycle = `${down} D D D D C C C C`;
    const heart = { rhythm: "x..x............" };
    const twice = { rhythm: "x..x....x..x...." };
    const plain = "K.......S.......";
    const sink = "K.......S.....B.";
    const brush = "K..Z....S..Z....";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine", pulse: "#TYPE sawtooth", ping: "#TYPE sine",
      },
      parts: {
        surface: {
          bars: 8, sub: swell("D1", 32, bpm, [0, 0.25, 0.1, 0.4, 0.2], null, 0.16), near: `${rest(64)}\n${near("D D D D", 0.14, wide)}`,
        },
        descent: {
          bars: 8, sub: sub(down, 0.2, 0.45, slow), near: near(down, 0.2, wide), far: far("F4", 0.08, back),
          pulse: pulse(down, [0, 0.12], heart), drums: ".".repeat(64) + "K...............".repeat(3) + "K...........K.K.",
        },
        deep: {
          bars: 16, sub: sub(cycle, 0.3, 0.6, slow), near: near(cycle, 0.26, wide), far: far("D5 C5 -", 0.1, back),
          pulse: pulse(cycle, [0.12, 0.16, 0.12], heart), drums: (plain.repeat(3) + sink).repeat(4),
        },
        floor: {
          bars: 16, sub: sub(cycle, 0.3, 0.6, slow), growl: growl(cycle, 0.2), near: near(cycle, 0.28, wide),
          far: far("A4 - C5", 0.12, back), pulse: pulse(cycle, [0.16, 0.2, 0.16], twice), drums: (brush.repeat(3) + sink).repeat(4),
        },
        ascent: {
          bars: 8, sub: sub(down, 0.3, 0.5, slow), growl: `${growl("D D D D", 0.12)}\n${rest(64)}`, near: near(down, 0.2, wide),
          pulse: pulse(down, [0.14, 0], heart), drums: plain.repeat(4) + "K...............".repeat(4),
        },
        end: {
          bars: 8, sub: swell("D1", 32, bpm, [0.3, 0.45, 0.15, 0.3, 0.05, 0], null, 0.16),
          near: `${near("D D D D", 0.12, wide)}\n${rest(64)}`, ping: ping("- - A4 -", 0.12),
        },
      },
      form: "surface descent deep floor ascent end",
    });
  },
  pressure: () => {
    const bpm = 104;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { "F#": "F#1", C: "C1" }, lows: { "F#": "F#2", C: "C2" }, tones: { "F#": "C#4", C: "G3" },
    });
    const push = "F# F# F# F# F# F# C C";
    const twice = `${push} ${push}`;
    const fast = (p, high) => growl(p, high, 8, 0.025);
    const plain = "K.......S.......";
    const drive = "K..H..K.S..H..H.";
    const lift = "K..H..K.S.HX..HX";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN -0.4",
      },
      parts: {
        intro: {
          bars: 8, sub: `${swell("F#1", 24, bpm, [0, 0.2, 0.1, 0.35, 0.2, 0.45, 0.3])}\n${swell("C1", 8, bpm, [0.3, 0.5, 0.3])}`,
          far: far("C#5 - C#5", 0.08), drums: ".".repeat(64) + plain.repeat(3) + "K.......S...S.SS",
        },
        build: {
          bars: 8, sub: sub(push, 0.3, 0.5), near: near(push, 0.18),
          growl: `${growl("F# F# F# F#", 0.1, 2)}\n${growl("F# F# C C", 0.18, 4)}`,
          pulse: `${pulse("F# F# F# F#", [0.02, 0.1])}\n${pulse("F# F# C", [0.1, 0.2], { rhythm: "x".repeat(16) })}\n${rest(16)}`,
          drums: plain.repeat(6) + "K...S...K...S..." + "SSSSSSSSSSSS....",
        },
        drop: {
          bars: 16, sub: sub(twice, 0.35, 0.6), near: near(twice, 0.22), growl: `${growl(push, 0.24, 4)}\n${fast(push, 0.26)}`,
          far: far("- - - C#5 - - G4", 0.1), drums: drive.repeat(7) + "K..H..K.S.S.S.SS" + drive.repeat(7) + "K..H..K.S..SSSSS",
        },
        break: {
          bars: 8, sub: sub(push, 0.15, 0.5), ping: ping("C#5 D5 C#5 -", 0.22), echo: echo("C#5 D5 C#5 -", 0.09),
          drums: ".".repeat(64) + "K.......K.......".repeat(3) + "K...K...K.K.K.K.",
        },
        drop2: {
          bars: 16, sub: sub(twice, 0.35, 0.6), near: near(twice, 0.24), growl: fast(twice, 0.28), far: far("C#5 - - - C#5 - D5", 0.11),
          drums: lift.repeat(7) + "K..H..K.S.S.S.SS" + lift.repeat(6) + drive + plain,
        },
        outro: {
          bars: 8, sub: swell("F#1", 32, bpm, [0.35, 0.5, 0.25, 0.4, 0.15, 0.25, 0.05, 0.1, 0]),
          growl: `${growl("F# F# F# F#", 0.14, 2)}\n${rest(64)}`, near: `${near("F# F# F# F#", 0.14)}\n${rest(64)}`,
          drums: plain.repeat(4) + "K...............".repeat(2) + ".".repeat(32),
        },
      },
      form: "intro build drop break drop2 outro",
    });
  },
  silt: () => {
    const bpm = 76;
    const { sub, growl, near, pass, ping, echo } = depth(bpm, {
      roots: { A: "A1", F: "F1", D: "D1", E: "E1" }, lows: { A: "A2", F: "F2", D: "D2", E: "E2" },
      tones: { A: "C4", F: "C4", D: "A3", E: "B3" },
    });
    const sink = "A A F F D D E E";
    const twice = `${sink} ${sink}`;
    const cross = (notes, high) => pass(notes, high, [0, 1, 0], { pans: [0.6, -0.6] });
    const over = "E4 A4 F4 G4";
    const rim = "K.......X.......";
    const sand = "K.....Z.X.....Z.";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", hum: "#TYPE triangle", near: "#TYPE triangle", cross: "#TYPE sine",
        ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN -0.4",
      },
      parts: {
        intro: { bars: 8, sub: sub(sink, 0.15, 0.4), near: `${rest(64)}\n${near("D D E E", 0.14)}` },
        settle: {
          bars: 16, sub: sub(twice, 0.25, 0.5), hum: growl(twice, 0.14), near: near(twice, 0.24), cross: cross(`${over} ${over}`, 0.1),
          ping: ping("- - E5 - - - C5 -", 0.14), echo: echo("- - E5 - - - C5 -", 0.06),
          drums: rim.repeat(7) + "K.......X...X.X." + rim.repeat(7) + "K.......X...XXX.",
        },
        still: { bars: 8, sub: sub(sink, 0.2, 0.45), cross: cross(over, 0.12), ping: ping("E5 - C5 -", 0.16), echo: echo("E5 - C5 -", 0.07) },
        settle2: {
          bars: 16, sub: sub(twice, 0.3, 0.55), hum: growl(twice, 0.18), near: near(twice, 0.26), cross: cross(`${over} ${over}`, 0.11),
          ping: ping("- - - - E5 - - -", 0.12), echo: echo("- - - - E5 - - -", 0.05),
          drums: sand.repeat(7) + "K.....Z.X...X.X." + sand.repeat(7) + "K.....Z.X.......",
        },
        outro: {
          bars: 8, sub: `${sub("A A F F", 0.15, 0.4)}\n${swell("A1", 16, bpm, [0.15, 0.3, 0.1, 0.15, 0])}`,
          near: `${near("A A F F", 0.16)}\n${rest(64)}`, cross: cross("- E4 - -", 0.08), drums: rim.repeat(2) + ".".repeat(96),
        },
      },
      form: "intro settle still settle2 outro",
    });
  },
  ballast: () => {
    const bpm = 100;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { C: "C1", Eb: "Eb1", F: "F1" }, lows: { C: "C2", Eb: "Eb2", F: "F2" }, tones: { C: "G3", Eb: "G3", F: "C4" },
    });
    const load = "C C C C Eb Eb F F";
    const twice = `${load} ${load}`;
    const tilt = { rhythm: "x..x..x.x..x..x." };
    const keel = "K..K....S......Z";
    const heave = "K..K..H.S..H..HZ";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN 0.4",
      },
      parts: {
        intro: { bars: 8, pulse: pulse(load, [0, 0.12, 0.08, 0.16], tilt) },
        weight: {
          bars: 8, pulse: pulse(load, [0.16, 0.08, 0.18, 0.1], tilt), sub: sub(load, 0.2, 0.45),
          drums: "K..K............".repeat(7) + "K..K....K..K.KK.",
        },
        hull: {
          bars: 16, pulse: pulse(twice, [0.1, 0.2, 0.1, 0.2, 0.1], tilt), sub: sub(twice, 0.3, 0.55), near: near(twice, 0.22),
          drums: (keel.repeat(3) + "K..K....S..S.SSZ").repeat(4),
        },
        cargo: {
          bars: 16, pulse: pulse(twice, [0.1, 0.2, 0.1, 0.2, 0.1], tilt), sub: sub(twice, 0.3, 0.55), near: near(twice, 0.24),
          growl: growl(twice, 0.2, 2), far: far("Eb4 - G4 - Eb4 - F4", 0.1), drums: (heave.repeat(3) + "K..K..H.S..S.SSZ").repeat(4),
        },
        cut: {
          bars: 4, sub: swell("C1", 16, bpm, [0.3, 0.6, 0.2, 0.45, 0.25]), ping: ping("G4 -", 0.22), echo: echo("G4 -", 0.09),
          drums: ".".repeat(48) + "K..K....K..K.KK.",
        },
        cargo2: {
          bars: 16, pulse: pulse(twice, [0.14, 0.22, 0.12, 0.22, 0.14], tilt), sub: sub(twice, 0.3, 0.6), near: near(twice, 0.26),
          growl: growl(twice, 0.24, 2), far: far("G4 - Eb4 - G4 - C5", 0.11), drums: (heave.repeat(3) + "K..K..H.S..S.SSZ").repeat(4),
        },
        dock: {
          bars: 8, pulse: pulse("C C C C C C C C", [0.16, 0.1, 0.12, 0], tilt),
          sub: swell("C1", 32, bpm, [0.3, 0.4, 0.15, 0.3, 0.1, 0.15, 0.05, 0, 0]),
          drums: keel.repeat(4) + "K..K............".repeat(2) + ".".repeat(32),
        },
      },
      form: "intro weight hull cargo cut cargo2 dock",
    });
  },
  nightferry: () => {
    const bpm = 92;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { G: "G1", D: "D1", C: "C1" }, lows: { G: "G2", D: "D2", C: "C2" }, tones: { G: "Bb3", D: "A3", C: "G3" },
    });
    const route = "G G D D C C D D";
    const twice = `${route} ${route}`;
    const thrice = `${twice} ${route}`;
    const roll = (p, low, high) => sub(p, low, high, { per: 2 });
    const chug = { rhythm: "xx..xx..xx..xx.." };
    const horn = (high) => swell("D3", 16, bpm, [0, 1, 0.85, 1, 0.6, 0].map((level) => level * high), [0.9, 0.5, 0.1, -0.3, -0.6, -0.8]);
    const deck = "K.......S...H...";
    const wake = "K.Z...Z.S.Z...Z.";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", horn: "#TYPE sawtooth", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN -0.4",
      },
      parts: {
        departure: { bars: 8, pulse: pulse(route, [0, 0.1], chug), horn: `${horn(0.07)}\n${rest(64)}`, sub: roll(route, 0.05, 0.2) },
        cruise: {
          bars: 16, sub: roll(twice, 0.25, 0.5), near: near(twice, 0.24), pulse: pulse(twice, [0.1, 0.14, 0.1], chug),
          horn: `${rest(128)}\n${horn(0.06)}\n${rest(64)}`, drums: (deck.repeat(7) + "K.......S...H.HH").repeat(2),
        },
        open: {
          bars: 8, sub: swell("G1", 32, bpm, [0.25, 0.5, 0.2, 0.45, 0.15, 0.4, 0.2, 0.35, 0.3]), horn: `${horn(0.08)}\n${rest(64)}`,
          far: far("Bb4 - A4", 0.1), ping: ping("- - D5 -", 0.18), echo: echo("- - D5 -", 0.07),
          drums: ".".repeat(96) + "K.......S.......K.......S...S.SS",
        },
        cruise2: {
          bars: 24, sub: roll(thrice, 0.3, 0.6), growl: growl(thrice, 0.18), near: near(thrice, 0.26),
          far: far("- D5 - C5 - Bb4 - A4 - D5 -", 0.11), pulse: pulse(thrice, [0.12, 0.18, 0.14], chug),
          horn: `${rest(128)}\n${horn(0.06)}\n${rest(128)}\n${horn(0.05)}`, drums: (wake.repeat(7) + "K.Z...Z.S.Z.S.SS").repeat(3),
        },
        harbor: {
          bars: 8, pulse: pulse(route, [0.14, 0], chug), horn: `${rest(64)}\n${horn(0.06)}`,
          sub: `${roll("G G D D", 0.15, 0.4)}\n${swell("G1", 16, bpm, [0.15, 0.3, 0.1, 0.2, 0.05, 0.1, 0, 0.03, 0], null, 0.04)}`,
          near: `${near("G G D D", 0.16)}\n${rest(64)}`, drums: deck.repeat(4) + ".".repeat(64),
        },
      },
      form: "departure cruise open cruise2 harbor",
    });
  },
  landslide: () => {
    const bpm = 108;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { B: "B1", A: "A1" }, lows: { B: "B2", A: "A2" }, tones: { B: "D4", A: "C4" },
    });
    const slab = "B B B B A A A A";
    const twice = `${slab} ${slab}`;
    const stone = "K.......S.......";
    const stride = "K..K....S.......";
    const rumble = "K..K....S..T..T.";
    const quarry = "K.HK..H.S.HK..H.";
    const avalanche = "K.HK.TH.S.HK.TBB";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN 0.4",
      },
      parts: {
        intro: {
          bars: 8, sub: sub(slab, 0.15, 0.45), far: far("F#4 - E4", 0.08),
          drums: ".".repeat(64) + "K...............".repeat(3) + "K.......K...K.K.",
        },
        one: { bars: 8, sub: sub(slab, 0.3, 0.55), growl: growl(slab, 0.16), near: near(slab, 0.2), drums: stone.repeat(7) + "K.......S...S.SS" },
        two: { bars: 8, sub: sub(slab, 0.3, 0.55), growl: growl(slab, 0.2, 2), near: near(slab, 0.22), drums: stride.repeat(7) + "K..K....S...S.SS" },
        three: {
          bars: 8, sub: sub(slab, 0.3, 0.55), growl: growl(slab, 0.22, 3), near: near(slab, 0.24), far: far("F#4 - D5", 0.1),
          drums: rumble.repeat(7) + "K..K....S.TTTBBB",
        },
        four: {
          bars: 16, sub: sub(twice, 0.35, 0.6), growl: growl(twice, 0.24, 4, 0.025), near: near(twice, 0.26),
          far: far("D5 - C5 - D5 - E5", 0.1), pulse: pulse(twice, [0.12, 0.18]), drums: quarry.repeat(15) + "K.HK..H.S.SSTTBB",
        },
        six: {
          bars: 16, sub: sub(twice, 0.35, 0.6), growl: growl(twice, 0.28, 6, 0.025), near: near(twice, 0.28),
          far: far("F#4 - E4 - F#4 - D5", 0.11), pulse: pulse(twice, [0.18, 0.2]), drums: avalanche.repeat(15) + "K.HK.TH.S.SSTTBB",
        },
        stop: {
          bars: 8, sub: swell("B1", 32, bpm, [0.35, 0.6, 0.3, 0.5, 0.2, 0.3, 0.1, 0.05, 0]), ping: ping("B4 - - -", 0.2),
          echo: echo("B4 - - -", 0.08), drums: "K" + ".".repeat(127),
        },
      },
      form: "intro one two three four six stop",
    });
  },
  sonar: () => {
    const bpm = 84;
    const { sub, growl, near, far, ping, echo } = depth(bpm, {
      roots: { Eb: "Eb1", Gb: "Gb1", Db: "Db1" }, lows: { Eb: "Eb2", Gb: "Gb2", Db: "Db2" }, tones: { Eb: "Bb3", Gb: "Bb3", Db: "Ab3" },
    });
    const dive = "Eb Eb Gb Gb Eb Eb Db Db";
    const twice = `${dive} ${dive}`;
    const sound = "Eb5 Eb5 Eb5 -";
    const plain = "K.......S.......";
    const deeper = "K...Z...S...Z...";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine", ping: "#TYPE sine", echo: "#TYPE sine\n#PAN 0.5",
      },
      parts: {
        surface: { bars: 8, ping: ping(sound, 0.24), echo: echo(sound, 0.1), far: far("Bb4 - Bb4", 0.08), sub: sub(dive, 0.05, 0.2) },
        descent: {
          bars: 8, ping: ping(sound, 0.18), echo: echo(sound, 0.07), sub: sub(dive, 0.2, 0.45), near: near(dive, 0.2),
          drums: "K...............".repeat(7) + "K.......K...K.K.",
        },
        deep: {
          bars: 16, ping: ping(`${sound} ${sound}`, 0.13), echo: echo(`${sound} ${sound}`, 0.05), sub: sub(twice, 0.3, 0.55),
          growl: growl(twice, 0.2, 0.5), near: near(twice, 0.24), drums: plain.repeat(7) + "K.......S...Z.Z." + plain.repeat(7) + "K.......S...S.SS",
        },
        abyss: {
          bars: 16, ping: ping("Eb5 - - - Eb5 - - -", 0.08), echo: echo("Eb5 - - - Eb5 - - -", 0.04), sub: sub(twice, 0.35, 0.6),
          growl: growl(twice, 0.28, 0.5), near: near(twice, 0.26), far: far("- Gb4 - - - Gb4 -", 0.1),
          drums: (deeper.repeat(7) + "K...Z...S...ZZZZ").repeat(2),
        },
        floor: {
          bars: 8, sub: swell("Eb1", 32, bpm, [0.35, 0.6, 0.3, 0.45, 0.15, 0.25, 0.05, 0, 0]),
          growl: swell("Eb2", 32, bpm, [0, 0.22, 0, 0.12, 0, 0.04, 0, 0, 0], null, 0.16), ping: ping("- - - Eb5", 0.06),
          drums: plain.repeat(2) + "K...............".repeat(2) + ".".repeat(64),
        },
      },
      form: "surface descent deep abyss floor",
    });
  },
  tremor: () => {
    const bpm = 112;
    const tones = { "G#": "D#4", "F#": "C#4", E: "B3", "D#": "A#3" };
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { "G#": "G#1", "F#": "F#1", E: "E1", "D#": "D#1" }, lows: { "G#": "G#2", "F#": "F#2", E: "E2", "D#": "D#2" }, tones,
    });
    const walk = "G# G# F# F# E E D# D#";
    const twice = `${walk} ${walk}`;
    const thrice = `${twice} ${walk}`;
    const shudder = (p, high) => growl(p, high, 12, 0.03);
    const skank = { rhythm: "..x...x...x...x.", notes: tones };
    const plain = "K.......S.......";
    const rumble = "K..H..K.S..H....";
    const quake = "K.HH..K.S.HZ..H.";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE triangle\n#PAN 0.2", ping: "#TYPE sine", echo: "#TYPE triangle\n#PAN 0.4",
      },
      parts: {
        intro: {
          bars: 8, sub: sub(walk, 0.15, 0.45), near: `${rest(64)}\n${near("E E D# D#", 0.16)}`,
          drums: ".".repeat(64) + plain.repeat(3) + "K.......S...TTBB",
        },
        quake: {
          bars: 16, sub: sub(twice, 0.3, 0.55), growl: shudder(twice, 0.2), near: near(twice, 0.24),
          pulse: pulse(twice, [0.1, 0.14, 0.1], skank), drums: (rumble.repeat(3) + "K..H..K.S..TTBB.").repeat(4),
        },
        lull: {
          bars: 8, sub: sub(walk, 0.2, 0.5), far: far("D#5 - B4", 0.1), ping: ping("D#5 - B4 -", 0.18), echo: echo("D#5 - B4 -", 0.07),
          drums: plain.repeat(4) + "K...............".repeat(3) + "K.......K.K.K.K.",
        },
        build: {
          bars: 8, sub: sub(walk, 0.3, 0.55), growl: `${growl("G# G# F# F#", 0.12, 6, 0.03)}\n${growl("E E D# D#", 0.2, 6, 0.03)}`,
          near: near(walk, 0.24), pulse: pulse(walk, [0, 0.16], skank), drums: rumble.repeat(6) + "K..H..K.S.S.S.SS" + "S.S.S.SSSSSSSS..",
        },
        quake2: {
          bars: 24, sub: sub(thrice, 0.35, 0.6), growl: shudder(thrice, 0.26), near: near(thrice, 0.28),
          far: far("- D#5 - C#5 - B4 - A#4 - D#5 -", 0.11), pulse: pulse(thrice, [0.14, 0.2, 0.14], skank),
          drums: (quake.repeat(7) + "K.HH..K.S..TTBB.").repeat(3),
        },
        after: {
          bars: 8, sub: swell("G#1", 32, bpm, [0.35, 0.5, 0.2, 0.4, 0.1, 0.2, 0.05, 0, 0]),
          growl: `${shudder("G# G#", 0.12)}\n${rest(96)}`, near: `${near("G# G# F# F#", 0.14)}\n${rest(64)}`,
          drums: rumble.repeat(2) + plain.repeat(2) + ".".repeat(64),
        },
      },
      form: "intro quake lull build quake2 after",
    });
  },
  blackice: () => {
    const bpm = 80;
    const { sub, growl, near, far, pulse, ping, echo } = depth(bpm, {
      roots: { F: "F1", Bb: "Bb1", Gb: "Gb1" }, lows: { F: "F2", Bb: "Bb2", Gb: "Gb2" }, tones: { F: "C4", Bb: "Db4", Gb: "Db4" },
    });
    const cold = "F F Bb Bb F F Gb Gb";
    const twice = `${cold} ${cold}`;
    const sleet = "K.HZ.HZ.S.HZ.HZ.";
    const shuffle = "K..Z..Z.S..Z..Z.";
    const plain = "K.......S.......";
    return song({
      bpm,
      voices: {
        sub: "#TYPE sine", growl: "#TYPE sawtooth", near: "#TYPE triangle", far: "#TYPE sine",
        pulse: "#TYPE sawtooth", ping: "#TYPE triangle", echo: "#TYPE sine\n#PAN -0.5",
      },
      parts: {
        open: {
          bars: 16, sub: sub(twice, 0.3, 0.6), growl: growl(twice, 0.24, 4, 0.025), near: near(twice, 0.28),
          far: far("C5 - Db5 - C5 - Db5", 0.1), pulse: pulse(twice, [0.16, 0.18, 0.16]),
          drums: (sleet.repeat(7) + "K.HZ.HZ.S.S.S.SS").repeat(2),
        },
        thaw: {
          bars: 16, sub: sub(twice, 0.3, 0.55), growl: growl(twice, 0.2, 2), near: near(twice, 0.26), far: far("- Db5 - - - C5 -", 0.09),
          pulse: pulse(twice, [0.14, 0.08], { rhythm: "x...x...x...x..." }), drums: (shuffle.repeat(7) + "K..Z..Z.S..Z.ZZ.").repeat(2),
        },
        thin: {
          bars: 16, sub: sub(twice, 0.25, 0.5), growl: growl(twice, 0.14), near: near(twice, 0.22),
          ping: ping("C5 - Db5 - C5 - - -", 0.18), echo: echo("C5 - Db5 - C5 - - -", 0.07), drums: plain.repeat(15) + "K.......S...S...",
        },
        frost: {
          bars: 8, sub: sub(cold, 0.2, 0.4), ping: ping("Db5 - C5 -", 0.16), echo: echo("Db5 - C5 -", 0.06),
          drums: "K...............".repeat(4) + ".".repeat(64),
        },
        gone: { bars: 4, sub: swell("F1", 16, bpm, [0.2, 0.35, 0.1, 0.15, 0]), near: `${near("F F", 0.1)}\n${rest(32)}`, ping: ping("- C5", 0.08) },
      },
      form: "open thaw thin frost gone",
    });
  },
  apogee: () => {
    const bpm = 100;
    const { melody, guitar, coda, record } = space(bpm, { EG: ["E4", "G4"], DF: ["D4", "F#4"], BD: ["B3", "D4"], CE: ["C4", "E4"], FA: ["F#4", "A4"] });
    const verse = "EG DF BD CE EG FA BD CE";
    const pickup = "-:dh E4:e F#4:e";
    const theme = "G4:dh E4:e F#4:e A4:dh F#4:e G4:e B4:w A4:q G4:q -:q E4:e F#4:e G4:q B4:q D5:h E5:dh D5:q B4:h A4:h G4:h -:q E4:e F#4:e";
    const orbit = "-:h B4:q D5:q F#5:dh E5:q D5:w -:q D5:q E5:q F#5:q G5:dh F#5:e E5:e F#5:h E5:h D5:dh B4:q B4:h -:q E4:e F#4:e";
    const hats = "O.......O.......";
    const ghost = "O.......O.....Z.";
    const ride = "O...Z...O...Z...";
    const parts = {
      void: {
        bars: 8, hum: [0, 0.5, 0.8, 0.9], ...guitar(verse, "strum", [0.45, 0.7, 0.8]),
        ...melody(`-:w -:w -:w ${pickup} -:w -:w -:w ${pickup}`, [0.55, 0.65]), drums: ".".repeat(64) + "Z.......Z.......".repeat(4),
      },
      seed: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "strum", [0.8, 0.85, 0.8]),
        ...melody(`G4:w -:w -:w ${pickup} G4:h A4:h B4:w -:w ${pickup}`, [0.6, 0.75, 0.85, 0.75, 0.6]), drums: hats.repeat(8),
      },
      theme: {
        bars: 8, hum: [1, 1], ...guitar(verse, "strum", [0.85, 0.9, 0.8, 0.85]),
        ...melody(theme, [0.6, 0.85, 0.95, 0.7, 0.75, 1, 0.8, 0.55]), drums: (hats + ghost).repeat(4),
      },
      orbit: {
        bars: 8, hum: [1, 0.9], ...guitar(verse, "roll", [0.75, 0.9, 0.85, 0.7]),
        ...melody(orbit, [0.7, 0.9, 1, 0.85, 0.95, 1, 0.8, 0.6]), drums: (ride + ride + ride + ghost).repeat(2),
      },
      drift: {
        bars: 8, hum: [0.9, 0.4, 0.3, 0.6], ...guitar(verse, "swell", [0.6, 0.8, 0.7]),
        ...melody(`-:w -:dh E5:e F#5:e G5:w -:w -:w ${pickup} G4:w ${pickup}`, [0.5, 0.6, 0.55, 0.5]),
        drums: ".".repeat(96) + "Z.......Z.......".repeat(2),
      },
      return: {
        bars: 8, hum: [0.7, 1], ...guitar(verse, "strum", [0.9, 1, 0.85, 0.9]),
        ...melody(theme, [0.7, 0.9, 1, 0.8, 0.85, 1.05, 0.85, 0.6]), drums: (hats + ghost).repeat(4),
      },
      orbit2: {
        bars: 8, hum: [1, 1], ...guitar(verse, "roll", [0.85, 1, 0.95, 0.8]),
        ...melody(orbit, [0.8, 1, 1.05, 0.9, 1, 1.05, 0.85, 0.65]), drums: (ride + ride + ride + ghost).repeat(2),
      },
      outro: {
        bars: 8, hum: [1, 0.6], ...guitar(verse, "strum", [0.85, 0.7, 0.5]),
        ...melody(`G4:dh E4:e F#4:e A4:w B4:w -:w -:w ${pickup} G4:w ${pickup}`, [0.75, 0.65, 0.55, 0.45, 0.4]),
        drums: hats.repeat(6) + "Z.......Z.......".repeat(2),
      },
      end: coda("EG", "G4"),
    };
    return record({ root: "E2", parts, form: "void seed theme orbit drift return orbit2 outro end" });
  },
  perigee: () => {
    const bpm = 92;
    const { melody, guitar, coda, record } = space(bpm, {
      D: ["F#3", "D4"], A: ["E3", "C#4"], Bm: ["D3", "B3"], G: ["B3", "G4"], D6: ["A3", "F#4"], Em: ["G3", "E4"],
    });
    const verse = "D A Bm G D A Em A";
    const lift = "G D6 Bm Em G D6 Em A";
    const pickup = "-:dh D4:e E4:e";
    const theme = "F#4:h E4:q D4:q | E4:dh -:e A3:e | D4:q E4:q F#4:q A4:q | B4:h A4:h | G4:dh F#4:e E4:e | F#4:w | E4:q D4:q B3:q D4:q | E4:dh -:e A4:e";
    const high = "D5:dh B4:q | A4:h F#4:h | F#4:dh E4:e D4:e | E4:w | D5:q B4:q D5:q E5:q | F#5:h D5:h | E5:q D5:q B4:q A4:q | A4:w";
    const parts = {
      intro: {
        bars: 8, hum: [0, 0.6, 0.9], ...guitar(verse, "strum", [0.4, 0.7, 0.8]),
        ...melody(`-:w -:w -:w ${pickup} F#4:w -:w -:w ${pickup}`, [0.5, 0.6]), drums: ".".repeat(64) + "........Z.......".repeat(4),
      },
      verse: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "pedal", [0.75, 0.85, 0.8]),
        ...melody(theme, [0.6, 0.8, 0.9, 0.75, 0.7, 0.9, 0.75, 0.6]), drums: "....Z.......Z...".repeat(8),
      },
      lift: {
        bars: 8, hum: [1, 1], ...guitar(lift, "rock", [0.8, 0.95, 0.85]),
        ...melody(high, [0.7, 0.9, 0.8, 0.85, 1, 0.95, 0.8]), drums: "O.....Z.O.....Z.".repeat(8),
      },
      still: {
        bars: 8, hum: [0.9, 0.5, 0.4, 0.7], ...guitar("Bm Bm G G Em Em A A", "swell", [0.6, 0.75, 0.65]),
        ...melody(`-:w F#5:w -:w D5:w -:w B4:w -:w ${pickup}`, [0.45, 0.6, 0.5]), drums: ".".repeat(96) + "Z.......Z.......".repeat(2),
      },
      verse2: {
        bars: 8, hum: [0.8, 1], ...guitar(verse, "pedal", [0.85, 0.95, 0.9]),
        ...melody(theme, [0.7, 0.9, 1, 0.85, 0.8, 1, 0.85, 0.7]), drums: "O...Z.......Z...".repeat(8),
      },
      lift2: {
        bars: 8, hum: [1, 1], ...guitar(lift, "rock", [0.85, 1, 0.9]),
        ...melody(high, [0.8, 1, 0.9, 0.95, 1.05, 1, 0.85]), drums: "O.....Z.O.....Z.".repeat(7) + "O.....Z.O...Z.Z.",
      },
      outro: {
        bars: 8, hum: [1, 0.6], ...guitar(verse, "strum", [0.85, 0.65, 0.5]),
        ...melody("F#4:w | E4:w | D4:w | -:w | D4:dh E4:q | F#4:w | E4:w | -:w", [0.7, 0.55, 0.4]),
        drums: "....Z.......Z...".repeat(6) + ".".repeat(32),
      },
      end: coda("D", "F#4"),
    };
    return record({ root: "D2", echo: 1, parts, form: "intro verse lift still verse2 lift2 outro end" });
  },
  aphelion: () => {
    const bpm = 66;
    const { melody, guitar, coda, record } = space(bpm, {
      Fm: ["F#3", "C#4"], D: ["D3", "A3"], A: ["A3", "E4"], E: ["E3", "B3"], Bm: ["B3", "F#4"], Cs: ["C#4", "G#4"],
    });
    const near = "Fm Fm D D A A E E";
    const far = "Bm Bm D D Fm Fm Cs Cs";
    const bow = { attack: 0.5, decay: 14, edge: 0 };
    const theme = "C#5:w | B4:h A4:h | A4:dh F#4:q | E4:w | E4:h C#5:h | B4:w | B4:dh G#4:q | F#4:w";
    const distant = "F#5:w | D5:h B4:h | A4:w | F#4:h A4:h | C#5:dh B4:q | A4:w | G#4:w | -:w";
    const parts = {
      void: { bars: 8, hum: [0, 0.6, 1], ...guitar(near, "swell", [0.3, 0.6, 0.7]) },
      near: {
        bars: 8, hum: [1, 0.9], ...guitar(near, "swell", [0.65, 0.75, 0.7]),
        ...melody(theme, [0.5, 0.7, 0.8, 0.65, 0.7, 0.8, 0.7, 0.55], bow),
      },
      far: {
        bars: 8, hum: [0.9, 0.7, 1], ...guitar(far, "toll", [0.6, 0.8, 0.75]),
        ...melody(distant, [0.6, 0.8, 0.7, 0.65, 0.85, 0.7, 0.6], bow), drums: "Z...............".repeat(8),
      },
      near2: {
        bars: 8, hum: [1, 1], ...guitar(near, "toll", [0.75, 0.9, 0.8]),
        ...melody(theme, [0.6, 0.8, 0.9, 0.75, 0.8, 0.9, 0.75, 0.6], { attack: 0.06, decay: 9 }), drums: "O.......Z.......".repeat(8),
      },
      drift: {
        bars: 8, hum: [1, 0.5, 0.7], ...guitar(far, "swell", [0.7, 0.55, 0.5]),
        ...melody("-:w | -:w | C#5:w | -:w | -:w | A4:w | -:w | G#4:w", [0.5, 0.45], bow), drums: ".".repeat(112) + "Z.......".repeat(2),
      },
      end: coda("Fm", "C#5", "swell", bow),
    };
    return record({ root: "F#2", echo: 1.5, buzz: 0.012, parts, form: "void near far near2 drift end" });
  },
  zenith: () => {
    const bpm = 112;
    const chords = { A: ["E4", "A4"], B: ["F#4", "B4"], Fm: ["C#4", "F#4"], E: ["B3", "E4"], D: ["A3", "D4"], Cm: ["G#3", "C#4"] };
    const { melody, guitar, bell, coda, record } = space(bpm, chords);
    const verse = "A B Fm E A B D E";
    const peak = "D E Cm Fm D E B B";
    const chime = (progression) => progression.split(" ").map((chord) => `${transpose(chords[chord][1], 12)}:dq ${transpose(chords[chord][0], 12)}:dq -:q`).join(" ");
    const pickup = "-:h C#5:q D#5:q";
    const rise = "E5:dq C#5:dq B4:q | D#5:h C#5:q B4:q | C#5:dh A4:q | B4:w | E5:dq F#5:dq E5:q | D#5:dh F#5:q | F#5:q E5:q D5:q A4:q | B4:h C#5:q D#5:q";
    const summit = "F#5:dh E5:q | E5:dq B4:dq G#4:q | C#5:h E5:h | F#5:w | F#5:dq D5:dq A4:q | E5:dh B4:q | D#5:h F#5:h | D#5:h C#5:q D#5:q";
    const ride = "O.......Z.......";
    const parts = {
      intro: {
        bars: 8, hum: [0, 0.7, 0.9], ...guitar(verse, "strum", [0.5, 0.7, 0.75]),
        ...melody(`-:w -:w -:w -:w -:w -:w -:w ${pickup}`, [0.6, 0.6]), drums: ".".repeat(64) + "O...............".repeat(4),
      },
      verse: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "rock", [0.75, 0.85, 0.8]),
        ...melody(rise, [0.65, 0.8, 0.85, 0.7, 0.85, 0.95, 0.85, 0.7]), drums: ride.repeat(8),
      },
      summit: {
        bars: 8, hum: [1, 1], ...guitar(peak, "strum", [0.8, 0.95, 0.9]), ...bell(chime(peak), [0.7, 1, 0.9]),
        ...melody(summit, [0.8, 0.95, 0.9, 1, 0.95, 1, 0.9, 0.75]), drums: (ride + ride + ride + "O.......Z...Z...").repeat(2),
      },
      air: {
        bars: 8, hum: [0.9, 0.5, 0.8], ...guitar(verse, "swell", [0.6, 0.75, 0.7]), ...bell(chime(verse), [0.5, 0.8, 0.6]),
        ...melody(`-:w | E5:w | -:w | B4:w | -:w | F#5:w | -:w | ${pickup}`, [0.5, 0.6, 0.55]), drums: ".".repeat(96) + "O.......".repeat(4),
      },
      verse2: {
        bars: 8, hum: [1, 1], ...guitar(verse, "rock", [0.85, 0.95, 0.9]),
        ...melody(rise, [0.75, 0.9, 0.95, 0.8, 0.95, 1.05, 0.95, 0.8]), drums: ride.repeat(8),
      },
      summit2: {
        bars: 8, hum: [1, 1], ...guitar(peak, "strum", [0.85, 1, 0.95]), ...bell(chime(peak), [0.8, 1, 0.9]),
        ...melody(summit, [0.85, 1, 0.95, 1.05, 1, 1.05, 0.95, 0.8]), drums: (ride + ride + ride + "O.......Z...Z...").repeat(2),
      },
      outro: {
        bars: 8, hum: [1, 0.6], ...guitar(verse, "strum", [0.8, 0.6, 0.45]), ...bell(chime(verse), [0.6, 0.4, 0.25]),
        ...melody("E5:w | D#5:w | C#5:w | B4:w | A4:w | -:w | -:w | -:w", [0.7, 0.5, 0.35]), drums: ride.repeat(4) + ".".repeat(64),
      },
      end: coda("A", "C#5"),
    };
    return record({ root: "A2", echo: 0.5, parts, form: "intro verse summit air verse2 summit2 outro end" });
  },
  parallax: () => {
    const bpm = 96;
    const { melody, guitar, coda, record } = space(bpm, {
      Bm: ["D4", "F#4"], E: ["G#3", "B3"], D: ["F#3", "A3"], A: ["C#4", "E4"], G: ["B3", "D4"], Fm: ["A3", "C#4"],
    });
    const verse = "Bm Bm E E D D A A";
    const shift = "G G A A Bm Bm Fm Fm";
    const theme = "F#4:dq B4:dq D5:q | C#5:dq B4:dq F#4:q | E4:dq G#4:dq B4:q | C#5:dh B4:q | A4:dq F#4:dq D5:q | C#5:dq A4:dq F#4:q | E4:dq A4:dq C#5:q | B4:dq A4:dq E4:q";
    const wide = "D5:w | B4:h G4:h | C#5:w | E5:h C#5:h | D5:w | F#5:h D5:h | C#5:w | A4:w";
    const cross = "O.....Z.....Z...";
    const parts = {
      intro: { bars: 8, hum: [0, 0.6, 0.9], ...guitar(verse, "strum", [0.45, 0.7, 0.8]), drums: ".".repeat(64) + "Z.....Z.....Z...".repeat(4) },
      verse: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "cross", [0.7, 0.85, 0.8]),
        ...melody(theme, [0.6, 0.75, 0.85, 0.8, 0.7, 0.85, 0.8, 0.65]), drums: cross.repeat(8),
      },
      shift: {
        bars: 8, hum: [1, 0.8], ...guitar(shift, "toll", [0.75, 0.9, 0.8]),
        ...melody(wide, [0.65, 0.8, 0.9, 0.8, 0.85, 0.95, 0.8, 0.6]), drums: "O.......Z.......".repeat(8),
      },
      verse2: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "cross", [0.8, 0.95, 0.9]),
        ...melody(theme, [0.7, 0.85, 0.95, 0.9, 0.8, 0.95, 0.9, 0.75]), drums: cross.repeat(8),
      },
      shift2: {
        bars: 8, hum: [1, 0.5, 0.8], ...guitar(shift, "swell", [0.65, 0.8, 0.7]),
        ...melody(wide, [0.55, 0.7, 0.75, 0.65, 0.7, 0.8, 0.7, 0.5], { attack: 0.35, decay: 12, edge: 0 }), drums: ".".repeat(96) + cross.repeat(2),
      },
      outro: {
        bars: 8, hum: [1, 0.6], ...guitar(verse, "cross", [0.8, 0.6, 0.45]),
        ...melody("F#4:dq B4:dq D5:q | C#5:w | E4:dq G#4:dq B4:q | B4:w | A4:dq F#4:dq D5:q | C#5:w | -:w | -:w", [0.7, 0.5, 0.35]),
        drums: cross.repeat(6) + ".".repeat(32),
      },
      end: coda("Bm", "F#4"),
    };
    return record({ root: "B1", echo: 2, parts, form: "intro verse shift verse2 shift2 outro end" });
  },
  albedo: () => {
    const bpm = 84;
    const chords = { C: ["C3", "E4"], D: ["D3", "F#4"], Em: ["E3", "G4"], Am: ["A2", "C4"], G: ["G2", "B3"] };
    const { melody, guitar, bell, coda, record } = space(bpm, chords);
    const verse = "C D C D Am G C D";
    const glare = "Em D C G Am G D D";
    const glints = (progression) => progression.split(" ").map((chord) => `-:q ${transpose(chords[chord][1], 12)}:dq ${transpose(chords[chord][0], 24)}:dq`).join(" ");
    const theme = "G4:dh E4:q | F#4:w | G4:h B4:h | A4:w | C5:dh B4:q | D5:w | E5:dh D5:q | F#5:w";
    const glow = "B4:w | A4:h F#4:h | G4:w | D5:h B4:h | C5:w | B4:h G4:h | A4:w | -:w";
    const parts = {
      intro: {
        bars: 8, hum: [0, 0.6, 0.9], ...guitar(verse, "swell", [0.4, 0.65, 0.7]), ...bell(glints(verse), [0.3, 0.7, 0.8]),
        drums: ".".repeat(96) + "O...............".repeat(2),
      },
      verse: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "strum", [0.75, 0.85, 0.8]),
        ...melody(theme, [0.55, 0.65, 0.7, 0.75, 0.8, 0.85, 0.9, 0.8]), drums: ("O" + ".".repeat(31)).repeat(4),
      },
      glare: {
        bars: 8, hum: [1, 1], ...guitar(glare, "pedal", [0.8, 0.9, 0.8]), ...bell(glints(glare), [0.7, 0.9, 0.7]),
        ...melody(glow, [0.75, 0.85, 0.8, 0.9, 0.85, 0.8, 0.7]), drums: "O.......Z.......".repeat(8),
      },
      verse2: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "strum", [0.85, 0.95, 0.9]), ...bell(glints(verse), [0.5, 0.7]),
        ...melody(theme, [0.65, 0.75, 0.8, 0.85, 0.9, 0.95, 1, 0.9]), drums: ("O" + ".".repeat(15)).repeat(8),
      },
      glare2: {
        bars: 8, hum: [1, 1], ...guitar(glare, "pedal", [0.85, 1, 0.85]), ...bell(glints(glare), [0.8, 1, 0.8]),
        ...melody(glow, [0.8, 0.9, 0.85, 0.95, 0.9, 0.85, 0.75]), drums: "O.......Z.......".repeat(7) + "O.......Z...Z...",
      },
      outro: {
        bars: 8, hum: [1, 0.5], ...guitar(verse, "swell", [0.75, 0.55, 0.4]), ...bell(glints(verse), [0.8, 0.5, 0.3]),
        ...melody("G4:w | F#4:w | -:w | A4:w | -:w | D5:w | -:w | -:w", [0.6, 0.45]), drums: ("O" + ".".repeat(31)).repeat(2) + ".".repeat(64),
      },
      end: coda("C", "E4", "swell"),
    };
    return record({ root: "C2", parts, form: "intro verse glare verse2 glare2 outro end" });
  },
  umbra: () => {
    const bpm = 74;
    const { melody, guitar, coda, record } = space(bpm, {
      Cm: ["E3", "G#3"], D: ["F#3", "A3"], A: ["C#3", "E3"], B: ["D#3", "F#3"], Fm: ["A3", "C#4"],
    });
    const verse = "Cm Cm D Cm A B Cm Cm";
    const deep = "A A B B Fm Fm D D";
    const dark = { level: 0.3, attack: 0.06, decay: 5, edge: 0.07 };
    const shadow = "G#3:dh B3:q | C#4:dh B3:q | D4:dh A3:q | G#3:w | A3:q C#4:q E4:q C#4:q | D#4:dh F#4:q | E4:h D#4:q C#4:q | C#4:w";
    const below = "E4:w | C#4:h E4:h | F#4:w | D#4:h F#4:h | A4:dh G#4:q | F#4:w | F#4:h E4:h | D4:w";
    const parts = {
      void: { bars: 8, hum: [0, 0.7, 1], ...guitar(verse, "toll", [0.4, 0.65, 0.7]), drums: ".".repeat(64) + "X...............".repeat(4) },
      shadow: {
        bars: 8, hum: [1, 1], ...guitar(verse, "toll", [0.7, 0.8, 0.75]),
        ...melody(shadow, [0.6, 0.75, 0.8, 0.65, 0.75, 0.85, 0.8, 0.6], dark), drums: "X...............".repeat(8),
      },
      below: {
        bars: 8, hum: [1, 0.6, 0.9], ...guitar(deep, "swell", [0.6, 0.75, 0.65]),
        ...melody(below, [0.55, 0.7, 0.75, 0.7, 0.8, 0.7, 0.6, 0.5], { attack: 0.4, decay: 12, edge: 0 }), drums: "X.......Z.......".repeat(8),
      },
      shadow2: {
        bars: 8, hum: [0.9, 1], ...guitar(verse, "rock", [0.75, 0.9, 0.8]),
        ...melody(shadow, [0.7, 0.85, 0.9, 0.75, 0.85, 0.95, 0.9, 0.7], dark), drums: "X.......Z.....Z.".repeat(8),
      },
      fade: {
        bars: 8, hum: [1, 0.6], ...guitar(deep, "toll", [0.7, 0.55, 0.4]),
        ...melody(below, [0.6, 0.5, 0.4, 0.35], dark), drums: "X...............".repeat(6) + ".".repeat(32),
      },
      end: coda("Cm", "G#3", "toll", dark),
    };
    return record({ root: "C#2", echo: 1, buzz: 0.035, parts, form: "void shadow below shadow2 fade end" });
  },
  lagrange: () => {
    const bpm = 104;
    const { melody, guitar, play, breathe, coda, record } = space(bpm, {
      G: ["B3", "G4"], F: ["A3", "F4"], C: ["C4", "G4"], Am: ["C4", "A4"], Dm: ["D4", "A4"],
    });
    const call = "G F C G G F Am Dm";
    const poise = "C C G G F F G G";
    const ask = "D5:q B4:q G4:q A4:q | C5:dh A4:q | -:w | -:w | D5:q E5:q F5:q D5:q | C5:w | -:w | -:w";
    const reply = "-:w | -:w | E4:q G4:q C4:h | D4:dh B3:q | -:w | -:w | E4:q C4:q A3:h | D4:w";
    const over = "E5:w | D5:h C5:h | D5:w | B4:w | C5:w | A4:h F4:h | B4:w | -:w";
    const under = "C4:w | E4:h G4:h | B3:w | D4:w | A3:w | C4:h F4:h | D4:w | G3:w";
    const answer = (line, contour) => ({ answer: play(breathe(line, contour), { level: 0.24, attack: 0.04, decay: 6, step: 0.3 }) });
    const parts = {
      intro: { bars: 8, hum: [0, 0.6, 0.9], ...guitar(call, "strum", [0.45, 0.7, 0.8]), drums: ".".repeat(64) + "O.......Z.......".repeat(4) },
      call: {
        bars: 8, hum: [0.9, 1], ...guitar(call, "rock", [0.75, 0.85, 0.8]),
        ...melody(ask, [0.7, 0.85, 0.8, 0.75, 0.9, 0.85, 0.8, 0.75]), ...answer(reply, [0.7, 0.8, 0.85, 0.75, 0.8, 0.9, 0.85, 0.7]),
        drums: "O.......Z...Z...".repeat(8),
      },
      poise: {
        bars: 8, hum: [1, 1], ...guitar(poise, "pedal", [0.8, 0.9, 0.85]),
        ...melody(over, [0.75, 0.85, 0.9, 0.8, 0.85, 0.9, 0.75]), ...answer(under, [0.65, 0.75, 0.8, 0.75, 0.8, 0.85, 0.7]),
        drums: "O...Z...O...Z...".repeat(8),
      },
      call2: {
        bars: 8, hum: [0.9, 1], ...guitar(call, "rock", [0.85, 0.95, 0.9]),
        ...melody(ask, [0.8, 0.95, 0.9, 0.85, 1, 0.95, 0.9, 0.85]), ...answer(reply, [0.8, 0.9, 0.95, 0.85, 0.9, 1, 0.95, 0.8]),
        drums: "O.......Z...Z...".repeat(8),
      },
      still: {
        bars: 8, hum: [1, 0.5, 0.8], ...guitar(poise, "swell", [0.6, 0.75, 0.65]),
        ...answer("E4:w | -:w | D4:w | -:w | C4:w | -:w | B3:w | -:w", [0.6, 0.7, 0.6]), drums: ".".repeat(96) + "Z.......Z.......".repeat(2),
      },
      poise2: {
        bars: 8, hum: [0.9, 1], ...guitar(poise, "pedal", [0.85, 1, 0.9]),
        ...melody(over, [0.85, 0.95, 1, 0.9, 0.95, 1, 0.85]), ...answer(under, [0.75, 0.85, 0.9, 0.85, 0.9, 0.95, 0.8]),
        drums: "O...Z...O...Z...".repeat(7) + "O...Z...O.Z.Z...",
      },
      outro: {
        bars: 8, hum: [1, 0.6], ...guitar(call, "strum", [0.8, 0.6, 0.45]),
        ...melody(ask, [0.7, 0.55, 0.45, 0.35]), ...answer(reply, [0.65, 0.5, 0.4, 0.3]), drums: "O.......Z.......".repeat(4) + ".".repeat(64),
      },
      end: coda("G", "B4"),
    };
    return record({
      root: "G2", echo: 1.5, parts, form: "intro call poise call2 still poise2 outro end", voices: { answer: "#TYPE sine\n#PAN -0.4" },
    });
  },
  redshift: () => {
    const bpm = 88;
    const chords = {
      F: ["A3", "F4"], Ce: ["G3", "E4"], Dm: ["F3", "D4"], C: ["E3", "C4"], Bb: ["D3", "A#3"], Fa: ["C3", "A3"], Gm: ["A#2", "G3"], C7: ["E3", "A#3"],
    };
    const { melody, guitar, coda, record } = space(bpm, chords);
    const down = space(bpm, Object.fromEntries(Object.entries(chords).map(([chord, strings]) => [chord, strings.map((note) => transpose(note, -2))])));
    const falling = "F Ce Dm C Bb Fa Gm C7";
    const drift = "Dm Bb F C Dm Bb Gm C7";
    const theme = "A4:q C5:q F5:h | E5:dh C5:q | D5:dh A4:q | C5:h G4:h | D5:dh A#4:q | C5:h A4:h | G4:dh A#4:q | E4:w";
    const away = "F5:h E5:h | D5:w | C5:h A4:h | G4:w | F5:dq E5:dq D5:q | D5:w | A#4:h A4:h | G4:w";
    const hats = "O.......O.......";
    const parts = {
      intro: { bars: 8, hum: [0, 0.6, 0.9], ...guitar(falling, "strum", [0.45, 0.7, 0.8]), drums: ".".repeat(64) + "Z.......Z.......".repeat(4) },
      theme: {
        bars: 8, hum: [0.9, 1], ...guitar(falling, "roll", [0.75, 0.85, 0.8]),
        ...melody(theme, [0.7, 0.9, 0.85, 0.75, 0.85, 0.8, 0.7, 0.6]), drums: hats.repeat(8),
      },
      drift: {
        bars: 8, hum: [1, 0.8], ...guitar(drift, "toll", [0.75, 0.85, 0.8]),
        ...melody(away, [0.8, 0.85, 0.75, 0.7, 0.9, 0.8, 0.7, 0.6]), drums: "....Z.......Z...".repeat(8),
      },
      theme2: {
        bars: 8, hum: [0.9, 1], ...guitar(falling, "strum", [0.85, 0.95, 0.9]),
        ...melody(theme, [0.8, 1, 0.95, 0.85, 0.95, 0.9, 0.8, 0.7]), drums: (hats + "O.......O.....Z.").repeat(4),
      },
      shift: {
        bars: 8, root: "D#2", hum: [1, 1], ...down.guitar(falling, "roll", [0.85, 0.95, 0.85]),
        ...down.melody(transpose(theme, -2), [0.8, 0.95, 0.9, 0.8, 0.9, 0.85, 0.75, 0.65]), drums: (hats + "O.......O.....Z.").repeat(4),
      },
      fall: {
        bars: 8, root: "D#2", hum: [1, 0.6], ...down.guitar(drift, "swell", [0.75, 0.6, 0.45]),
        ...down.melody(transpose(away, -2), [0.7, 0.6, 0.5, 0.4]), drums: "....Z.......Z...".repeat(4) + ".".repeat(64),
      },
      end: { ...down.coda("F", "G4"), root: "D#2" },
    };
    return record({ root: "F2", echo: 1, parts, form: "intro theme drift theme2 shift fall end" });
  },
  nadir: () => {
    const bpm = 60;
    const { melody, guitar, coda, record } = space(bpm, {
      Bbm: ["A#2", "F3"], Eb: ["D#3", "G3"], Ab: ["G#2", "D#3"], Gb: ["F#2", "C#3"], Db: ["C#3", "G#3"], Fm: ["F3", "C4"],
    });
    const low = "Bbm Bbm Eb Eb Bbm Bbm Ab Ab";
    const abyss = "Gb Gb Db Db Eb Eb Fm Fm";
    const deep = { attack: 0.05, decay: 8 };
    const bow = { attack: 0.6, decay: 16, edge: 0 };
    const theme = "F4:w | C#4:h D#4:h | G4:w | F4:h D#4:h | C#4:dh A#3:q | C4:w | D#4:w | C4:w";
    const depth = "A#4:w | C#5:h A#4:h | G#4:w | F4:h G#4:h | G4:w | A#4:h G4:h | G#4:w | F4:w";
    const parts = {
      void: { bars: 8, hum: [0, 0.7, 1], ...guitar(low, "swell", [0.35, 0.6, 0.7]), drums: ".".repeat(96) + "Z...............".repeat(2) },
      sink: {
        bars: 8, hum: [1, 1], ...guitar(low, "toll", [0.7, 0.8, 0.75]),
        ...melody(theme, [0.6, 0.75, 0.85, 0.7, 0.75, 0.8, 0.7, 0.6], deep), drums: ("Z" + ".".repeat(31)).repeat(4),
      },
      abyss: {
        bars: 8, hum: [1, 0.7, 1], ...guitar(abyss, "swell", [0.65, 0.8, 0.7]),
        ...melody(depth, [0.55, 0.7, 0.8, 0.7, 0.8, 0.85, 0.7, 0.55], bow), drums: ("O" + ".".repeat(31)).repeat(4),
      },
      sink2: {
        bars: 8, hum: [1, 0.8], ...guitar(low, "toll", [0.8, 0.9, 0.7]),
        ...melody(theme, [0.7, 0.85, 0.9, 0.8, 0.8, 0.75, 0.6, 0.5], deep), drums: ("Z.......".repeat(3) + "O.......").repeat(3) + ".".repeat(32),
      },
      end: coda("Bbm", "F4", "toll", deep),
    };
    return record({ root: "A#1", echo: 1.5, drone: 0.12, buzz: 0.03, parts, form: "void sink abyss sink2 end" });
  },
  chinook: () => {
    const bpm = 80;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.05, slide: 0.025, echo: 0.75, tail: "G1" });
    const pedal = {
      Dm: ["D3", "A3", "D4", "E4", "F4", "A4"], C: ["D3", "G3", "C4", "E4", "G4", "C5"], Bb: ["D3", "F3", "A#3", "D4", "F4", "A4"],
      Gm: ["D3", "G3", "A#3", "D4", "G4", "A#4"], A: ["A2", "E3", "A3", "C#4", "E4", "G4"],
    };
    const open = { Dm: ["D3", "A3", "D4", "E4", "F4", "A4"], Bb: ["A#2", "F3", "A#3", "D4", "F4", "C5"], F: ["F2", "C3", "F3", "A3", "C4", "G4"], C: ["C3", "G3", "C4", "E4", "G4", "D5"] };
    const picking = ["02435342", "0.2.4.3.5.4.3.21"];
    const theme = "A4:q D5:q E5:e F5:e E5:e D5:e | F5:h~ D5:q C5:q | A4:q C5:q F5:e G5:e A5:e C6:e | G5:dh~ -:e E5:e"
      + " | F5:q E5:e D5:e A4:q D5:q | F5:q G5:e F5:e D5:h~ | C5:q F5:e G5:e A5:q C6:q | G5>A5:h~ G5:e E5:e C5:q";
    const walk = "D2^:dq D2^,:e -:e D2^:e F2^:e G2^:e | A#2^:dq A#2^,:e -:e A#2^:e A2^:e G2^:e"
      + " | F2^:dq F2^,:e -:e F2^:e G2^:e A2^:e | C3^:dq C3^,:e -:e C3^:e A#2^:e C3^:e";
    const held = "D2^:w~ | A#2^:w~ | F2^:w~ | C3^:w~";
    const drive = ["D2", "A#2", "F2", "C3"].map((x) => `${x}^:e ${x},:e ${x},:e ${x}^:e ${x},:e ${x},:e ${x}^:e ${x},:e`).join(" | ");
    const solo = "-:h -:e A4:e C5:e D5:e | F5>G5:h~ _G5>F5:e D5:e C5:q | A4:q~ -:q -:e C5:e D5:e F5:e | G5:dh~ -:q"
      + " | G5>A5:q~ _A5>G5:e F5:e D5:h~ | -:e D5:e F5:e G5:e F5:e D5:e C5:q | C6:q~ A5:e G5:e F5:e G5:e A5:q~ | _G5:e E5:e C5:e E5:e G5:h~"
      + " | D6:q~~ C6:e A5:e G5>A5:e G5:e F5:e D5:e | F5:s G5:s A5:s C6:s D6:e C6:e A5:q~ G5:e F5:e"
      + " | A5:s C6:s D6:s F6:s E6>F6:h~~ _F6>E6:e C6:e | D6:s C6:s A5:s G5:s E6:q~ D6:s C6:s G5:s E5:s G5:q~"
      + " | D6:x C6:x A5:x D6:x C6:x A5:x D6:x C6:x A5:x D6:x C6:x A5:x F6:x E6:x D6:x C6:x A5:x G5:x A5:q~"
      + " | F5:x G5:x A5:x C6:x D6:x F6:x F6:q~~ _D6:s C6:s A5:s G5:s F5:s G5:s A5:s C6:s"
      + ` | ${times(4, "C6>D6:x C6:x A5:x").join(" ")} C6>D6:q~~ C6:e A5:e`
      + " | E6:x D6:x C6:x G5:x E6:x D6:x C6:x G5:x E5:x C6:x G5:x E5:x G5:s A5:s C6:s D6:s E6:q~"
      + ` | D6>F6:h~~ _F6>D6:e C6:e D6>F6:q~~ | ${times(4, "F6:x D6:x C6:x").join(" ")} D6>F6:h~~`
      + " | C6:s D6:s F6:s G6:s A6:dh~~ | _A6>G6:s E6:s D6:s C6:s G6:s E6:s D6:s C6:s E6:s D6:s C6:s G5:s D6>E6:q~~"
      + " | _E6>D6:s C6:s A5:s G5:s F5:s E5:s D5:s C5:s D5:h~ | -:q F5:e G5:e A5>A#5:h~ | _A#5>A5:e G5:e F5:e G5:e A5:q~ C6:q"
      + " | G5:q~ E5:e G5:e A5:e C6:e C6>D6:q";
    const half = { kick: "K.......K.k.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const climb = { kick: "K.K...K.K.K...K.", snare: "....S.......S...", hat: "O...O...O...O...", cym: "C..." };
    const parts = {
      intro: part(8, fingers("Dm Dm C Bb Dm Dm Gm A", pedal, picking, { contour: [0.7, 1] })),
      verse: part(4, fingers("Dm Bb F C", open, picking, { level: 0.8 }), lead(theme.split("|").slice(0, 4).join("|"), { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 }),
        bassline("D2:w | A#1:w | F2:w | C2:h~ -:h", { level: 0.8, contour: [0.5, 1] }), drums(drummer({}, {}, {}, { kick: "........K...K...", tom: "........TTMMFFFF" }))),
      build: part(8, fingers("Dm Bb F C", open, picking, { level: 0.7, contour: [1, 0.5] }), riff(`${held} | ${walk}`, { level: 0.9 }),
        drums(drummer(crash(half), half, half, { ...half, tom: "............TTFF" }, crash(rock), rock, rock2, triplets))),
      theme: part(8, riff(`${walk} | ${walk}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(rock), rock, rock2, toms, crash(rock), rock, rock2, down))),
      solo: part(24, riff(`${drive} | ${drive} | ${drive} | ${drive} | ${held} | ${drive}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(ride), ride, ride2, toms, ride, ride, ride2, down, crash(ride), ride, ride2, toms, crash(ride), ride, ride2, triplets,
          climb, climb, climb, thunder, crash(ride), ride, ride2, down))),
      outro: part(4, riff("D2^:w~ | A#2^:h C3^:h | D2^:w~ | -:w"), lead("_D6:dh~~ -:q | -:w | -:w | -:w"), fingers("- - - Dm", pedal, picking),
        drums(drummer({ kick: "K...", tom: "........TTMMFFFF", cym: "C..." }, { kick: "K.......K.......", snare: "........S.......", cym: "C.......C......." },
          { kick: "K...", tom: "....F.F.F.F.FFFF", cym: "C..." }, {}))),
      end: part(2, fingers("Dm Dm", pedal, ["02435342", "012345.........."], { contour: [0.9, 0.7], lanes: 6 })),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo outro end" });
  },
  bora: () => {
    const bpm = 84;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.05, slide: 0.025, echo: 0.75, tail: "G1" });
    const voicing = {
      Em: ["E3", "B3", "E4", "G4", "B4", "E5"], G: ["G2", "D3", "G3", "B3", "D4", "G4"], A: ["A2", "E3", "A3", "C#4", "E4", "A4"],
      B7sus: ["B2", "F#3", "B3", "E4", "F#4", "A4"], B7: ["B2", "F#3", "A3", "D#4", "F#4", "B4"],
    };
    const picking = ["03142543", "03142.5.4.3.2.1."];
    const theme = "E5:dq D5:e B4:q G4:q | D5:e G5:e B5:q~ -:e A5:e G5:e D5:e | C#6:dq B5:e A5:q E5:q | F#5:h~ D#5:q -:e B4:e"
      + " | E5:dq D5:e B4:e D5:e E5:e G5:e | B5:q~ A5:e G5:e D5:h~ | A5:e C#6:e B5:e A5:e E5>F#5:q~ E5:q | F#5>G5:q _G5>F#5:e D#5:e B4:h~";
    const gust = "E2^:de E2^,:de E2^:e B2:s D3:s E3:e D3:s B2:s A2:e | G2^:de G2^,:de G2^:e D3:s E3:s G3:e E3:s D3:s B2:e"
      + " | A2^:de A2^,:de A2^:e E3:s G3:s A3:e G3:s E3:s C#3:e | B2^:q~ B2^,:e B2^,:e A2:e F#2:e G2:e D#2:e";
    const drive = ["E2", "G2", "A2", "B2"].map((x) => `${x}^:de ${x},:s ${x},:e ${x},:e ${x}^:de ${x},:s ${x},:e ${x},:e`).join(" | ");
    const stops = "E2^:q~ -:dh | G2^:q~ -:dh | A2^:q~ -:dh | B2^:e -:dq B2^:e B2^:e B2^:e B2^:e";
    const peak = "E2^:h~ E2^:h~ | G2^:h~ G2^:h~ | A2^:h~ A2^:h~ | B2^:h~ B2:e A2:e F#2:e D#2:e";
    const solo = "B4:h~ -:e D5:e E5:e G5:e | A5>B5:dq~ _B5>A5:e G5:q D5:q | -:q E5:e G5:e A5:q~ -:q | F#5:e A5:e F#5:e D#5:e E5>F#5:h~"
      + " | -:e E6:e D6:e B5:e D6>E6:h~ | _E6>D6:e B5:e G5:e D5:e G5:q~ -:q | -:e A4:e C#5:e D5:e E5:e D5:e C#5:e A4:e | B4>C5:q~ _C5>B4:e A4:e B4:h~"
      + " | E5:s G5:s A5:s B5:s D6:s B5:s A5:s G5:s E5:s G5:s A5:s B5:s D6:q~ | E6:x D6:x B5:x A5:x B5:x D6:x E6:x D6:x B5:x A5:x G5:x D5:x G5:h~"
      + " | A5:s C#6:s E6:s G6:s G6>A6:q~ _A6>G6:s E6:s C#6:s A5:s E5:q | B5:x A5:x F#5:x D#5:x F#5:x A5:x B5:q~ -:e A5:e F#5:e D#5:e"
      + " | E5:s G5:s A5:s G5:s G5:s A5:s B5:s A5:s A5:s B5:s D6:s B5:s B5:s D6:s E6:s D6:s | E6:q~ D6:e B5:e C6>D6:h~"
      + " | _D6>C#6:e A5:e E5:e G5:e A5:s C#6:s E6:s G6:s E6:q | D#6:q~ B5:e A5:e F#5:e A5:e B5:q"
      + ` | ${times(4, "D6>E6:t D6:t B5:t").join(" ")} | D6>E6:q~~ _E6>D6:e B5:e G5:e B5:e D6:q | ${times(4, "G6>A6:t G6:t E6:t").join(" ")}`
      + " | F#6:x E6:x D#6:x B5:x F#6:x E6:x D#6:x B5:x A5:x F#5:x D#5:x B4:x D#5:s F#5:s A5:s B5:s D6:s E6:s G6:s A6:s"
      + " | _A6>B6:w~~ | _B6>A6:e G6:e E6:e D6:e B5:e A5:e G5:e D5:e | -:e A5:e C#6:e B5:e A5:e G5:e E5>F#5:q~ | _F#5:e D#5:e B4:e D#5:e E5:h";
    const gale = { kick: "K..K..K.K.......", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.H." };
    const gale2 = { kick: "K..K..K.K.....K.", snare: "....S.......S..G", hat: "H.H.H.H.H.H.H.O." };
    const gallop = { kick: "K..K....K..K....", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const gallop2 = { kick: "K..K....K..K..K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const stop = { kick: "K...", cym: "C..." };
    const back = { kick: "K.......K.K.K.K.", snare: "........S.S.S.SS", cym: "C..." };
    const surge = { kick: "K.K.K.K.K.K.K.K.", snare: "....S.......S...", hat: "O...O...O...O..." };
    const parts = {
      intro: part(8, fingers("Em Em G G A A B7sus B7", voicing, picking, { contour: [0.6, 1] })),
      verse: part(8, fingers("Em G A B7", voicing, picking, { level: 0.8 }), lead(transpose(theme, -12), { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 }),
        bassline("E2:w | G2:w | A2:w | B2:w | E2:w | G2:w | A2:w | B2:h~ -:h", { level: 0.8, contour: [0.5, 1] }),
        drums(drummer(times(7, {}), { kick: "K.......K...K...", snare: "........SGSGSSSS" }))),
      build: part(4, fingers("Em G A B7", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(gust, { level: 0.85, contour: [0.7, 1] }),
        drums(drummer(crash(gale), gale, gale2, toms))),
      theme: part(8, riff(`${gust} | ${gust}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(gale), gale, gale2, toms, crash(gale), gale, gale2, down))),
      solo: part(24, riff(`${drive} | ${drive} | ${stops} | ${drive} | ${drive} | ${peak}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(gallop), gallop, gallop2, toms, gallop, gallop, gallop2, down, stop, stop, stop, back,
          crash(gallop), gallop, gallop2, triplets, surge, surge, surge, thunder, crash(ride), ride, ride2, down))),
      outro: part(4, riff(gust, { level: 0.85 }), lead(theme.split("|").slice(4).join("|"), { level: 0.9 }),
        drums(drummer(crash(gale), gale, gale2, { kick: "K.......K...K...", tom: "........TTMMFFFF", cym: "C..." }))),
      end: part(2, riff("E2^:dh~ -:q | -:w", { level: 0.8 }), fingers("Em Em", voicing, ["03142543", "543210.........."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo outro end" });
  },
  meltemi: () => {
    const bpm = 88;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.045, slide: 0.025, echo: 0.75, tail: "A1" });
    const voicing = {
      Asus2: ["A2", "E3", "A3", "B3", "E4", "A4"], A: ["A2", "E3", "A3", "C#4", "E4", "A4"], Em7: ["E3", "B3", "D4", "G4", "B4", "D5"],
      G: ["G2", "D3", "G3", "B3", "D4", "G4"], Dsus4: ["D3", "A3", "D4", "G4", "A4", "D5"], D: ["D3", "A3", "D4", "F#4", "A4", "D5"],
    };
    const picking = ["05240314", "0.5.2.4.0.345.1."];
    const theme = "E5:q C#5:e E5:e A5:q~ G5:e E5:e | G5:dq E5:e D5:e B4:e A4:q | D5:e E5:e G5:e A5:e B5:q~ A5:e G5:e | A5:h~ F#5:q D5:q"
      + " | E5:q C#5:e E5:e A5:e B5:e C#6:q~ | B5:e A5:e G5:e E5:e D5>E5:h~ | D5:e B4:e D5:e E5:e G5:q B5:q | A5>B5:q~ _B5>A5:e F#5:e A5:h~";
    const sail = "A2^:q A2^:e G2:e A2^:q C#3:e D3:e | E2^:q E2^:e D2:e E2^:q G2:e A2:e | G2^:q G2^:e F#2:e G2^:q B2:e C#3:e"
      + " | D3^:q D3^,:e D3^,:e D3^:e E3:e C#3:e B2:e";
    const drive = ["A2", "E2", "G2", "D3"].map((x) => `${x}^:e ${x},:e ${x}^:q ${x},:e ${x}^:e ${x}^:q`).join(" | ");
    const bridge = "F2^:q~ F2^,:e F2^:e F2^:q F2^:q | G2^:q~ G2^,:e G2^:e G2^:q G2^:q | A2^:w~ | A2^:e A2^,:e A2^:e A2^,:e A2^:q E2^:e G2^:e";
    const hold = ["A2", "G2", "D3", "A2"].map((x) => `${x}^:h~ ${x}^:e ${x}^:dq~`).join(" | ");
    const solo = "-:q C#5:e E5:e F#5:e E5:e C#5>D5:q~ | _D5>C#5:e B4:e G4:e A4:e B4:h~ | -:h G5:e A5:e A5>B5:q~ | _B5>A5:e F#5:e A5:e F#5:e E5:e D5:e E5:q"
      + " | A5:s B5:s C#6:s E6:s E6:h~~ -:e C#6:e | D6:e B5:e G5:e E5:e G5:q~ -:q | -:e B4:e D5:e B4:e G4:e A4:e B4:q | A4:s C#5:s D5:s E5:s F#5:q~ _E5:e D5:e A4:q"
      + " | C5:t F5:t A5:t C6:t A5:t F5:t C6:q~ A5:e C6:e | D6:t B5:t G5:t D6:t B5:t G5:t D6:q~ B5:e D6:e | E6:t C#6:t A5:t E6:t C#6:t A5:t E6:h~~"
      + " | _D6:e C#6:e A5:e G5:e E5:e G5:e A5:q | F5:e A5:e C6:e D6:e C6>D6:q~ C6:e A5:e | G5:e B5:e D6:e E6:e D6>E6:q~ D6:e B5:e"
      + " | E6:q~ C#6:e B5:e C#6:e E6:e F#6:e E6:e | C6>C#6:q~ A5:e G5:e E5:e G5:e A5:e C6:e"
      + ` | ${times(5, "C6>C#6:e A5:s").join(" ")} E5:s | ${times(5, "D6>E6:e B5:s").join(" ")} G5:s | D6>E6:q~~ _E6>D6:e B5:e D6:e E6:e G6:q`
      + " | A5:x D6:x E6:x F#6:x E6:x D6:x A5:x D6:x E6:x F#6:x A6:x E6:x F#6>G6:h~"
      + " | _G6>A6:w~~ | _A6>G6:e E6:e D6:e B5:e D6:e E6:e G6:q~ | F#6:e D6:e A5:e F#5:e A5:e D6:e E6>F#6:q~ | _F#6>E6:e C#6:e A5:e E5:e C#5:e A4:e B4>C#5:q";
    const wake = { kick: "K.........K.....", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const wake2 = { kick: "K.K.......K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const deck = { kick: "K...K...K...K...", snare: "....S.......S...", hat: "O.H.O.H.O.H.O.H." };
    const light = { kick: "K.........K.....", hat: "H.H.H.H.H.H.H.H." };
    const parts = {
      intro: part(8, fingers("Asus2 A Em7 Em7 G G Dsus4 D", voicing, picking, { contour: [0.7, 1] })),
      verse: part(8, fingers("A Em7 G D", voicing, picking, { level: 0.8 }), lead(theme, { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 }),
        bassline("A1:h A2:h | E2:h E1:h | G1:h G2:h | D2:w | A1:h A2:h | E2:h E1:h | G1:h G2:h | D2:h~ -:h", { level: 0.8, contour: [0.5, 1] }),
        drums(drummer(times(7, {}), { kick: "K.......K.K.K...", tom: "........TTMMFFFF" }))),
      build: part(4, fingers("A Em7 G D", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(sail, { level: 0.8, contour: [0.6, 1] }),
        drums(drummer(crash(light), light, light, toms))),
      theme: part(8, riff(`${sail} | ${sail}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(rock), rock, rock2, toms, crash(rock), rock, rock2, down))),
      solo: part(24, riff(`${drive} | ${drive} | ${bridge} | ${bridge} | ${drive} | ${hold}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(wake), wake, wake2, toms, wake, wake, wake2, down, crash(deck), deck, crash(deck), triplets, crash(deck), deck, crash(deck), triplets,
          crash(wake), wake, wake2, toms, crash(ride), ride, ride2, thunder))),
      end: part(2, riff("A2^:dh~ -:q | -:w", { level: 0.8 }), fingers("A A", voicing, ["05240314", "0.5.2.4.0......."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo theme end" });
  },
  khamsin: () => {
    const bpm = 74;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.055, slide: 0.03, echo: 0.75, tail: "F#1" });
    const voicing = {
      Bm: ["B2", "F#3", "B3", "D4", "F#4", "B4"], C: ["C3", "G3", "C4", "E4", "G4", "B4"], Em: ["E3", "G3", "B3", "E4", "G4", "B4"],
      "F#7": ["F#2", "C#3", "F#3", "A#3", "E4", "F#4"],
    };
    const picking = ["05152535", "05154321"];
    const theme = "B4:q D5:q F#5:dq E5:e | G5:q~ E5:e C5:e B4:h~ | -:e E5:e G5:e B5:e A5:q G5:e E5:e | F#5>G5:q~ _G5>F#5:e E5:e A#4:h"
      + " | B4:q D5:q F#5:e A5:e B5:q~ | C6:dq B5:e G5:q E5:q | G5:e F#5:e E5:e D5:e E5:e G5:e B5>C6:q~ | _C6>B5:e A#5:e F#5:e E5:e C#5:e A#4:e F#4:q";
    const heat = "B2^:q~ B2^,:s B2^,:s C3^:e~ B2^:h~ | C3^:q~ C3^,:s C3^,:s D3^:e~ C3^:h~"
      + " | E2^:q~ E2^,:s E2^,:s F#2^:e~ E2^:q~ G2:e A2:e | F#2^:q~ F#2^,:s F#2^,:s G2^:e~ F#2^:q A#2:e C#3:e";
    const drive = ["B2", "C3", "E2", "F#2"].map((x) => `${x}^:q ${x},:e ${x},:e ${x},:e ${x}^:dq`).join(" | ");
    const held = "B2^:w~ | C3^:w~ | E2^:w~ | F#2^:h~ F#2:e A#2:e C#3:e E3:e";
    const solo = "-:q F#4:e A4:e B4>C#5:dq~ -:e | -:q E5:e D5:e C5:q~ B4:q | -:e G4:e B4:e D5:e E5>F#5:h~ | _F#5>E5:e C#5:e A#4:e C#5:e F#4:h~"
      + " | -:e D5:e F#5:e A5:e B5:q~~ A5:e F#5:e | G5:e E5:e C5:e E5:e G5>A5:q~ -:q | B5:s A5:s G5:s E5:s G5:s E5:s D5:s B4:s D5:q~ E5:q | C#5:e E5:e F#5:e A#5:e C#6:q~ -:q"
      + " | F#5:x B5:x D6:x C#6:x B5:x F#5:x D5:x F#5:x B5:x A5:x F#5:x D5:x F#5:h~ | E5:x G5:x C6:x B5:x G5:x E5:x C5:x E5:x G5:x B5:x C6:x B5:x G5:h~"
      + " | E6:x D6:x B5:x G5:x B5:x D6:x E6:x D6:x B5:x G5:x E5:x D5:x E5:s G5:s B5:s D6:s E6:q | C#6:x A#5:x F#5:x C#6:x A#5:x F#5:x E6:x C#6:x A#5:x E6:x C#6:x A#5:x F#6:h~~"
      + ` | ${times(4, "D6:s C#6:s B5:s C#6:s").join(" ")} | ${times(4, "E6:s D6:s B5:s D6:s").join(" ")} | ${times(2, "G6:s F#6:s E6:s F#6:s").join(" ")} G6>A6:h~`
      + " | A#5:e C#6:e E6:e F#6:e A#6:h~"
      + " | _A#6>B6:w~~ | _B6:e G6:e E6:e C6:e G6:e E6:e C6:e G5:e | B4:e E5:e G5:e B5:e A5>B5:h~ | _B5>A#5:e F#5:e E5:e C#5:e A#4:e C#5:e F#4:q";
    const slow = { kick: "K.....K.........", snare: "........S.......", hat: "H...H...H...H..." };
    const slow2 = { kick: "K.....K...K.....", snare: "........S.....g.", hat: "H...H...H...H.h." };
    const dune = { kick: "K.........K.....", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const dune2 = { kick: "K.K.......K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const clean = { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 };
    const parts = {
      intro: part(8, fingers("Bm Bm C C Bm Bm Em F#7", voicing, picking, { contour: [0.6, 1] })),
      verse: part(4, fingers("Bm C Em F#7", voicing, picking, { level: 0.8 }), lead(theme.split("|").slice(0, 4).join("|"), clean),
        bassline("B1:w | C2:w | E2:w | F#1:h~ -:h", { level: 0.8, contour: [0.5, 1] }), drums(drummer({}, {}, {}, { kick: "K.......K.K.....", tom: "....TT..MMMMFFFF" }))),
      build: part(4, fingers("Bm C Em F#7", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(heat, { level: 0.85, contour: [0.6, 1] }),
        drums(drummer(crash(slow), slow, slow2, toms))),
      theme: part(8, riff(`${heat} | ${heat}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(rock), rock, rock2, toms, crash(rock), rock, rock2, down))),
      calm: part(4, fingers("Bm C Em F#7", voicing, picking, { level: 0.8 }), lead(theme.split("|").slice(4).join("|"), clean),
        bassline("B1:w | C2:w | E2:w | F#1:w", { level: 0.8 }), drums(drummer({ hat: "H...H...H...H..." }, { hat: "H...H...H...H..." }, { hat: "H...H...H...H..." }, { kick: "K.......K.K.K.K.", tom: "........TTMMFFFF" }))),
      solo: part(20, riff(`${drive} | ${drive} | ${drive} | ${drive} | ${held}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(dune), dune, dune2, toms, dune, dune, dune2, down, crash(dune), dune, dune2, triplets, crash(rock), rock, rock2, thunder, crash(ride), ride, ride2, down))),
      outro: part(4, riff(heat, { level: 0.85 }), lead("F#5:q B5:h~ A5:e F#5:e | G5:h~ E5:q C5:q | B4:e D5:e E5:e F#5:e G5:q E5:q | F#5:w~~", { level: 0.9 }),
        drums(drummer(crash(rock), rock, rock2, { kick: "K.......K...K...", tom: "........TTMMFFFF", cym: "C..." }))),
      end: part(2, riff("B2^:dh~ -:q | -:w", { level: 0.8 }), fingers("Bm Bm", voicing, ["05152535", "0.5.1.5.2......."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme calm solo outro end" });
  },
  tramontane: () => {
    const bpm = 78;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.05, slide: 0.025, echo: 0.75, tail: "G1" });
    const voicing = {
      G: ["G2", "D3", "G3", "B3", "D4", "G4"], Cadd9: ["C3", "G3", "C4", "D4", "E4", "G4"], Em7: ["E3", "B3", "D4", "G4", "B4", "D5"],
      Dsus4: ["D3", "A3", "D4", "G4", "A4", "D5"], D: ["D3", "A3", "D4", "F#4", "A4", "D5"], Bm: ["B2", "F#3", "B3", "D4", "F#4", "B4"],
      C: ["C3", "G3", "C4", "E4", "G4", "C5"],
    };
    const picking = ["034543134543", "034543135453"];
    const theme = "D5:q G5:q~ A5:e B5:e A5:q | F#5:dq D5:e B4:h~ | C5:q E5:e G5:e C6:q~ B5:e A5:e | A5:h~ F#5:q -:e D5:e"
      + " | D5:q G5:q B5:e D6:e B5:q | A5:e B5:e F#5:e D5:e F#5:h~ | E5:q G5:e C6:e B5:q~ A5:e G5:e | A5>B5:q~ _B5>A5:e F#5:e G5:h~";
    const peaks = "G2^:e G2^,:e A2:s B2:s D3:e~ G2^:q B2:e A2:e | B2^:e B2^,:e C#3:s D3:s F#3:e~ B2^:q D3:e C#3:e"
      + " | C3^:e C3^,:e D3:s E3:s G3:e~ C3^:q E3:e D3:e | D3^:q D3^,:e D3^,:e D3^:e C3:e B2:e A2:e";
    const drive = ["E2", "C3", "G2", "D3"].map((x) => `${x}^:q ${x}^:e ${x}^:q~ ${x}^:e ${x}^:q`).join(" | ");
    const held = "C3^:w~ | D3^:w~ | G2^:w~ | G2^:h~ G2^:e F#2:e E2:e D2:e";
    const solo = "-:q E5:q~ G5:e A5:e B5:q | C6>D6:dh~ -:q | _D6>C6:e B5:e G5:e D5:e B4:h~ | -:e A4:e D5:e F#5:e A5:q~ G5:e F#5:e"
      + " | E5:q~ -:e B4:e E5:e G5:e B5>C6:q~ | _C6>B5:e G5:e E5:e C5:e E5:h~ | -:e D6:e B5:e G5:e B5:e D6:e G6:q~ | F#6:e E6:e D6:e A5:e F#5:q~ -:q"
      + " | G5:t A5:t B5:t D6:t B5:t A5:t G5:t A5:t B5:t E6:q~ | D6:t C6:t B5:t G5:t A5:t C6:t D6>E6:h~"
      + " | _E6>D6:t B5:t G5:t B5:t D6:t G6:t E6:t D6:t B5:t G5:q~ | A5:t F#5:t D5:t F#5:t A5:t D6:t F#6:t E6:t D6:t E6:q"
      + " | B5>D6:dq~ _D6>B5:e A5:e G5:e E5:q | G5>A5:dq~ _A5>G5:e E5:e C5:e E5:q | D5>E5:dq~ _E5>D5:e B4:e G4:e B4:q | A4:e D5:e F#5:e A5:e D6:e F#6:e A6:q~"
      + ` | _G6:s F#6:s E6:s D6:s B5:s D6:s E6:s G6:s G6>A6:h~ | ${times(4, "C6:s E6:s G6:s E6:s").join(" ")} | ${times(2, "B5:s D6:s G6:s D6:s").join(" ")} D6>E6:h~`
      + " | F#6:x D6:x A5:x F#6:x D6:x A5:x F#6:x D6:x A5:x F#5:x A5:x D6:x F#6>G6:h~"
      + " | -:e G5:e C6:e E6:e G6:q~ A6:e G6:e | F#6:e A6:e F#6:e D6:e A5:e D6:e F#6:e A6:e | _A6>B6:w~~ | _B6>A6:e G6:e D6:e B5:e G5:e D5:e B4:e A4:e";
    const breeze = { kick: "K.......K.......", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const ridge = { kick: "K.....K...K.....", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const ridge2 = { kick: "K.....K...K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const clean = { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 };
    const parts = {
      intro: part(8, fingers("G G Cadd9 Cadd9 Em7 Em7 Dsus4 D", voicing, picking, { contour: [0.6, 1] })),
      verse: part(8, fingers("G Bm C D", voicing, picking, { level: 0.8 }), lead(theme, clean),
        bassline("G1:w | B1:w | C2:w | D2:w | G1:w | B1:w | C2:w | D2:h~ -:h", { level: 0.8, contour: [0.4, 1] }),
        drums(drummer(times(7, {}), { kick: "K.......K...K...", tom: "........TTTTMMFF" }))),
      build: part(4, fingers("G Bm C D", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(peaks, { level: 0.85, contour: [0.6, 1] }),
        drums(drummer(crash(breeze), breeze, breeze, toms))),
      theme: part(8, riff(`${peaks} | ${peaks}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(rock), rock, rock2, toms, crash(rock), rock, rock2, down))),
      solo: part(24, riff(`${drive} | ${drive} | ${drive} | ${drive} | ${drive} | ${held}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(ridge), ridge, ridge2, toms, ridge, ridge, ridge2, down, crash(ridge), ridge, ridge2, triplets, crash(ridge), ridge, ridge2, toms,
          crash(rock), rock, rock2, thunder, crash(ride), ride, crash(ride), { kick: "K.......K.......", snare: "........S.......", cym: "C..." }))),
      home: part(4, fingers("G Bm C D", voicing, picking, { level: 0.8 }), lead(theme.split("|").slice(4).join("|"), clean),
        bassline("G1:w | B1:w | C2:w | D2:w", { level: 0.7 }), drums(drummer(times(4, { ride: "Y...Y...Y...Y..." })), { level: 0.6 })),
      end: part(2, fingers("G G", voicing, ["034543134543", "054321......"], { contour: [0.9, 0.6], lanes: 6 })),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo home end" });
  },
  zonda: () => {
    const bpm = 92;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.045, slide: 0.022, echo: 0.75, tail: "F#1" });
    const voicing = {
      "F#m": ["F#2", "C#3", "F#3", "A3", "B3", "E4"], A: ["A2", "E3", "A3", "C#4", "E4", "A4"], E: ["E3", "B3", "E4", "G#4", "B4", "E5"],
      B: ["B2", "F#3", "B3", "D#4", "F#4", "B4"], Bsus: ["B2", "F#3", "B3", "E4", "F#4", "B4"],
    };
    const picking = ["04231423", "0.4.2.3.15432.1."];
    const theme = "C#5:e F#5:e A5:e G#5:e F#5:q E5:e C#5:e | E5:dq F#5:e A5:q~ C#6:e B5:e | B5:q G#5:e E5:e F#5:e G#5:e B5:q | A5>B5:h~ _B5>A5:e F#5:e D#5:q"
      + " | C#6:q~ B5:e A5:e G#5:e A5:e F#5:q | E6:q~ C#6:e B5:e A5:e E5:e C#5:q | B4:e E5:e G#5:e B5:e A5:e G#5:e E5>F#5:q~ | _F#5:e D#5:e F#5:e A5:e B5:h~";
    const rush = "F#2^:q F#2^,:e F#2^:e A2:s B2:s C#3:s B2:s A2:s F#2:s E2:s G#2:s | A2^:q A2^,:e A2^:e C#3:s B2:s A2:s F#2:s E2:s F#2:s G#2:s D#2:s"
      + " | E2^:q E2^,:e E2^:e G#2:s A2:s B2:s C#3:s B2:s G#2:s F#2:s A#2:s | B2^:q B2^,:e B2^:e~ -:e A2:e B2:e C#3:e";
    const drive = ["F#2", "A2", "E2", "B2"].map((x) => `${x}^:e ${x},:e ${x}^:e ${x},:e ${x},:e ${x}^:e ${x},:s ${x},:s ${x}^:e`).join(" | ");
    const calls = "-:h F#2^:e F#2^:e F#2^:e A2^:e | -:h A2^:e A2^:e A2^:e E2^:e | -:h E2^:e E2^:e E2^:e B2^:e | -:h B2^:e B2^:e A2^:e B2^:e";
    const stop = "F#2^:w~ | -:w | A2^:w~ | B2^:h~ B2^:e A2^:e E2^:e B2^:e";
    const solo = "F#5:s G#5:s A5:s C#6:s E6:s C#6:s A5:s G#5:s F#5:s A5:s C#6:s E6:s F#6:q~ | _E6:e C#6:e A5:e E5:e C#5:q~ -:q"
      + " | -:h -:e B4:e E5:e G#5:e | F#5>G#5:h~ _G#5>F#5:e D#5:e B4:e D#5:e"
      + " | -:e A5:e C#6:e E6:e E6>F#6:q~ -:q | -:e A5:e B5:e C#6:e E6:e C#6:e B5:e A5:e | G#5:q~ E5:e B4:e G#4:e B4:e E5>F#5:q~ | _F#5>E5:e D#5:e B4:e A4:e B4:h~"
      + " | F#5:e A5:e C#6:e E6>F#6:e~ -:h | E6:e C#6:e A5:e C#6>E6:e~ -:h | B5:e G#5:e E5:e G#5>A5:e~ -:h | F#5:s A5:s B5:s D#6:s F#6>G#6:q~ -:h"
      + " | A5:e A5:s B5:s C#6:e C#6:s E6:s F#6:e F#6:s E6:s C#6:e A5:e | C#6:e C#6:s E6:s F#6:e F#6:s E6:s C#6:e E6:e F#6>G#6:q~"
      + " | _G#6:e E6:e F#6:e B5:e B5:s D#6:s E6:s F#6:s G#6:q | F#6:x D#6:x B5:x F#6:x D#6:x B5:x F#6:x D#6:x B5:x A5:x F#5:x D#5:x F#5:h~"
      + " | E6:x C#6:x B5:x A5:x B5:x C#6:x E6:x C#6:x B5:x G#5:x A5:x F#5:x E5:x F#5:x A5:x B5:x C#6:x E6:x F#6:q~"
      + " | E6:s C#6:s E6:s B5:s E6:s A5:s E6:s B5:s E6:s C#6:s E6:s B5:s E6:s A5:s C#6:s E6:s"
      + " | G#6:s E6:s G#6:s B5:s G#6:s E6:s G#6:s B5:s G#6:s F#6:s G#6:s E6:s G#6:s B5:s E6:s G#6:s"
      + " | F#6>G#6:q~~ _G#6>F#6:e D#6:e B5:e D#6:e F#6:e G#6:e"
      + " | _G#6>A6:w~~ | _A6:h~ G#6:e F#6:e E6:e C#6:e | B5:e A5:e F#5:e E5:e C#5:e E5:e F#5>G#5:q~ | _G#5>F#5:e D#5:e B4:e D#5:e F#5:e A5:e B5:e C#6:e";
    const charge = { kick: "K.....K.K.K.K.K.", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.H." };
    const charge2 = { kick: "K.....K.K.K.K...", snare: "....S.......S.GG", hat: "H.H.H.H.H.H.H.O." };
    const spur = { kick: "K.........K...K.", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const spur2 = { kick: "K...K.....K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const answer = { kick: "........K.K.K.K.", snare: "........S.S.S.S.", hat: "H.H.H.H........." };
    const hit = { kick: "K...", cym: "C..." };
    const parts = {
      intro: part(8, fingers("F#m F#m A A E E Bsus B", voicing, picking, { contour: [0.6, 1] })),
      build: part(4, fingers("F#m A E B", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(rush, { level: 0.85, contour: [0.6, 1] }),
        drums(drummer(crash(charge), charge, charge2, toms))),
      theme: part(8, riff(`${rush} | ${rush}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(charge), charge, charge2, toms, crash(charge), charge, charge2, down))),
      verse: part(4, fingers("F#m A E B", voicing, picking, { level: 0.8 }), lead(theme.split("|").slice(0, 4).join("|"), { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 }),
        bassline("F#2:w | A1:w | E2:w | B1:h~ -:h", { level: 0.8 })),
      brk: part(2, drums(drummer({ kick: "K.....K.K.....K.", snare: "....S.......SSSS", hat: "H.H.H.H.H.H....." }, thunder))),
      solo: part(24, riff(`${drive} | ${drive} | ${calls} | ${drive} | ${drive} | ${stop}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(spur), spur, spur2, toms, spur, spur, spur2, down, answer, answer, answer, answer, crash(spur), spur, spur2, triplets,
          crash(charge), charge, charge2, thunder, hit, {}, hit, { kick: "K.......K.K.K.K.", snare: "........S.S.S.S.", cym: "C..." }))),
      end: part(2, riff("F#2^:dh~ -:q | -:w", { level: 0.8 }), fingers("F#m F#m", voicing, ["04231423", "0.4.2.3.1......."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro build theme verse brk solo theme end" });
  },
  halny: () => {
    const bpm = 72;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.06, slide: 0.03, echo: 0.75, tail: "G1" });
    const voicing = {
      Am: ["A2", "E3", "A3", "C4", "E4", "A4"], Em: ["E3", "B3", "E4", "G4", "B4", "E5"], F: ["F2", "C3", "F3", "A3", "C4", "F4"],
      C: ["C3", "G3", "C4", "E4", "G4", "C5"], Dm: ["D3", "A3", "D4", "F4", "A4", "D5"], Bm7b5: ["B2", "F3", "A3", "D4", "F4", "A4"],
      E7: ["E3", "B3", "D4", "G#4", "B4", "E5"],
    };
    const chords = "Am Em F C Dm Am Bm7b5 E7";
    const picking = ["05432143", "05432.1.4.3.5..."];
    const theme = "E5:h~ C5:e D5:e E5:q | G5:dq F#5:e E5:h~ | A5:q~ G5:e F5:e C5:q F5:q | E5:dh~ -:e G4:e"
      + " | A4:q D5:q F5:e E5:e D5:e F5:e | E5:h~ C5:q A4:q | D5:q F5:q~ A5:e G#5:e F5:e D5:e | E5>F5:q~ _F5>E5:e D5:e B4:h~";
    const toll = "A2^:dh~ C3:s B2:s A2:s G2:s | E2^:dh~ G2:s F#2:s E2:s D2:s | F2^:dh~ A2:s G2:s F2:s E2:s | C3^:h~ C3^,:e C3^,:e B2:e G2:e"
      + " | D3^:dh~ F3:s E3:s D3:s C3:s | A2^:dh~ C3:s B2:s A2:s G#2:s | B2:q~ F3:e D3:e B2:e F3:e A3:e F3:e | E2^:q~ E2^,:e E2^,:e G#2:e B2:e D3:e G#2:e";
    const pulse = (x, c = "^") => `${x}${c}:h~ ${x}${c},:e ${x}${c},:e ${x}${c}:q`;
    const drive = [pulse("A2"), pulse("E2"), pulse("F2"), pulse("C3"), pulse("D3"), pulse("A2"), pulse("B2", ""), pulse("E2")].join(" | ");
    const held = "A2^:w~ | E2^:w~ | F2^:w~ | C3^:w~ | D3^:w~ | A2^:w~ | B2:w~ | E2^:h~ E2^,:e E2^,:e G#2:e B2:e";
    const solo = "-:dq C5:e D5>E5:h~ | _E5>D5:e B4:e G4:e B4:e E5:h~ | -:e A5:e G5:e F5:e G5>A5:h~ | _A5>G5:e E5:e C5:e E5:e G5:q~ -:q"
      + " | -:e D5:e F5:e A5:e C6>D6:h~ | _D6>C6:e A5:e E5:e C5:e E5:q~ -:q | -:e B4:e D5:e F5:e A5:q~ G#5:q | B5>C6:dq~ _C6>B5:e G#5:e E5:e D5:e B4:e"
      + " | A5:s C6:s E6:s C6:s A5:s E5:s C5:s E5:s A5:s C6:s E6:s G6:s E6:q~ | _D6:s B5:s G5:s E5:s G5:s B5:s D6:s E6:s G6:s E6:s D6:s B5:s G5:q"
      + " | F5:s A5:s C6:s F6:s E6:s C6:s A5:s G5:s A5:s C6:s E6:s F6:s E6:q~ | _D6:e C6:e G5:e E5:e C5:e E5:e G5:q"
      + " | A5/D6:q~ C6:e A5:e F5/A5:q~ G5:e F5:e | E5/A5:q~ G5:e E5:e C5/E5:q~ D5:e C5:e | D5/F5:q~ A5:e F5:e D5/F5:q~ B4:e D5:e | E5/G#5:q~ B5:e D6:e E6>F6:h~"
      + " | _F6>E6:e C6:e A5:e C6:e E6:q~ G6:q | E6>G6:h~ _G6>E6:e D6:e B5:e G5:e | C6:e F6:e E6:e C6:e E6>F6:h~ | _F6>E6:e C6:e G5:e C6:e E6:q~ G6:q"
      + " | _G6>A6:w~~ | _A6>G6:e E6:e C6:e A5:e E6:e C6:e A5:e E5:e | F5:e A5:e D6:e F6:e E6>F6:q~ _F6>E6:e D6:e | B5>C6:q~ _C6>B5:e G#5:e E5:e D5:e B4:q";
    const lean = { kick: "K.......K.......", snare: "........S.......", ride: "Y...Y...Y...Y..." };
    const sway = { kick: "K.......K.K.....", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const sway2 = { kick: "K.......K.K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const parts = {
      intro: part(8, fingers(chords, voicing, picking, { contour: [0.6, 1] })),
      verse: part(8, fingers(chords, voicing, picking, { level: 0.8 }), lead(theme, { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 }),
        bassline("A2:w | E2:w | F2:w | C2:w | D2:w | A1:w | B1:w | E2:h~ -:h", { level: 0.8, contour: [0.5, 1] }),
        drums(drummer(times(7, {}), { kick: "........K.......", tom: "....T...M...F.F." }))),
      build: part(4, fingers(chords, voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(toll.split("|").slice(0, 4).join("|"), { level: 0.85, contour: [0.6, 1] }),
        drums(drummer(crash(lean), lean, lean, toms))),
      theme: part(8, riff(toll, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(rock), rock, rock2, toms, crash(rock), rock, rock2, down))),
      solo: part(24, riff(`${drive} | ${drive} | ${held}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(sway), sway, sway2, sway, sway, sway, sway2, down, crash(sway), sway, sway2, toms, sway, sway, sway2, triplets,
          crash(ride), ride, ride2, ride, crash(ride), ride, ride2, thunder))),
      outro: part(4, riff("A2^:w~ | F2^:w~ | E2^:w~ | A2^:w~", { level: 0.8 }), lead("C6:h~ B5:e G#5:e E5:q | F5:dh~ E5:q | D5:e B4:e G#4:e B4:e E5:h~ | A4:w~~", { level: 0.9 }),
        drums(drummer(crash(rock), rock, { kick: "K.......K...K...", tom: "........TTMMFFFF" }, crash({ kick: "K..." })))),
      end: part(2, fingers("Am Am", voicing, ["05432143", "0.5.4.3.2.1....."], { contour: [0.9, 0.6], lanes: 6 })),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo outro end" });
  },
  sundowner: () => {
    const bpm = 76;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.055, slide: 0.03, echo: 0.75, tail: "G1" });
    const voicing = {
      Fmaj7: ["F2", "C3", "E3", "A3", "C4", "E4"], Em7: ["E3", "B3", "D4", "G4", "B4", "D5"], Dm7: ["D3", "A3", "D4", "F4", "A4", "C5"],
      C: ["C3", "G3", "C4", "E4", "G4", "C5"], G7sus: ["G2", "D3", "F3", "C4", "D4", "G4"],
    };
    const picking = ["03524135", "0.3.5.24..1.3.5."];
    const theme = "C5:e E5:e A5:q~ G5:e E5:e C5:q | B4:q~ D5:e E5:e G5:h~ | F5:q A5:q~ G5:e F5:e D5:q | E5:h~ -:e C5:e D5:e E5:e"
      + " | A5:q C6:q~ B5:e A5:e G5:q | G5:e B5:e E6:q~ D6:e B5:e G5:q | A5:e F5:e D5:e F5:e A5:e C6:e D6>E6:q~ | _E6>D6:e C6:e G5:e E5:e C5:h~";
    const sun = "F2^:e F2^,:e F2^:e~ -:e A2:e C3:e -:e E2^:e~ | -:e E2^,:e E2^:e~ -:e G2:e B2:e -:e D3^:e~"
      + " | -:e D3^,:e D3^:e~ -:e F3:e A3:e -:e C3^:e~ | -:e C3^,:e C3^:e~ -:e E3:e G3:e E3:e D3:e";
    const drive = [["F2", "G2"], ["G2", "E2"], ["E2", "A2"], ["A2", "F2"]].map(([x, y]) => `-:e ${x},:e ${x},:e ${x},:e ${x}^:e~ -:e ${x},:e ${y}^:e~`).join(" | ");
    const held = "-:q F2^:dh~ | G2^:w~ | C3^:w~ | C3^:h~ C3^:e G2:e E2:e G2:e";
    const solo = "-:q A5:e G5:e Eb5>E5:q~ C5:q | D5:e B4:e D5:e G5:e F5>G5:h~ | -:e E5:e G5:e B5:e A5>B5:q~ G5:q | E5:e C5:e A4:e C5:e E5:h~"
      + " | -:e C6:e A5:e F5:e A5/C6:q~ -:q | B5:e D6:e B5:e G5:e D5:q~ -:q | -:e G4:e A4:e B4:e D5:e E5:e G5:e E5:e | D5>E5:q~ _E5>D5:e C5:e A4:h~"
      + " | A5:s C6:s D6:s C6:s Eb6>E6:q~ _E6>D6:s C6:s A5:s G5:s A5:q | G5:s A5:s B5:s A5:s D6>E6:q~ _E6>D6:s B5:s G5:s D5:s G5:q"
      + " | E5:s G5:s B5:s G5:s D6>E6:q~ _E6>D6:s B5:s G5:s E5:s G5:q | A5:x C6:x E6:x C6:x A5:x E5:x A5:x C6:x E6:x G6:x E6:x C6:x A5:h~"
      + ` | ${times(3, "C6/D6:e C6:s A5:s").join(" ")} F5:q | ${times(3, "D6/E6:e D6:s B5:s").join(" ")} G5:q | ${times(3, "E6/G6:e E6:s B5:s").join(" ")} G5:q`
      + " | A5:e C6:e E6:e G6:e E6>G6:h~"
      + " | _G6>A6:w~~ | _A6>G6:e D6:e B5:e G5:e B5:e D6:e E6:e G6:e | E6:q~ C6:e G5:e E5:e G5:e C6:e E6:e | D6>E6:q~ _E6>D6:e C6:e A5:e G5:e E5:e C5:e";
    const lazy = { snare: 0.028, hat: 0.01, tom: 0.012 };
    const glow = { kick: "K.........K...K.", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.H." };
    const glow2 = { kick: "K.K.......K...K.", snare: "....S.......S..G", hat: "H.H.H.H.H.H.H.O." };
    const dusk = { kick: "K.......K.....K.", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const dusk2 = { kick: "K.......K.K...K.", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const half = { kick: "K.......K.......", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const clean = { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 };
    const parts = {
      intro: part(4, fingers("Fmaj7 Em7 Dm7 G7sus", voicing, picking, { contour: [0.6, 1] })),
      verse: part(8, fingers("Fmaj7 Em7 Dm7 C", voicing, picking, { level: 0.8 }), lead(theme, clean),
        bassline("F2:w | E2:w | D2:w | C2:w | F2:w | E2:w | D2:w | C2:h~ -:h", { level: 0.8, contour: [0.5, 1] }),
        drums(drummer(times(7, {}), { kick: "K.......K.....K.", tom: "........TTMM.FF." }), { late: lazy })),
      build: part(4, fingers("Fmaj7 Em7 Dm7 C", voicing, picking, { level: 0.7, contour: [1, 0.4] }), riff(sun, { level: 0.85, contour: [0.6, 1] }),
        drums(drummer(crash(half), half, half, toms), { late: lazy })),
      theme: part(8, riff(`${sun} | ${sun}`, { level: 0.85 }), lead(theme, { level: 0.9 }),
        drums(drummer(crash(glow), glow, glow2, toms, crash(glow), glow, glow2, down), { late: lazy })),
      solo: part(20, riff(`${drive.replace("-:e", "F2^:e~")} | ${drive} | ${drive} | ${drive} | ${held}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(dusk), dusk, dusk2, toms, dusk, dusk, dusk2, down, crash(dusk), dusk, dusk2, triplets, crash(dusk), dusk, dusk2, thunder,
          crash(ride), ride, ride2, down), { late: lazy })),
      dusk: part(8, riff(`${sun} | ${sun}`, { level: 0.6 }), lead(theme, clean),
        drums(drummer(crash(glow), glow, glow2, glow, glow, glow, glow2, { kick: "K.......K.......", snare: "........S.......", cym: "C..." }), { level: 0.7, late: lazy })),
      end: part(2, fingers("C C", voicing, ["03524135", "0.3.5.2........."], { contour: [0.9, 0.6], lanes: 6 })),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse build theme solo dusk end" });
  },
  shamal: () => {
    const bpm = 82;
    const { lead, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.05, slide: 0.025, echo: 0.75, tail: "G1" });
    const voicing = {
      Gm9: ["G2", "D3", "F3", "Bb3", "D4", "A4"], Gm: ["G2", "D3", "G3", "Bb3", "D4", "G4"], Cm: ["C3", "G3", "C4", "Eb4", "G4", "C5"],
      Eb: ["Eb3", "Bb3", "Eb4", "G4", "Bb4", "Eb5"], D: ["D3", "A3", "D4", "F#4", "A4", "D5"],
    };
    const picking = ["03241354", "0.3.2.4.1.354.3."];
    const dust = "G2^:e G2,:s G2,:s Bb2:s C3:s D3:e~ -:s F3:s D3:s C3:s Bb2:e A2:e | C3^:e C3,:s C3,:s Eb3:s F3:s G3:e~ -:s Bb3:s G3:s F3:s Eb3:e D3:e"
      + " | Eb3^:e Eb3,:s Eb3,:s G3:s F3:s Eb3:e~ -:s D3:s C3:s Bb2:s C3:e D3:e | D3^:q~ D3^,:e D3^,:e F#2:e A2:e C3:e A2:e";
    const figure = transpose(dust.replace(/[\^,]/g, ""), 12);
    const theme = "D5:q G5:e A5:e Bb5:q~ A5:e G5:e | Eb5:dq D5:e C5:q G4:q | Bb4:e Eb5:e G5:e Bb5:e Ab5:q~ G5:e Eb5:e | F#5:h~ A5:q D5:q"
      + " | G5:q Bb5:e C6:e D6:q~ C6:e Bb5:e | C6:dq Bb5:e G5:q Eb5:q | F5:e G5:e Bb5:e G5:e F5:e Eb5:e Bb4>C5:q~ | _C5>Bb4:e A4:e F#4:e A4:e D5:h~";
    const answers = "-:h -:e G5:e Bb5:e C6:e | Db6>D6:q~ _D6>C6:e Bb5:e G5:h | -:h -:e Eb5:e G5:e Bb5:e | A5:q~ F#5:e D5:e -:h";
    const drive = ["G2", "C3", "Eb3", "D3"].map((x) => `${x}^:de ${x}^:de ${x},:de ${x},:de ${x}^:q`).join(" | ");
    const pound = "G2^:q G2^:q G2^:q G2^:q | C3^:q C3^:q C3^:q C3^:q | Eb3^:q Eb3^:q Eb3^:q Eb3^:q | D3^:q D3^:q D3^:e D3^:e D3^:e D3^:e";
    const solo = "G4:q~ Bb4:e C5:e Db5>D5:q~ -:q | C5:e Bb4:e G4:e Bb4:e C5:h~ | -:e Eb5:e G5:e Bb5:e Ab5>Bb5:q~ G5:q | F#5:e A5:e F#5:e D5:e C5:e A4:e D5:q~"
      + " | -:e D6:e Bb5:e G5:e C6>D6:h~ | _D6>C6:e Bb5:e G5:e Eb5:e C5:h~ | -:e G4:e Bb4:e C5:e Eb5:e F5:e G5:e Bb5:e | A5>Bb5:q~ _Bb5>A5:e F#5:e D5:h~"
      + " | D5:s G5:s C6:s G5:s F5:s Bb5:s Eb6:s Bb5:s G5:s C6:s F6:s C6:s Bb5:s Eb6:s G6:s Eb6:s | F6:q~ Eb6:e C6:e G5:e Eb5:e C5:q"
      + " | G5:x Bb5:x Eb6:x Bb5:x G5:x Eb5:x G5:x Bb5:x C6:x Eb6:x C6:x Bb5:x Eb6:h~ | D6:s A5:s F#5:s A5:s C6:s A5:s F#5:s D5:s F#5:q~ A5:q"
      + ` | ${times(4, "D6:s Db6:s C6:s Bb5:s").join(" ")} | ${times(4, "G6:s Gb6:s F6:s Eb6:s").join(" ")}`
      + " | Eb6>F6:q~~ _F6>Eb6:e C6:e Bb5:e G5:e Bb5:q | A5:s C6:s D6:s F#6:s A6:q~ _G6:s F#6:s D6:s C6:s A5:q"
      + " | Bb5:e D6:e G6:e F6:e D6:e Bb5:e G5:q | C6:e Eb6:e G6:e Eb6:e C6:e G5:e Eb5:q"
      + " | Eb6:x G6:x Bb6:x G6:x Eb6:x Bb5:x Eb6:x G6:x Bb6:x G6:x Eb6:x Bb5:x Eb6:h~ | D6:e F#6:e A6:e F#6:e C6:e A5:e F#6>G6:q~"
      + " | _G6>Bb6:w~~ | _Bb6>G6:e Eb6:e C6:e G5:e Eb6:e C6:e G5:e Eb5:e | Bb5:e G5:e Eb5:e G5:e Bb5:e C6:e D6>Eb6:q~ | _Eb6>D6:e C6:e A5:e F#5:e D5:e A4:e D5:q";
    const devil = { kick: "K.KK..K.........", snare: "....S.......S...", hat: "H.H.H.H.H.H.H.H." };
    const devil2 = { kick: "K.KK..K.....K...", snare: "....S.......S..G", hat: "H.H.H.H.H.H.H.O." };
    const haze = { kick: "K..K........K...", snare: "....S.......S...", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const haze2 = { kick: "K..K......K.K...", snare: "....S.......S..G", ride: "Y.Y.Y.Y.Y.Y.Y.Y." };
    const stomp = { kick: "K...K...K...K...", snare: "....S.......S...", hat: "O...O...O...O..." };
    const parts = {
      intro: part(8, fingers("Gm9 Gm9 Cm Cm Gm Cm Eb D", voicing, picking, { contour: [0.6, 1] }),
        lead(`${times(4, "-:w").join(" | ")} | ${figure}`, { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 })),
      storm: part(4, riff(dust, { level: 0.9 }), drums(drummer(crash(devil), devil, devil2, toms))),
      theme: part(8, riff(`${dust} | ${dust}`, { level: 0.85 }), lead(theme, { level: 0.9 }), drums(drummer(crash(devil), devil, devil2, toms, crash(devil), devil, devil2, down))),
      storm2: part(4, riff(dust, { level: 0.9 }), lead(answers, { level: 0.85 }), drums(drummer(crash(devil), devil, devil2, triplets))),
      solo: part(24, riff(`${drive} | ${drive} | ${drive} | ${drive} | ${dust} | ${pound}`, { level: 0.8 }), lead(solo, { contour: [0.85, 0.95, 1.05, 1] }),
        drums(drummer(crash(haze), haze, haze2, toms, haze, haze, haze2, down, crash(haze), haze, haze2, triplets, crash(haze), haze, haze2, toms,
          crash(devil), devil, devil2, thunder, crash(stomp), stomp, crash(stomp), { kick: "K...K...K.K.K.K.", snare: "....S...S.S.SSSS" }))),
      end: part(2, riff("G2^:dh~ -:q | -:w", { level: 0.8 }), fingers("Gm9 Gm9", voicing, ["03241354", "0.3.2.4.1......."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro storm theme storm2 solo storm end" });
  },
  williwaw: () => {
    const bpm = 86;
    const { lead, bassline, riff, fingers, drums, part, record } = stage(bpm, { bend: 0.045, slide: 0.022, echo: 0.75, tail: "G1" });
    const voicing = {
      "C#m": ["C#3", "G#3", "B3", "E4", "G#4", "C#5"], E: ["E3", "B3", "E4", "G#4", "B4", "E5"], "F#": ["F#2", "C#3", "F#3", "A#3", "C#4", "F#4"],
      A: ["A2", "E3", "A3", "C#4", "E4", "A4"], "G#7": ["G#2", "D#3", "F#3", "C4", "D#4", "G#4"],
    };
    const chords = "C#m E F# A";
    const picking = ["02453421", "02453.4.2.1.3.5."];
    const theme = "G#4:q C#5:q E5:e D#5:e C#5:e B4:e | E5:q~ B4:e G#4:e B4:h~ | A#4:q C#5:e F#5:e A#5:q~ G#5:e F#5:e | E5:h~ C#5:q A4:q"
      + " | G#5:q~ E5:e C#5:e E5:e G#5:e B5:q | B5:e G#5:e E5:e B4:e E5:h~ | F#5:e A#5:e C#6:e A#5:e G#5:q F#5:q | E5>F#5:q~ _F#5>E5:e C#5:e G#4:h~";
    const blast = "C#3^:h~ C#3^:e~ -:e C#3,:s C#3,:s C#3,:s C#3,:s | E2^:h~ E2^:e~ -:e E2,:s E2,:s E2,:s E2,:s"
      + " | F#2^:h~ F#2^:e~ -:e F#2,:s F#2,:s F#2,:s F#2,:s | A2^:h~ A2^:e~ -:e A2,:s A2,:s B2,:s B2,:s";
    const drive = ["C#3", "E2", "F#2", "A2"].map((x) => `${x}^:e ${x},:e ${x}^:e ${x},:e ${x}^:e ${x},:e ${x},:s ${x},:s ${x},:s ${x},:s`).join(" | ");
    const calm = "-:q C#5:q~ E5:e D#5:e C#5:q | B4:h~ -:e G#4:e B4:e E5:e | F#5:dq E5:e C#5:e A#4:e C#5:q | E5:e C#5:e B4:e A4:e B4:h~"
      + " | -:e E5:e G#5:e B5:e C#6:q~ B5:q | G#5:e E5:e B4:e E5:e G#5:h~ | A#5:q~ G#5:e F#5:e C#5:e F#5:e A#5:e C#6:e | B5:q~ A5:e E5:e C#5:q -:q";
    const fierce = "B5>C#6:h~~ _C#6>B5:e G#5:e E5:e G#5:e | B5:s G#5:s E5:s G#5:s B5:s E6:s B5:s G#5:s B5:s E6:s G#6:s E6:s B5:q~"
      + " | C#6:e A#5:e F#5:e A#5:e C#6:e F#6:e E6>F#6:q~ | _F#6>E6:e C#6:e B5:e A5:e E5:e A5:e C#6:q"
      + " | E6:x D#6:x C#6:x G#5:x C#6:x D#6:x E6:x D#6:x C#6:x G#5:x E5:x C#5:x E5:h~ | -:e B5:e E6:e G#6:e F#6>G#6:q~ E6:q"
      + " | A#5:s C#6:s E6:s F#6:s F#6>G#6:h~ _G#6>F#6:e C#6:e | E6:s C#6:s A5:s C#6:s E6:s A6:s E6:s C#6:s E6:s G#6:s E6:s G#6:s A6:q~"
      + " | G#6:e~ -:s G#6:s E6:s C#6:s G#6:e~ -:s G#6:s E6:s C#6:s G#6:q | G#6:e~ -:s G#6:s E6:s B5:s G#6:e~ -:s G#6:s E6:s B5:s E6:q"
      + " | A#6:e~ -:s A#6:s F#6:s C#6:s A#6:e~ -:s A#6:s F#6:s C#6:s F#6:q | A6:x G#6:x E6:x C#6:x E6:x G#6:x A6:x G#6:x E6:x C#6:x E6:x G#6:x A6:h~"
      + " | _A6>B6:w~~ | _B6>A6:e G#6:e E6:e B5:e G#5:e B5:e E6:e G#6:e | F#6:e C#6:e A#5:e F#5:e A#5:e C#6:e E6>F#6:q~ | _F#6:e E6:e C#6:e B5:e G#5:e E5:e C#5:e B4:e";
    const rest = (bars) => times(bars, "-:w").join(" | ");
    const gust = { kick: "K.......K.......", snare: "....S.......gGGS", hat: "O...O...O......." };
    const gust2 = { kick: "K.....K.K.......", snare: "....S.......gGGS", hat: "O...O...O......." };
    const roll = { kick: "K.......K.....K.", snare: "....S...g.g.GGGS" };
    const rush = { kick: "K...K...K.......", snare: "....S.......gGGS", ride: "Y.Y.Y.Y.Y.Y....." };
    const rush2 = { kick: "K...K...K.K.....", snare: "....S.......gGGS", ride: "Y.Y.Y.Y.Y.Y....." };
    const hush = { kick: "K.......K.......", ride: "Y...Y...Y...Y..." };
    const clean = { wave: "triangle", level: 1.05, pick: 1.1, share: 0.35 };
    const parts = {
      intro: part(8, fingers("C#m C#m E E F# F# A G#7", voicing, picking, { contour: [0.6, 1] })),
      verse: part(8, fingers(chords, voicing, picking, { level: 0.8 }), lead(theme, clean),
        bassline("C#2:w | E2:w | F#2:w | A1:w | C#2:w | E2:w | F#2:w | A1:h~ -:h", { level: 0.8, contour: [0.5, 1] })),
      blast: part(8, riff(`${blast} | ${blast}`, { level: 0.9 }), lead(theme, { level: 0.9 }), drums(drummer(crash(gust), gust, gust2, gust, crash(gust), gust, gust2, roll))),
      solo: part(24, fingers(`${chords} ${chords} ${times(16, "-").join(" ")}`, voicing, picking, { level: 0.8 }),
        bassline(`C#2:w | E2:w | F#2:w | A1:w | C#2:w | E2:w | F#2:w | A1:h~ -:h | ${rest(16)}`, { level: 0.8 }),
        riff(`${rest(8)} | ${drive} | ${drive} | ${drive} | ${blast}`, { level: 0.8 }),
        lead(`${calm} | ${rest(16)}`, clean), lead(`${rest(8)} | ${fierce}`, { contour: [1, 1, 1, 0.9, 1, 1.05, 1.05, 1] }),
        drums(drummer(times(7, hush), { kick: "K...", ride: "Y...Y...Y......." }, times(16, {})), { level: 0.6 }),
        drums(drummer(times(8, {}), crash(rush), rush, rush2, toms, crash(rush), rush, rush2, triplets, crash(rush), rush, rush2, down, crash(gust), gust, gust2, roll))),
      end: part(2, riff("C#3^:q~ -:dh | -:w", { level: 0.8 }), fingers("C#m C#m", voicing, ["02453421", "0.2.4.5.3......."], { contour: [0.9, 0.6], lanes: 6 }),
        drums(drummer(crash({ kick: "K..." }), {}))),
    };
    return record({ voices: { ...band, ...strings }, parts, form: "intro verse blast solo blast end" });
  },
  pinatubo: () => {
    const bpm = 175;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      Gm: ["G2", "D4", "G4", "A#4"], Eb: ["D#3", "D#4", "G4", "A#4"],
      Bb: ["A#2", "D4", "F4", "A#4"], F: ["F2", "C4", "F4", "A4"],
    };
    const loop = "Gm Eb Bb F";
    const hook = "- - - - - - D5/F5 G5 | . . . A#5 . . G5 . | - - - - - - C5/D5 F5 | . . . A5 . . F5 ."
      + " | - - - - - - D5/F5 G5 | . . . A#5 . . G5 . | - - - - - - D5/F5 A#5 | . . . A5 . . - -";
    const half = "- - - - - - D5/F5 G5 | . . . A#5 . . G5 . | - - - - - - C5/D5 F5 | . . . A5 . . F5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.50) => stab(rave, riff, chords, { level, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const offbeats = pulse(loop, 8, "- x - x - - x .", "- - x . - x - -");
    const bass = "R:dq R:e -:e 8:e R:q | R:e -:e 5:e -:e 8:q R:e N:e";
    const sun = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.O.H.h.H.O." };
    const sun2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.O.H.h.H.Oh" };
    const turn = { kick: "K.........K..k..", snare: "....S..g.g..SgSS", hat: "H.h.H.O.H.h....." };
    const groove = drummer(crash(sun), sun2, sun, sun2, sun, sun2, sun, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . . . - - x' .", "x . . . . . . ."), 0.14, [0.6, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(mist, `${pause(8)} | ${hook}`, { level: 0.24 }), drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "T.T.T.T.T.T.T.T." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }), piano(pulse(loop, 4, "x . . . . . . ."), 0.12),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "G4", "G5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${offbeats} | ${pause(8)} | ${offbeats}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . - x' - - x' .", "x . . . - x' . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.h.H.h.H.h.", shaker: "T.T.T.T.T.T.T.T." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "G4", "G5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${hook.split(" | ").slice(0, 4).join(" | ")} | ${pause(4)}`), hits(`${pause(8)} | ${offbeats} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(sun, sun2, sun, turn, times(3, { hat: "H.h.H.O.H.h.H.O." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . . - - x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("Gm . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - - - D5/F5 G5 | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  bromo: () => {
    const bpm = 174;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      G: ["G2", "D4", "G4", "B4"], D: ["D3", "D4", "F#4", "A4"],
      Em: ["E2", "E4", "G4", "B4"], C: ["C3", "E4", "G4", "C5"],
    };
    const loop = "G D Em C";
    const hook = "- - - - - B4 D5 A5 | . . . . F#5 . E5 . | - - - - - B4 E5 G5 | . . . . E5 . D5 ."
      + " | - - - - - B4 D5 A5 | . . . . F#5 . A5 . | - - - - - B4 E5 B5 | . . . . G5 . - -";
    const half = "- - - - - B4 D5 A5 | . . . . F#5 . E5 . | - - - - - B4 E5 G5 | . . . . E5 . D5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const offbeats = pulse(loop, 8, "- - x . - x - x", "- x - - x . - -");
    const bass = "R:dq 8:e -:e R:e 5:e -:e | R:e -:e R:e -:e 8:e -:e R:e N:e";
    const dawn = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "..T...T...T...T." };
    const dawn2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "..T...T...T...T." };
    const turn = { kick: "K.........K..k..", snare: "....S..g.g..SgSS", hat: "H.h.H.h.H.h.O...", shaker: "..T...T...T....." };
    const groove = drummer(crash(dawn), dawn2, dawn, dawn2, dawn, dawn2, dawn, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . - x' - - x' .", "x . . . - x' . ."), 0.14, [0.6, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(mist, `${pause(8)} | ${hook}`, { level: 0.24 }), drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "..T...T...T...T." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }), piano(pulse(loop, 4, "x . . . . . . ."), 0.12),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "G4", "G5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${offbeats} | ${pause(8)} | ${offbeats}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . . - x' - .", "x . - - x' . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.h.H.h.H.h.", shaker: "..T...T...T...T." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "G4", "G5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${offbeats} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(dawn, dawn2, dawn, turn, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . - x' - - x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("G . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - - B4 D5 A5 | . . . . G5 . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  kilauea: () => {
    const bpm = 177;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      "C#m": ["C#3", "E4", "G#4", "C#5"], A: ["A2", "E4", "A4", "C#5"],
      E: ["E2", "E4", "G#4", "B4"], B: ["B2", "D#4", "F#4", "B4"],
    };
    const loop = "C#m A E B";
    const hook = "C#6 . . B5 . . G#5 . | . . . E5 . . - - | B5 . . G#5 . . E5 . | . . . D#5 . . - -"
      + " | C#6 . . B5 . . G#5 . | . . . E5 . . - - | B5 . . G#5 . . F#5 . | . . . . . . - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.48) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- - x . - x - .", "x' - - x - - - -");
    const bass = "R:dq R:e -:e R:e 8:e -:e | R:e -:e R:e -:e 5:e 8:e -:e N:e";
    const warm = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "..T...T...T...T." };
    const warm2 = { kick: "K.........K..K..", snare: "....S..g.g..S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "..T...T...T...T." };
    const turn = { kick: "K.........K.....", snare: "....S..g.g..SgSS", hat: "H.h.H.h.H.h.O...", shaker: "..T...T...T....." };
    const groove = drummer(crash(warm), warm2, warm, warm2, warm, warm2, warm, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . . . . . x' .", "- - x . . . . ."), 0.13, [0.5, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(bell, `${pause(8)} | ${hook}`, { level: 0.26, ring: 0.75 }), drums(drummer(times(8, {}), times(7, { shaker: "..T...T...T...T.", hat: "H...H...H...H..." }), { shaker: "..T...T...TTTTTT" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${hook}`, { level: 0.28 }),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "E4", "E5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . - x' - - x' .", "x . . . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { shaker: "T.T.T.T.T.T.T.T." }), { shaker: "T.T.T.T.TTTTTTTT" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, hook, { level: 0.24, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "E4", "E5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${hook.split(" | ").slice(0, 4).join(" | ")} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(warm, warm2, warm, turn, times(3, { shaker: "..T...T...T...T." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . . . . x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("C#m . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "C#6 . . B5 . . G#5 . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  mayon: () => {
    const bpm = 178;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      Dm: ["D3", "D4", "F4", "A4"], Bb: ["A#2", "D4", "F4", "A#4"],
      F: ["F2", "C4", "F4", "A4"], C: ["C3", "C4", "E4", "G4"],
    };
    const loop = "Dm Bb F C";
    const hook = "- - - A4 - D5 - F5 | . . . . D5 . C5 . | - - - A4 - C5 - A5 | . . . . G5 . E5 ."
      + " | - - - A4 - D5 - F5 | . . . . D5 . F5 . | - - - C5 - F5 - C6 | . . . . G5 . - -";
    const half = "- - - A4 - D5 - F5 | . . . . D5 . C5 . | - - - A4 - C5 - A5 | . . . . G5 . E5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 1, pump: 0.85 });
    const bed = (level = 0.38) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const bounce = pulse(loop, 8, "- x - x - x - .", "x . - - x . - -");
    const bass = "R:e R:e -:e 8:e -:e R:e 5:e -:e | R:e -:e 8:e -:e R:e -:e 8:e N:e";
    const hop = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.O.H.h.H.O.", shaker: "..T...T...T...T." };
    const hop2 = { kick: "K.....K...K.....", snare: "....S..g....S..g", hat: "H.h.H.O.H.h.H.O.", shaker: "..T...T...T...T." };
    const turn = { kick: "K.........K..k..", snare: "....S..g.g..SgSS", hat: "H.h.H.O.H.h.....", shaker: "..T...T...T....." };
    const groove = drummer(crash(hop), hop2, hop, hop2, hop, hop2, hop, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${bounce} | ${pause(8)} | ${bounce}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . - x' - x' - .", "x . . . . . x' ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.42, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { shaker: "T.T.T.T.T.T.T.T." }), { shaker: "T.T.T.T.TTTTTTTT" })), rise(48, 15)),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.3, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        drums(climb(8), { gains: rolled, contour: [0.7, 1] }), rise(0, 31), sweep(16, 15, "F4", "F5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${bounce} | ${pause(16)}`),
        bed(0.42), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(hop, hop2, hop, turn, times(3, { shaker: "..T...T...T...T.", hat: "H.h.H.O.H.h.H.O." }), {}), { contour: [1, 0.6] }),
        hits(bounce, 0.36, [1, 0.3])),
      end: part(2, stab(rave, "F! . . . . . . . | -*8", chords, { level: 0.4 }), sing(anthem, "- - - C5 - F5 - C6 | . . . . . . - -", { level: 0.4 }),
        drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: desk, parts, form: "drop breakdown build drop2 outro end" });
  },
  fuji: () => {
    const bpm = 172;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      G: ["G2", "D4", "G4", "B4"], A: ["A2", "C#4", "E4", "A4"],
      "F#m": ["F#2", "C#4", "F#4", "A4"], Bm: ["B2", "D4", "F#4", "B4"],
    };
    const loop = "G A F#m Bm";
    const hook = "- - - - D5 . B5 . | . . . . A5 . E5 . | - - - - C#5 . A5 . | . . . . F#5 . D5 ."
      + " | - - - - D5 . B5 . | . . . . A5 . C#6 . | - - - - E5 . C#6 . | . . . . B5 . - -";
    const half = "- - - - D5 . B5 . | . . . . A5 . E5 . | - - - - C#5 . A5 . | . . . . F#5 . D5 .";
    const picking = "G4 - D5 B5 - D5 - - | A4 - E5 C#6 - E5 - - | F#4 - C#5 A5 - C#5 - - | B4 - F#5 D6 - F#5 - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const pluck = (bars, level = 0.4, contour) => sing(spark, Array(bars / 4).fill(picking).join(" | "), { voice: "k4", level, contour, ring: 0.5 });
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- - x . - - x .", "- x - x - - - -");
    const bass = "R:e -:e R:e 8:e -:e R:e -:e 5:e | R:dq -:e R:e 8:e R:q";
    const clear = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.hhH.h.H.hhH.h.", shaker: "..T...T...T...T." };
    const clear2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.hhH.h.H.hhH.O.", shaker: "..T...T...T...T." };
    const turn = { kick: "K.........K.....", snare: "....S..g.gS.SgSS", hat: "H.hhH.h.H.h.....", shaker: "..T...T...T....." };
    const groove = drummer(crash(clear), clear2, clear, clear2, clear, clear2, clear, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, pluck(16, 0.36, [0.3, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, pluck(8, 0.38), stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "D4", "D5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), pluck(16, 0.3), piano(pulse(loop, 16, "x . . . . . . .", "- - - - x' . . ."), 0.18),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.hhH.h.H.hhH.h." }), { hat: "H.hhH.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, pluck(8, 0.36), stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "D4", "D5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(clear, clear2, clear, turn, times(3, { hat: "H.hhH.h.H.hhH.h." }), {}), { contour: [1, 0.6] }),
        pluck(8, 0.34, [1, 0.3]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("G . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - D5 . B5 . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  cotopaxi: () => {
    const bpm = 176;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      Fm: ["F2", "F4", "G#4", "C5"], Db: ["C#3", "F4", "G#4", "C#5"],
      Ab: ["G#2", "D#4", "G#4", "C5"], Eb: ["D#3", "D#4", "G4", "A#4"],
    };
    const big = { ...chords, Fm2: ["F2", "G#4", "C5", "F5"], Db2: ["C#3", "G#4", "C#5", "F5"], Ab2: ["G#2", "G#4", "C5", "D#5"], Eb2: ["D#3", "G4", "A#4", "D#5"] };
    const loop = "Fm Db Ab Eb";
    const riff = "Fm! . - Fm - Fm2 . . | - - Db2 . - Db . . | Ab! . - Ab - Ab2 . . | - - Eb2 . - Eb . .";
    const sky = "- - - - C6 . . . | . . G#5 . F5 . . . | - - - - D#6 . . . | . . C6 . A#5 . . ."
      + " | - - - - C6 . . . | . . G#5 . F5 . . . | - - - - D#6 . . . | . . F6 . D#6 . - -";
    const call = (melody, level = 0.75, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riffs, level = 0.50, contour) => stab(rave, riffs, big, { level, contour, hold: 1.5, pump: 0.85 });
    const bed = (level = 0.36, progression = loop) => pad(progression, chords, { level, swell: 0.3, pump: 0.9 });
    const bass = "R:q -:e R:e -:e R:dq | -:e 8:e R:e -:e R:e 5:e 8:e R:e";
    const peak = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", ride: "..Y...Y...Y...Y." };
    const peak2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.h.H.hhH.h.", ride: "..Y...Y...Y...Y." };
    const turn = { kick: "K.........K.....", snare: "....S..g.gS.SgSS", hat: "H.h.H.h.H.h.....", ride: "..Y...Y...Y....." };
    const groove = drummer(crash(peak), peak2, peak, peak2, peak, peak2, peak, turn);
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const parts = {
      drop: part(32, slam(bass, loop, chords), pound(groove), hits(`${riff} | ${riff} | ${pause(8)} | ${riff} | ${riff} | ${pause(8)}`),
        call(`${pause(8)} | ${sky} | ${pause(8)} | ${sky}`), bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . . . . x' .", "- - x . . . . ."), 0.2),
        sing(bell, `${sky} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${sky}`, 0.44), slam("R:w", loop, chords, { level: 0.42, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.h.H.h.H.h." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build: part(8, stab(gate, `${riff} | ${riff}`, big, { level: 0.26, contour: [0.3, 1], hold: 0.5 }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(0, 31), sweep(16, 15, "G#4", "G#5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), hits(`${riff} | ${riff} | ${pause(8)} | ${riff} | ${riff} | ${riff} | ${riff}`),
        call(`${pause(8)} | ${sky} | ${pause(8)} | ${sky}`), bed(0.40, `${`${loop} `.repeat(6)}- - - - - - - -`), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(peak, peak2, peak, turn, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        hits(`${riff} | ${riff}`, 0.34, [1, 0.3])),
      end: part(2, stab(rave, "Fm2! . . . . . . . | -*8", big, { level: 0.4 }), drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: desk, parts, form: "drop breakdown build drop2 outro end" });
  },
  teide: () => {
    const bpm = 173;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      D: ["D3", "D4", "F#4", "A4"], A: ["A2", "C#4", "E4", "A4"],
      E: ["E2", "E4", "G#4", "B4"], "F#m": ["F#2", "C#4", "F#4", "A4"],
    };
    const loop = "D A E F#m";
    const hook = "D5 . . F#5 . . A5 . | . . . . E5 . C#5 . | E5 . . G#5 . . B5 . | . . . . A5 . F#5 ."
      + " | D5 . . F#5 . . A5 . | . . . . E5 . A5 . | E5 . . G#5 . . C#6 . | . . . . A5 . - -";
    const half = "D5 . . F#5 . . A5 . | . . . . E5 . C#5 . | E5 . . G#5 . . B5 . | . . . . A5 . F#5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "x . . x . . x .", "- - x . - - - -");
    const bass = "R:dq R:dq 8:q | -:e R:e -:e 5:e R:e 8:e -:e R:e";
    const isle = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.hhH.h.H.hhH.h.", ride: "Y...Y...Y...Y..." };
    const isle2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.hhH.h.H.hhH.O.", ride: "Y...Y...Y...Y..." };
    const turn = { kick: "K.........K.....", snare: "....S..g.gS.SgSS", hat: "H.hhH.h.H.h.....", ride: "Y...Y...Y......." };
    const groove = drummer(crash(isle), isle2, isle, isle2, isle, isle2, isle, turn);
    const parts = {
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . x' . . x' .", "x . . . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.42, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.hhH.h.H.hhH.h." }), { hat: "H.hhH.h.H.hhhhhh" })), rise(48, 15)),
      build: part(8, stab(gate, pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-"), chords, { level: 0.26, contour: [0.3, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        drums(climb(8), { gains: rolled, contour: [0.7, 1] }), rise(0, 31), sweep(16, 15, "A4", "A5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(isle, isle2, isle, turn, times(3, { hat: "H.hhH.h.H.hhH.h." }), {}), { contour: [1, 0.6] }),
        hits(answers, 0.36, [1, 0.3])),
      end: part(2, stab(rave, "A! . . . . . . . | -*8", chords, { level: 0.4 }), drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: desk, parts, form: "drop breakdown build drop2 outro end" });
  },
  santorini: () => {
    const bpm = 171;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      Eb: ["D#3", "D#4", "G4", "A#4"], Cm: ["C3", "D#4", "G4", "C5"],
      Ab: ["G#2", "D#4", "G#4", "C5"], Bb: ["A#2", "D4", "F4", "A#4"],
    };
    const loop = "Eb Cm Ab Bb";
    const hook = "- - - - D#5/- G5/- A#5 . | . . . . G5 . D#5 . | - - - - D#5/- G#5/- C6 . | . . . . A#5 . F5 ."
      + " | - - - - D#5/- G5/- A#5 . | . . . . G5 . C6 . | - - - - D#5/- G#5/- C6 . | . . . . D6 . - -";
    const half = "- - - - D#5/- G5/- A#5 . | . . . . G5 . D#5 . | - - - - D#5/- G#5/- C6 . | . . . . A#5 . F5 .";
    const picking = "A#4 - D#5 G5 - D#5 - - | G4 - C5 D#5 - C5 - - | G#4 - C5 D#5 - C5 - - | A#4 - D5 F5 - D5 - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const pluck = (bars, level = 0.42, contour) => sing(spark, Array(bars / 4).fill(picking).join(" | "), { voice: "k4", level, contour, ring: 0.5 });
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- - x . - x - -", "x . - - - - x .");
    const bass = "R:q -:e R:e 8:e -:e R:q | R:e -:e 5:e R:e -:e 8:e R:e -:e";
    const dusk = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "T...T...T...T..." };
    const dusk2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "T...T...T...T..." };
    const turn = { kick: "K.........K..k..", snare: "....S..g.g..SgSS", hat: "H.h.H.h.H.h.O...", shaker: "T...T...T......." };
    const groove = drummer(crash(dusk), dusk2, dusk, dusk2, dusk, dusk2, dusk, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, pluck(16, 0.38, [0.3, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "T...T...T...T..." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, pluck(8, 0.4), stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "D#4", "D#5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), pluck(16, 0.3), piano(pulse(loop, 16, "x . . . . . . .", "- - x' . . . . ."), 0.18),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.h.H.h.H.h.", shaker: "T...T...T...T..." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, pluck(8, 0.38), stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "D#4", "D#5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(dusk, dusk2, dusk, turn, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        pluck(8, 0.34, [1, 0.3]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("Eb . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - D#5/- G5/- A#5 . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  arenal: () => {
    const bpm = 170;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "G1" });
    const chords = {
      F: ["F2", "C4", "F4", "A4"], G: ["G2", "D4", "G4", "B4"],
      Am: ["A2", "E4", "A4", "C5"], C: ["C3", "E4", "G4", "C5"],
    };
    const loop = "F G Am C";
    const hook = "- - - - C5/F5 A5 . . | . . . . G5 . D5 . | - - - - C5/E5 A5 . . | . . . . G5 . E5 ."
      + " | - - - - C5/F5 A5 . . | . . . . B5 . G5 . | - - - - E5/A5 C6 . . | . . . . . . - -";
    const half = "- - - - C5/F5 A5 . . | . . . . G5 . D5 . | - - - - C5/E5 A5 . . | . . . . G5 . E5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- x - - x - x .", "- - - x - - - -");
    const bass = "R:dh 8:q | R:e -:e R:e 5:e -:e 8:e R:q";
    const glow = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.hhH.h.H.hh", shaker: "T...T...T...T..." };
    const glow2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "T...T...T...T..." };
    const turn = { kick: "K.........K.....", snare: "....S..g.gS.SgSS", hat: "H.h.H.hhH.h.....", shaker: "T...T...T......." };
    const groove = drummer(crash(glow), glow2, glow, glow2, glow, glow2, glow, turn);
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . . . x' . . .", "x . . x' . . x' ."), 0.14, [0.6, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(grand, `${pause(8)} | ${hook}`, { voice: "k4", level: 0.2, ring: 0.5 }), drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "T...T...T...T..." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }), piano(pulse(loop, 4, "x . . . . . . ."), 0.12),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "C4", "C5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . . x' . . .", "x . - x' - - x' ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.hhH.h.H.hh" }), { hat: "H.h.H.hhH.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "C4", "C5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(glow, glow2, glow, turn, times(3, { hat: "H.h.H.hhH.h.H.hh" }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . . x' . . ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("C . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - E5/A5 C6 . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: desk, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  surtsey: () => {
    const bpm = 176;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm, { tail: "A1" });
    const chords = {
      "G#m": ["G#2", "D#4", "G#4", "B4"], E: ["E2", "E4", "G#4", "B4"],
      B: ["B2", "D#4", "F#4", "B4"], "F#": ["F#2", "C#4", "F#4", "A#4"],
    };
    const loop = "G#m G#m E E B B F# F#";
    const hook = "- - - D#5 D#5 . D#5 F#5 | . . . . D#5 . B4 . | - - - D#5 D#5 . D#5 G#5 | . . . . F#5 . B4 ."
      + " | - - - D#5 D#5 . D#5 F#5 | . . . . D#5 . B4 . | - - - C#5 C#5 . C#5 A#5 | . . . . . . - -";
    const half = "- - - D#5 D#5 . D#5 F#5 | . . . . D#5 . B4 . | - - - D#5 D#5 . D#5 G#5 | . . . . F#5 . B4 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- x - x - - x .", "- - x . - x - x");
    const bass = "R:q R:e -:e R:e 8:e -:e R:e | -:e R:e -:e 5:e R:e -:e 8:e N:e";
    const surf = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.O.H.h.H.O.", ride: "Y...Y...Y...Y..." };
    const surf2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.O.H.h.H.Oh", ride: "Y...Y...Y...Y..." };
    const turn = { kick: "K.........K..k..", snare: "....S..g.g..SgSS", hat: "H.h.H.O.H.h.....", ride: "Y...Y...Y......." };
    const groove = drummer(crash(surf), surf2, surf, surf2, surf, surf2, surf, turn);
    const parts = {
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . . . . x' .", "- - x' . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.42, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.O.H.h.H.O." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build: part(8, stab(gate, pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-"), chords, { level: 0.26, contour: [0.3, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        drums(climb(8), { gains: rolled, contour: [0.7, 1] }), rise(0, 31), sweep(16, 15, "B3", "B4")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(surf, surf2, surf, turn, times(3, { hat: "H.h.H.O.H.h.H.O." }), {}), { contour: [1, 0.6] }),
        hits(answers, 0.36, [1, 0.3])),
      end: part(2, stab(rave, "B! . . . . . . . | -*8", chords, { level: 0.4 }), sing(anthem, "- - - D#5 D#5 . D#5 F#5 | . . . . . . - -", { level: 0.4 }),
        drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: desk, parts, form: "drop breakdown build drop2 outro end" });
  },
  tambora: () => {
    const bpm = 174;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Db: ["C#3", "C#4", "F4", "G#4"], Eb: ["D#3", "D#4", "G4", "A#4"], Fm: ["F2", "C4", "F4", "G#4"],
      Ab: ["G#2", "C4", "D#4", "G#4"], C: ["C3", "C4", "E4", "G4"], Bbm: ["A#2", "C#4", "F4", "A#4"],
    };
    const horns = {
      Db: ["C#3", "F4", "G#4", "C#5"], Eb: ["D#3", "G4", "A#4", "D#5"], Fm: ["F2", "G#4", "C5", "F5"],
      Ab: ["G#2", "G#4", "C5", "D#5"], C: ["C3", "G4", "C5", "E5"],
    };
    const loop = "Db Eb Fm Fm Db Eb Ab C";
    const verse = "Fm Fm Db Db Bbm Bbm C C";
    const hook = "- - - - C5 . F5 G#5 | . . . . G5 . D#5 . | - - - - C5 . F5 C6 | . . . . A#5 . G#5 ."
      + " | - - - - C5 . F5 G#5 | . . . . G5 . A#5 . | - - - - D#5 . G#5 C6 | . . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const line = "- - C5~ . . . G#4 . | G4 . . . C5 . - - | - - C#5~ . . . C5 . | G#4 . . . C#5 . - -"
      + " | - - C#5 . . . F5~ . | . . . . D#5 . F5 . | - - C5 . . . E5~ . | . . . . . . - -";
    const call = (melody, level = 0.8, contour) => [sing(soar, melody, { level, contour, pump: 0.7 }),
      sing(soar, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const singer = (melody, level = 0.3, contour) => sing(vox, melody, { voice: "vox", level, contour, ring: 0.5 });
    const riff = (cells, level = 0.46) => stab(gated, cells, chords, { level, hold: 0.75, pump: 0.85 });
    const horn = (cells, level = 0.42) => stab(brass, cells, horns, { level, hold: 1.5, pump: 0.6, prefix: "b" });
    const bed = (level = 0.38) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (progression, bars, level, contour, ...cells) => stab(keys, pulse(progression, bars, ...cells), chords, { level, contour, prefix: "k" });
    const gates = pulse(loop, 8, "x . - x/x - - x .", "- x - x/- - x . .");
    const fanfare = pulse(loop, 8, "x . . x - x . .", "- - x . . - x' .");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:dq -:e -:e R:e 5:e -:e | R:q -:e R:e -:e R:e 8:e N:e";
    const lope = "R:dq -:e -:e R:e -:q | R:h -:e R:e -:q";
    const drive = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h." };
    const drive2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O." };
    const fill = { kick: "K.........K.....", snare: "....S..gS.SgSSSS", hat: "H.h.H.h........." };
    const ridden = { ...drive, hat: "..h...h...h...h.", ride: "Y...Y...Y...Y..." };
    const ridden2 = { ...drive2, hat: "..h...h...h...O.", ride: "Y...Y...Y...Y..." };
    const sung = [crash(drive), drive2, drive, drive2, crash(drive), drive2, drive, fill];
    const riffed = [crash(ridden), ridden2, ridden, ridden2, crash(ridden), ridden2, ridden, fill];
    const groove = drummer(sung, riffed, sung, riffed);
    const slow = { kick: "K.........K.....", snare: "........S.......", hat: "H...H.h.H...H.h." };
    const slow2 = { kick: "K.........K..k..", snare: "........S.....g.", hat: "H...H.h.H...H.hh" };
    const ascent = drummer(
      times(4, slow),
      times(2, { kick: "K...K...K...K...", snare: "S...S...S...S...", hat: "..h...h...h...h." }),
      { kick: "K...K...K...K...", snare: "S.S.S.S.S.S.S.S.", hat: "..O...O...O...O." },
      { kick: "K...K...K.......", snare: "S.S.S.S.S.S.....", roll: ".G.G.G.G.G.G...." },
    );
    const parts = {
      intro: part(16, piano(verse, 16, 0.14, [0.5, 1], "x . . x' . . x' .", "x . . . . . - -"), pad(verse, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        singer(`${pause(8)} | ${line}`), drums(drummer(times(8, {}), times(3, [slow, slow2]), slow, { ...slow2, snare: "........S...SgSS" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.24, contour: [0.2, 1] }), singer(`${half} | ${pause(4)}`, 0.3),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(ascent, { gains: { roll: 3 }, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "F4", "F5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(), fall(0, 8)),
      verse: part(8, slam(lope, verse, chords, { level: 0.6 }), drums(drummer(crash(slow), slow2, slow, slow2, slow, slow2, slow, { ...slow2, snare: "........S...SgSS" })),
        singer(line, 0.34), piano(verse, 8, 0.12, [1, 1], "- - x' . - - x' .", "- x' . . - - x' .")),
      breakdown: part(8, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(loop, 8, 0.18, [1, 1], "x . . x' . . x' .", "x . . . . . . ."),
        sing(vox, hook, { voice: "vox", level: 0.36, ring: 0.75 }), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(7, {}), { hat: "H.h.H.h.H.hhhhhh" })), rise(16, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(soar, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(ascent, { gains: { roll: 3 }, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "F4", "F5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${fanfare} | ${pause(8)} | ${fanfare}`),
        bed(0.42), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(drive, drive2, drive, fill, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        piano(loop, 8, 0.13, [1, 0.5], "x . . x' . . x' ."), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, stab(keys, "Fm . . . . . . . | -*8 | -*8 | -*8", chords, { level: 0.16, prefix: "k" }), singer("- - - - C5 . F5 G#5~ | . . . . . . . . | -*8 | -*8", 0.3),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro build drop verse breakdown build2 drop2 outro end" });
  },
  stromboli: () => {
    const bpm = 171;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      B: ["B2", "D#4", "F#4", "B4"], "C#": ["C#3", "C#4", "F4", "G#4"], "D#m": ["D#3", "D#4", "F#4", "A#4"],
      "F#": ["F#2", "C#4", "F#4", "A#4"], "G#m": ["G#2", "D#4", "G#4", "B4"], "A#": ["A#2", "D4", "F4", "A#4"],
    };
    const big = {
      ...chords, Bt: ["B2", "F#4", "B4", "D#5"], "C#t": ["C#3", "F4", "G#4", "C#5"], "C#f": ["C#3", "G#4", "C#5", "F5"],
      "D#mt": ["D#3", "F#4", "A#4", "D#5"], "D#mf": ["D#3", "A#4", "D#5", "F#5"], "F#t": ["F#2", "F#4", "A#4", "C#5"], "F#f": ["F#2", "A#4", "C#5", "F#5"],
    };
    const loop = "B C# D#m D#m B C# F# F#";
    const dark = "D#m D#m B B G#m G#m A# A#";
    const riff = "Bt! . - Bt/Bt - B . . | - - C#t . - C#f . . | D#mt! . - D#mt/D#mt - D#mf . . | - - D#mt . - D#m . ."
      + " | Bt! . - Bt/Bt - B . . | - - C#t . - C#f . . | F#t! . - F#t/F#t - F#f . . | - - F#f . - - - -";
    const sky = "- - F#5 . . . . . | . . . . F5 . . . | - - F#5 . . . . . | A#5 . . . . . . . | - - D#5 . . . . . | . . . . F5 . . . | - - F#5 . . . . . | . . . . . . - -";
    const glow = "- - A#5 . . . F#5 . | . . . . D#5 . - - | - - B5 . . . F#5 . | . . . . D#5 . - - | - - B5 . . . G#5 . | . . . . D#5 . - - | - - F5 . . . A#5 . | . . . . . . - -";
    const call = (melody, level = 0.74, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const riffs = (line, level = 0.5, contour, hold = 1.5) => stab(gated, line, big, { level, contour, hold, pump: 0.85 });
    const bed = (level = 0.36) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(keys, riff, chords, { level, contour, prefix: "k" });
    const bass = "R:dq R:e -:e 8:e -:e R:e | -:e R:e -:e R:e 5:e -:e R:q";
    const fire = { kick: "K.........K.....", snare: "....S.......S...", hat: "..h...h...h...h.", ride: "Y...Y...Y...Y..." };
    const fire2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "..h...h...h...O.", ride: "Y...Y...Y...Y..." };
    const calm = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h." };
    const calm2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O." };
    const fill = { kick: "K.........K.....", snare: "....S...S.S.SSSS", ride: "Y...Y...Y......." };
    const out = { kick: "K.........K....." };
    const chords8 = [crash(fire), fire2, fire, fire2, fire, fire2, fire, fill];
    const lead8 = [crash(calm), calm2, calm, calm2, calm, calm2, calm, out];
    const groove = drummer(chords8, lead8, chords8, lead8);
    const groove2 = drummer(chords8, lead8, chords8, chords8);
    const parts = {
      drop: part(32, slam(bass, loop, chords), pound(groove), riffs(`${riff} | ${pause(8)} | ${riff} | ${pause(8)}`), call(`${pause(8)} | ${sky} | ${pause(8)} | ${sky}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(dark, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(dark, 16, "x . . . - x' . .", "x . . x' . . . ."), 0.2),
        sing(bell, `${glow} | ${glow}`, { level: 0.32, ring: 0.75 }), slam("R:w", dark, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.h.H.h.H.h.H.h." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build: part(8, riffs(riff, 0.26, [0.3, 1], 0.5), sing(mist, `${sky.split(" | ").slice(0, 4).join(" | ")} | ${pause(4)}`, { level: 0.3 }),
        drums(climb(8), { gains: rolled, contour: [0.7, 1] }), rise(0, 31), sweep(16, 15, "F#4", "F#5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove2), riffs(`${riff} | ${pause(8)} | ${riff} | ${riff}`), call(`${pause(8)} | ${sky} | ${pause(16)}`),
        bed(0.4), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(fire, fire2, fire, fill, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        riffs(riff, 0.34, [1, 0.3])),
      end: part(2, stab(rave, "F#f! . . . . . . . | -*8", big, { level: 0.4 }), drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: hall, parts, form: "drop breakdown build drop2 outro end" });
  },
  merapi: () => {
    const bpm = 170;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = { Am: ["A2", "E4", "A4", "C5"], G: ["G2", "D4", "G4", "B4"], F: ["F2", "C4", "F4", "A4"] };
    const loop = "Am G F G";
    const hook = "E5 ./D5 C5 . . . - - | - - D5 ./B4 G4 . . . | A5 ./G5 F5 . . . - - | - - G5 ./D5 B4 . . ."
      + " | E5 ./D5 C5 . . . - - | - - D5 ./B4 D5 . G5 . | A5 ./G5 F5 . . . C6 . | . . . . B5 . . .";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const riff = (cells, level = 0.44) => stab(gated, cells, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const gates = pulse(loop, 8, "x/x - - x . - x .", "- - x/x - x . - -");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:dq -:e R:e 8:e -:q | R:e R:e -:e 5:e -:e R:e 8:e -:e";
    const smoke = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.hhH.h." };
    const smoke2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.h.H.h.H.O." };
    const ghosts = { ...smoke, roll: "............g.gg" };
    const turn = { kick: "K.........K.....", snare: "....S..g....SgSS", hat: "H.h.H.h.H.h....." };
    const fill = { kick: "K.........K.....", snare: "....S.SgS.SgSSSS", hat: "H.h.H.h........." };
    const eight = (start, end) => [start, smoke2, smoke, smoke2, smoke, smoke2, smoke, end];
    const groove = drummer(eight(crash(smoke), ghosts), eight(smoke, turn), eight(crash(smoke), ghosts), eight(smoke, fill));
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . . x' . . - -", "x . . . - - x' ."), 0.14, [0.6, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(mist, `${pause(8)} | ${hook}`, { level: 0.24 }), drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "T.T.T.T.T.T.T.T." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }), piano(pulse(loop, 4, "x . . . . . . ."), 0.12),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "A4", "A5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . x' . . - -", "x . . . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.46), slam("R:w", loop, chords, { level: 0.42, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.hhH.hhH.hhH.hh" }), { hat: "H.hhH.hhH.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "A4", "A5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(smoke, smoke2, smoke, fill, times(3, { hat: "H.hhH.hhH.hhH.hh" }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . x' . . - -"), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("Am . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "E5 ./D5 C5 . . . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  etna: () => {
    const bpm = 176;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Em: ["E2", "E4", "G4", "B4"], C: ["C3", "C4", "E4", "G4"], Am: ["A2", "C4", "E4", "A4"], B: ["B2", "D#4", "F#4", "B4"],
      D: ["D3", "D4", "F#4", "A4"], E: ["E2", "E4", "G#4", "B4"], A: ["A2", "C#4", "E4", "A4"],
    };
    const horns = {
      C: ["C3", "G4", "C5", "E5"], D: ["D3", "A4", "D5", "F#5"], E: ["E2", "G#4", "B4", "E5"],
      A: ["A2", "A4", "C#5", "E5"], B: ["B2", "F#4", "B4", "D#5"],
    };
    const verse = "Em Em C C Am Am B B";
    const loop = "C D E E C D A B";
    const hook = "G5 . . . . . E5/F#5 G5 | . . . . F#5 . D5 . | G#5 . . . . . . . | . . . . B5 . G#5 ."
      + " | G5 . . . . . E5/F#5 G5 | . . . . B5 . F#5 . | E5 . . . . . C#5/D#5 E5 | . . . . B5 . F#5 .";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const line = "- - B4 . . . G4 . | A4 . . . B4 . - - | - - E5~ . . . B4 . | G4 . . . E4 . - -"
      + " | - - A4 . . . C5 . | E5~ . . . . . - - | - - D#5 . . . F#5 . | . . . . - - - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const singer = (melody, level = 0.32, contour) => sing(vox, melody, { voice: "vox", level, contour, ring: 0.5 });
    const hits = (riff, level = 0.48) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const horn = (cells, level = 0.42) => stab(brass, cells, horns, { level, hold: 1.5, pump: 0.6, prefix: "b" });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(keys, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- x - - x . - -", "- - x . - x - x");
    const fanfare = pulse(loop, 8, "x . . . - x . .", "- - x . x . . .");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:q -:e R:e -:e R:e 8:e -:e | R:dq -:e R:e -:e 5:e N:e";
    const lope = "R:dh -:e R:e | R:h -:h";
    const live = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.hhH.h.H.hhH.h." };
    const live2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.hhH.h.H.hhH.O." };
    const fill = { kick: "K.........K.....", snare: "....S..gSgS.SSSS", hat: "H.hhH.h........." };
    const ridden = { ...live, hat: "..h...h...h...h.", ride: "Y...Y...Y...Y..." };
    const ridden2 = { ...live2, hat: "..h...h...h...O.", ride: "Y...Y...Y...Y..." };
    const close = (bar) => ({ ...bar, snare: "....S..g....SgSS" });
    const lead8 = [crash(live), live2, crash(live), close(live2), crash(live), live2, crash(live), close(live)];
    const stab8 = (end) => [ridden, ridden2, ridden, close(ridden2), ridden, ridden2, ridden, end];
    const groove = drummer(lead8, stab8(close(ridden)), lead8, stab8(fill));
    const slow = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H...H.h.H..." };
    const slow2 = { kick: "K.......K.K.....", snare: "........S.......", hat: "H.h.H...H.h.H.h." };
    const burst = { kick: "K.......K.......", snare: "..S.S.S.SSSS....", roll: ".G.G.G.........." };
    const parts = {
      intro: part(8, piano(pulse(verse, 8, "x . . . - - x' .", "x . . . . . . ."), 0.13, [0.4, 1]), pad(verse, chords, { wave: "triangle", level: 0.08, contour: [0, 1] }),
        slam("R:w", verse, chords, { level: 0.25, contour: [0, 0.8] })),
      verse: part(8, singer(line), piano(pulse(verse, 8, "- - x' . - - x' .", "x . . . - - - -"), 0.12), slam(lope, verse, chords, { level: 0.55 }),
        drums(drummer(crash(slow), slow2, slow, slow2, slow, slow2, slow, burst), { gains: rolled }), rise(24, 7)),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . x' . . - -", "x . . . . . x' ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.hhH.h.H.hhH.h." }), { hat: "H.hhH.h.H.hhhhhh" })), rise(48, 15)),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "E4", "E5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${fanfare} | ${pause(8)} | ${fanfare}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(live, live2, live, fill, times(3, { hat: "H.hhH.h.H.hhH.h." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . . - - x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("E . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "G#5 . . . . . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro verse drop breakdown build drop2 outro end" });
  },
  krakatoa: () => {
    const bpm = 178;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      B: ["B2", "D#4", "F#4", "B4"], "F#": ["F#2", "C#4", "F#4", "A#4"], "G#m": ["G#2", "D#4", "G#4", "B4"], "D#m": ["D#3", "D#4", "F#4", "A#4"],
      E: ["E2", "E4", "G#4", "B4"], "C#m": ["C#3", "E4", "G#4", "C#5"], "D#": ["D#3", "D#4", "G4", "A#4"],
    };
    const horns = {
      B: ["B2", "F#4", "B4", "D#5"], "F#": ["F#2", "F#4", "A#4", "C#5"], "G#m": ["G#2", "G#4", "B4", "D#5"],
      "D#m": ["D#3", "F#4", "A#4", "D#5"], E: ["E2", "G#4", "B4", "E5"],
    };
    const gloom = "G#m G#m E E C#m C#m D# D#";
    const loop = "B F# G#m D#m E B E F#";
    const hook = "- B4 . B5 . . . . | . . . . A#5 . F#5 . | - G#4 . G#5 . . . . | . . . . F#5 . D#5 ."
      + " | - B4 . B5 . . . . | . . . . . . D#5 F#5 | G#5 . . . . . E5 . | F#5 . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const omen = "- G#4 . G#5 . . . . | . . . . D#5 . B4 . | - B4 . B5 . . . . | . . . . G#5 . E5 ."
      + " | - C#5 . C#6 . . . . | . . . . G#5 . E5 . | - A#4 . A#5 . . . . | . . . . G5 . D#5 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const horn = (cells, level = 0.44, contour) => stab(brass, cells, horns, { level, contour, hold: 1.5, pump: 0.6, prefix: "b" });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const blasts = pulse(loop, 8, "x . . x . . - -", "- - x . . . x .");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:e -:e R:e 8:e -:e R:e -:e R:e | R:dq -:e R:e 5:e -:q";
    const boom = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "..T...T...T...T." };
    const boom2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "..T...T...T...T." };
    const fill = { kick: "K.........K.....", snare: "....S..g.gSSgSSS", hat: "H.h.H.h.H.h.....", shaker: "..T...T........." };
    const big = { ...crash(boom), hat: "O.h.H.h.H.h.H.h." };
    const two = { kick: "K.........K.....", snare: "....S...S.SgSSSS", hat: "H.h.H.h.........", shaker: "..T...T........." };
    const gap = { kick: "K.........K.....", shaker: "..T...T...T...T." };
    const eight = (end) => [big, boom2, boom, boom2, boom, boom2, boom, end];
    const groove = drummer(eight(two), eight(gap), eight(two), eight(fill));
    const parts = {
      intro: part(16, piano(pulse(gloom, 16, "x . . . - x' . .", "x . . . . . . ."), 0.14, [0.5, 1]), pad(gloom, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(bell, `${pause(8)} | ${omen}`, { level: 0.26, ring: 0.75 }),
        drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "..T...T...T...T." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "B3", "B4")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${blasts} | ${pause(8)} | ${blasts}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . . - x' . .", "x . . x' . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { shaker: "T.T.T.T.T.T.T.T." }), { shaker: "T.T.T.T.TTTTTTTT" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "B3", "B4")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${blasts} | ${pause(8)} | ${blasts}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(boom, boom2, boom, fill, times(3, { shaker: "..T...T...T...T." }), {}), { contour: [1, 0.6] }),
        horn(blasts, 0.3, [1, 0.3]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("B . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- B4 . B5 . . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  hekla: () => {
    const bpm = 174;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Gm: ["G2", "D4", "G4", "A#4"], Eb: ["D#3", "D#4", "G4", "A#4"], Cm: ["C3", "C4", "D#4", "G4"], D: ["D3", "D4", "F#4", "A4"],
      G: ["G2", "D4", "G4", "B4"], Bm: ["B2", "D4", "F#4", "B4"], C: ["C3", "C4", "E4", "G4"],
    };
    const verse = "Gm Gm Eb Eb Cm Cm D D";
    const loop = "G Bm C Cm";
    const hook = "- D6 . C6 B5 . . . | . . . . - - F#5 A5 | G5 . . . . . E5 . | D#5 . . . . . - -"
      + " | - D6 . C6 B5 . . . | . . . . - - F#5 B5 | C6 . . . . . E5 . | D#5 . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const line = "- - D5 . . . A#4 . | C5 . . . D5 . - - | - - G5~ . . . D#5 . | D5 . . . A#4 . - -"
      + " | - - C5 . . . D#5 . | G5~ . . . . . - - | - - F#5 . . . D5 . | A4 . . . - - - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const singer = (melody, level = 0.32, contour) => sing(vox, melody, { voice: "vox", level, contour, ring: 0.5 });
    const hits = (riff, level = 0.48) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "- - x . - - x .", "x . - x - - - -");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:h -:e R:e 8:q | R:e -:e R:e -:e R:e 5:e N:q";
    const lope = "R:h -:q R:q | R:dh -:q";
    const ash = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "T...T...T...T..." };
    const ash2 = { kick: "K.........K..K..", snare: "....S..g.g..S..g", hat: "H.h.H.hhH.h.H.O.", shaker: "T...T...T...T..." };
    const fill = { kick: "K.........K.....", snare: "....S..gS.SgS.SS", hat: "H.h.H.h.H.h.....", shaker: "T...T...T......." };
    const halftime = { kick: "K.........K.....", snare: "........S.......", roll: "..........g.gggg", hat: "H...H...H...H...", shaker: "T...T...T...T..." };
    const eight = (end) => [crash(ash), ash2, ash, ash2, ash, ash2, ash, end];
    const groove = drummer(eight(halftime), eight(halftime), eight(halftime), eight(fill));
    const slow = { kick: "K.........K.....", snare: "........S.......", hat: "H...H...H...H.h." };
    const slow2 = { kick: "K.........K..k..", snare: "........S.....g.", hat: "H...H...H...H.hh" };
    const ascent = drummer(
      times(4, slow),
      times(2, { kick: "K...K...K...K...", snare: "S...S...S...S...", hat: "..h...h...h...h." }),
      { kick: "K...K...K...K...", snare: "S.S.S.S.S.S.S.S.", hat: "..O...O...O...O." },
      { kick: "K...K...K.......", snare: "S.S.S.S.S.S.....", roll: ".G.G.G.G.G.G...." },
    );
    const parts = {
      intro: part(8, piano(pulse(verse, 8, "x . . . . . x' .", "- - x . . . . ."), 0.14, [0.4, 1]), pad(verse, chords, { wave: "triangle", level: 0.08, contour: [0, 1] })),
      verse: part(8, singer(line), piano(pulse(verse, 8, "x . - - x' . - -", "- - x' . . . - -"), 0.12), slam(lope, verse, chords, { level: 0.55 }),
        drums(drummer(crash(slow), slow2, slow, slow2, slow, slow2, slow, { ...slow2, snare: "........S...SgSS" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.24, contour: [0.2, 1] }), singer(`${half} | ${pause(4)}`, 0.3),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(ascent, { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "G4", "G5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . - x' - - x' .", "x . . . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { shaker: "T.T.T.T.T.T.T.T." }), { shaker: "T.T.T.T.TTTTTTTT" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "G4", "G5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), hits(`${pause(8)} | ${answers} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(ash, ash2, ash, fill, times(3, { shaker: "T...T...T...T..." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . - x' - - x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("G . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- D6 . C6 B5 . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro verse build drop breakdown build2 drop2 outro end" });
  },
  ruapehu: () => {
    const bpm = 175;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Dm: ["D3", "D4", "F4", "A4"], Bb: ["A#2", "D4", "F4", "A#4"], Gm: ["G2", "D4", "G4", "A#4"], A: ["A2", "C#4", "E4", "A4"],
      F: ["F2", "C4", "F4", "A4"], Eb: ["D#3", "D#4", "G4", "A#4"],
    };
    const horns = { F: ["F2", "A4", "C5", "F5"], Eb: ["D#3", "G4", "A#4", "D#5"], Bb: ["A#2", "F4", "A#4", "D5"] };
    const verse = "Dm Dm Bb Bb Gm Gm A A";
    const loop = "F Eb Bb F";
    const hook = "A5 . - A5 - C6 . . | . . . . A#5 . G5 . | F5 . - F5 - A#5 . . | . . . . A5 . . ."
      + " | A5 . - A5 - C6 . . | . . . . A#5 . C6 . | D6 . - D6 - C6 . . | . . . . A5 . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const line = "- - A4 . . . D5 . | F5 . . . E5 . - - | - - D5~ . . . A#4 . | F4 . . . A#4 . - -"
      + " | - - G4 . . . A#4 . | D5~ . . . . . - - | - - C#5 . . . E5 . | A4 . . . - - - -";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const singer = (melody, level = 0.32, contour) => sing(vox, melody, { voice: "vox", level, contour, ring: 0.5 });
    const hits = (riff, level = 0.5) => stab(rave, riff, chords, { level, hold: 0.75, pump: 0.85 });
    const horn = (cells, level = 0.42) => stab(brass, cells, horns, { level, hold: 1.5, pump: 0.6, prefix: "b" });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(keys, riff, chords, { level, contour, prefix: "k" });
    const answers = pulse(loop, 8, "x . - x - - x .", "- x . . - x - -");
    const punches = pulse(loop, 8, "x . x . - - x .", "- x . . x . - -");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:e R:e -:e R:e -:e R:e 8:e R:e | R:q -:e R:e 5:q 8:q";
    const lope = "R:q -:q R:e -:e R:q | R:h -:e R:e -:q";
    const rock = { kick: "K.........K.....", snare: "....S.......S...", hat: "..h...h...h...h.", ride: "Y...Y...Y...Y..." };
    const rock2 = { kick: "K.........K..K..", snare: "....S..g....S..g", hat: "..h...h...h...O.", ride: "Y...Y...Y...Y..." };
    const four = { ...rock2, snare: "....S...R.RgSgSS", ride: "Y...Y..........." };
    const fill = { kick: "K.........K.....", snare: "....SgSgS.SgSSSS", hat: "H.h.H.h.........", cym: "..............C." };
    const eight = (start) => [start, rock2, rock, four, rock, rock2, rock, fill];
    const groove = drummer(eight(crash(rock)), eight(rock), eight(rock), eight(rock));
    const slow = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const slow2 = { kick: "K.....K...K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.hh" };
    const ascent = drummer(
      times(4, slow),
      times(2, { kick: "K...K...K...K...", snare: "S...S...S...S...", ride: "Y...Y...Y...Y..." }),
      { kick: "K...K...K...K...", snare: "S.S.S.S.S.S.S.S.", hat: "..O...O...O...O." },
      { kick: "K...K...K.......", snare: "S.S.S.S.S.S.....", roll: ".G.G.G.G.G.G...." },
    );
    const parts = {
      verse: part(8, singer(line), piano(pulse(verse, 8, "x . . x' . . - -", "- - x' . - - x' ."), 0.13, [0.6, 1]), slam(lope, verse, chords, { level: 0.55, contour: [0.5, 1] }),
        drums(drummer(crash(slow), slow2, slow, slow2, slow, slow2, slow, { ...slow2, snare: "........S...SgSS" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.24, contour: [0.2, 1] }), singer(`${half} | ${pause(4)}`, 0.3),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(ascent, { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "F4", "F5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), hits(`${pause(8)} | ${answers} | ${pause(8)} | ${answers}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . x' . . - -", "x . . . . . . ."), 0.2),
        sing(vox, `${hook} | ${pause(8)}`, { voice: "vox", level: 0.36, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { ride: "Y...Y...Y...Y..." }), { hat: "H.h.H.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "F4", "F5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${punches} | ${pause(8)} | ${punches}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(rock, rock2, rock, fill, times(3, { ride: "Y...Y...Y...Y..." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . x' . . - -"), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(2, stab(brass, "F! . . . . . . . | -*8", horns, { level: 0.4, prefix: "b" }), drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: hall, parts, form: "verse build drop breakdown build2 drop2 outro end" });
  },
  erebus: () => {
    const bpm = 177;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Bm: ["B2", "D4", "F#4", "B4"], G: ["G2", "D4", "G4", "B4"], Em: ["E2", "E4", "G4", "B4"], "F#": ["F#2", "C#4", "F#4", "A#4"],
      D: ["D3", "D4", "F#4", "A4"], A: ["A2", "C#4", "E4", "A4"],
    };
    const horns = { D: ["D3", "F#4", "A4", "D5"], G: ["G2", "G4", "B4", "D5"], Bm: ["B2", "F#4", "B4", "D5"], A: ["A2", "E4", "A4", "C#5"] };
    const verse = "Bm Bm G G Em Em F# F#";
    const loop = "D G Bm A";
    const hook = "A5 . . . . . . . | . . . . . . B5/A5 F#5 | . . . . D5 . . . | C#5 . . . E5 . - -"
      + " | A5 . . . . . . . | . . . . . . B5/A5 D6 | . . . . B5 . . . | C#6 . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const line = "- - F#5 . . . D5 . | B4 . . . C#5 . - - | - - D5~ . . . B4 . | G4 . . . B4 . - -"
      + " | - - E5 . . . G5 . | F#5~ . . . . . - - | - - A#4 . . . C#5 . | F#5 . . . - - - -";
    const call = (melody, level = 0.78, contour) => [sing(soar, melody, { level, contour, pump: 0.7 }),
      sing(soar, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const singer = (melody, level = 0.32, contour) => sing(vox, melody, { voice: "vox", level, contour, ring: 0.5 });
    const horn = (cells, level = 0.42) => stab(brass, cells, horns, { level, hold: 1.5, pump: 0.6, prefix: "b" });
    const bed = (level = 0.44) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(keys, riff, chords, { level, contour, prefix: "k" });
    const blasts = pulse(loop, 8, "x . . . x . . .", "- - x . . . x' .");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:w | -:e R:e -:e R:e 8:e -:e R:e N:e";
    const lope = "R:dq -:e -:e R:e -:q | R:h -:h";
    const ice = { kick: "K.........K.....", snare: "....S.......S...", hat: "H.h.H.h.H.h.H.h.", shaker: "..T...T...T...T." };
    const ice2 = { kick: "K.........K..K..", snare: "....S.......S...", hat: "H.h.H.hhH.h.H.O.", shaker: "..T...T...T...T." };
    const fill = { kick: "K.........K.....", snare: "....S..gS.gSSgSS", hat: "H.h.H.h.H......." };
    const thin = { kick: "K.........K.....", shaker: "..T...T...T...T." };
    const groove = drummer(crash(ice), ice2, ice, ice2, ice, ice2, ice, ice, ice, ice2, ice, ice2, ice, ice2, thin, thin,
      crash(ice), ice2, ice, ice2, ice, ice2, ice, ice, ice, ice2, ice, ice2, ice, ice2, ice, fill);
    const slow = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const slow2 = { kick: "K.......K.K.....", snare: "........S.....g.", hat: "H.h.H.h.H.h.H.hh" };
    const ascent = drummer(
      times(4, slow),
      times(2, { kick: "K...K...K...K...", snare: "S...S...S...S...", hat: "..h...h...h...h." }),
      { kick: "K...K...K...K...", snare: "S.S.S.S.S.S.S.S.", hat: "..O...O...O...O." },
      { kick: "K...K...K.......", snare: "S.S.S.S.S.S.....", roll: ".G.G.G.G.G.G...." },
    );
    const parts = {
      intro: part(8, pad(verse, chords, { level: 0.12, swell: 1.5, contour: [0, 1] }), piano(pulse(verse, 8, "x . . . . . . .", "- - - - x' . . ."), 0.13, [0.4, 1]),
        slam("R:w", verse, chords, { level: 0.25, contour: [0, 0.8] })),
      verse: part(8, singer(line), piano(pulse(verse, 8, "x . . x' . . - -", "- - x' . . . . ."), 0.12), slam(lope, verse, chords, { level: 0.55 }),
        drums(drummer(crash(slow), slow2, slow, slow2, slow, slow2, slow, { ...slow2, snare: "........S...SgSS" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.24, contour: [0.2, 1] }), singer(`${half} | ${pause(4)}`, 0.3),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(ascent, { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "D4", "D5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${blasts} | ${pause(8)} | ${blasts}`),
        bed(), fall(0, 8)),
      breakdown: part(8, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 8, "x . . x' . . x' .", "x . . . . . . ."), 0.18),
        sing(vox, hook, { voice: "vox", level: 0.36, ring: 0.75 }), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(7, {}), { hat: "H.h.H.h.H.hhhhhh" })), rise(16, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(soar, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "D4", "D5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), horn(`${pause(8)} | ${blasts} | ${pause(8)} | ${blasts}`),
        bed(0.46), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(ice, ice2, ice, fill, times(3, { hat: "H.h.H.h.H.h.H.h." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . x' . . x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(2, stab(brass, "D! . . . . . . . | -*8", horns, { level: 0.4, prefix: "b" }), drums({ kick: "K...............|................", cym: "C...|...." })),
    };
    return record({ voices: hall, parts, form: "intro verse build drop breakdown build2 drop2 outro end" });
  },
  sakurajima: () => {
    const bpm = 173;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = {
      Bbm: ["A#2", "C#4", "F4", "A#4"], Gb: ["F#2", "C#4", "F#4", "A#4"], Ebm: ["D#3", "D#4", "F#4", "A#4"], F: ["F2", "C4", "F4", "A4"],
      Db: ["C#3", "C#4", "F4", "G#4"], Ab: ["G#2", "C4", "D#4", "G#4"],
    };
    const gloom = "Bbm Bbm Gb Gb Ebm Ebm F F";
    const loop = "Gb Bbm Db Ab";
    const hook = "- - - - G#5>A#5 . . . | . . . . F5 . C#5 . | - - - - D#5>F5 . . . | . . . . C5 . D#5 ."
      + " | - - - - G#5>A#5 . . . | . . . . C#6 . A#5 . | - - - - F#5>G#5 . . . | . . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const dusk = "- - - - G#5>A#5 . . . | . . . . F5 . C#5 . | - - - - G#5>A#5 . . . | . . . . F#5 . C#5 ."
      + " | - - - - F5>F#5 . . . | . . . . D#5 . A#4 . | - - - - E5>F5 . . . | . . . . C5 . A4 .";
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const riff = (cells, level = 0.44) => stab(gated, cells, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const gates = pulse(loop, 8, "- x/x - x . - x .", "x . - - x/x - - .");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:q R:e -:e -:e R:e 8:e, -:e | R:e -:e 5:e, -:e R:e -:e 8:e N:e";
    const ash = { kick: "K.........K.....", snare: "....S.......S...", rim: "......R.......R.", hat: "H.hhH.h.H.hhH.h.", shaker: "..T...T...T...T." };
    const ash2 = { kick: "K.........K..K..", snare: "....S.......S...", rim: "..R...R.......R.", hat: "H.hhH.h.H.hhH.O.", shaker: "..T...T...T...T." };
    const four = { ...ash2, snare: "....S.......S.RS", rim: "..R...R.........", hat: "H.hhH.h.H.h....." };
    const fill = { kick: "K.........K.....", snare: "....S..g.gSgS.SS", hat: "H.hhH.h.H.h.....", shaker: "..T...T........." };
    const eight = (end) => [crash(ash), ash2, ash, four, ash, ash2, ash, end];
    const groove = drummer(eight(ash), eight(fill), eight(ash), eight(fill));
    const parts = {
      intro: part(16, piano(pulse(gloom, 16, "x . . x' . . x' .", "x . . . . . - -"), 0.14, [0.5, 1]), pad(gloom, chords, { wave: "triangle", level: 0.08, contour: [0, 1] }),
        sing(bell, `${pause(8)} | ${dusk}`, { level: 0.24, ring: 0.75 }),
        drums(drummer(times(8, {}), times(7, { hat: "..h...h...h...h.", shaker: "..T...T...T...T." }), { hat: "..h...h...h.hhhh" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }), piano(pulse(loop, 4, "x . . . . . . ."), 0.12),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "C#4", "C#5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . . x' . . x' .", "x . . . - - x' ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { hat: "H.hhH.h.H.hhH.h." }), { hat: "H.hhH.h.H.hhhhhh" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "C#4", "C#5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${half} | ${pause(4)}`), riff(`${pause(8)} | ${gates} | ${pause(16)}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(ash, ash2, ash, fill, times(3, { hat: "H.hhH.h.H.hhH.h." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . x' . . x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("Db . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- - - - F#5>G#5 . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  villarrica: () => {
    const bpm = 172;
    const { drums, slam, pound, sing, stab, pad, rise, fall, sweep, part, record } = arena(bpm);
    const chords = { "F#m": ["F#2", "C#4", "F#4", "A4"], Bm: ["B2", "D4", "F#4", "B4"], E: ["E2", "E4", "G#4", "B4"], A: ["A2", "C#4", "E4", "A4"] };
    const loop = "F#m Bm E A";
    const hook = "- C#6 A5 F#5 . . . . | . . . . - - D5 F#5 | G#5 . . . . . B5 . | . . . . A5 . - -"
      + " | - C#6 A5 F#5 . . . . | . . . . - - D5 F#5 | B5 . . . . . G#5 . | A5 . . . . . - -";
    const half = hook.split(" | ").slice(0, 4).join(" | ");
    const call = (melody, level = 0.8, contour) => [sing(anthem, melody, { level, contour, pump: 0.7 }),
      sing(anthem, melody, { voice: "echo", level: level * 0.22, contour, delay: 0.75, pump: 0.7, seed: 4 })];
    const riff = (cells, level = 0.44) => stab(gated, cells, chords, { level, hold: 0.75, pump: 0.85 });
    const bed = (level = 0.4) => pad(loop, chords, { level, swell: 0.3, pump: 0.9 });
    const piano = (riff, level = 0.14, contour) => stab(grand, riff, chords, { level, contour, prefix: "k" });
    const gates = pulse(loop, 8, "- - x . - x/x - .", "x . - x - - x/- -");
    const pulses = pulse(loop, 8, "x/- x/- x/- x/- x/- x/- x/- x/-");
    const bass = "R:dq 8:e -:e R:e -:q | R:e -:e R:dq -:e 5:e N:e";
    const lava = { kick: "K.........K.....", snare: "....S.......S...", ride: "..Y...Y...Y...Y.", shaker: "T...T...T...T..." };
    const lava2 = { kick: "K.........K..K..", snare: "....S..g....S..g", ride: "..Y...Y...Y...Y.", shaker: "T...T...T...T..." };
    const mark = { ...lava, hat: "O..............." };
    const four = { ...lava2, snare: "....S..g....SOSS", ride: "..Y...Y...Y....." };
    const fill = { kick: "K.........K.....", snare: "....S.O.S.O.SOSS", shaker: "T...T...T......." };
    const eight = (start, end) => [start, lava2, lava, four, mark, lava2, lava, end];
    const groove = drummer(eight(crash(lava), lava), eight(lava, fill), eight(crash(lava), lava), eight(lava, fill));
    const parts = {
      intro: part(16, piano(pulse(loop, 16, "x . . . - - x' .", "- - x' . . . . ."), 0.13, [0.5, 1]), pad(loop, chords, { wave: "triangle", level: 0.07, contour: [0, 1] }),
        sing(bell, `${pause(8)} | ${hook}`, { level: 0.26, ring: 0.75 }),
        drums(drummer(times(8, {}), times(7, { shaker: "T...T...T...T...", hat: "..h...h...h...h." }), { shaker: "T...T...TTTTTTTT" }))),
      build: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.2, 1] }), sing(mist, `${half} | ${pause(4)}`, { level: 0.3 }),
        slam(`${"R:w | ".repeat(6)}-:w | -:w`, loop, chords, { level: 0.5, contour: [0.5, 0.9] }), drums(climb(8), { gains: rolled, contour: [0.7, 1] }),
        rise(16, 15), sweep(16, 15, "A4", "A5")),
      drop: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.17, swell: 1.5, contour: [0.4, 1] }), piano(pulse(loop, 16, "x . - x' - - x' .", "x . . . . . . ."), 0.2),
        sing(bell, `${hook} | ${pause(8)}`, { level: 0.34, ring: 0.75 }), call(`${pause(8)} | ${hook}`, 0.44), slam("R:w", loop, chords, { level: 0.4, contour: [0, 1, 0.6] }),
        drums(drummer(times(12, {}), times(3, { shaker: "T.T.T.T.T.T.T.T." }), { shaker: "T.T.T.T.TTTTTTTT" })), rise(48, 15)),
      build2: part(8, stab(gate, pulses, chords, { level: 0.26, contour: [0.4, 1] }), sing(anthem, `${half} | ${pause(4)}`, { level: 0.26, contour: [0.5, 1] }),
        drums(climb(8), { gains: rolled, contour: [0.8, 1] }), rise(0, 31), sweep(16, 15, "A4", "A5")),
      drop2: part(32, slam(bass, loop, chords), pound(groove), call(`${hook} | ${pause(8)} | ${hook} | ${pause(8)}`), riff(`${pause(8)} | ${gates} | ${pause(8)} | ${gates}`),
        bed(0.44), fall(0, 8)),
      outro: part(8, slam(bass, loop, chords, { contour: [1, 0.5] }), drums(drummer(lava, lava2, lava, fill, times(3, { shaker: "T...T...T...T..." }), {}), { contour: [1, 0.6] }),
        piano(pulse(loop, 8, "x . . . - - x' ."), 0.13, [1, 0.5]), pad(loop, chords, { level: 0.1, contour: [1, 0.4] })),
      end: part(4, piano("A . . . . . . . | -*8 | -*8 | -*8", 0.16), sing(bell, "- C#6 A5 F#5 . . . . | . . . . . . . . | -*8 | -*8", { level: 0.24, ring: 4 }),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: hall, parts, form: "intro build drop breakdown build2 drop2 outro end" });
  },
  kraken: () => {
    const bpm = 110;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "F1" });
    const chords = {
      Db: ["C#3", "F4", "G#4", "C#5"], Ab: ["G#2", "D#4", "G#4", "C5"],
      Eb: ["D#3", "D#4", "G4", "A#4"], Fm: ["F2", "F4", "G#4", "C5"],
    };
    const loop = "Db Ab Eb Fm";
    const { sing, echo, pump, bass, lift } = sweet(chords, loop);
    const hook = "- - F4 . . F4 . . G#4 . ^D#5~ . . . . . | . . . . - - D#5 . C5 . G#4 . . . - -"
      + " | - - G4 . . G4 . . A#4 . ^G5~ . . . . . | . . . . - - F5 . D#5 . C5 . . . - -";
    const chopped = "- - F4/F4 . . F4/F4 . . G#4 . ^D#5~ . . . . . | . . . . - - D#5 . C5 . G#4 . . . - -"
      + " | - - G4/G4 . . G4/G4 . . A#4 . ^G5~ . . . . . | . . . . - - F5/F5 . D#5 . C5 . . . - -";
    const { talk, chug, wobble, screech, dive, zap } = sounds;
    const palette = { Y: talk([6, 4, 3, 4, 6, 8], [16, 12, 10, 12, 16, 20]), P: chug(), W: wobble(1 / 3), S: screech(), D: dive(), Z: zap() };
    const first = "Y . . . . - P P - - Y . . - - Z | W . . . . . - - S . . . . - - -"
      + " | Y+8 . . . . - P+8 P+8 - - Y+8 . . - - Z | W+10 . . . P+10 - P+10 - D . . . - - - -";
    const second = "Y . . . . - P P - - Y+3 . . - - Z | S . . . - - S+3 . . . - - W . . ."
      + " | Y+8 . . . . - P+8 P+8 - - Y+10 . . - - Z | P P - P P - P/P P/P D . . . . . - -";
    const third = "Y . . . . - P P - - Y . . - - Z | S . . . . . - - W . . . . - - -"
      + " | Y+8 . . . . - P+8 P+8 - - Y+8 . . - - Z | S+10 . . . P+10 - P+10 - D . . . - - - -";
    const fourth = "Y . . . . - P P - - Y+3 . . - - Z | W . . . - - W+3 . . . - - S . . ."
      + " | Y+8 . . . . - P+8 P+8 - - Y+10 . . - - Z | P/P P/P - P P - P/P P/P D . . . . . - -";
    const slam1 = { kick: "K.....K...K.....", snare: "....S.......S...", hat: "..H...H...H...H." };
    const slam2 = { kick: "K.....K...K..K..", snare: "....S.......S...", hat: "..H...H.H.H...H." };
    const fill = { kick: "K.....K.........", snare: "....S.......S.NN", hat: "..H...H........." };
    const groove = drummer(times(4, [crash(slam1), slam2, slam1, fill]));
    const four = { kick: "K.......K.......", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      intro: part(8, pump(8, ["x . - x - x . -", "x . - x - - x ."], 0.4, [0.5, 1]), sing(`${hook} | ${chopped}`, 0.6, [0.7, 1]), bass(0.65, [0.3, 1]),
        drums(drummer(times(4, { hat: "..h...h...h...h.", clap: "....L.......L..." }), times(3, four), { ...four, clap: "....L.......L.LL" }))),
      build: part(8, lift(8, hook, "C5", ["F4", "F5"])),
      drop: part(16, riff(`${first} | ${second} | ${third} | ${second}`, "F2", palette), drums(groove), fall(0, 8)),
      breakdown: part(8, pad(loop, chords, { level: 0.3, swell: 1.2, contour: [0.5, 1] }), sing(hook, 0.58), echo(hook), bass(0.55, [0.2, 1]),
        drums(drummer(times(7, { hat: "..h...h...h...h.", clap: "....L.......L..." }), { clap: "....L...L.L.LLLL" }))),
      build2: part(8, lift(8, chopped, "C5", ["F4", "F5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${third} | ${fourth} | ${first} | ${fourth}`, "F2", palette), drums(groove), fall(0, 8)),
      outro: part(4, pump(4, ["x . - x - x . -", "x . - x - - x .", "x . - x - x . -", "x . . . . . . ."], 0.4, [1, 0.7], 4), sing(hook, 0.58, [1, 0.7]), bass(0.6),
        drums(drummer(times(3, four), { kick: "K...............", cym: "C..." }))),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
  hydra: () => {
    const bpm = 140;
    const { drums, riff, call, sounds, sweet, pad, rise, sweep, fall, part, record } = rig(bpm, { tail: "E1" });
    const chords = {
      C: ["C3", "E4", "G4", "C5"], G: ["G2", "D4", "G4", "B4"],
      D: ["D3", "D4", "F#4", "A4"], Em: ["E2", "E4", "G4", "B4"],
    };
    const loop = "C G D Em";
    const { sing, pump, bass, lift } = sweet(chords, loop);
    const hook = "- - ^E5 . . E5 . G5 . . . . D5 . E5 . | . . . . . . - - D5 . B4 . . . - -"
      + " | - - ^D5 . . D5 . F#5 . . . . A5 . F#5 . | . . . . . . - - E5 . B4 . . . - -";
    const chopped = "- - E5/E5 . . E5/E5 . G5 . . . . D5 . E5 . | . . . . . . - - D5/D5 . B4 . . . - -"
      + " | - - D5/D5 . . D5/D5 . F#5 . . . . A5 . F#5 . | . . . . . . - - E5/E5 . B4 . . . - -";
    const { talk, chug, wobble, growl, screech, dive, zap } = sounds;
    const palette = { G: growl(), S: screech(), Y: talk([2, 3, 4, 6, 4, 3], [10, 12, 16, 12, 10]), P: chug(), W: wobble(0.5), D: dive(), Z: zap() };
    const heads = "G . . . . . P P - - Y . . . . - | - - S . . . . . P - P - Z - - -"
      + " | G+8 . . . . . P+8 P+8 - - Y+8 . . . . - | W+10 . . . . . . . D+10 . . . - - - -";
    const more = "G . . . . . P P - - Y+3 . . . . - | - - S+3 . . . S . . . - - G . . ."
      + " | Y+8 . . . . - P+8 P+8 - - Y+7 . . . . - | P/P P/P - P/P P/P - S+10 . . . . . D . . -";
    const cut = "Y . . . . . P P - - G . . . . - | - - S . . . . . P - P - Z - - -"
      + " | Y+8 . . . . . P+8 P+8 - - G+8 . . . . - | W+10 . . . . . . . D+10 . . . - - - -";
    const g1 = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const g2 = { kick: "K.....K...K.....", snare: "........S.....g.", hat: "H.h.H.h.H.h.H.hh" };
    const fill = { kick: "K.........K.....", snare: "........S...N.NN", hat: "H.h.H.h........." };
    const groove = drummer(times(4, [crash(g1), g2, g1, fill]));
    const four = { kick: "K...K...K...K...", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      shout: part(2, call(stammer, stutter("B4", 7, 2), { level: 0.45, contour: [0.4, 1] }), drums({ snare: "N.N.N.N.N.N.N.N.|NNNNNNNNNNNNNNNNNNNNNNNN........" }, { contour: [0.5, 1] }),
        rise(0, 7), sweep(0, 7, "E4", "E5")),
      drop: part(16, riff(`${heads} | ${more} | ${heads} | ${more}`, "E2", palette), drums(groove), fall(0, 8)),
      sweet: part(8, pad(loop, chords, { level: 0.28, swell: 1.2, contour: [0.3, 1] }), sing(`${hush(4)} | ${hook}`, 0.58), bass(0.5, [0.2, 1]),
        drums({ clap: "........L.......", hat: "..h...h...h...h." }, { contour: [0.4, 1] })),
      lift: part(8, pump(8, ["x . - x - x . -", "- x . x - x . -"]), sing(`${hook} | ${chopped}`), bass(0.6),
        drums(drummer(times(7, four), { ...four, clap: "....L...L.L.LLLL" }))),
      build: part(8, lift(8, chopped, "B4", ["E4", "E5"])),
      drop2: part(16, riff(`${cut} | ${more} | ${heads} | ${more}`, "E2", palette), drums(groove), fall(0, 8)),
      outro: part(8, pump(8, ["x . - x - x . -", "- x . x - x . -", "x . - x - x . -", "- x . x - x . -", "x . - x - x . -", "- x . x - x . -", "x . - x - x . -", "x . . . . . . ."], 0.4, [1, 0.6], 4),
        sing(`${hook} | ${hook}`, 0.58, [1, 0.6]), bass(0.6, [1, 0.6]), drums(drummer(times(7, four), { kick: "K...............", cym: "C..." }), { contour: [1, 0.7] })),
    };
    return record({ voices: booth, parts, form: "shout drop sweet lift build drop2 outro" });
  },
  chimera: () => {
    const bpm = 128;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "A1" });
    const chords = {
      F: ["F2", "F4", "A4", "C5"], C: ["C3", "E4", "G4", "C5"],
      G: ["G2", "D4", "G4", "B4"], Am: ["A2", "E4", "A4", "C5"],
    };
    const loop = "F C G Am";
    const { sing, echo, pump, bass, lift } = sweet(chords, loop);
    const hook = "- - A4 . C5 . F5 . . . E5 . . . C5 . | . . . . G4 . . . - - - - - - - -"
      + " | - - B4 . D5 . G5 . . . F5 . . . D5 . | . . . . E5 . . . - - - - - - - -";
    const chopped = "- - A4/A4 . C5 . F5 . . . E5 . . . C5/C5 . | . . . . G4 . G4/G4 . - - - - - - - -"
      + " | - - B4/B4 . D5 . G5 . . . F5 . . . D5/D5 . | . . . . E5 . E5/E5 . - - - - - - - -";
    const { talk, chug, wobble, bwaa, screech, dive, zap } = sounds;
    const palette = { B: bwaa(), P: chug(), Z: zap(), W: wobble(0.25), Y: talk([3, 4, 6, 8], [10, 12, 16, 20]), S: screech({ lift: 31 }), D: dive() };
    const first = "B . . . P - P - Z - W . . . - - | P P - Y . . . - S . . - B . . ."
      + " | B+8 . . . P+8 - P+8 - Z - W+8 . . . - - | P+10 P+10 - Y+10 . . . - S+10 . . - D+10 . . -";
    const second = "B . . . P - P - Z - W . . . - - | P P - Y+3 . . . - S+3 . . - Y . . ."
      + " | B+8 . . . P+8 - P+8 - Z - W+8 . . . - - | P/P P/P P/P P/P - - Y+10 . . . S+10 . . D . -";
    const third = "Y . . . P - P - Z - W . . . - - | P P - B . . . - S . . - Y . . ."
      + " | Y+8 . . . P+8 - P+8 - Z - W+8 . . . - - | P+10 P+10 - B+10 . . . - S+10 . . - D+10 . . -";
    const e1 = { kick: "K...K...K...K...", snare: "....S.......S...", hat: "..O...O...O...O." };
    const e2 = { kick: "K...K...K...K...", snare: "....S.......S..g", hat: "..O...O.H.O...O." };
    const fill = { kick: "K...K...K...K...", snare: "....S.......SNNN", hat: "..O...O........." };
    const groove = drummer(times(4, [crash(e1), e2, e1, fill]));
    const four = { kick: "K...K...K...K...", clap: "....L.......L...", hat: "..O...O...O...O." };
    const parts = {
      intro: part(8, pump(8, ["x x - x - x x -", "x . - x - x . x"], 0.38, [0.4, 1]), sing(`${hook} | ${chopped}`, 0.6, [0.6, 1]), bass(0.6, [0.3, 1]),
        drums(drummer(times(4, { hat: "..o...o...o...o.", clap: "....L.......L..." }), times(3, four), { ...four, clap: "....L.......LLLL" }))),
      build: part(8, lift(8, hook, "A4", ["A4", "A5"])),
      drop: part(16, riff(`${first} | ${second} | ${third} | ${second}`, "A2", palette), drums(groove), fall(0, 8)),
      breakdown: part(8, pad(loop, chords, { level: 0.3, swell: 1, contour: [0.4, 1] }), sing(chopped, 0.58), echo(chopped), bass(0.55, [0.2, 1]),
        drums(drummer(times(7, { hat: "..o...o...o...o.", clap: "....L.......L..." }), { clap: "....L...L.L.LLLL" }))),
      build2: part(8, lift(8, chopped, "A4", ["A4", "A5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${third} | ${second} | ${first} | ${second}`, "A2", palette), drums(groove), fall(0, 8)),
      outro: part(4, pump(4, ["x x - x - x x -", "x . - x - x . x", "x x - x - x x -", "x . . . . . . ."], 0.38, [1, 0.7], 4), sing(hook, 0.58, [1, 0.7]), bass(0.6),
        drums(drummer(times(3, four), { kick: "K...............", cym: "C..." }))),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
  basilisk: () => {
    const bpm = 150;
    const { drums, riff, sounds, sweet, pad, rise, sweep, fall, part, record } = rig(bpm, { tail: "G1" });
    const chords = {
      Eb: ["D#3", "D#4", "G4", "A#4"], Bb: ["A#2", "D4", "F4", "A#4"],
      F: ["F2", "C4", "F4", "A4"], Gm: ["G2", "D4", "G4", "A#4"],
    };
    const loop = "Eb Bb F Gm";
    const { sing, pump, bass, lift } = sweet(chords, loop);
    const hook = "^D5~ . . . . . . . C5 . A#4 . . . . . | . . . . - - F4 . A#4 . D5 . . . - -"
      + " | ^C5~ . . . . . . . A4 . F4 . . . . . | . . . . - - D4 . G4 . A#4 . . . - -";
    const chopped = "^D5~ . . . . . . . C5/C5 . A#4 . . . . . | . . . . - - F4/F4 . A#4 . D5 . . . - -"
      + " | ^C5~ . . . . . . . A4/A4 . F4 . . . . . | . . . . - - D4/D4 . G4 . A#4 . . . - -";
    const { talk, chug, growl, screech, dive, zap } = sounds;
    const palette = { S: screech({ bend: -7 }), G: growl({ rough: 0.4 }), D: dive(), P: chug({ fifth: 6 }), Y: talk([6, 4, 3, 4, 6, 8], [16, 12, 10, 12, 16, 20]), Z: zap() };
    const stare = "G . . . . - S . . . - - P P - Z | P - - P - - Y+1 . . . . - G . . -"
      + " | G+8 . . . . - S+8 . . . - - P+8 P+8 - Z | Y+7 . . . P+7 P+7 - - D+7 . . . . . - -";
    const stone = "G . . . . - S . . . - - P P - Z | P/P P - P/P P - Y+1 . . . . - S+1 . . -"
      + " | G+8 . . . . - S+8 . . . - - P+8 P+8 - Z | S+7 . . . . . . . D+7 . . . - - - -";
    const venom = "S . . . . - G . . . - - P P - Z | P - - P - - S+1 . . . . - Y . . -"
      + " | S+8 . . . . - G+8 . . . - - P+8 P+8 - Z | Y+7 . . . P+7 P+7 - - D+7 . . . . . - -";
    const v1 = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const v2 = { kick: "K.....K...K..K..", snare: "........S.......", hat: "H.h.H.hhH.h.H.h." };
    const fill = { kick: "K.........K.....", snare: "........S.N.NNNN", hat: "H.h.H.h........." };
    const groove = drummer(times(6, [crash(v1), v2, v1, fill]));
    const four = { kick: "K.......K.......", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      shout: part(2, riff("Z - Z - Z - Z - Z/Z Z/Z Z/Z Z/Z - - - - | - - - - - - - - - - - - - - - -", "G2", palette, { contour: [0.5, 1] }),
        drums({ kick: "K...............|................", snare: "N...N...N.N.N.N.|NNNNNNNNNNNNNNNNNNNNNNNN........" }, { contour: [0.5, 1] }),
        rise(0, 7), sweep(0, 7, "G3", "G5", { wave: "sawtooth", level: 0.07 })),
      drop: part(16, riff(`${stare} | ${stone} | ${stare} | ${stone}`, "G2", palette), drums(groove), fall(0, 8)),
      sweet: part(8, pad(loop, chords, { level: 0.28, swell: 1.5, contour: [0.3, 1] }), sing(`${hush(4)} | ${hook}`, 0.6), bass(0.5, [0.2, 1]),
        drums({ clap: "........L.......", hat: "..h...h...h...h." }, { contour: [0.4, 1] })),
      lift: part(8, pump(8, ["x . . x . . x .", "- - x . x . - x"]), sing(`${hook} | ${chopped}`), bass(0.6),
        drums(drummer(times(7, four), { ...four, clap: "....L...L.L.LLLL" }))),
      build: part(8, lift(8, chopped, "D5", ["G4", "G5"])),
      drop2: part(24, riff(`${venom} | ${stone} | ${stare} | ${stone} | ${venom} | ${stone}`, "G2", palette), drums(groove), fall(0, 8)),
      outro: part(4, pad(loop, chords, { level: 0.3, swell: 0.6, contour: [1, 0.5] }), sing(hook, 0.55, [1, 0.6]), bass(0.5, [1, 0.5]),
        drums({ kick: "K...............|................|................|................", cym: "C...|....|....|...." })),
    };
    return record({ voices: booth, parts, form: "shout drop sweet lift build drop2 outro" });
  },
  manticore: () => {
    const bpm = 115;
    const { drums, riff, sounds, sweet, fall, part, record } = rig(bpm, { tail: "A1" });
    const chords = {
      Bb: ["A#2", "D4", "F4", "A#4"], F: ["F2", "C4", "F4", "A4"],
      C: ["C3", "C4", "E4", "G4"], Dm: ["D3", "D4", "F4", "A4"],
    };
    const loop = "Bb F C Dm";
    const { sing, echo, pump, piano, bass, lift } = sweet(chords, loop);
    const hook = "- - D5 . . F5 . G5 . . F5 . D5 . . . | C5 . . . . . . . - - A4 . C5 . - -"
      + " | - - C5 . . E5 . G5 . . E5 . C5 . . . | D5 . . . . . . . - - - - A4 . D5 .";
    const chopped = "- - D5/D5 . . F5 . G5 . . F5/F5 . D5 . . . | C5 . . . . . . . - - A4/A4 . C5 . - -"
      + " | - - C5/C5 . . E5 . G5 . . E5/E5 . C5 . . . | D5 . . . . . . . - - - - A4/A4 . D5 .";
    const { talk, chug, wobble, growl, rise, dive } = sounds;
    const palette = { G: growl(), Y: talk([8, 6, 4, 3, 2], [20, 16, 12, 10, 8]), U: rise(), D: dive(), P: chug(), W: wobble(0.5) };
    const sting = "G . . . . . - - G+3 . . - P P - - | Y . . . . . . . - - P - P U . ."
      + " | G+8 . . . . . - - G+10 . . - P+8 P+8 - - | Y+10 . . . . - P+10 P+10 - - D . . . . -";
    const claw = "G . . . . . - - G+3 . . - P P - - | W . . . . . . . - - P - P U . ."
      + " | G+8 . . . . . - - G+10 . . - P+8 P+8 - - | P/P P/P - P/P P/P - Y+10 . . . . . - - - -";
    const r1 = { kick: "K.......K.K.....", snare: "....S.......S...", hat: "..H...H...H...H." };
    const r2 = { kick: "K.......K.K...K.", snare: "....S.......S...", hat: "..H.H.H...H.H.H." };
    const fill = { kick: "K.......K.......", snare: "....S.......S.NN", hat: "..H...H........." };
    const groove = drummer(times(4, [crash(r1), r2, r1, fill]));
    const four = { kick: "K.......K.......", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      intro: part(8, piano(4, ["x . . x . . x .", "x . . x . . . ."], 0.32, [0.6, 1]), sing(`${hook} | ${chopped}`, 0.55, [0.6, 1]),
        pump(8, [...times(4, "- - - - - - - -"), ...times(2, ["x . - x - x . -", "x . - x - - x ."]).flat()], 0.38),
        bass(0.6, [0.2, 1]), drums(drummer(times(4, { shaker: "..T...T...T...T." }), times(3, four), { ...four, clap: "....L.......L.LL" }))),
      build: part(8, lift(8, hook, "A4", ["D4", "D5"])),
      drop: part(16, riff(`${sting} | ${claw} | ${sting} | ${claw}`, "D3", palette), drums(groove), fall(0, 8)),
      breakdown: part(8, piano(8, ["x . . x . . x .", "x . . x . . . ."], 0.36), sing(hook, 0.55), echo(hook),
        bass(0.5, [0.2, 1]), drums(drummer(times(7, { shaker: "..T...T...T...T.", clap: "....L.......L..." }), { clap: "....L...L.L.LLLL" }))),
      build2: part(8, lift(8, chopped, "A4", ["D4", "D5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${claw} | ${sting} | ${claw} | ${sting}`, "D3", palette), drums(groove), fall(0, 8)),
      outro: part(4, piano(4, ["x . . x . . x .", "x . . x . . . .", "x . . x . . x .", "x . . . . . . ."], 0.32, [1, 0.7]), sing(hook, 0.52, [1, 0.6]), bass(0.5, [1, 0.6]),
        drums({ kick: "K...............|................|................|K...............", cym: "C...|....|....|C..." })),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
  wyvern: () => {
    const bpm = 145;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "G#1" });
    const chords = {
      A: ["A2", "E4", "A4", "C#5"], E: ["E2", "E4", "G#4", "B4"],
      B: ["B2", "D#4", "F#4", "B4"], "C#m": ["C#3", "E4", "G#4", "C#5"],
    };
    const loop = "A E B C#m";
    const { sing, echo, pump, bass, lift } = sweet(chords, loop);
    const hook = "- - C#5 . . E5 . . B4 . C#5 . E5 . . . | . . . . . . . . G#4 . . . B4 . . ."
      + " | - - D#5 . . F#5 . . B4 . D#5 . F#5 . . . | . . . . . . . . E5 . . . C#5 . . .";
    const chopped = "- - C#5/C#5 . . E5 . . B4 . C#5 . E5 . . . | . . . . . . . . G#4/G#4 . . . B4 . . ."
      + " | - - D#5/D#5 . . F#5 . . B4 . D#5 . F#5 . . . | . . . . . . . . E5/E5 . . . C#5 . . .";
    const { talk, wobble, dive, zap } = sounds;
    const palette = { W: wobble(0.5), T: wobble(1 / 3), Q: wobble(0.25), H: wobble(1), Y: talk([4, 6, 8, 6, 4], [12, 16, 20, 16, 12]), D: dive(), Z: zap() };
    const wings = "W . . . . . . - T . . . . . - - | Q . . . - - Y . . . - - Z - D ."
      + " | W+8 . . . . . . - T+8 . . . . . - - | H+10 . . . . . . - Q+10 . . . - - Z -";
    const tail = "W . . . . . . - T . . . . . - - | Y . . . - - Q . . . - - Y+3 . . -"
      + " | W+8 . . . . . . - T+8 . . . . . - - | Q+10 . . - Q+10 . . - D+10 . . . - - - -";
    const w1 = { kick: "K.........K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const w2 = { kick: "K.....K...K.....", snare: "........S.....g.", hat: "H.h.H.h.H.hhH.h." };
    const fill = { kick: "K.........K.....", snare: "........S..N.NNN", hat: "H.h.H.h........." };
    const groove = drummer(times(4, [crash(w1), w2, w1, fill]));
    const four = { kick: "K...K...K...K...", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      intro: part(8, pump(8, ["x . - x - x . -", "x . - - x - x ."], 0.4, [0.4, 1]), sing(`${hook} | ${chopped}`, 0.6, [0.6, 1]), bass(0.6, [0.3, 1]),
        drums(drummer(times(4, { clap: "........L.......", hat: "..h...h...h...h." }), times(3, four), { ...four, clap: "....L.......LLLL" }))),
      build: part(8, lift(8, hook, "G#4", ["C#4", "C#5"])),
      drop: part(16, riff(`${wings} | ${tail} | ${wings} | ${tail}`, "C#3", palette), drums(groove), fall(0, 8)),
      breakdown: part(16, pad(loop, chords, { level: 0.28, swell: 1.2, contour: [0.3, 1] }), sing(`${hush(4)} | ${hook} | ${hook} | ${chopped}`, 0.58), echo(`${hush(8)} | ${hook} | ${chopped}`),
        bass(0.55, [0.2, 1]), drums(drummer(times(8, { hat: "..h...h...h...h." }), times(7, { clap: "........L.......", hat: "..h...h...h...h." }), { clap: "........L...LLLL" }))),
      build2: part(8, lift(8, chopped, "G#4", ["C#4", "C#5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${tail} | ${wings} | ${tail} | ${wings}`, "C#3", palette), drums(groove), fall(0, 8)),
      outro: part(4, pump(4, ["x . - x - x . -", "x . - - x - x .", "x . - x - x . -", "x . . . . . . ."], 0.4, [1, 0.7], 4), sing(hook, 0.58, [1, 0.7]), bass(0.6),
        drums(drummer(times(3, four), { kick: "K...............", cym: "C..." }))),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
  gorgon: () => {
    const bpm = 105;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "B1" });
    const chords = {
      G: ["G2", "D4", "G4", "B4"], D: ["D3", "D4", "F#4", "A4"],
      A: ["A2", "C#4", "E4", "A4"], Bm: ["B2", "D4", "F#4", "B4"],
    };
    const loop = "G D A Bm";
    const { sing, pump, bass, lift } = sweet(chords, loop);
    const hook = "B4 . . . D5 . . . F#5 . G5 . . . . . | . . . . - - - - F#5 . . . D5 . . ."
      + " | A4 . . . C#5 . . . E5 . F#5 . . . . . | . . . . - - - - D5 . . . B4 . . .";
    const chopped = "B4/B4 . . . D5/D5 . . . F#5 . G5 . . . . . | . . . . - - - - F#5/F#5 . . . D5 . . ."
      + " | A4/A4 . . . C#5/C#5 . . . E5 . F#5 . . . . . | . . . . - - - - D5/D5 . . . B4 . . .";
    const { talk, chug, growl, screech, rise, dive } = sounds;
    const palette = { P: chug(), D: dive(), G: growl({ rough: 0.4 }), U: rise(), S: screech({ lift: 31, bend: -12 }), Y: talk([3, 4, 6, 8, 6]) };
    const stone = "P . . - P . . - - - P P D . . - | - - - - G . . . . . - - U . . ."
      + " | P+8 . . - P+8 . . - - - P+8 P+8 D+8 . . - | S+7 . . . . . - - P+7 - P+7 - P/P P/P - -";
    const gaze = "P . . - P . . - - - P P D . . - | Y . . . . . - - G+3 . . . - U . ."
      + " | P+8 . . - P+8 . . - - - P+8 P+8 D+8 . . - | G+10 . . . . . - - S+10 . . . . . - -";
    const s1 = { kick: "K.......K.......", snare: "....S.......S...", hat: "..H...H...H...H." };
    const s2 = { kick: "K.....K.K.......", snare: "....S.......S...", hat: "..H...H...H.H.H." };
    const fill = { kick: "K.......K.......", snare: "....S...S.N.NNNN", hat: "..H............." };
    const groove = drummer(times(4, [crash(s1), s2, s1, fill]));
    const four = { kick: "K.......K.......", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      shout: part(2, riff("P . . . . . . . - - - - - - - - | P . . . . . . . - - - - - - - -", "B2", palette),
        drums({ kick: "K...............|K...............", cym: "C...|C..." })),
      drop: part(16, riff(`${stone} | ${gaze} | ${stone} | ${gaze}`, "B2", palette), drums(groove), fall(0, 8)),
      sweet: part(8, pump(8, ["x . . . - - x .", "x . - x . . - -"], 0.38, [0.3, 1]), sing(`${hook} | ${chopped}`, 0.58, [0.6, 1]), bass(0.55, [0.2, 1]),
        drums(drummer(times(4, { hat: "..h...h...h...h." }), times(3, four), { ...four, clap: "....L...L.L.LLLL" }))),
      build: part(8, lift(8, chopped, "F#4", ["B3", "B4"])),
      drop2: part(16, riff(`${gaze} | ${stone} | ${gaze} | ${stone}`, "B2", palette), drums(groove), fall(0, 8)),
      outro: part(4, riff("P . . . . . . . - - - - - - - - | - - - - - - - - - - - - - - - - | P . . . . . . . - - - - - - - - | - - - - - - - - - - - - - - - -", "B2", palette, { contour: [1, 0.6] }),
        pad(loop, chords, { level: 0.24, swell: 0.4, contour: [1, 0.4] }), sing(hook, 0.5, [1, 0.5]),
        drums({ kick: "K...............|................|K...............|................", cym: "C...|....|C...|...." })),
    };
    return record({ voices: booth, parts, form: "shout drop sweet build drop2 outro" });
  },
  leviathan: () => {
    const bpm = 138;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "F#1" });
    const chords = {
      D: ["D3", "D4", "F#4", "A4"], A: ["A2", "C#4", "E4", "A4"],
      E: ["E2", "E4", "G#4", "B4"], "F#m": ["F#2", "C#4", "F#4", "A4"],
    };
    const loop = "D A E F#m";
    const { sing, echo, pump, bass, lift } = sweet(chords, loop);
    const hook = "F#4 . . . . . ^F#5~ . E5 . . . . . . . | . . . . - - C#5 . . . E5 . . . - -"
      + " | G#4 . . . . . ^G#5~ . F#5 . . . . . . . | . . . . - - C#5 . . . A4 . . . - -";
    const chopped = "F#4/F#4 . . . . . ^F#5~ . E5 . . . . . . . | . . . . - - C#5/C#5 . . . E5 . . . - -"
      + " | G#4/G#4 . . . . . ^G#5~ . F#5 . . . . . . . | . . . . - - C#5/C#5 . . . A4 . . . - -";
    const { talk, chug, wobble, bwaa, screech, dive, zap } = sounds;
    const palette = { B: bwaa(), Y: talk([2, 3, 4, 6, 8, 6, 4], [8, 10, 12, 16, 20, 16]), W: wobble(1), S: screech(), P: chug(), D: dive(), Z: zap() };
    const deep = "B . . . . . . - Y . . . . . - - | P - P - - - S . . . . - W . . -"
      + " | B+8 . . . . . . - Y+8 . . . . . - - | W+10 . . . . . . - P+10 P+10 - Z D+10 . . -";
    const swell = "B . . . . . . - Y . . . . . - - | P - P - - - Y+3 . . . . - S+3 . . -"
      + " | B+8 . . . . . . - Y+8 . . . . . - - | S+10 . . . . . - - P/P P/P P/P P/P D+10 . . -";
    const d1 = { kick: "K.........K.....", snare: "........S.......", hat: "H...H.h.H...H.h." };
    const d2 = { kick: "K.....K...K.....", snare: "........S.......", hat: "H...H.h.H...H.hh" };
    const fill = { kick: "K.........K.....", snare: "........S..N..NN", hat: "H...H.h........." };
    const groove = drummer(times(4, [crash(d1), d2, d1, fill]));
    const four = { kick: "K...K...K...K...", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      intro: part(8, pad(loop, chords, { level: 0.26, swell: 1.5, contour: [0.3, 1] }), sing(`${hook} | ${chopped}`, 0.58, [0.5, 1]), bass(0.55, [0.2, 1]),
        drums(drummer(times(4, { hat: "..h...h...h...h." }), times(3, { clap: "........L.......", hat: "..h...h...h...h." }), { clap: "........L...LLLL" }))),
      build: part(8, lift(8, hook, "C#5", ["F#4", "F#5"])),
      drop: part(16, riff(`${deep} | ${swell} | ${deep} | ${swell}`, "F#2", palette), drums(groove), fall(0, 8)),
      breakdown: part(16, pad(`${loop} ${loop} - - - - - - - -`, chords, { level: 0.24, swell: 1.5, contour: [0.4, 1] }), sing(`${hook} | ${hush(4)} | ${hook} | ${chopped}`, 0.58), echo(`${hook} | ${hush(4)} | ${hook} | ${chopped}`),
        pump(16, [...times(8, "- - - - - - - -"), ...times(4, ["x . - x - x . -", "- x . x - x . -"]).flat()], 0.38), bass(0.55, [0.2, 1]),
        drums(drummer(times(8, { hat: "..h...h...h...h." }), times(7, four), { ...four, clap: "....L...L.L.LLLL" }))),
      build2: part(8, lift(8, chopped, "C#5", ["F#4", "F#5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${swell} | ${deep} | ${swell} | ${deep}`, "F#2", palette), drums(groove), fall(0, 8)),
      outro: part(8, pad(loop, chords, { level: 0.26, swell: 0.8, contour: [1, 0.4] }), sing(`${hook} | ${hook}`, 0.55, [1, 0.5]), echo(`${hook} | ${hook}`, 0.15, [1, 0.5]), bass(0.5, [1, 0.4]),
        drums(drummer(times(7, { clap: "........L.......", hat: "..h...h...h...h." }), { kick: "K...............", cym: "C..." }), { contour: [1, 0.6] })),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
  cerberus: () => {
    const bpm = 132;
    const { drums, riff, sounds, sweet, pad, rise, fall, part, record } = rig(bpm, { tail: "A#1" });
    const chords = {
      B: ["B2", "D#4", "F#4", "B4"], "F#": ["F#2", "C#4", "F#4", "A#4"],
      "C#": ["C#3", "C#4", "F4", "G#4"], "D#m": ["D#3", "D#4", "F#4", "A#4"],
    };
    const loop = "B F# C# D#m";
    const { sing, pump, bass, lift } = sweet(chords, loop);
    const hook = "D#5 . . F#5 . . A#5 . . . . . F#5 . . . | C#5 . . . . . . . - - - - A#4 . C#5 ."
      + " | C#5 . . F5 . . G#5 . . . . . F5 . . . | A#4 . . . . . . . - - - - F#4 . A#4 .";
    const chopped = "D#5/D#5 . . F#5 . . A#5 . . . . . F#5 . . . | C#5 . . . . . . . - - - - A#4/A#4 . C#5 ."
      + " | C#5/C#5 . . F5 . . G#5 . . . . . F5 . . . | A#4 . . . . . . . - - - - F#4/F#4 . A#4 .";
    const { talk, chug, growl, screech, dive, zap } = sounds;
    const palette = { Y: talk([6, 4, 3, 4, 6, 8]), G: growl(), S: screech(), P: chug(), Z: zap(), D: dive() };
    const bark = "Y . - G . - S . - Y . - P P - - | G . - S . - Y . - G . - Z - - -"
      + " | Y+8 . - G+8 . - S+8 . - Y+8 . - P+8 P+8 - - | S+10 . - Y+10 . - G+10 . - D+10 . . . - - -";
    const bite = "Y . - G . - S . - Y . - P P - - | S . - G . - Y . - S . - Z - Z -"
      + " | Y+8 . - G+8 . - S+8 . - Y+3 . - P+3 P+3 - - | G+10 . . . . - S+10 . . . . - - - - -";
    const c1 = { kick: "K..K......K.....", snare: "........S.......", hat: "H.h.H.h.H.h.H.h." };
    const c2 = { kick: "K..K..K.........", snare: "........S.......", hat: "H.h.H.h.H.h.H.hh" };
    const fill = { kick: "K..K......K.....", snare: "........S..N..NN", hat: "H.h.H.h........." };
    const groove = drummer(times(4, [crash(c1), c2, c1, fill]));
    const four = { kick: "K...K...K...K...", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      shout: part(2, riff("Y . . . - - - - G . . . - - - - | S . . . - - - - - - - - - - - -", "D#3", palette),
        drums({ kick: "K.......K.......|K...............", cym: "C.C.|C..." }), rise(4, 3, { level: 0.08 })),
      drop: part(16, riff(`${bark} | ${bite} | ${bark} | ${bite}`, "D#3", palette), drums(groove), fall(0, 8)),
      sweet: part(8, pad(loop, chords, { level: 0.28, swell: 1.2, contour: [0.3, 1] }), sing(`${hush(4)} | ${hook}`, 0.58), bass(0.5, [0.2, 1]),
        drums({ clap: "........L.......", hat: "..h...h...h...h." }, { contour: [0.4, 1] })),
      lift: part(8, pump(8, ["x . - x - x . -", "x . - x . - x ."]), sing(`${hook} | ${chopped}`), bass(0.6),
        drums(drummer(times(7, four), { ...four, clap: "....L...L.L.LLLL" }))),
      build: part(8, lift(8, chopped, "A#4", ["D#4", "D#5"])),
      drop2: part(16, riff(`${bite} | ${bark} | ${bite} | ${bark}`, "D#3", palette), drums(groove), fall(0, 8)),
      outro: part(8, pump(8, ["x . - x - x . -", "x . - x . - x .", "x . - x - x . -", "x . - x . - x .", "x . - x - x . -", "x . - x . - x .", "x . - x - x . -", "x . . . . . . ."], 0.4, [1, 0.6], 4),
        sing(`${hook} | ${hook}`, 0.58, [1, 0.6]), bass(0.6, [1, 0.6]), drums(drummer(times(7, four), { kick: "K...............", cym: "C..." }), { contour: [1, 0.7] })),
    };
    return record({ voices: booth, parts, form: "shout drop sweet lift build drop2 outro" });
  },
  banshee: () => {
    const bpm = 124;
    const { drums, riff, sounds, sweet, pad, fall, part, record } = rig(bpm, { tail: "G#1" });
    const chords = {
      E: ["E2", "E4", "G#4", "B4"], B: ["B2", "D#4", "F#4", "B4"],
      "F#": ["F#2", "C#4", "F#4", "A#4"], "G#m": ["G#2", "D#4", "G#4", "B4"],
    };
    const loop = "E B F# G#m";
    const { sing, echo, pump, bass, lift } = sweet(chords, loop);
    const hook = "^B4~ . . . . . . . G#4 . B4 . D#5 . . . | . . . . . . . . - - F#4 . B4 . . ."
      + " | ^C#5~ . . . . . . . A#4 . C#5 . F#5 . . . | . . . . . . . . - - D#5 . B4 . . .";
    const chopped = "^B4~ . . . . . . . G#4/G#4 . B4 . D#5 . . . | . . . . . . . . - - F#4/F#4 . B4 . . ."
      + " | ^C#5~ . . . . . . . A#4/A#4 . C#5 . F#5 . . . | . . . . . . . . - - D#5/D#5 . B4 . . .";
    const { talk, chug, growl, screech, dive, zap } = sounds;
    const palette = { S: screech({ bend: -12 }), Y: talk([4, 6, 8, 6, 4, 3], [12, 16, 20, 16, 12, 10]), Z: zap(), P: chug(), D: dive(), G: growl() };
    const wail = "S . . . . . . . P P - Y . . . - | - - Z - P - P - S+3 . . . . . - -"
      + " | S+8 . . . . . . . P+8 P+8 - Y+8 . . . - | G+10 . . . . . P+10 - P+10 - D+10 . . . - -";
    const keen = "S . . . . . . . P P - Y . . . - | Y+3 . . . - - Z - S+3 . . . . . - -"
      + " | S+8 . . . . . . . P+8 P+8 - Y+8 . . . - | P/P P/P - P/P P/P - S+10 . . . . . . . - -";
    const a1 = { kick: "K.....K.K.......", snare: "....S.......S...", hat: "..H...H...H...H." };
    const a2 = { kick: "K.....K.K.....K.", snare: "....S.......S...", hat: "..H.H.H...H.H.H." };
    const fill = { kick: "K.....K.........", snare: "....S.......SNNN", hat: "..H...H........." };
    const groove = drummer(times(4, [crash(a1), a2, a1, fill]));
    const four = { kick: "K.......K.......", clap: "....L.......L...", hat: "..H...H...H...H." };
    const parts = {
      intro: part(8, pump(8, ["x . - x - x . -", "x . . x . . x ."], 0.4, [0.4, 1]), sing(`${hook} | ${chopped}`, 0.6, [0.6, 1]), bass(0.6, [0.3, 1]),
        drums(drummer(times(4, { hat: "..h...h...h...h.", clap: "....L.......L..." }), times(3, four), { ...four, clap: "....L.......LLLL" }))),
      build: part(8, lift(8, hook, "D#5", ["G#4", "G#5"])),
      drop: part(16, riff(`${wail} | ${keen} | ${wail} | ${keen}`, "G#2", palette), drums(groove), fall(0, 8)),
      breakdown: part(8, pad(loop, chords, { level: 0.3, swell: 1.2, contour: [0.5, 1] }), sing(hook, 0.58), echo(hook), bass(0.55, [0.2, 1]),
        drums(drummer(times(7, { hat: "..h...h...h...h.", clap: "....L.......L..." }), { clap: "....L...L.L.LLLL" }))),
      build2: part(8, lift(8, chopped, "D#5", ["G#4", "G#5"], { level: 0.34, low: 0 })),
      drop2: part(16, riff(`${keen} | ${wail} | ${keen} | ${wail}`, "G#2", palette), drums(groove), fall(0, 8)),
      outro: part(4, pump(4, ["x . - x - x . -", "x . . x . . x .", "x . - x - x . -", "x . . . . . . ."], 0.4, [1, 0.7], 4), sing(hook, 0.58, [1, 0.7]), bass(0.6),
        drums(drummer(times(3, four), { kick: "K...............", cym: "C..." }))),
    };
    return record({ voices: booth, parts, form: "intro build drop breakdown build2 drop2 outro" });
  },
};

const sheets = new Map();

// Composing takes a while, so a song is composed on its first play and kept,
// never the whole catalog when the module loads.
export function compose(key) {
  if (!sheets.has(key)) sheets.set(key, scores[key]());
  return sheets.get(key);
}
