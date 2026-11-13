// lineBlocks answers what puts an element under a line of a CodeMirror view,
// over its state and view modules, which a page only has once it loaded them
// at run time: the editor's threads and the field of a new comment, and the
// same in the inline diff of the assistant's aside. An element stays the very
// node it was handed, so what is typed into it survives every rebuild of the
// view, and keys, presses and focus inside it are its own, never the view's.
// It follows its line through every edit the way a line comment does, and it
// stands in the visible width beside the gutters, also where long lines
// scroll the code sideways.
export function lineBlocks(state, view) {
  const { StateEffect, StateField } = state;
  const { Decoration, EditorView, ViewPlugin, WidgetType } = view;

  // A short file holds its content at the pane's height, so CodeMirror sees
  // no change of it when a block grows and keeps the old line heights. The
  // block's height is part of its widget, a new height redraws it in place
  // and makes the view measure its lines again. A block that grew or shrank
  // in place hands the view its new height until it measured, else the gutter
  // stands a frame beside the wrong lines. A block put under a line leaves
  // the view its guess: the gutter follows only a change of the whole height,
  // and the very height of a block that just moved would keep it under its
  // old line.
  class LineBlock extends WidgetType {
    constructor(dom, height, sized) {
      super();
      this.dom = dom;
      this.height = height;
      this.sized = sized;
    }

    get estimatedHeight() {
      return this.sized ? this.height : -1;
    }

    eq(other) {
      return other.dom === this.dom && other.height === this.height;
    }

    updateDOM(dom) {
      return dom === this.dom;
    }

    toDOM() {
      return this.dom;
    }

    ignoreEvent() {
      return true;
    }
  }

  const place = StateEffect.define();
  const resize = StateEffect.define();
  const decorate = (entries, doc) => Decoration.set(entries.map((entry) => Decoration.widget({ widget: new LineBlock(entry.dom, entry.height, entry.sized), block: true, side: 1 }).range(doc.lineAt(entry.from).to)), true);
  const field = StateField.define({
    create: () => ({ entries: [], deco: Decoration.none }),
    update(value, tr) {
      for (const effect of tr.effects) if (effect.is(place)) return { entries: effect.value, deco: decorate(effect.value, tr.newDoc) };
      let entries = value.entries;
      for (const effect of tr.effects) {
        if (effect.is(resize)) entries = entries.map((entry) => (entry.dom === effect.value.dom ? { ...entry, height: effect.value.height, sized: true } : entry));
      }
      if (tr.docChanged && entries.length) entries = entries.map((entry) => ({ ...entry, from: tr.newDoc.lineAt(tr.changes.mapPos(entry.from, 1)).from }));
      return entries === value.entries ? value : { entries, deco: decorate(entries, tr.newDoc) };
    },
    provide: (f) => EditorView.decorations.from(f, (value) => value.deco),
  });

  const fitKey = {};
  const fit = ViewPlugin.define((v) => {
    const measure = () => v.requestMeasure({
      key: fitKey,
      read: () => {
        const gutters = v.dom.querySelector(".cm-gutters");
        const left = gutters ? gutters.offsetWidth : 0;
        return { left, width: Math.max(0, v.scrollDOM.clientWidth - left) };
      },
      write: ({ left, width }) => {
        v.dom.style.setProperty("--dc-line-block-left", `${left}px`);
        v.dom.style.setProperty("--dc-line-block-width", `${width}px`);
      },
    });
    measure();
    return { update: (u) => { if (u.geometryChanged || u.docChanged) measure(); } };
  });

  const entryOf = (v, dom) => v.state.field(field, false)?.entries.find((entry) => entry.dom === dom);
  const sizes = new ResizeObserver((records) => {
    for (const { target, borderBoxSize } of records) {
      const root = target.isConnected && target.closest(".cm-editor");
      const v = root && EditorView.findFromDOM(root);
      const entry = v && entryOf(v, target);
      const height = borderBoxSize[0].blockSize;
      if (entry && entry.height !== height) v.dispatch({ effects: resize.of({ dom: target, height }) });
    }
  });
  const placed = new WeakMap();

  return {
    extension: [field, fit],
    // set puts the elements under their lines, a list of { dom, line }, and
    // takes every other element of the view away. A line past the end of
    // the document holds none.
    set(v, list) {
      const doc = v.state.doc;
      const entries = list.filter((item) => item.line >= 1 && item.line <= doc.lines).map((item) => ({ dom: item.dom, from: doc.line(item.line).from, height: entryOf(v, item.dom)?.height }));
      const before = placed.get(v) || new Set();
      const now = new Set(entries.map((entry) => entry.dom));
      for (const dom of before) if (!now.has(dom)) sizes.unobserve(dom);
      for (const dom of now) if (!before.has(dom)) sizes.observe(dom);
      placed.set(v, now);
      v.dispatch({ effects: place.of(entries) });
    },
    // held is what a view holds under its lines, in the list set takes, so a
    // view built in its place takes it over.
    held(v) {
      const value = v.state.field(field, false);
      return value ? value.entries.map((entry) => ({ dom: entry.dom, line: v.state.doc.lineAt(entry.from).number })) : [];
    },
    // lineOf is the line an element stands under now, 0 where the view holds
    // it nowhere.
    lineOf(v, dom) {
      const entry = entryOf(v, dom);
      return entry ? v.state.doc.lineAt(entry.from).number : 0;
    },
    destroy() {
      sizes.disconnect();
    },
  };
}
