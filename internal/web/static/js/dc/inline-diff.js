import { commentGutter } from "@dc/comment-gutter";
import { isDark } from "@dc/theme";

// How many unchanged lines stay visible around a change when the folding is
// on. Three reads well on a phone and on a wide screen, it is not worth a
// setting.
const collapseMargin = 3;

export const collapseOption = (collapse) => (collapse ? { margin: collapseMargin } : undefined);

export function unifiedDiff(merge, original, collapse) {
  return merge.unifiedMergeView({
    original,
    // Nothing in the editor writes a chunk back into the buffer, the
    // revision side is there to be read.
    mergeControls: false,
    gutter: true,
    highlightChanges: true,
    collapseUnchanged: collapseOption(collapse),
  });
}

const threadCount = (n) => (n === 1 ? "1 thread" : `${n} threads`);

// inlineDiff draws doc against original as the editor's inline diff, for the
// assistant's aside. onLine hears a press on the gutter beside a line of doc,
// a line only original holds stands in a widget and takes none. The handle
// marks the lines of line comment threads the way the editor does
// (setComments), puts an element under a line (setBlocks), whose height the
// diff follows, and a fold of unchanged lines names the threads it hides.
export async function inlineDiff(original, doc, { onLine } = {}) {
  const [view, state, merge, theme] = await Promise.all([
    import("@codemirror/view"),
    import("@codemirror/state"),
    import("@codemirror/merge"),
    import("@codemirror/theme-one-dark"),
  ]);
  const { BlockType, Decoration, EditorView, ViewPlugin, WidgetType, lineNumbers } = view;
  const { Compartment, EditorState, StateEffect, StateField } = state;
  const scheme = new Compartment();
  const marks = new Compartment();
  const commentsExtension = commentGutter(state, view);
  const schemeTheme = () => (isDark() ? theme.oneDark : []);
  let threads = [];

  class LineBlock extends WidgetType {
    constructor(dom) {
      super();
      this.dom = dom;
    }

    eq(other) {
      return other.dom === this.dom;
    }

    toDOM() {
      return this.dom;
    }

    ignoreEvent() {
      return true;
    }
  }

  const placeBlocks = StateEffect.define();
  const blocks = StateField.define({
    create: () => Decoration.none,
    update: (set, tr) => {
      for (const effect of tr.effects) if (effect.is(placeBlocks)) return effect.value;
      return set.map(tr.changes);
    },
    provide: (f) => EditorView.decorations.from(f),
  });

  // The fold is @codemirror/merge's own widget, its note is a child that
  // takes no pointer, so a press on it still lands where the fold reads its
  // place to unfold.
  const paintFolds = (v) => {
    for (const fold of v.contentDOM.querySelectorAll(".cm-collapsedLines")) {
      const block = v.lineBlockAt(v.posAtDOM(fold));
      const from = v.state.doc.lineAt(block.from).number;
      const to = v.state.doc.lineAt(block.to).number;
      const hidden = threads.filter((t) => t.line >= from && t.line <= to).length;
      let note = fold.querySelector(":scope > [data-fold-threads]");
      if (!hidden) {
        note?.remove();
        continue;
      }
      if (!note) {
        note = document.createElement("span");
        note.className = "pe-none";
        note.dataset.foldThreads = "";
        fold.append(note);
      }
      note.textContent = ` · ${threadCount(hidden)}`;
    }
  };
  const foldKey = {};
  const folds = ViewPlugin.define((v) => {
    const paint = () => v.requestMeasure({ key: foldKey, read: () => null, write: () => paintFolds(v) });
    paint();
    return { update: paint };
  });

  const editor = new EditorView({
    state: EditorState.create({
      doc,
      extensions: [
        lineNumbers(),
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        EditorView.lineWrapping,
        scheme.of(schemeTheme()),
        marks.of(commentsExtension(null)),
        blocks,
        folds,
        unifiedDiff(merge, original, true),
      ],
    }),
  });
  const ac = new AbortController();
  document.addEventListener("dc:theme", () => editor.dispatch({ effects: scheme.reconfigure(schemeTheme()) }), { signal: ac.signal });
  if (onLine) {
    editor.dom.addEventListener("click", (event) => {
      if (!(event.target instanceof Element) || !event.target.closest(".cm-gutters")) return;
      const cell = event.target.closest(".cm-gutterElement")?.getBoundingClientRect();
      const y = (cell ? cell.top + cell.height / 2 : event.clientY) - editor.documentTop;
      const block = editor.elementAtHeight(y);
      if (block.type !== BlockType.Text || y < block.top || y > block.bottom) return;
      onLine(editor.state.doc.lineAt(block.from).number);
    }, { signal: ac.signal });
  }
  const sizes = new ResizeObserver(() => editor.requestMeasure());
  let placed = new Set();
  return {
    dom: editor.dom,
    get doc() {
      return editor.state.doc;
    },
    lineText(line) {
      const text = editor.state.doc;
      return line >= 1 && line <= text.lines ? text.line(line).text : "";
    },
    setComments(data) {
      threads = data || [];
      editor.dispatch({ effects: marks.reconfigure(commentsExtension(data)) });
    },
    setBlocks(byLine) {
      const text = editor.state.doc;
      const ranges = [];
      const next = new Set();
      for (const [line, dom] of byLine) {
        if (line < 1 || line > text.lines) continue;
        ranges.push(Decoration.widget({ widget: new LineBlock(dom), block: true, side: 1 }).range(text.line(line).to));
        next.add(dom);
        if (!placed.has(dom)) sizes.observe(dom);
      }
      for (const dom of placed) if (!next.has(dom)) sizes.unobserve(dom);
      placed = next;
      editor.dispatch({ effects: placeBlocks.of(Decoration.set(ranges, true)) });
    },
    destroy() {
      ac.abort();
      sizes.disconnect();
      editor.destroy();
    },
  };
}
