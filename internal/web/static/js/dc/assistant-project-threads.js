import { FLASH_MS, el, flash, isModEnter, keepGrown } from "@dc/dom";
import { getJSON } from "@dc/http";
import { confirm } from "@dc/dialog";
import { THREAD_FIELDS, answerLocal, closeNoteEdit, closeReply, deleteCommentQuestion, deleteLineComments, deleteReplyQuestion, findUniqueQuoteLine, growField, mayDelete, noteEditOf, openNoteEdit, openReply, paintReply, placeChildren, postForgeThread, publishComments, publishQuestion, refuse, replyOf, typingIn, writeLineComment } from "@dc/editor-comments";
import { inlineDiff } from "@dc/inline-diff";
import { notifyError } from "@dc/toast";

const FIELDS = `${THREAD_FIELDS}, [data-assistant-project-comment] [data-project-comment-text]`;

// projectThreads draws the inline diffs of a project opened in the assistant's
// aside and holds its line comment threads, in Reviews and under the lines of
// those diffs, with the fields that answer, edit and add them. host is what
// the list lends: its element (root), the view the project stands in, the
// project's name, and its pull of the view, which a reply or a note being
// typed holds back (hold) until the form closes (resume).
export function projectThreads(host) {
  const { root, signal } = host;
  const diffs = new Map();
  let landing = null;

  root.addEventListener("click", (event) => {
    const add = event.target.closest("[data-assistant-project-comment] [data-project-comment-add]");
    if (add) {
      event.preventDefault();
      void addProjectComment(add.closest("[data-assistant-project-comment]"));
      return;
    }
    const drop = event.target.closest("[data-line-comment-cancel]");
    if (drop) {
      event.preventDefault();
      dropField(drop.closest("[data-line-comment-form]"));
      return;
    }
    const action = event.target.closest("[data-line-thread-reply-open], [data-line-thread-reply-cancel], [data-line-thread-resolve], [data-line-thread-reopen], [data-line-thread-handoff], [data-line-thread-delete], [data-comment-note-drop], [data-comment-note-edit], [data-comment-note-cancel], [data-assistant-project-publish]");
    if (!action) return;
    event.preventDefault();
    void threadAction(action);
  }, { signal });
  root.addEventListener("submit", (event) => {
    const form = event.target instanceof HTMLFormElement && event.target.closest("[data-line-thread-reply], [data-line-comment-form], [data-comment-note-form]");
    if (!form) return;
    event.preventDefault();
    event.stopPropagation();
    if (form.matches("[data-line-comment-form]")) void addComment(form);
    else if (form.matches("[data-comment-note-form]")) void saveNote(form);
    else void sendReply(form);
  }, { signal });
  root.addEventListener("input", (event) => {
    if (!event.target.matches?.(FIELDS)) return;
    event.target.classList.remove("is-invalid");
    growField(event.target);
    if (event.target.form?.matches("[data-line-thread-reply]")) paintReply(event.target.form);
  }, { signal });
  root.addEventListener("change", (event) => {
    if (event.target.matches?.("[data-line-thread-reply-resolve]")) paintReply(event.target.form);
  }, { signal });
  keepGrown(host.view(), () => host.view()?.querySelectorAll(FIELDS).forEach(growField), signal);
  root.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && event.target.matches?.("[data-comment-note-field]")) {
      event.preventDefault();
      event.stopPropagation();
      endNote(event.target.form);
      return;
    }
    if (!isModEnter(event)) return;
    if (event.target.matches?.("[data-assistant-project-comment] [data-project-comment-text]")) {
      event.preventDefault();
      void addProjectComment(event.target.closest("[data-assistant-project-comment]"));
      return;
    }
    if (!event.target.matches?.(THREAD_FIELDS)) return;
    event.preventDefault();
    event.target.form?.querySelector("[data-line-thread-reply-send], [data-line-comment-add], [data-comment-note-save]")?.click();
  }, { signal });
  root.addEventListener("show.bs.collapse", (event) => {
    if (event.target.dataset?.assistantRest) return;
    const box = event.target.querySelector?.("[data-assistant-diff]");
    if (box) void loadDiff(event.target.id, box);
  }, { signal });
  root.addEventListener("hidden.bs.collapse", (event) => {
    if (!event.target.dataset?.assistantRest) dropDiff(event.target.id, true);
  }, { signal });
  signal.addEventListener("abort", () => dropDiffs());

  // draw answers once every unfolded diff is drawn or says why not, so a
  // place put back in the view lands on what stands.
  function draw() {
    const loads = [];
    for (const box of root.querySelectorAll("[data-assistant-diff]")) {
      const fold = box.closest("[data-assistant-fold]");
      if (!fold.classList.contains("show") || fold.parentElement.closest("[data-assistant-fold]:not(.show)")) continue;
      if (diffs.get(fold.id)?.box !== box) loads.push(loadDiff(fold.id, box));
    }
    return Promise.all(loads);
  }

  function carryDiffs(fresh) {
    for (const [id, record] of [...diffs]) {
      const next = fresh.querySelector(`#${CSS.escape(id)}.show [data-assistant-diff]`);
      if (!next) {
        dropDiff(id, false);
        continue;
      }
      next.replaceWith(record.box);
      void pullDiff(id, record);
    }
  }

  function loadDiff(id, box) {
    dropDiff(id, false);
    const record = { box, handle: null, text: null, loading: false, again: false, open: new Set(), waiting: new Set(), fields: new Map(), wraps: new Map() };
    diffs.set(id, record);
    showDiff(box, "wait");
    return pullDiff(id, record);
  }

  function dropDiff(id, reset) {
    const record = diffs.get(id);
    if (!record) return;
    diffs.delete(id);
    record.handle?.destroy();
    if (reset) showDiff(record.box, "wait");
  }

  function dropDiffs() {
    for (const id of [...diffs.keys()]) dropDiff(id, false);
  }

  async function pullDiff(id, record) {
    if (record.loading) {
      record.again = true;
      return;
    }
    record.loading = true;
    record.again = false;
    const { box } = record;
    const live = () => diffs.get(id) === record;
    try {
      const url = new URL(root.getAttribute("diff-url"), window.location.href);
      url.searchParams.set("project", host.project());
      url.searchParams.set("list", box.dataset.list);
      url.searchParams.set("path", box.dataset.path);
      const answer = await getJSON(url.pathname + url.search);
      if (!live()) return;
      if (answer.note) {
        record.handle?.destroy();
        record.handle = null;
        record.text = null;
        showDiff(box, "note", answer.note);
        return;
      }
      const text = [answer.original || "", answer.content || ""];
      if (record.handle && record.text[0] === text[0] && record.text[1] === text[1]) return;
      const handle = await inlineDiff(text[0], text[1], { onLine: (line) => pressLine(record, line) });
      if (!live()) {
        handle.destroy();
        return;
      }
      showDiff(box, "view");
      box.querySelector("[data-assistant-diff-view]").replaceChildren(handle.dom);
      record.handle?.destroy();
      record.handle = handle;
      record.text = text;
      anchorFields(record);
      syncThreads(record);
    } catch (err) {
      if (!live()) return;
      record.handle?.destroy();
      record.handle = null;
      record.text = null;
      showDiff(box, "note", err?.message || "The diff could not be read.");
    } finally {
      record.loading = false;
      if (record.again && live()) void pullDiff(id, record);
    }
  }

  async function threadAction(button) {
    const thread = button.closest("[data-line-thread]");
    const form = thread?.querySelector("[data-line-thread-reply]");
    if (button.matches("[data-line-thread-reply-open]")) {
      openReply(thread);
      return;
    }
    if (button.matches("[data-line-thread-reply-cancel]")) {
      endReply(form);
      return;
    }
    if (button.matches("[data-line-thread-handoff]")) {
      handOff(button.dataset.lineThreadHandoff);
      return;
    }
    if (button.matches("[data-comment-note-edit]")) {
      openNoteEdit(button.closest("[data-comment-note]"));
      return;
    }
    if (button.matches("[data-comment-note-cancel]")) {
      endNote(button.closest("form"));
      return;
    }
    const reviews = reviewsOf(button);
    if (button.matches("[data-line-thread-delete], [data-comment-note-drop]")) {
      button.disabled = true;
      await deleteNote(reviews, thread, button.matches("[data-comment-note-drop]") ? button.closest("[data-comment-note]")?.dataset.commentNote : "");
      button.disabled = false;
      return;
    }
    if (button.matches("[data-line-thread-resolve], [data-line-thread-reopen]")) {
      button.disabled = true;
      const resolve = button.matches("[data-line-thread-resolve]");
      if (thread.hasAttribute("data-line-thread-local")) await resolveLocal(reviews, thread, resolve);
      else await postForgeThread(reviews.dataset.threadUrl, { thread: thread.dataset.thread, branch: reviews.dataset.branch, resolve: true });
      button.disabled = false;
      void host.pull();
      return;
    }
    if (button.matches("[data-assistant-project-publish]")) {
      const ids = (button.dataset.ids || "").split(" ").filter(Boolean);
      const offer = { forge: button.dataset.forgePublishForge, noun: button.dataset.forgePublishNoun, ref: button.dataset.forgePublishRef };
      if (!ids.length || !(await confirm(publishQuestion(ids.length, Number(button.dataset.general) || 0, offer)))) return;
      button.disabled = true;
      await publishComments(button.dataset.assistantProjectPublish, ids);
      button.disabled = false;
      void host.pull();
    }
  }

  function endReply(form) {
    if (!form) return;
    closeReply(form);
    const record = recordOf(form);
    if (record) syncThreads(record);
    host.resume();
  }

  async function sendReply(form) {
    const answer = replyOf(form);
    if (!answer) return;
    const reviews = reviewsOf(form);
    const thread = form.closest("[data-line-thread]");
    const buttons = Array.from(form.querySelectorAll("button"));
    buttons.forEach((button) => { button.disabled = true; });
    const done = thread.hasAttribute("data-line-thread-local")
      ? await answerLocal(answer, {
        reply: (body) => writeLineComment(`${reviews.dataset.commentsUrl}/reply`, { thread: thread.dataset.lineThread, text: body }, "The reply could not be saved."),
        toggle: (resolve) => resolveLocal(reviews, thread, resolve),
      })
      : await postForgeThread(reviews.dataset.threadUrl, { thread: form.dataset.thread, branch: reviews.dataset.branch, body: answer.body, resolve: answer.toggles });
    buttons.forEach((button) => { button.disabled = false; });
    if (!done) return;
    host.hold();
    endReply(form);
  }

  // A note edits in place, its pencil putting its field where its text
  // stood. Save writes it the way the editor does, a reply by its id and the
  // first note at the place the comments hold for it, so the edit moves
  // nothing, and its new text shows at once in Reviews and in every copy
  // under a diff's line. A thread gone meanwhile takes the edit along.
  async function saveNote(form) {
    const edit = noteEditOf(form);
    const url = reviewsOf(form)?.dataset.commentsUrl;
    const id = form.closest("[data-line-thread]")?.dataset.lineThread;
    if (!edit || !url || !id) return;
    if (!edit.same) {
      const field = form.querySelector("textarea");
      const refused = (sentence) => refuse(field, sentence);
      const buttons = Array.from(form.querySelectorAll("button"));
      buttons.forEach((button) => { button.disabled = true; });
      const saved = edit.id
        ? await writeLineComment(`${url}/reply`, { thread: id, id: edit.id, text: edit.body }, "The reply could not be saved.", refused)
        : await saveFirstNote(url, id, edit.body, refused);
      buttons.forEach((button) => { button.disabled = false; });
      if (!saved) return;
      host.hold();
    }
    const texts = `[data-line-thread="${CSS.escape(id)}"] [data-comment-note="${CSS.escape(edit.id)}"] > [data-comment-note-text]`;
    for (const text of host.view()?.querySelectorAll(texts) || []) text.textContent = edit.body;
    endNote(form, edit.body);
  }

  async function saveFirstNote(url, id, text, refused) {
    let comment;
    try {
      comment = ((await getJSON(url)).comments || []).find((c) => c.id === id);
    } catch (err) {
      refused(err?.message || "The comment could not be read.");
      return null;
    }
    if (!comment) return { gone: true };
    const place = comment.project ? { project: true } : { path: comment.path, line: comment.line, lineText: comment.lineText || "" };
    return writeLineComment(url, { id, ...place, text }, "The comment could not be saved.", refused);
  }

  function endNote(form, saved) {
    if (!form) return;
    closeNoteEdit(form, saved);
    const record = recordOf(form);
    if (record) syncThreads(record);
    host.resume();
  }

  function resolveLocal(reviews, thread, resolve) {
    return writeLineComment(`${reviews.dataset.commentsUrl}/resolve`, { id: thread.dataset.lineThread, resolved: resolve }, resolve ? "The thread could not be resolved." : "The thread could not be reopened.");
  }

  // A thread or one of its replies goes the way the editor deletes it: the
  // editor's confirm over the thread as the editor reads it, then its route.
  // What went leaves Reviews and the diffs at once, a reply being typed
  // elsewhere holds the rest of the view until it closes.
  async function deleteNote(reviews, thread, reply) {
    const url = reviews?.dataset.commentsUrl;
    const id = thread?.dataset.lineThread;
    if (!url || !id || reply === undefined) return;
    let comment;
    try {
      comment = ((await getJSON(url)).comments || []).find((c) => c.id === id);
    } catch (err) {
      notifyError(err?.message || "The comment could not be read.");
      return;
    }
    const note = reply ? (comment?.replies || []).find((r) => r.id === reply) : comment;
    if (!note || (!reply && !mayDelete(comment))) {
      void host.pull();
      return;
    }
    if (reply) {
      if (!(await confirm(deleteReplyQuestion(comment, note)))) return;
      if (!(await writeLineComment(`${url}/reply/delete`, { thread: id, id: reply }, "The reply could not be deleted."))) return;
    } else {
      if (!(await confirm(deleteCommentQuestion(comment)))) return;
      if (!(await deleteLineComments(`${url}/delete`, { ids: [id] }))) return;
    }
    const card = `[data-line-thread="${CSS.escape(id)}"]`;
    for (const node of host.view()?.querySelectorAll(reply ? `${card} [data-comment-note="${CSS.escape(reply)}"]` : card) || []) node.remove();
    sync();
    void host.pull();
  }

  // A thread in a diff is a copy of its card in Reviews, which carries where
  // its writes go.
  function reviewsOf(node) {
    return node.closest("[data-assistant-project-reviews]") || host.view()?.querySelector("[data-assistant-project-reviews]");
  }

  function recordOf(node) {
    for (const record of diffs.values()) if (record.box.contains(node)) return record;
    return null;
  }

  function threadCards(record) {
    const path = record.box.dataset.path;
    return Array.from(host.view()?.querySelectorAll("[data-assistant-project-reviews] [data-line-thread-path]") || []).filter((card) => card.dataset.lineThreadPath === path);
  }

  function sync() {
    for (const record of diffs.values()) syncThreads(record);
  }

  // A diff marks the lines of its file's threads in Reviews and holds under a
  // line the threads unfolded there and the field of a new comment. A copy
  // whose reply or note is being typed stays as it is until the form closes,
  // the others follow their cards. A line unfolded for a comment just added waits
  // for its card.
  function syncThreads(record) {
    const { handle } = record;
    if (!handle) return;
    const cards = threadCards(record);
    handle.setComments(cards.map((card) => ({
      line: Number(card.dataset.lineThreadLine),
      outdated: card.dataset.lineThreadMark === "outdated",
      resolved: card.dataset.lineThreadMark === "resolved",
    })));
    const byLine = new Map();
    for (const card of cards) {
      const line = Number(card.dataset.lineThreadLine);
      byLine.set(line, [...(byLine.get(line) || []), card]);
    }
    for (const line of [...record.open]) {
      if (byLine.has(line)) record.waiting.delete(line);
      else if (!record.waiting.has(line)) record.open.delete(line);
    }
    const blocks = new Map();
    for (const line of new Set([...record.open, ...record.fields.keys(), ...record.wraps.keys()])) {
      const wrap = record.wraps.get(line) || el("div", { class: "dc-line-block d-flex flex-column gap-2 p-2 text-wrap", "data-line-block": line });
      const held = new Map(Array.from(wrap.querySelectorAll(":scope > [data-line-thread]"), (copy) => [copy.dataset.lineThread, copy]));
      const want = [];
      for (const card of record.open.has(line) ? byLine.get(line) || [] : []) {
        const copy = held.get(card.dataset.lineThread);
        held.delete(card.dataset.lineThread);
        const source = threadSource(card);
        if (copy && (typingIn(copy) || copy.dcSource === source)) {
          want.push(copy);
          continue;
        }
        const fresh = copyOf(card);
        fresh.dcSource = source;
        want.push(fresh);
      }
      want.push(...[...held.values()].filter(typingIn));
      const field = record.fields.get(line);
      if (field) want.push(field.form);
      if (!want.length) {
        record.wraps.delete(line);
        continue;
      }
      placeChildren(wrap, want);
      record.wraps.set(line, wrap);
      blocks.set(line, wrap);
    }
    handle.setBlocks(blocks);
  }

  // A press on a marked line folds its threads in or out under it. On another
  // line of the working copy it opens the owner's field of a new comment
  // there, where a press again closes it while it holds no words.
  function pressLine(record, line) {
    if (!record.handle || ![...diffs.values()].includes(record)) return;
    if (threadCards(record).some((card) => Number(card.dataset.lineThreadLine) === line)) {
      if (!record.open.delete(line)) record.open.add(line);
      record.waiting.delete(line);
      syncThreads(record);
      return;
    }
    const field = record.fields.get(line);
    if (field) {
      const text = field.form.querySelector("textarea");
      if (text.value.trim()) {
        text.focus();
        return;
      }
      record.fields.delete(line);
      syncThreads(record);
      return;
    }
    const template = host.view()?.querySelector("template[data-assistant-diff-comment]");
    const form = template?.content.firstElementChild?.cloneNode(true);
    if (!form) return;
    record.fields.set(line, { form, quote: record.handle.lineText(line) });
    syncThreads(record);
    growField(form.querySelector("textarea"));
    if (matchMedia("(pointer: fine)").matches) form.querySelector("textarea")?.focus();
  }

  // A file that changed under an open field keeps the field under its line,
  // found again by the line's text the way the editor finds a comment's line,
  // else under the same number.
  function anchorFields(record) {
    const { handle } = record;
    const moved = new Map();
    for (const [line, field] of record.fields) {
      const at = handle.lineText(line) === field.quote ? line : findUniqueQuoteLine(handle.doc, field.quote) || Math.min(line, handle.doc.lines);
      field.quote = handle.lineText(at);
      if (!moved.has(at)) moved.set(at, field);
    }
    record.fields = moved;
  }

  function dropField(form) {
    const record = form && recordOf(form);
    if (!record) return;
    for (const [line, field] of record.fields) if (field.form === form) record.fields.delete(line);
    syncThreads(record);
  }

  // A new comment goes through the editor's line comments as the editor
  // sends it, anchored on the line's text, and unfolds under its line once
  // its card arrives. A refusal stands at the field, which keeps the words.
  async function addComment(form) {
    const record = recordOf(form);
    const at = record && [...record.fields].find(([, field]) => field.form === form);
    const url = host.view()?.querySelector("template[data-assistant-diff-comment]")?.dataset.assistantDiffComment;
    if (!at || !url || !record.handle) return;
    const text = form.querySelector("textarea");
    const body = text.value.trim();
    if (!body) {
      refuse(text);
      return;
    }
    const [line] = at;
    const buttons = Array.from(form.querySelectorAll("button"));
    buttons.forEach((button) => { button.disabled = true; });
    const saved = await writeLineComment(url, { id: "", path: record.box.dataset.path, line, lineText: record.handle.lineText(line), text: body }, "The comment could not be saved.", (sentence) => refuse(text, sentence));
    buttons.forEach((button) => { button.disabled = false; });
    if (!saved) return;
    landing = { id: saved.comment?.id, record, at: 0 };
    for (const [held, field] of record.fields) {
      if (field.form !== form) continue;
      record.fields.delete(held);
      record.open.add(held);
      record.waiting.add(held);
    }
    syncThreads(record);
    host.hold();
    void host.pull();
  }

  // A project comment goes through the editor's line comments as its
  // comments sheet sends it. The field empties once it went, unless it was
  // typed on meanwhile, and the comment shows with the pull. A refusal
  // stands at the field, which keeps the words.
  async function addProjectComment(box) {
    const url = box && reviewsOf(box)?.dataset.commentsUrl;
    const text = box?.querySelector("[data-project-comment-text]");
    const add = box?.querySelector("[data-project-comment-add]");
    if (!url || !text || !add || add.disabled) return;
    const body = text.value.trim();
    if (!body) {
      refuse(text);
      return;
    }
    add.disabled = true;
    const saved = await writeLineComment(url, { id: "", project: true, text: body }, "The comment could not be saved.", (sentence) => refuse(text, sentence));
    add.disabled = false;
    if (!saved) return;
    if (text.value.trim() === body) {
      text.value = "";
      growField(text);
    }
    landing = { id: saved.comment?.id, record: null, at: 0 };
    void host.pull();
  }

  // A comment just made shows where it landed once a pull brings it: its copy
  // under its line in the diff it was made in, else its card in Reviews,
  // scrolled into view and marked briefly. Its own signal pulls again right
  // after, so the mark goes over into the card of every swap while it lasts.
  // A diff folded meanwhile drops it.
  function showLanding() {
    if (!landing) return;
    const live = !landing.record || [...diffs.values()].includes(landing.record);
    const at = landing.record ? landing.record.box : host.view()?.querySelector("[data-assistant-project-reviews]");
    if (!live || !at || !landing.id || (landing.at && performance.now() - landing.at >= FLASH_MS)) {
      landing = null;
      return;
    }
    const node = at.querySelector(`[data-line-thread="${CSS.escape(landing.id)}"]`);
    if (!node) return;
    if (!landing.at) {
      landing.at = performance.now();
      node.scrollIntoView({ block: "nearest" });
    }
    flash(node, signal, landing.at);
  }

  // The composer of the page takes the draft, saves it the way a typed one is
  // saved and sends nothing. On a phone the sheet steps aside, so the box the
  // draft went into is what shows.
  function handOff(text) {
    const taken = !root.dispatchEvent(new CustomEvent("dc-assistant-handoff", { bubbles: true, cancelable: true, detail: { text } }));
    if (!taken) {
      notifyError("This assistant is read only.");
      return;
    }
    const aside = root.closest("[data-assistant-aside]");
    if (aside?.classList.contains("show")) window.bootstrap?.Offcanvas.getOrCreateInstance(aside).hide();
  }

  // reset drops what the open view held, when a project opens or closes.
  function reset() {
    landing = null;
    dropDiffs();
  }

  return { draw, carry: carryDiffs, drop: dropDiff, reset, sync, recordOf, land: showLanding };
}

