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
// gives that order back. It answers the rows left, in their new order.
export function rankRows(rows, rankOf, { groupOf = () => "", unitOf = (row) => row } = {}) {
  const groups = new Map();
  rows.forEach((row, index) => {
    const rank = rankOf(row);
    unitOf(row).hidden = rank < 0;
    const key = groupOf(row);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push({ row, index, rank: rank < 0 ? Infinity : rank });
  });
  const shown = [];
  for (const members of groups.values()) {
    members.sort((a, b) => a.rank - b.rank || a.index - b.index);
    const units = new Set(members.map(({ row }) => unitOf(row)));
    const last = [...units].find((unit) => !units.has(unit.nextElementSibling));
    const after = last?.nextSibling || null;
    for (const { row } of members) {
      last?.parentNode?.insertBefore(unitOf(row), after);
      if (!unitOf(row).hidden) shown.push(row);
    }
  }
  return shown;
}

// The lowercased name keeps every offset of the original, which alone still
// carries the case a camel case word start is read from.
function lowerInPlace(text) {
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
