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

export async function inlineDiff(original, doc) {
  const [view, state, merge, theme] = await Promise.all([
    import("@codemirror/view"),
    import("@codemirror/state"),
    import("@codemirror/merge"),
    import("@codemirror/theme-one-dark"),
  ]);
  const { EditorView, lineNumbers } = view;
  const { EditorState, Compartment } = state;
  const scheme = new Compartment();
  const schemeTheme = () => (isDark() ? theme.oneDark : []);
  const editor = new EditorView({
    state: EditorState.create({
      doc,
      extensions: [
        lineNumbers(),
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        EditorView.lineWrapping,
        scheme.of(schemeTheme()),
        unifiedDiff(merge, original, true),
      ],
    }),
  });
  const ac = new AbortController();
  document.addEventListener("dc:theme", () => editor.dispatch({ effects: scheme.reconfigure(schemeTheme()) }), { signal: ac.signal });
  return {
    dom: editor.dom,
    destroy() {
      ac.abort();
      editor.destroy();
    },
  };
}
