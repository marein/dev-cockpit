// commentGutter answers the extension maker that marks the lines carrying a
// line comment thread in every gutter beside them, over CodeMirror's own state
// and view modules, which a page only has once it loaded them at run time. The
// change gutter of a diff stays plain, a tint there reads as a change.
// The editor and the inline diff of the assistant's aside mark alike. data
// lists the threads as { line, outdated, resolved }, null marks nothing.
export function commentGutter(state, view) {
  class CommentLineMarker extends view.GutterMarker {
    constructor(kind) {
      super();
      this.kind = kind;
      this.elementClass = kind ? `cm-comment-line cm-comment-line-${kind}` : "cm-comment-line";
    }

    eq(other) {
      return other.kind === this.kind;
    }
  }

  const markers = {
    open: new CommentLineMarker(""),
    outdated: new CommentLineMarker("outdated"),
    resolved: new CommentLineMarker("resolved"),
  };

  const cell = ".cm-gutter:not(.cm-changeGutter) .cm-comment-line";
  const theme = view.EditorView.theme({
    ".cm-gutters": { cursor: "pointer" },
    [cell]: {
      color: "var(--tblr-yellow, #f59f00)",
      fontWeight: "700",
      backgroundColor: "rgba(245, 159, 0, 0.18)",
    },
    [`${cell}.cm-comment-line-outdated`]: {
      color: "var(--tblr-orange, #f76707)",
      backgroundColor: "rgba(247, 103, 7, 0.18)",
      textDecoration: "line-through",
    },
    [`${cell}.cm-comment-line-resolved`]: {
      color: "var(--tblr-green, #2fb344)",
      fontWeight: "400",
      backgroundColor: "rgba(47, 179, 68, 0.14)",
    },
  });

  // A note of a resolved thread asks for nothing more, so it marks its line
  // only while no other note there still asks for something.
  const rank = { resolved: 0, open: 1, outdated: 2 };

  return function commentsExtension(data) {
    if (!data) return [];
    if (data.length === 0) return [theme];
    const build = (doc) => {
      const byLine = new Map();
      for (const c of data) {
        if (c.line < 1 || c.line > doc.lines) continue;
        const kind = c.resolved ? "resolved" : c.outdated ? "outdated" : "open";
        const held = byLine.get(c.line);
        if (!held || rank[kind] > rank[held]) byLine.set(c.line, kind);
      }
      const ranges = [];
      for (const [line, kind] of byLine) {
        ranges.push(markers[kind].range(doc.line(line).from));
      }
      return state.RangeSet.of(ranges, true);
    };
    const field = state.StateField.define({
      create: (st) => build(st.doc),
      update: (set, tr) => (tr.docChanged ? set.map(tr.changes) : set),
      provide: (f) => view.gutterLineClass.from(f),
    });
    return [theme, field];
  };
}