function showDiff(box, part, note = "") {
  box.querySelector("[data-assistant-diff-wait]").hidden = part !== "wait";
  const line = box.querySelector("[data-assistant-diff-note]");
  line.hidden = part !== "note";
  line.textContent = note;
  const view = box.querySelector("[data-assistant-diff-view]");
  view.hidden = part !== "view";
  if (part !== "view") view.replaceChildren();
}

// copyOf is a thread's card as its copy in a diff starts, with its reply and
// its notes' edits closed whatever the card in Reviews holds open.
function copyOf(card) {
  const copy = card.cloneNode(true);
  copy.querySelector("[data-line-thread-reply]")?.setAttribute("hidden", "");
  for (const form of copy.querySelectorAll("[data-comment-note-form]")) closeNoteEdit(form);
  for (const field of copy.querySelectorAll(THREAD_FIELDS)) field.removeAttribute("style");
  copy.querySelector("[data-line-thread-reply-open]")?.setAttribute("aria-expanded", "false");
  const form = copy.querySelector("[data-line-thread-reply]");
  if (form) paintReply(form);
  return copy;
}

// threadSource is what a thread's card shows, its ages aside, which tick on
// their own.
function threadSource(card) {
  const copy = copyOf(card);
  for (const time of copy.querySelectorAll("dc-time")) time.replaceChildren();
  return copy.outerHTML;
}
