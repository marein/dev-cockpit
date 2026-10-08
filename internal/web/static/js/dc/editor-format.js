// commitDate is how a commit's time reads next to it: the device's own format,
// because that is the one its owner reads without translating.
export function commitDate(seconds) {
  if (!seconds) return "";
  return new Date(seconds * 1000).toLocaleString();
}

// isoDate is the compact form for a gutter, the local day as 2026-09-25. The
// whole date stands in the tooltip through commitDate.
export function isoDate(seconds) {
  if (!seconds) return "";
  const d = new Date(seconds * 1000);
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// shortName keeps a gutter narrow. The full name is in the tooltip.
export function shortName(name) {
  const first = String(name || "").split(/\s+/)[0] || "";
  return first.length > 12 ? `${first.slice(0, 11)}…` : first;
}

export function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

// The two measures the diff limits are read against.
export function countLines(text) {
  if (!text) return 0;
  let lines = 1;
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) lines += 1;
  }
  return lines;
}

const utf8 = new TextEncoder();

export function byteLength(text) {
  return text ? utf8.encode(text).length : 0;
}
