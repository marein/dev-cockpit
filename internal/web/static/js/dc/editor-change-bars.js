import { diffLines, lineTokens } from "@dc/linediff";

// The gutter bars mark what the buffer holds against HEAD: a blue bar on
// changed lines, a green one on new lines, a grey tick where lines were
// deleted. They are not a mode like the diff, but a fact about the file,
// always visible, so there is no switch anywhere. The chunks are a line
// diff against HEAD, kept in a state field. An
// edit tokenizes only the lines it touched and diffs the whole file again,
// which stays cheap: equal ends are skipped and lines only one side holds
// never reach the diff. The search is bounded the way the diff views bound
// theirs: CHANGES_SCAN_LIMIT is a depth limit of the same shape as
// @codemirror/merge's default scanLimit and has the same value, but it
// counts edited lines where merge counts characters. A region past the
// bound is marked as one block, and the same text always gives the same
// bars. Only the plain editor carries them, a comparison already shows its
// changes.
const CHANGES_SCAN_LIMIT = 500;

// changeBars answers the extension maker over CodeMirror's own state and
// view modules, which the editor only has once it loaded them at run time.
export function changeBars(state, view) {
  const CHANGE_COLORS = {
    mod: "var(--tblr-azure, #4299e1)",
    add: "var(--tblr-green, #2fb344)",
    del: "var(--tblr-secondary, #6b7280)",
  };

  class ChangeMarker extends view.GutterMarker {
    constructor(key) {
      super();
      this.key = key;
    }

    eq(other) {
      return other.key === this.key;
    }

    toDOM() {
      const [kind, first, last, cap] = this.key.split("|");
      const el = document.createElement("div");
      if (kind === "spacer") {
        el.style.cssText = "width:3px;height:100%;margin:0 2px";
        return el;
      }
      if (kind === "delTop" || kind === "delBottom" || kind === "delBoth") {
        el.style.cssText = "position:relative;width:3px;height:100%;margin:0 2px";
        if (kind !== "delBottom") el.appendChild(delTick("top:-3px"));
        if (kind !== "delTop") el.appendChild(delTick("bottom:-3px"));
        return el;
      }
      const r = (on) => (on === "1" ? "2px" : "0");
      el.style.cssText = `width:3px;height:100%;margin:0 2px;background:${CHANGE_COLORS[kind]};`
        + `border-radius:${r(first)} ${r(first)} ${r(last)} ${r(last)}`;
      if (cap === "1") {
        el.style.position = "relative";
        el.appendChild(delTick("bottom:-3px"));
      }
      return el;
    }
  }

  function delTick(edge) {
    const tick = document.createElement("div");
    tick.style.cssText = `position:absolute;left:-1px;width:6px;height:5px;border-radius:2px;background:${CHANGE_COLORS.del};${edge}`;
    return tick;
  }

  const changeMarkerCache = new Map();

  function changeMarker(kind, first = false, last = false, cap = false) {
    const key = `${kind}|${first ? "1" : "0"}|${last ? "1" : "0"}|${cap ? "1" : "0"}`;
    let marker = changeMarkerCache.get(key);
    if (!marker) {
      marker = new ChangeMarker(key);
      changeMarkerCache.set(key, marker);
    }
    return marker;
  }

  // changeMarkers turns the line chunks into gutter marks: buffer lines with
  // HEAD lines beside them are modified, buffer lines alone are new, and HEAD
  // lines alone put a tick on the boundary they vanished from. A modification
  // that swallowed more HEAD lines than it shows carries the tick under its
  // last line.
  function changeMarkers(chunks, doc) {
    const ranges = [];
    // Deletions above and below the same untouched line merge into one marker,
    // two would stack in the gutter cell and break its height.
    const ticks = new Map();
    const tick = (kind, lineNo) => ticks.set(lineNo, ticks.has(lineNo) && ticks.get(lineNo) !== kind ? "delBoth" : kind);
    for (const chunk of chunks) {
      const lostA = chunk.toLineA - chunk.fromLineA;
      const shownB = chunk.toLineB - chunk.fromLineB;
      if (shownB === 0) {
        tick(chunk.fromLineB < doc.lines ? "delTop" : "delBottom", Math.min(chunk.fromLineB + 1, doc.lines));
        continue;
      }
      for (let n = chunk.fromLineB + 1; n <= chunk.toLineB; n++) {
        const last = n === chunk.toLineB;
        ranges.push(changeMarker(lostA ? "mod" : "add", n === chunk.fromLineB + 1, last, last && lostA > shownB).range(doc.line(n).from));
      }
    }
    for (const [lineNo, kind] of ticks) {
      ranges.push(changeMarker(kind).range(doc.line(lineNo).from));
    }
    return state.RangeSet.of(ranges, true);
  }

  function changesExtension(text) {
    if (text == null) return [];
    const head = lineTokens(text.split("\n"));
    const docTokens = (doc) => {
      const tokens = new Int32Array(doc.lines);
      let i = 0;
      for (const line of doc.iterLines()) tokens[i++] = head.tokenOf(line);
      return tokens;
    };
    const diffed = (tokens) => ({ tokens, chunks: diffLines(head.tokens, tokens, CHANGES_SCAN_LIMIT) });
    const field = state.StateField.define({
      create: (st) => diffed(docTokens(st.doc)),
      update(value, tr) {
        if (!tr.docChanged) return value;
        const before = tr.startState.doc;
        const doc = tr.newDoc;
        let from = before.lines;
        let toOld = 0;
        tr.changes.iterChangedRanges((fromA, toA) => {
          from = Math.min(from, before.lineAt(fromA).number - 1);
          toOld = Math.max(toOld, before.lineAt(toA).number);
        });
        const toNew = toOld + doc.lines - before.lines;
        const tokens = new Int32Array(doc.lines);
        tokens.set(value.tokens.subarray(0, from));
        for (let j = from; j < toNew; j++) tokens[j] = head.tokenOf(doc.line(j + 1).text);
        tokens.set(value.tokens.subarray(toOld), toNew);
        return diffed(tokens);
      },
    });
    let cache = { chunks: null, doc: null, set: state.RangeSet.empty };
    return [field, view.gutter({
      class: "cm-changes",
      markers(v) {
        const { chunks } = v.state.field(field);
        if (cache.chunks !== chunks || cache.doc !== v.state.doc) {
          cache = { chunks, doc: v.state.doc, set: changeMarkers(chunks, v.state.doc) };
        }
        return cache.set;
      },
      initialSpacer: () => changeMarker("spacer"),
    })];
  }

  return changesExtension;
}
