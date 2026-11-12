import { genreOf, onChange, playing, position } from "@dc/music";

// Moves are measured in beats of the song. A step lands on its beat, a jump
// lands on the one, so the dancer keeps time with any tempo. Jumps stay low,
// the status line clips what leaves its height.
const STYLES = {
  "Party DnB": { prop: "cap", accents: [[0, 1], [1, 0.8], [2.5, 0.9], [3, 0.8]], kick: 0.6, shift: 0.9, tilt: 6, bounce: 1.2 },
  "Party Dubstep": { prop: "horns", accents: [[0, 1], [1, 0.4], [2, 1.8], [3, 0.4]], robot: 0.25, shift: 0.6, tilt: 5, bounce: 1.5, wobble: 0.04 },
  "Dark Techno": { prop: "headphones", trick: "stutter", every: 4, stomp: true, lids: 0.45, bounce: 1.4, tilt: 1.5 },
  "Deep Dubstep": { prop: "snorkel", steps: 0.5, every: 4, spin: "pirouette", air: 4, lift: 0.8, sway: 3 },
  "Melodic House": { prop: "glowsticks", every: 4, spin: "salto" },
  "Chill Electronic": { prop: "shades", steps: 0.5, tilt: 1.5, sway: 4, bounce: 0.7 },
  "Space Ambient": { prop: "rocket", float: true },
  "Classic Rock": { prop: "guitar", every: 2, air: 1, height: 2, bounce: 1.6 },
};
const BASE = {
  prop: null, steps: 1, every: 0, spin: "none", air: 2, height: 1.5, lift: 1, bounce: 1, tilt: 3, sway: 0,
  wobble: 0, float: false, accents: null, kick: 0, shift: 0, robot: 0, stomp: false, trick: null, lids: 0,
};
const EYE = 3.047;
const BLINK_MS = 160;
const CROUCH = 0.5;
const LAND = 0.4;

const mod = (a, n) => ((a % n) + n) % n;
const ease = (t) => (1 - Math.cos(Math.PI * t)) / 2;

function step(style, beat) {
  if (!style.accents) {
    const at = beat * style.steps;
    const n = Math.floor(at);
    return { n, p: at - n, strength: 1, since: (at - n) / style.steps };
  }
  const accents = style.accents;
  let bar = Math.floor(beat / 4);
  let i = -1;
  for (let j = 0; j < accents.length; j++) if (accents[j][0] <= beat - bar * 4) i = j;
  if (i < 0) {
    bar -= 1;
    i = accents.length - 1;
  }
  const start = bar * 4 + accents[i][0];
  const end = i + 1 < accents.length ? bar * 4 + accents[i + 1][0] : (bar + 1) * 4 + accents[0][0];
  return { n: bar * accents.length + i, p: (beat - start) / (end - start), strength: accents[i][1], since: beat - start };
}

