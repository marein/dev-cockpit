// Links in terminal text: web addresses, and files of the terminal's project,
// which only the server can tell from other words.

const WEB_ADDRESS = /https?:\/\/[^\s"'<>`]+/g;

// The web addresses in a text. Trailing punctuation stays outside the link and
// inside the text, so a copy yields the same text either way.
export function webLinks(text) {
  const links = [];
  for (const match of text.matchAll(WEB_ADDRESS)) {
    const href = match[0].replace(/[).,;:!?\]]+$/, "");
    if (href) links.push({ start: match.index, end: match.index + href.length, href });
  }
  return links;
}

// A file opens in its project's editor, in place when this page is that
// editor (dc:open-file), else through a navigation. Ctrl, Cmd or Shift asks
// for a tab of its own, as on any link.
export function openFileLink(href, event) {
  if (event?.ctrlKey || event?.metaKey || event?.shiftKey) {
    window.open(href, "_blank", "noopener");
  } else if (document.dispatchEvent(new CustomEvent("dc:open-file", { cancelable: true, detail: { href } }))) {
    window.app.navigate(href);
  }
}

// The words of the terminal project's files carry the link xterm gives a
// program's OSC 8 link, so they show and open like one. The mark starts the
// id of each, new per page so no program's own id reads as one, and the text
// it was put on ends it.
const MARK = `dc${Math.random().toString(36).slice(2, 10)}.`;
const HAS_EXTENDED = 0x10000000;
const SEPARATORS = "\"'`<>()[]{},;|\u00a0";
const JOIN_ROWS = 4;
const JOINED_FULL = 1;
const JOINED_INDENT = 2;
// A row whose text replaced twice in a row one that stood shorter than this
// keeps changing, a clock redrawn every second does, and is asked once it
// stood this long. Once is a command's echo giving way to its output.
const STILL = 1500;
const MAX_WORD = 4096;
const BATCH = 500;
const CACHE_MAX = 4096;
// A file a program writes after its name was shown links once its name is
// asked again, and a screen of words that name nothing costs one request in
// this time at most.
const NEGATIVE_TTL = 15000;

const separates = (ch) => (ch.length === 1 ? ch <= " " || ch === "\x7f" || SEPARATORS.includes(ch) : ch === "");
const isFrame = (text) => [...text].every((ch) => ch >= "\u2500" && ch <= "\u259f");
const blank = (ch) => !ch.trim() || isFrame(ch);
const askable = (text) => {
  const name = text.replace(/[.,:;!?]+$/, "");
  return name.length <= MAX_WORD && /[./:]/.test(name) && /\p{L}/u.test(name) && !name.includes("://") && !name.includes("\\") && !name.startsWith("~");
};

