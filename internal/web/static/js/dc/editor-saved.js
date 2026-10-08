// The open tabs, their order, the active one, the revision each one is
// compared against and whether the blame gutter is on. Every entry carries a
// type; a bare string is an older saved state and reads as type "file", so
// nothing has to be migrated. A comparison is its two paths: the content
// comes from the disk on restore, unsaved changes fall away like they do for
// every other tab.
export function savedLines(lines) {
  if (!Array.isArray(lines)) return [];
  return [...new Set(lines.filter((n) => Number.isInteger(n) && n > 0))].slice(0, 500);
}

export function savedView(v) {
  const place = (p) => !!p && typeof p.line === "number" && typeof p.column === "number";
  if (!v || !place(v.anchor) || !place(v.head)) return null;
  const out = {
    anchor: { line: v.anchor.line, column: v.anchor.column },
    head: { line: v.head.line, column: v.head.column },
    scrollTop: Math.max(0, Math.round(Number(v.scrollTop) || 0)),
    scrollLeft: Math.max(0, Math.round(Number(v.scrollLeft) || 0)),
  };
  const expanded = savedLines(v.expanded);
  if (expanded.length) out.expanded = expanded;
  return out;
}

export function savedScroll(s) {
  if (!s || typeof s !== "object") return null;
  return {
    top: Math.max(0, Math.round(Number(s.top) || 0)),
    left: Math.max(0, Math.round(Number(s.left) || 0)),
  };
}

// savedEntries reads a stored set, whatever age it is: bare strings are file
// paths, and the legacy diff map marks which of them had a comparison open.
// An entry it cannot read stays as null, the groups count positions.
export function savedEntries(saved, headRev) {
  const legacy = saved.diff && typeof saved.diff === "object" ? saved.diff : {};
  return saved.open.map((e) => {
    if (typeof e === "string" && e) {
      const old = legacy[e];
      return { type: "file", path: e, diff: old && old.mode && old.mode !== "off" ? headRev : "", blame: false, preview: false };
    }
    if (e && typeof e === "object" && e.type === "compare" && typeof e.left === "string" && typeof e.right === "string") return e;
    if (e && typeof e === "object" && e.type === "revdiff" && typeof e.left === "string" && typeof e.right === "string"
      && typeof e.leftRev === "string" && typeof e.rightRev === "string") return e;
    if (e && typeof e === "object" && e.type === "external" && typeof e.path === "string" && e.path) {
      return { type: "external", path: e.path, view: savedView(e.view) };
    }
    if (e && typeof e === "object" && typeof e.path === "string" && e.path) {
      return {
        type: "file",
        path: e.path,
        diff: typeof e.diff === "string" ? e.diff : "",
        blame: e.blame === true,
        preview: e.preview === true,
        view: savedView(e.view),
      };
    }
    return null;
  });
}