function pose(style, beat) {
  const P = { x: 0, y: 0, rot: 0, spin: 1, by: 0, sx: 1, sy: 1, tilt: 0, lift: [0, 0, 0, 0], eyes: style.lids, strobe: false };
  const s = step(style, style.robot ? Math.floor(beat / style.robot) * style.robot : beat);
  const wave = Math.sin(Math.PI * s.p);
  const left = mod(s.n, 2) === 0;
  const side = left ? 1 : -1;
  const hit = s.strength * Math.exp(-s.since * 9);
  if (style.float) {
    P.y = -1.2 * Math.sin((Math.PI * beat) / 8);
    P.x = Math.sin((Math.PI * beat) / 16);
    P.rot = 4 * Math.sin((Math.PI * beat) / 8 + 1);
    P.by = -0.3 * Math.abs(Math.sin((Math.PI * beat) / 2));
    P.tilt = 2 * Math.sin((Math.PI * beat) / 4);
  } else {
    const lift = 1.4 * style.lift * (style.stomp ? Math.sin(Math.PI * s.p ** 1.6) : wave ** 1.2);
    if (left) P.lift = [lift * (1 + style.kick), lift * (1 - style.kick / 2), 0, 0];
    else P.lift = [0, 0, lift * (1 - style.kick / 2), lift * (1 + style.kick)];
    P.by = style.bounce * (-0.45 * wave + 0.25 * hit);
    P.sy = 1 - 0.05 * style.bounce * hit;
    P.sx = 1 + 0.03 * style.bounce * hit;
    P.tilt = side * style.tilt * (style.robot ? 1 : wave) + style.sway * Math.sin((Math.PI * beat) / 2);
    P.x = -side * style.shift * (style.robot ? 1 : wave);
  }
  if (style.wobble) {
    const wub = Math.sin(4 * Math.PI * beat);
    P.sx += style.wobble * wub;
    P.sy -= style.wobble * 0.6 * wub;
  }
  if (!style.every) return P;
  const cycle = style.every * 4;
  const pos = mod(beat, cycle);
  if (style.trick === "stutter") {
    if (pos >= cycle - 2) {
      const at = beat * 4;
      const n = Math.floor(at);
      const ramp = (pos - (cycle - 2)) / 2;
      const lift = 0.8 * Math.sin(Math.PI * (at - n));
      const h = Math.exp(-(at - n) * 6);
      P.lift = mod(n, 2) ? [0, 0, lift, lift] : [lift, lift, 0, 0];
      P.by = 0.3 + 0.6 * ramp + 0.2 * h;
      P.sy = 0.96 - 0.06 * ramp - 0.03 * h;
      P.sx = 1.03 + 0.03 * ramp;
      P.tilt = 0;
      P.x = (mod(n, 2) ? 1 : -1) * 0.2 * ramp;
      P.eyes = 1;
      P.strobe = true;
    } else if (beat >= cycle && pos < 0.6) {
      const k = (1 - pos / 0.6) ** 2;
      P.by += 1.4 * k;
      P.sy -= 0.2 * k;
      P.sx += 0.1 * k;
    }
    return P;
  }
  const air = Math.min(style.air, cycle - 1);
  const takeoff = cycle - air;
  const round = Math.floor(beat / cycle);
  if (pos >= takeoff) {
    const q = (pos - takeoff) / air;
    const turn = ease(q);
    P.y -= style.height * 4 * q * (1 - q);
    if (style.spin === "salto") P.rot += (mod(round, 2) ? -360 : 360) * turn;
    else if (style.spin === "pirouette") P.spin = Math.cos(2 * Math.PI * turn);
    const tuck = 1.6 * Math.sin(Math.PI * q);
    const stretch = Math.max(0, 1 - q * 4);
    Object.assign(P, { x: 0, by: 0, tilt: 0, sy: 1 + 0.12 * stretch, sx: 1 - 0.06 * stretch, lift: [tuck, tuck, tuck, tuck] });
  } else if (pos >= takeoff - CROUCH) {
    const c = ease((pos - (takeoff - CROUCH)) / CROUCH);
    P.lift = P.lift.map((v) => v * (1 - c));
    P.by = P.by * (1 - c) + c;
    P.sy = 1 - 0.15 * c;
    P.sx = 1 + 0.07 * c;
    P.tilt *= 1 - c;
  } else if (beat >= cycle && pos < LAND) {
    const k = (1 - pos / LAND) ** 2;
    P.by += k;
    P.sy -= 0.15 * k;
    P.sx += 0.08 * k;
    P.lift = P.lift.map((v) => v * (1 - k));
  }
  return P;
}

const headTop = (P) => P.by + 12.079 * (1 - P.sy);

