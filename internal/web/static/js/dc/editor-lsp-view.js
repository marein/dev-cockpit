// lspView answers the code navigation's extensions over CodeMirror's own state
// and view modules, which the editor only has once it loaded them at run time.
export function lspView(state, view, hooks) {
  const { StateEffect, StateField } = state;
  const { EditorView, Decoration, showTooltip } = view;

  function positionAt(st, pos) {
    const line = st.doc.lineAt(pos);
    const word = st.wordAt(pos);
    return {
      line: line.number - 1,
      character: pos - line.from,
      word: word ? st.doc.sliceString(word.from, word.to) : "",
    };
  }

  // The word under the mouse while Ctrl or Cmd is held.
  const setLSPHint = StateEffect.define();
  const lspHintField = StateField.define({
    create: () => null,
    update(value, tr) {
      for (const e of tr.effects) {
        if (e.is(setLSPHint)) value = e.value;
      }
      if (tr.docChanged) value = null;
      return value;
    },
    provide: (f) => EditorView.decorations.from(f, (r) => (
      r ? Decoration.set([Decoration.mark({ class: "cm-dc-lsp-target" }).range(r.from, r.to)]) : Decoration.none
    )),
  });
  const lspTheme = EditorView.theme({
    ".cm-dc-lsp-target": { textDecoration: "underline", cursor: "pointer" },
    ".cm-tooltip.dc-lsp-pill": { padding: "2px", borderRadius: "8px", display: "flex", gap: "2px" },
  });

  function lspHintRange(v, r) {
    const cur = v.state.field(lspHintField, false);
    if (cur === undefined) return;
    if (!r && !cur) return;
    if (r && cur && cur.from === r.from && cur.to === r.to) return;
    v.dispatch({ effects: setLSPHint.of(r ? { from: r.from, to: r.to } : null) });
  }

  // Off a word the modifier click stays CodeMirror's own add-cursor gesture.
  function lspWordAt(v, e) {
    const pos = v.posAtCoords({ x: e.clientX, y: e.clientY });
    if (pos == null) return null;
    const word = v.state.wordAt(pos);
    return word ? { from: word.from, to: word.to, pos } : null;
  }

  // Whether a right click is claimed is decided inside the event, so the
  // class names are asked before it, where the pointer rests and where a tap
  // puts the cursor, and kept by the ranges the server answered with. Spans
  // already asked are not asked again; an edit drops it all.
  const setNames = StateEffect.define();
  const noNames = { asked: [], types: [] };
  const namesField = StateField.define({
    create: () => noNames,
    update(value, tr) {
      if (tr.docChanged) value = noNames;
      for (const e of tr.effects) {
        if (e.is(setNames)) value = e.value;
      }
      return value;
    },
  });
  const within = (spans, pos) => spans.find((s) => pos >= s.from && pos <= s.to);
  const typeAt = (st, pos) => within(st.field(namesField, false)?.types || [], pos)?.name || "";
  const offsetOf = (doc, p) => {
    const line = doc.line(Math.min(Math.max(p.line + 1, 1), doc.lines));
    return line.from + Math.min(Math.max(p.character, 0), line.length);
  };

  // One lookup at a time and the newest position waiting behind it, so a
  // pointer crossing the text never stacks up requests. A position waiting
  // for a document the view no longer holds, after a tab switch or an edit,
  // is dropped.
  let asking = false;
  let queued = null;
  function lookUp(v, pos) {
    const st = v.state;
    const word = st.wordAt(pos);
    const names = st.field(namesField, false);
    if (!word || !names || within(names.asked, pos) || !hooks.lspNamesUsable?.()) return;
    if (asking) {
      queued = { v, pos, doc: st.doc };
      return;
    }
    asking = true;
    const doc = st.doc;
    Promise.resolve(hooks.lookupClassName?.(positionAt(st, pos))).then((res) => {
      const cur = v.state.field(namesField, false);
      if (!res || !cur || v.state.doc !== doc) return;
      const span = res.range ? { from: offsetOf(doc, res.range.start), to: offsetOf(doc, res.range.end) } : word;
      v.dispatch({
        effects: setNames.of({
          asked: [...cur.asked, { from: word.from, to: word.to }, span],
          types: res.name ? [...cur.types, { from: span.from, to: span.to, name: res.name }] : cur.types,
        }),
      });
      namePill(v);
    }).catch(() => {}).finally(() => {
      asking = false;
      const next = queued;
      queued = null;
      if (next && next.v.state.doc === next.doc) lookUp(next.v, next.pos);
    });
  }

  // A long press raises contextmenu on some phones, and there it stays the
  // browser's text selection; the pill is the touch way to the copy.
  let lastPointer = "";
  const lspMouse = EditorView.domEventHandlers({
    pointerdown(e) {
      lastPointer = e.pointerType;
      return false;
    },
    mousedown(e, v) {
      if (e.button !== 0 || !(e.ctrlKey || e.metaKey) || e.shiftKey || e.altKey) return false;
      if (!hooks.lspUsable?.()) return false;
      const target = lspWordAt(v, e);
      if (!target) return false;
      e.preventDefault();
      lspHintRange(v, null);
      hooks.onLSPClick?.(positionAt(v.state, target.pos));
      return true;
    },
    mousemove(e, v) {
      const hint = (e.ctrlKey || e.metaKey) && hooks.lspUsable?.();
      const target = hint || hooks.lspNamesUsable?.() ? lspWordAt(v, e) : null;
      if (target) lookUp(v, target.pos);
      lspHintRange(v, hint ? target : null);
      return false;
    },
    // A right click into a selection keeps the browser's copy.
    contextmenu(e, v) {
      if ((e.pointerType || lastPointer) === "touch") return false;
      const target = lspWordAt(v, e);
      if (!target) return false;
      const sel = v.state.selection.main;
      if (!sel.empty && target.pos >= sel.from && target.pos <= sel.to) return false;
      const name = typeAt(v.state, target.pos);
      if (!name) return false;
      e.preventDefault();
      hooks.onTypeMenu?.(name, e.clientX, e.clientY);
      return true;
    },
  });
  // The cursor pill, the touch way to the lookups: static, its one request
  // is the class name of the word it stands on; empty selection only, so it
  // never sits on selection handles.
  const setLSPPill = StateEffect.define();
  const lspPillField = StateField.define({
    create: () => null,
    update(value, tr) {
      let set = false;
      for (const e of tr.effects) {
        if (e.is(setLSPPill)) {
          value = e.value;
          set = true;
        }
      }
      if (!set && (tr.docChanged || tr.selection)) value = null;
      return value;
    },
    provide: (f) => showTooltip.from(f),
  });
  // Touch only: the window opens with a touch and closes with an edit.
  let lastSurfaceTouch = 0;
  const lspPillTouch = EditorView.domEventHandlers({
    touchstart() {
      lastSurfaceTouch = Date.now();
      return false;
    },
    // A re-tap on the same spot fires no update, so the lift looks itself.
    touchend(_, v) {
      setTimeout(() => syncLSPPill(v), 250);
      return false;
    },
  });
  const pillButton = (label, act) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn btn-sm";
    btn.textContent = label;
    btn.addEventListener("click", (e) => {
      e.preventDefault();
      act();
    });
    return btn;
  };
  // The server decides definition or usages on invocation, so the label
  // must not pretend to know the case. Copy FQCN joins only once the server
  // named a class there, and copies inside the tap.
  const lspPillTooltip = (pos, name) => ({
    pos,
    name,
    above: true,
    create: (v) => {
      const dom = document.createElement("div");
      dom.className = "dc-lsp-pill";
      dom.setAttribute("data-editor-lsp-pill", "");
      const offset = { x: 0, y: 6 };
      const done = () => {
        lastSurfaceTouch = 0;
        v.dispatch({ effects: setLSPPill.of(null) });
      };
      const act = pillButton("Look up", () => {
        done();
        void hooks.onGoToDefinition?.();
      });
      act.setAttribute("data-pill-action", "");
      dom.append(act);
      if (name) {
        const copy = pillButton("Copy FQCN", () => {
          hooks.copyClassName?.(name);
          done();
        });
        copy.title = name;
        copy.setAttribute("data-pill-name", "");
        dom.append(copy);
      }
      return { dom, offset };
    },
  });
  function namePill(v) {
    const field = v.state.field(lspPillField, false);
    if (!field) return;
    const name = typeAt(v.state, field.pos);
    if (name !== field.name) v.dispatch({ effects: setLSPPill.of(lspPillTooltip(field.pos, name)) });
  }
  // A dispatch cannot run inside an update.
  let pillScheduled = false;
  function syncLSPPill(v) {
    if (pillScheduled) return;
    pillScheduled = true;
    requestAnimationFrame(() => {
      pillScheduled = false;
      const field = v.state.field(lspPillField, false);
      if (field === undefined) return;
      const sel = v.state.selection.main;
      const word = sel.empty ? v.state.wordAt(sel.head) : null;
      const want = !!word && Date.now() - lastSurfaceTouch < 800 && !!hooks.lspUsable?.();
      if (want) {
        const name = typeAt(v.state, sel.head);
        if (!field || field.pos !== sel.head || field.name !== name) v.dispatch({ effects: setLSPPill.of(lspPillTooltip(sel.head, name)) });
        lookUp(v, sel.head);
      } else if (field) {
        v.dispatch({ effects: setLSPPill.of(null) });
      }
    });
  }

  return {
    extension: [lspHintField, lspTheme, lspMouse, lspPillField, lspPillTouch, namesField],
    positionAt,
    clearHint: (v) => lspHintRange(v, null),
    update(u) {
      if (u.docChanged) lastSurfaceTouch = 0;
      if (u.docChanged || u.selectionSet) syncLSPPill(u.view);
    },
  };
}
