const NO_MATCH = -1;

export function lineTokens(lines) {
  const ids = new Map();
  const tokens = new Int32Array(lines.length);
  lines.forEach((line, i) => {
    let id = ids.get(line);
    if (id === undefined) {
      id = ids.size;
      ids.set(line, id);
    }
    tokens[i] = id;
  });
  return { tokens, tokenOf: (line) => ids.get(line) ?? NO_MATCH };
}

export function diffLines(a, b, scanLimit = Infinity) {
  let lo = 0;
  while (lo < a.length && lo < b.length && a[lo] === b[lo]) lo++;
  let hiA = a.length;
  let hiB = b.length;
  while (hiA > lo && hiB > lo && a[hiA - 1] === b[hiB - 1]) hiA--, hiB--;
  const chunks = [];
  let pa = lo;
  let pb = lo;
  const gap = (i, j) => {
    if (i > pa || j > pb) chunks.push({ fromLineA: pa, toLineA: i, fromLineB: pb, toLineB: j });
  };
  const inB = new Set();
  for (let j = lo; j < hiB; j++) inB.add(b[j]);
  const inA = new Set();
  const keepA = [];
  for (let i = lo; i < hiA; i++) {
    if (inB.has(a[i])) keepA.push(i);
    inA.add(a[i]);
  }
  const keepB = [];
  for (let j = lo; j < hiB; j++) if (inA.has(b[j])) keepB.push(j);
  const ca = Int32Array.from(keepA, (i) => a[i]);
  const cb = Int32Array.from(keepB, (j) => b[j]);
  commonPairs(ca, cb, scanLimit / 2, (x, y) => {
    gap(keepA[x], keepB[y]);
    pa = keepA[x] + 1;
    pb = keepB[y] + 1;
  });
  gap(hiA, hiB);
  return chunks;
}

function commonPairs(a, b, maxDepth, emit) {
  const size = a.length + b.length + 2;
  const vf = new Int32Array(2 * size + 1);
  const vb = new Int32Array(2 * size + 1);
  const run = (i, j, n) => {
    for (let k = 0; k < n; k++) emit(i + k, j + k);
  };
  const tasks = [[0, a.length, 0, b.length]];
  while (tasks.length) {
    const task = tasks.pop();
    if (task.length === 3) {
      run(...task);
      continue;
    }
    let [aLo, aHi, bLo, bHi] = task;
    let head = 0;
    while (aLo + head < aHi && bLo + head < bHi && a[aLo + head] === b[bLo + head]) head++;
    run(aLo, bLo, head);
    aLo += head;
    bLo += head;
    let tail = 0;
    while (aHi - tail > aLo && bHi - tail > bLo && a[aHi - tail - 1] === b[bHi - tail - 1]) tail++;
    aHi -= tail;
    bHi -= tail;
    if (tail) tasks.push([aHi, bHi, tail]);
    if (aLo === aHi || bLo === bHi) continue;
    const snake = middleSnake(a, aLo, aHi, b, bLo, bHi, vf, vb, size, maxDepth);
    if (!snake) continue;
    const [x0, y0, x1, y1] = snake;
    tasks.push([aLo + x1, aHi, bLo + y1, bHi]);
    if (x1 > x0) tasks.push([aLo + x0, bLo + y0, x1 - x0]);
    tasks.push([aLo, aLo + x0, bLo, bLo + y0]);
  }
}

function middleSnake(a, aLo, aHi, b, bLo, bHi, vf, vb, off, maxDepth) {
  const n = aHi - aLo;
  const m = bHi - bLo;
  const delta = n - m;
  const odd = (delta & 1) !== 0;
  const max = Math.ceil((n + m) / 2);
  vf[off + 1] = 0;
  vb[off + 1] = 0;
  for (let d = 0; d <= max; d++) {
    if (d > maxDepth) return null;
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && vf[off + k - 1] < vf[off + k + 1]) ? vf[off + k + 1] : vf[off + k - 1] + 1;
      let y = x - k;
      const x0 = x;
      const y0 = y;
      while (x < n && y < m && a[aLo + x] === b[bLo + y]) x++, y++;
      vf[off + k] = x;
      const back = delta - k;
      if (odd && back >= -(d - 1) && back <= d - 1 && x + vb[off + back] >= n) return [x0, y0, x, y];
    }
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && vb[off + k - 1] < vb[off + k + 1]) ? vb[off + k + 1] : vb[off + k - 1] + 1;
      let y = x - k;
      const x0 = x;
      const y0 = y;
      while (x < n && y < m && a[aHi - 1 - x] === b[bHi - 1 - y]) x++, y++;
      vb[off + k] = x;
      const fwd = delta - k;
      if (!odd && fwd >= -d && fwd <= d && vf[off + fwd] + x >= n) return [n - x, m - y, n - x0, m - y0];
    }
  }
  return null;
}