class SongDancer extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.char = this.querySelector("[data-dancer-char]");
    this.body = this.querySelector("[data-dancer-body]");
    this.legs = [...this.querySelectorAll("[data-dancer-leg]")];
    this.lids = [...this.querySelectorAll("[data-dancer-lid]")];
    this.props = [...this.querySelectorAll("[data-dancer-prop]")];
    this.pulsers = [...this.querySelectorAll("[data-dancer-pulse]")];
    this.sticks = [...this.querySelectorAll("[data-dancer-stick]")];
    this.flame = this.querySelector("[data-dancer-flame]");
    this.strum = this.querySelector("[data-dancer-strum]");
    this.still = matchMedia("(prefers-reduced-motion: reduce)");
    this.still.addEventListener("change", () => this.update(), { signal });
    signal.addEventListener("abort", onChange(() => this.update()));
    this.update();
  }

  disconnectedCallback() {
    this.halt();
    this.ac?.abort();
    this.ac = null;
  }

  halt() {
    if (this.frame) cancelAnimationFrame(this.frame);
    this.frame = null;
  }

  update() {
    this.halt();
    const genre = genreOf(playing());
    this.moves = { ...BASE, ...STYLES[genre] };
    this.dataset.dancerGenre = genre ?? "";
    for (const prop of this.props) prop.style.display = prop.dataset.dancerProp === this.moves.prop ? "" : "none";
    this.blink = -1;
    this.nextBlink = performance.now() + 2000;
    if (!genre) return;
    if (this.still.matches) {
      this.draw(0, -1);
      return;
    }
    const frame = (now) => {
      const at = position();
      this.draw(at?.bpm ? (at.elapsed * at.bpm) / 60 : 0, now);
      this.frame = requestAnimationFrame(frame);
    };
    this.frame = requestAnimationFrame(frame);
  }

  draw(beat, now) {
    const style = this.moves;
    const P = pose(style, beat);
    this.char.setAttribute("transform", `translate(${P.x} ${P.y}) rotate(${P.rot} 12 12.5) translate(12 0) scale(${P.spin} 1) translate(-12 0)`);
    this.body.setAttribute("transform", `translate(0 ${P.by}) rotate(${P.tilt} 12 17.079) translate(12 17.079) scale(${P.sx} ${P.sy}) translate(-12 -17.079)`);
    this.legs.forEach((leg, i) => leg.setAttribute("transform", `translate(0 ${-P.lift[i]})`));
    const prop = this.props.find((el) => el.dataset.dancerProp === style.prop);
    if (prop?.hasAttribute("data-dancer-lag")) {
      const lag = Math.min(0.3, Math.max(-0.3, (headTop(pose(style, beat - 0.07)) - headTop(P)) * 0.8));
      prop.setAttribute("transform", `translate(0 ${lag})`);
    }
    const pulse = P.strobe ? Math.exp(-mod(beat * 4, 1) * 4) : Math.exp(-mod(beat, 1) * 5) * (mod(Math.floor(beat), 4) === 0 ? 1 : 0.7);
    for (const el of this.pulsers) {
      if (el.dataset.dancerPulse === "stroke") {
        el.setAttribute("stroke-opacity", (0.3 + 0.7 * pulse).toFixed(3));
        el.setAttribute("stroke-width", (0.36 + 0.14 * pulse).toFixed(3));
      } else {
        el.setAttribute("fill-opacity", (0.45 + 0.55 * pulse).toFixed(3));
      }
    }
    if (style.prop === "glowsticks") {
      const swing = 32 * Math.cos(Math.PI * beat);
      this.sticks[0].setAttribute("transform", `translate(0.4 12.5) rotate(${-28 + swing})`);
      this.sticks[1].setAttribute("transform", `translate(23.6 12.5) rotate(${28 + swing})`);
    } else if (style.prop === "rocket") {
      const fx = 1 + 0.5 * pulse + 0.12 * Math.sin(now / 40);
      this.flame.setAttribute("transform", `translate(-1 17.3) scale(${fx} ${0.9 + 0.15 * pulse}) translate(1 -17.3)`);
    } else if (style.prop === "guitar") {
      this.strum.setAttribute("transform", `rotate(${2.5 * Math.sin(4 * Math.PI * beat)} 7 14)`);
    }
    if (now >= this.nextBlink) {
      this.blink = now;
      this.nextBlink = now + 2500 + Math.random() * 4000;
    }
    const since = now - this.blink;
    const blink = this.blink >= 0 && since < BLINK_MS ? Math.sin((Math.PI * since) / BLINK_MS) : 0;
    for (const lid of this.lids) lid.setAttribute("height", (EYE * Math.max(blink, P.eyes)).toFixed(3));
  }
}

customElements.define("dc-song-dancer", SongDancer);
