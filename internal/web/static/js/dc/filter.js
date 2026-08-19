export function matchesTokens(haystack, query) {
  const lower = haystack.toLowerCase();
  return query.toLowerCase().split(/\s+/).every((token) => lower.includes(token));
}
