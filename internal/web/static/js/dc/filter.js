function queryTokens(query) {
  return query.toLowerCase().split(/\s+/).filter(Boolean);
}

export function matchesTokens(haystack, query) {
  const lower = haystack.toLowerCase();
  return queryTokens(query).every((token) => lower.includes(token));
}

export function hitRanges(text, query) {
  const lower = lowerInPlace(text);
  const ranges = queryTokens(query)
    .map((token) => [lower.indexOf(token), token.length])
    .filter(([at]) => at >= 0)
    .map(([at, length]) => [at, at + length])
    .sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const range of ranges) {
    const last = merged[merged.length - 1];
    if (last && range[0] <= last[1]) last[1] = Math.max(last[1], range[1]);
    else merged.push(range);
  }
  return merged;
}

const ASCII = /^[\x00-\x7f]*$/;
const WORD_SEPARATOR = /[\s\-_./:]/;
const UPPER = /\p{Lu}/u;
const LOWER = /\p{Ll}/u;
const DIGIT = /\p{Nd}/u;
// A position in a string stays below 2^32, so the tier dominates the sum.
const TIER = 2 ** 32;

// rankTokens orders the hits of matchesTokens the way quick open's
// scoreQuickOpen does: the first token alone decides. A hit at a word start
// in the name beats a hit inside a word, each earlier position first, and a
// hit only outside the name comes last. name is one name or a list of them.
export function rankTokens(name, haystack, query) {
  if (!matchesTokens(haystack, query)) return -1;
  const first = queryTokens(query)[0] || "";
  return Math.min(...[].concat(name).map((each) => rankName(each, first)));
}

// rankRows hides the rows ranked below zero and sorts the others of each
// group by rank where the group stands, so the groups keep their place: the
// units of a group, the elements that carry its rows (unitOf), stand next to
// each other. Equal ranks keep the order the rows come in, so an empty query
// gives that order back. A group shows only its limitOf(group) best rows. It
// answers the rows shown, in their new order, and per group the count of
// ranked rows the limit hid. Only the shown units that stand out of that
// order move, a moved node restarts its animations and is laid out anew.
export function rankRows(rows, rankOf, { groupOf = () => "", unitOf = (row) => row, limitOf = () => Infinity } = {}) {
  const groups = new Map();
  rows.forEach((row, index) => {
    const key = groupOf(row);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push({ row, unit: unitOf(row), index, rank: rankOf(row) });
  });
  const shown = [];
  const rest = new Map();
  for (const [key, members] of groups) {
    const kept = members.filter(({ rank }) => rank >= 0).sort((a, b) => a.rank - b.rank || a.index - b.index);
    const limit = limitOf(key);
    if (kept.length > limit) rest.set(key, kept.length - limit);
    const show = kept.slice(0, limit);
    const units = new Set(show.map(({ unit }) => unit));
    for (const { unit } of members) {
      const hide = !units.has(unit);
      if (unit.hidden !== hide) unit.hidden = hide;
    }
    arrange(new Set(members.map(({ unit }) => unit)), [...units]);
    for (const { row } of show) shown.push(row);
  }
  return { shown, rest };
}

function arrange(units, order) {
  if (order.length < 2) return;
  let first = units.values().next().value;
  while (units.has(first.previousElementSibling)) first = first.previousElementSibling;
  const at = new Map();
  let last = first;
  for (let node = first; units.has(node); node = node.nextElementSibling) {
    at.set(node, at.size);
    last = node;
  }
  const stay = longestRising(order.map((unit) => at.get(unit)));
  let next = last.nextSibling;
  for (let index = order.length - 1; index >= 0; index--) {
    if (!stay.has(index)) last.parentNode.insertBefore(order[index], next);
    next = order[index];
  }
}

function longestRising(values) {
  const tails = [];
  const before = [];
  values.forEach((value, index) => {
    if (value === undefined) return;
    let low = 0;
    let high = tails.length;
    while (low < high) {
      const mid = (low + high) >> 1;
      if (values[tails[mid]] < value) low = mid + 1;
      else high = mid;
    }
    before[index] = low > 0 ? tails[low - 1] : -1;
    tails[low] = index;
  });
  const stay = new Set();
  for (let index = tails.length ? tails[tails.length - 1] : -1; index >= 0; index = before[index]) stay.add(index);
  return stay;
}

// The lowercased name keeps every offset of the original, which alone still
// carries the case a camel case word start is read from.
function lowerInPlace(text) {
  if (ASCII.test(text)) return text.toLowerCase();
  return Array.from(text, (ch) => {
    const low = ch.toLowerCase();
    return low.length === ch.length ? low : ch;
  }).join("");
}

function rankName(name, token) {
  const lower = lowerInPlace(name);
  let inside = -1;
  for (let at = lower.indexOf(token); at >= 0; at = lower.indexOf(token, at + 1)) {
    if (startsWord(name, at)) return at;
    if (inside < 0) inside = at;
  }
  return inside >= 0 ? TIER + inside : 2 * TIER;
}

// A word starts at the start of the name, after a separator, at a camel case
// hump (Game|Controller) or at the last capital of an acronym that a word
// follows (HTTP|Client).
function startsWord(name, at) {
  if (at === 0) return true;
  const before = name[at - 1];
  if (WORD_SEPARATOR.test(before)) return true;
  if (!UPPER.test(name[at])) return false;
  if (LOWER.test(before) || DIGIT.test(before)) return true;
  return UPPER.test(before) && LOWER.test(name[at + 1] || "");
}