// markFileLinks marks the files of the terminal's project in its own buffer,
// which always holds the finished screen however a program drew it. After
// every write the screen is read word by word as the server reads text
// (filelinks.go), the words it was never asked about go out in one request,
// and the cells of every word it named get their link. A mark whose cells no
// longer read its word goes in the same pass, a program's own link stays. The
// row the cursor stands on may be half written, a word typed, a count going
// up or a frame half drawn, so its new words are asked only once the cursor
// left it. A row whose text a program keeps replacing, a clock, a counter or
// a progress, is asked only once it stood still, while a row whose old text
// moved on, as on a screen that scrolls, is asked at once, and so is a row
// first seen, whose age is unknown. ask(words) answers the server's links for
// the words.
export function markFileLinks(term, { ask, signal }) {
  const core = term._core;
  const links = core._oscLinkService;
  const cache = new Map();
  const queue = new Set();
  const sent = new Set();
  const ids = new Map();
  const cell = term.buffer.active.getNullCell();
  const stands = new WeakMap();
  let asking = false;
  let frame = 0;
  let wake = 0;

  const known = (key, text, may) => {
    if (!askable(text)) return null;
    const entry = cache.get(key);
    if (entry && (entry.link || Date.now() - entry.at < NEGATIVE_TTL)) return entry.link;
    if (may && !sent.has(key)) queue.add(key);
    return entry ? null : undefined;
  };
  const remember = (key, link) => {
    cache.delete(key);
    cache.set(key, { link, at: Date.now() });
    if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value);
  };
  const ours = (id) => links.getLinkData(id)?.id?.startsWith(MARK);
  const linkId = (uri, text) => {
    const key = `${uri}\n${text}`;
    if (!links.getLinkData(ids.get(key))) ids.set(key, links.registerLink({ id: MARK + text, uri }));
    return ids.get(key);
  };

  // The logical lines of the screen, rows the terminal wrapped as one, with
  // their first and last non blank cell. A row's text is followed by xterm's
  // own line, which moves with it when the screen scrolls.
  const read = () => {
    const buffer = term.buffer.active;
    const cursor = buffer.baseY + buffer.cursorY;
    const now = performance.now();
    const lines = [];
    const rows = [];
    for (let y = buffer.viewportY; y < buffer.viewportY + term.rows; y++) {
      const row = buffer.getLine(y);
      if (!row) break;
      if (!lines.length || !row.isWrapped) lines.push({ cells: [], cursor: false, rows: 0 });
      const line = lines.at(-1);
      line.cursor ||= y === cursor;
      line.rows += 1;
      let text = "";
      for (let x = 0; x < row.length; x++) {
        row.getCell(x, cell);
        const c = { y, x, ch: cell.getChars(), w: cell.getWidth(), id: cell.bg & HAS_EXTENDED ? cell.extended.urlId : 0 };
        line.cells.push(c);
        text += c.ch || " ";
        if (c.w && !blank(c.ch)) {
          line.head ||= c;
          line.tail = c;
        }
      }
      rows.push({ key: core._bufferService.buffer.lines.get(y), text: text.trimEnd(), line });
    }
    const shown = new Set(rows.map((r) => r.text));
    let soonest = Infinity;
    for (const { key, text, line } of rows) {
      const was = stands.get(key);
      if (was?.text !== text) {
        const quick = Boolean(was?.text) && now - was.since < STILL && !shown.has(was.text);
        stands.set(key, { text, since: was ? now : -Infinity, quick, changing: quick && was.quick });
      }
      const { since, changing } = stands.get(key);
      if (changing && now - since < STILL) {
        line.restless = true;
        soonest = Math.min(soonest, since + STILL);
      }
    }
    clearTimeout(wake);
    if (soonest < Infinity) wake = setTimeout(() => { if (!signal.aborted) guarded(); }, soonest - now);
    return lines;
  };
  const wordsOf = (line, index) => {
    const words = [];
    let word = null;
    for (const c of line.cells) {
      if (!c.w) {
        word?.cells.push(c);
        continue;
      }
      if (separates(c.ch)) {
        word = null;
        continue;
      }
      if (!word) words.push((word = { text: "", cells: [], line: index, owned: false, may: !line.cursor && !line.restless }));
      word.text += c.ch;
      word.cells.push(c);
      word.tail = c;
      word.owned ||= Boolean(c.id && !ours(c.id) && links.getLinkData(c.id));
    }
    return words.filter((w) => !isFrame(w.text));
  };

  // How each line joins the next, the rule rowJoins answers for the copy
  // view: a line that fills the last column goes on in a next one starting in
  // the first, as the terminal wraps, else a lone row filled up to the margin
  // of its block goes on at an indent where a word of it starts, as a program
  // wraps at its margin.
  const joinsOf = (lines) => {
    const reach = (line) => line.tail.x + line.tail.w;
    const full = (i) => i >= 0 && i + 1 < lines.length && lines[i].tail?.y === lines[i].cells.at(-1).y && reach(lines[i]) === term.cols
      && lines[i + 1].head?.y === lines[i + 1].cells[0].y && lines[i + 1].head.x === 0;
    const startsWordAt = (line, x) => {
      const i = line.cells.findIndex((c) => c.x === x && c.w);
      const before = line.cells.slice(0, Math.max(i, 0)).findLast((c) => c.w);
      return i > 0 && !blank(line.cells[i].ch) && Boolean(before) && blank(before.ch);
    };
    const lone = lines.map((line, i) => line.rows === 1 && Boolean(line.head) && !full(i - 1) && !full(i));
    const joins = lines.map((_, i) => (full(i) ? JOINED_FULL : 0));
    for (let a = 0; a < lines.length; a++) {
      const indent = lines[a].head?.x;
      if (!lone[a] || !indent) continue;
      let b = a;
      while (b + 1 < lines.length && lone[b + 1] && lines[b + 1].head.x === indent) b++;
      const head = a > 0 && lone[a - 1] && startsWordAt(lines[a - 1], indent) ? a - 1 : a;
      const margin = Math.max(...lines.slice(head, b + 1).map(reach));
      for (let i = head; i < b; i++) if (reach(lines[i]) === margin) joins[i] = JOINED_INDENT;
      a = b;
    }
    return joins;
  };

  // A word that ends its line and those that start the lines right below it
  // may be one the terminal or a program wrapped. Over rows the terminal
  // wrapped the joined word wins, asked as one word, and else the first
  // stands alone. Over an indent a word that names a file alone keeps its
  // own link, the others are asked joined, apart by line breaks, and the
  // server answers how far the mention reaches (wrappedMention). A word that
  // waits to be asked ends the join for now.
  const resolve = (run, i, place) => {
    const first = run[i];
    if (run.wrap === JOINED_FULL) {
      const parts = i === 0 ? run.slice(0, JOIN_ROWS) : [];
      const text = parts.map((w) => w.text).join("");
      const joined = parts.length > 1 ? known(text, text, parts.every((w) => w.may)) : null;
      if (joined) {
        place([{ cells: parts.flatMap((w) => w.cells) }], joined);
        return parts.length;
      }
      const alone = known(first.text, first.text, first.may);
      return alone ? place([first], alone) : 1;
    }
    const alone = known(first.text, first.text, first.may);
    if (alone) return place([first], alone);
    if (alone === undefined) return 1;
    const parts = [first];
    for (let k = i + 1; k < run.length && parts.length < JOIN_ROWS; k++) {
      const own = known(run[k].text, run[k].text, run[k].may);
      if (own === undefined && run[k].may) return 1;
      if (own !== null) break;
      parts.push(run[k]);
    }
    const texts = parts.map((w) => w.text);
    const joined = parts.length > 1 ? known(texts.join("\n"), texts.join(""), parts.every((w) => w.may)) : null;
    return joined ? place(parts.slice(0, joined.part + 1), joined) : 1;
  };

  const scan = () => {
    queue.clear();
    const lines = read();
    const want = new Map();
    const place = (words, link) => {
      const cells = words.slice(0, -1).flatMap((w) => w.cells);
      let units = 0;
      for (const c of words.at(-1).cells) {
        if (units >= link.end && c.w) break;
        cells.push(c);
        units += c.ch.length;
      }
      const id = linkId(link.uri, cells.map((c) => c.ch).join(""));
      for (const c of cells) want.set(c, id);
      return words.length;
    };
    const joins = joinsOf(lines);
    const runs = [];
    let prev = null;
    lines.forEach((line, index) => {
      for (const word of wordsOf(line, index)) {
        if (word.owned) {
          prev = null;
          continue;
        }
        const join = prev && word.line === prev.line + 1 && prev.tail === lines[prev.line].tail && word.cells[0] === line.head ? joins[prev.line] : 0;
        if (join) runs.at(-1).push(word);
        else runs.push([word]);
        runs.at(-1).wrap ||= join;
        prev = word;
      }
    });
    for (const run of runs) {
      for (let i = 0; i < run.length; ) i += resolve(run, i, place);
    }
    const buffer = core._bufferService.buffer;
    const added = new Set();
    let from = Infinity;
    let to = -1;
    for (const line of lines) {
      for (const c of line.cells) {
        const id = want.get(c) || 0;
        if (c.id === id || (c.id && !ours(c.id) && links.getLinkData(c.id))) continue;
        const target = term.buffer.active.getLine(c.y).getCell(c.x);
        const extended = target.extended.clone();
        extended.urlId = id;
        target.extended = extended;
        target.updateExtended();
        buffer.lines.get(c.y).setCell(c.x, target);
        if (id && !added.has(`${id} ${c.y}`)) {
          added.add(`${id} ${c.y}`);
          links.addLineToLink(id, c.y);
        }
        from = Math.min(from, c.y);
        to = Math.max(to, c.y);
      }
    }
    if (to >= 0) term.refresh(from - term.buffer.active.viewportY, to - term.buffer.active.viewportY);
    void flush();
  };

  const flush = async () => {
    if (asking || !queue.size || signal.aborted) return;
    asking = true;
    const words = [...queue].slice(0, BATCH);
    for (const word of words) {
      queue.delete(word);
      sent.add(word);
    }
    const answer = await ask(words).catch(() => ({}));
    for (const word of words) {
      sent.delete(word);
      remember(word, answer.links?.[word] || null);
    }
    asking = false;
    if (!signal.aborted) guarded();
  };

  // A bug here costs the links, never the terminal: xterm calls the scan
  // from inside its write loop.
  let broken = false;
  const guarded = () => {
    if (broken) return;
    try {
      scan();
    } catch (error) {
      broken = true;
      console.error("file links:", error);
    }
  };
  const later = () => {
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; if (!signal.aborted) guarded(); });
  };
  const subscriptions = [term.onWriteParsed(guarded), term.onScroll(later), term.onResize(later)];
  signal.addEventListener("abort", () => {
    cancelAnimationFrame(frame);
    clearTimeout(wake);
    for (const subscription of subscriptions) subscription.dispose();
  }, { once: true });
}

// The marks follow the text after every write xterm parsed, but a click may
// come between the slices of a long write, so a mark holds only while its
// cells read the text it was put on: the run under the pointer, joined with
// the last run of the rows above and the first of the rows below while the
// text goes on there, as a path a program wrapped. Mentions of one text share
// the id, so a row may carry several runs of it. A program's own link always
// holds. xterm 5.5 keeps a cell's link out of its public API: the cell carries
// an id, the core the link.
export function linkHolds(term, range) {
  const buffer = term.buffer.active;
  const y = range.start.y - 1;
  const x = range.start.x - 1;
  const id = buffer.getLine(y)?.getCell(x)?.extended.urlId;
  const data = term._core._oscLinkService.getLinkData(id)?.id;
  if (!data?.startsWith(MARK)) return true;
  const want = data.slice(MARK.length);
  const runsOn = (row) => {
    const line = buffer.getLine(row);
    const runs = [];
    for (let at = 0; line && at < line.length; at++) {
      const cell = line.getCell(at);
      if (cell.extended.urlId !== id) continue;
      if (runs.at(-1)?.end !== at - 1) runs.push({ text: "" });
      runs.at(-1).text += cell.getChars();
      runs.at(-1).end = at;
    }
    return runs;
  };
  let text = runsOn(y).find((run) => run.end >= x).text;
  for (let row = y - 1; text !== want; row--) {
    const above = runsOn(row).at(-1)?.text;
    if (!above || !want.includes(above + text)) break;
    text = above + text;
  }
  for (let row = y + 1; text !== want; row++) {
    const below = runsOn(row)[0]?.text;
    if (!below || !want.startsWith(text + below)) break;
    text += below;
  }
  return text === want;
}
