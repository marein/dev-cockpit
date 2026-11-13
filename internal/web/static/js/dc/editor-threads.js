import { el, flash, isModEnter } from "@dc/dom";
import { THREAD_FIELDS, answerLocal, closeNoteEdit, closeReply, findUniqueQuoteLine, growField, noteEditOf, noteEdited, openEdits, openNoteEdit, openReply, paintReply, placeChildren, postForgeThread, refuse, replyOf, replyOpen } from "@dc/editor-comments";
import { getText } from "@dc/http";

const holdsWords = (form) => !!form?.querySelector("textarea")?.value.trim();
// typedField is the field of a card holding words a fold would take: a reply
// being typed or a note edited.
const typedField = (card) => {
  const reply = card?.querySelector("[data-line-thread-reply]:not([hidden])");
  if (holdsWords(reply)) return reply.querySelector("textarea");
  const edit = card ? openEdits(card).map((note) => note.querySelector("[data-comment-note-form]")).find(noteEdited) : null;
  return edit?.querySelector("textarea") || null;
};
const finePointer = () => matchMedia("(pointer: fine)").matches;

// editorThreads shows the line comment threads of the editor under their
// lines, the way the inline diffs of the assistant's project view do: a press
// on a marked line folds its threads in or out, on another line it opens the
// field of a new comment, which a press again closes while it holds no words.
// A thread is the server's card (GET .../comments/thread), the one rendering
// of a thread, pulled again whenever the thread changes; a reply being typed
// stays where it is, the rest of its card follows. A project comment opens
// the same card in the editor's dialog. host is what the editor lends: its
// comments, its writes, where a thread stands and the group showing a tab.
export function editorThreads(host) {
  const { base, signal } = host;
  const states = new WeakMap();
  const dialog = { cards: new Map(), pulls: new Map(), id: "" };
  const fieldTemplate = host.root.querySelector("template[data-editor-line-comment]");
  let offerKey = "";
  // The field typed in last. A view that swaps its document under it takes
  // it off the page for a moment, and the focus with it.
  let lastField = null;
  document.addEventListener("focusin", (event) => {
    lastField = event.target instanceof HTMLTextAreaElement && event.target.matches(THREAD_FIELDS) ? event.target : null;
  }, { signal });

  const stateOf = (tab) => {
    let st = states.get(tab);
    if (!st) {
      st = { open: new Set(), fields: [], cards: new Map(), pulls: new Map(), landing: null };
      states.set(tab, st);
    }
    return st;
  };

  const signature = (c) => JSON.stringify([c.text, c.author, c.updatedAt, c.resolved, c.deletes, c.mine, c.forge,
    (c.replies || []).map((r) => [r.id, r.text, r.author, r.updatedAt, r.mine]), c.forge ? offerKey : ""]);

  // The outdated mark follows the buffer, which reads a thread outdated where
  // the disk does not yet, and the other way round. Under its line the quote
  // shows only with the mark, in the dialog always.
  function paintMarks(card, c, inDialog) {
    const outdated = !!c.outdated;
    card.querySelector(":scope > [data-line-thread-head] > [data-line-thread-outdated]")?.toggleAttribute("hidden", !outdated);
    card.querySelector(":scope > [data-line-thread-quote]")?.toggleAttribute("hidden", !outdated && !inDialog);
  }

  // A card standing on the page takes the fresh one's parts in place, its
  // reply form while that is open and a note being edited excepted, so the
  // words typed there, the caret and the focus stay. A note the reader may
  // no longer edit takes the fresh one's place. Such a card is pulled again
  // once its forms close.
  function patch(card, fresh) {
    for (const { name } of [...card.attributes]) if (!fresh.hasAttribute(name)) card.removeAttribute(name);
    for (const { name, value } of [...fresh.attributes]) card.setAttribute(name, value);
    const form = card.querySelector(":scope > [data-line-thread-reply]:not([hidden])");
    const edits = new Map(openEdits(card).map((note) => [note.dataset.commentNote, note]));
    const notes = edits.size ? card.querySelector(":scope > [data-line-thread-notes]") : null;
    const freshNotes = fresh.querySelector(":scope > [data-line-thread-notes]");
    if (notes && freshNotes) {
      placeChildren(notes, [...freshNotes.children].map((note) => {
        const edit = edits.get(note.dataset.commentNote);
        return edit && note.querySelector("[data-comment-note-form]") ? edit : note;
      }));
    }
    const parts = [...fresh.children].map((part) => (form && part.matches("[data-line-thread-reply]") ? form : notes && part === freshNotes ? notes : part));
    if (form && !parts.includes(form)) parts.push(form);
    placeChildren(card, parts);
    if (form) card.querySelector("[data-line-thread-reply-open]")?.setAttribute("aria-expanded", "true");
    card.dcFormStale = !!form || openEdits(card).length > 0;
  }

  // settle pulls a card again that kept a form open through a change, once
  // that form closed.
  function settle(card) {
    if (card?.dcFormStale) card.dcSig = "";
    paint();
  }

  async function pull(slot, c, sig, done) {
    if (slot.pulls.get(c.id) === sig) return;
    slot.pulls.set(c.id, sig);
    let html;
    try {
      html = await getText(`${base}/comments/thread?id=${encodeURIComponent(c.id)}`, { signal });
    } catch {
      if (slot.pulls.get(c.id) === sig) slot.pulls.delete(c.id);
      return;
    }
    if (slot.pulls.get(c.id) !== sig) return;
    slot.pulls.delete(c.id);
    const holder = document.createElement("template");
    holder.innerHTML = html.trim();
    const fresh = holder.content.firstElementChild;
    if (!fresh) return;
    await window.app?.loadElements?.(holder.content);
    let card = slot.cards.get(c.id);
    if (card) patch(card, fresh);
    else card = fresh;
    card.dcSig = sig;
    slot.cards.set(c.id, card);
    done();
  }

  // cardOf answers the card a thread shows, pulling it first or again where
  // the thread changed since, null until the first one arrived.
  function cardOf(slot, c, done) {
    const card = slot.cards.get(c.id);
    const sig = signature(c);
    if (!card || card.dcSig !== sig) void pull(slot, c, sig, done);
    if (card) paintMarks(card, c, slot === dialog);
    return card || null;
  }

  // A field stands under the line the view carried it to, else, where the
  // document was replaced under it, under the line holding its quote, else
  // under the same number.
  function anchorField(g, tab, field) {
    const doc = host.docOf(tab);
    let at = g.editor.blockLine(field.form);
    if (!at) {
      const line = Math.min(Math.max(1, field.line), doc.lines);
      at = doc.line(line).text === field.quote ? line : findUniqueQuoteLine(doc, field.quote) || line;
    }
    field.line = at;
    field.quote = doc.line(at).text;
    return at;
  }

  // sync puts under their lines what a group's open file holds unfolded: the
  // cards of its unfolded threads and its fields. The field with the focus
  // keeps it, with its caret, where the view drew the blocks again or swapped
  // its document under them.
  function sync(g) {
    if (!g.editor.canBlocks) return;
    const tab = host.shownTab(g);
    if (!tab) {
      g.editor.setBlocks([]);
      return;
    }
    const st = stateOf(tab);
    const list = host.commentsFor(tab.path);
    const ids = new Set(list.map((c) => c.id));
    // A thread published on the forge comes back under a new id, naming the
    // one it had in was, and stays unfolded under that.
    for (const id of [...st.open]) {
      if (ids.has(id)) continue;
      st.open.delete(id);
      const heir = list.find((c) => c.was === id);
      const card = st.cards.get(id);
      st.cards.delete(id);
      if (!heir) continue;
      st.open.add(heir.id);
      if (card) st.cards.set(heir.id, card);
    }
    for (const id of [...st.cards.keys()]) if (!st.open.has(id)) st.cards.delete(id);
    const lines = new Map();
    const put = (line, node) => lines.set(line, [...(lines.get(line) || []), node]);
    const repaint = () => sync(tab.group);
    for (const c of list) {
      if (!st.open.has(c.id)) continue;
      const card = cardOf(st, c, repaint);
      if (card) put(c.line, card);
    }
    for (const field of st.fields) put(anchorField(g, tab, field), field.form);
    // A press in a block focuses the block itself, never the editable code
    // around it, so its text selects and a key typed there stays out of the
    // code.
    const used = new Set();
    const blocks = [...lines].sort((a, b) => a[0] - b[0]).map(([line, nodes]) => {
      const wrap = nodes.map((node) => node.parentElement).find((p) => p?.matches?.("[data-line-block]") && !used.has(p))
        || el("div", { class: "dc-line-block d-flex flex-column gap-2 p-2", tabIndex: -1, "data-line-block": "" });
      used.add(wrap);
      wrap.dataset.lineBlock = String(line);
      placeChildren(wrap, nodes);
      return { line, dom: wrap };
    });
    const active = document.activeElement;
    const lost = !active || active === document.body;
    const typing = active?.closest?.("[data-line-block]") ? active
      : lost && lastField && blocks.some((b) => b.dom.contains(lastField)) ? lastField : null;
    const caret = typing && typeof typing.setSelectionRange === "function" ? [typing.selectionStart, typing.selectionEnd, typing.selectionDirection] : null;
    g.editor.setBlocks(blocks);
    if (typing?.isConnected && document.activeElement !== typing) {
      typing.focus({ preventScroll: true });
      if (caret) typing.setSelectionRange(...caret);
    }
    land(st);
  }

  // A thread asked for from a menu, the sheet or the keyboard shows once its
  // card stands under its line: scrolled into view, marked briefly and, where
  // asked, with its reply open.
  function land(st) {
    const { landing } = st;
    if (!landing) return;
    const card = st.cards.get(landing.id);
    if (!card?.isConnected) return;
    st.landing = null;
    requestAnimationFrame(() => {
      card.scrollIntoView({ block: "nearest" });
      flash(card, signal);
      if (landing.reply && card.querySelector("[data-line-thread-reply]")) {
        if (!replyOpen(card)) openReply(card);
        card.querySelector("[data-line-thread-reply-text]")?.focus();
      }
    });
  }

  const busy = (form, on) => form.querySelectorAll("button").forEach((button) => { button.disabled = on; });

  function closeField(tab, field, focusEditor) {
    const st = stateOf(tab);
    st.fields = st.fields.filter((f) => f !== field);
    sync(tab.group);
    if (focusEditor) host.focusEditor(tab.group);
  }

  function openField(tab, line) {
    const st = stateOf(tab);
    const form = fieldTemplate?.content.firstElementChild?.cloneNode(true);
    if (!form) return;
    const field = { form, line, quote: host.docOf(tab).line(line).text };
    st.fields.push(field);
    sync(tab.group);
    growField(form.querySelector("textarea"));
    requestAnimationFrame(() => {
      form.scrollIntoView({ block: "nearest" });
      if (finePointer()) form.querySelector("textarea")?.focus();
    });
  }

  const fieldAt = (tab, line) => stateOf(tab).fields.find((f) => f.line === line) || null;

  // press is a press on a line: a marked one folds its threads in or out, a
  // reply holding words staying unfolded with the focus in it; another one
  // opens or closes the field of a new comment. It answers false where it
  // has nothing to show, a reader who writes no comments on a bare line.
  function press(tab, line) {
    const st = stateOf(tab);
    const threads = host.commentsFor(tab.path).filter((c) => c.line === line);
    if (threads.length) {
      if (threads.some((c) => st.open.has(c.id))) {
        for (const c of threads) {
          const field = typedField(st.cards.get(c.id));
          if (field) field.focus();
          else st.open.delete(c.id);
        }
      } else {
        for (const c of threads) st.open.add(c.id);
      }
      sync(tab.group);
      return true;
    }
    const field = fieldAt(tab, line);
    if (field) {
      if (holdsWords(field.form)) field.form.querySelector("textarea").focus();
      else closeField(tab, field, false);
      return true;
    }
    if (!fieldTemplate) return false;
    openField(tab, line);
    return true;
  }

  // reveal unfolds a thread under its line and shows it, its reply open
  // where asked; the editor stands on the line already.
  function reveal(tab, c, { reply = false } = {}) {
    const st = stateOf(tab);
    st.open.add(c.id);
    st.landing = { id: c.id, reply };
    sync(tab.group);
  }

  // compose opens the field of a new comment under a line, or shows the one
  // standing there, with the focus in it.
  function compose(tab, line) {
    const field = fieldAt(tab, line);
    if (!field) {
      openField(tab, line);
      return;
    }
    field.form.scrollIntoView({ block: "nearest" });
    field.form.querySelector("textarea").focus();
  }

  async function addComment(form) {
    const tab = host.tabOfNode(form);
    const field = tab && stateOf(tab).fields.find((f) => f.form === form);
    if (!field) return;
    const text = form.querySelector("textarea");
    const body = text.value.trim();
    if (!body) {
      refuse(text);
      return;
    }
    const line = anchorField(tab.group, tab, field);
    busy(form, true);
    const saved = await host.saveComment({ id: "", path: tab.path, line, lineText: field.quote, text: body }, (sentence) => refuse(text, sentence));
    busy(form, false);
    if (!saved) return;
    const st = stateOf(tab);
    st.fields = st.fields.filter((f) => f !== field);
    if (saved.id) {
      st.open.add(saved.id);
      st.landing = { id: saved.id, reply: false };
    }
    sync(tab.group);
    if (finePointer()) host.focusEditor(tab.group);
  }

  const commentOf = (card) => host.comments().find((c) => c.id === card?.dataset.lineThread) || null;

  function cancelReply(form) {
    closeReply(form);
    settle(form.closest("[data-line-thread]"));
  }

  async function sendReply(form) {
    const c = commentOf(form.closest("[data-line-thread]"));
    if (!c) return;
    const refused = (sentence) => refuse(form.querySelector("textarea"), sentence);
    const answer = replyOf(form);
    if (!answer) return;
    busy(form, true);
    const done = c.forge
      ? await postForgeThread(host.forgeThreadUrl(), { thread: c.forge.thread, body: answer.body, resolve: answer.toggles })
      : await answerLocal(answer, { reply: (body) => host.saveReply(c, body, "", refused), toggle: (resolve) => host.resolveThread(c, resolve) });
    busy(form, false);
    if (!done) return;
    cancelReply(form);
    const tab = host.tabOfNode(form);
    if (tab && finePointer()) host.focusEditor(tab.group);
  }

  // The pencil of a note puts its field in place of its text, Save writes
  // the note as the editor does, a reply or the thread's first note, and
  // puts the new text in its place right away.
  async function saveNote(form) {
    const c = commentOf(form.closest("[data-line-thread]"));
    const edit = noteEditOf(form);
    if (!c || !edit) return;
    let done = edit.same;
    if (!done) {
      const refused = (sentence) => refuse(form.querySelector("textarea"), sentence);
      busy(form, true);
      done = edit.id
        ? await host.saveReply(c, edit.body, edit.id, refused)
        : await host.saveComment(c.project ? { id: c.id, project: true, text: edit.body } : { id: c.id, path: c.path, line: c.line, lineText: host.lineText(c), text: edit.body }, refused);
      busy(form, false);
    }
    if (done) endNote(form, edit.body, finePointer());
  }

  function endNote(form, saved, focusEditor) {
    const tab = host.tabOfNode(form);
    closeNoteEdit(form, saved);
    settle(form.closest("[data-line-thread]"));
    if (tab && focusEditor) host.focusEditor(tab.group);
  }

  async function act(button) {
    const card = button.closest("[data-line-thread]");
    const c = commentOf(card);
    if (button.matches("[data-line-thread-reply-open]")) {
      openReply(card);
      return;
    }
    if (button.matches("[data-line-thread-reply-cancel]")) {
      cancelReply(button.closest("form"));
      return;
    }
    if (button.matches("[data-comment-note-edit]")) {
      openNoteEdit(button.closest("[data-comment-note]"));
      return;
    }
    if (button.matches("[data-comment-note-cancel]")) {
      endNote(button.closest("form"), undefined, false);
      return;
    }
    if (!c) return;
    button.disabled = true;
    if (button.matches("[data-line-thread-delete]")) await host.deleteThread(c);
    else if (button.matches("[data-comment-note-drop]")) {
      const reply = (c.replies || []).find((r) => r.id === button.closest("[data-comment-note]")?.dataset.commentNote);
      if (reply) await host.deleteReply(c, reply);
    } else if (c.forge) await postForgeThread(host.forgeThreadUrl(), { thread: c.forge.thread, resolve: true });
    else await host.resolveThread(c, button.matches("[data-line-thread-resolve]"));
    button.disabled = false;
  }

  // Keys typed into a field are its own: under a line Escape folds an empty
  // one away and hands the focus back to the code, in the dialog it closes
  // the dialog, Ctrl or Cmd plus Enter sends it, and a key without Ctrl or
  // Cmd never reaches the editor's page keys. Escape in the field of a note
  // being edited cancels the edit, the dialog stays. The view's own key
  // bindings never see them, see @dc/line-blocks.
  function onKey(event) {
    const text = event.target;
    if (!(text instanceof HTMLTextAreaElement) || !text.matches(THREAD_FIELDS)) return;
    const form = text.form;
    if (isModEnter(event)) {
      event.preventDefault();
      event.stopPropagation();
      form?.requestSubmit();
      return;
    }
    const tab = host.tabOfNode(form);
    if (event.key === "Escape" && form?.matches("[data-comment-note-form]")) {
      event.preventDefault();
      event.stopPropagation();
      endNote(form, undefined, true);
      return;
    }
    if (event.key === "Escape" && tab) {
      event.preventDefault();
      event.stopPropagation();
      const field = stateOf(tab).fields.find((f) => f.form === form);
      if (field && !holdsWords(form)) {
        closeField(tab, field, true);
        return;
      }
      if (!field && !holdsWords(form)) cancelReply(form);
      host.focusEditor(tab.group);
      return;
    }
    if (!event.ctrlKey && !event.metaKey) event.stopPropagation();
  }

  function wire(container) {
    container.addEventListener("click", (event) => {
      if (!(event.target instanceof Element)) return;
      const cancel = event.target.closest("[data-line-comment-cancel]");
      if (cancel) {
        const form = cancel.closest("[data-line-comment-form]");
        const tab = host.tabOfNode(form);
        const field = tab && stateOf(tab).fields.find((f) => f.form === form);
        if (field) closeField(tab, field, false);
        return;
      }
      const button = event.target.closest("[data-line-thread] :is([data-line-thread-reply-open], [data-line-thread-reply-cancel], [data-line-thread-resolve], [data-line-thread-reopen], [data-line-thread-delete], [data-comment-note-drop], [data-comment-note-edit], [data-comment-note-cancel])");
      if (!button || button.disabled) return;
      event.preventDefault();
      void act(button);
    }, { signal });
    container.addEventListener("submit", (event) => {
      const form = event.target instanceof HTMLFormElement && event.target.closest("[data-line-thread-reply], [data-line-comment-form], [data-comment-note-form]");
      if (!form) return;
      event.preventDefault();
      event.stopPropagation();
      if (form.matches("[data-line-comment-form]")) void addComment(form);
      else if (form.matches("[data-comment-note-form]")) void saveNote(form);
      else void sendReply(form);
    }, { signal });
    container.addEventListener("input", (event) => {
      if (!event.target.matches?.(THREAD_FIELDS)) return;
      event.target.classList.remove("is-invalid");
      growField(event.target);
      if (event.target.form?.matches("[data-line-thread-reply]")) paintReply(event.target.form);
    }, { signal });
    container.addEventListener("change", (event) => {
      if (event.target.matches?.("[data-line-thread-reply-resolve]")) paintReply(event.target.form);
    }, { signal });
    container.addEventListener("keydown", onKey, { signal });
    container.addEventListener("keyup", (event) => {
      if (event.target.matches?.(THREAD_FIELDS) && !event.ctrlKey && !event.metaKey) event.stopPropagation();
    }, { signal });
  }

  // The dialog of a thread that stands on no line, a project comment's or one
  // whose file or line is gone, holds its card the same way, pulled again
  // while it stands and the thread changes, and closes once the thread is
  // gone.
  function paintDialog() {
    if (!dialog.id) return;
    const c = host.comments().find((x) => x.id === dialog.id) || host.comments().find((x) => x.was === dialog.id);
    if (!c) {
      host.closeDialog();
      return;
    }
    if (c.id !== dialog.id) {
      const card = dialog.cards.get(dialog.id);
      dialog.cards.delete(dialog.id);
      if (card) dialog.cards.set(c.id, card);
      dialog.id = c.id;
    }
    const card = cardOf(dialog, c, paintDialog);
    if (card && card.parentElement !== host.dialogBody) host.dialogBody.replaceChildren(card);
    const active = document.activeElement;
    if (!active || active === document.body) host.dialogBody.closest(".modal")?.focus({ preventScroll: true });
  }

  async function openDialog(c) {
    dialog.id = c.id;
    dialog.cards.clear();
    dialog.pulls.clear();
    host.dialogBody.replaceChildren();
    paintDialog();
    await host.showDialog(c);
    dialog.id = "";
    dialog.cards.clear();
    dialog.pulls.clear();
    host.dialogBody.replaceChildren();
  }

  function paint() {
    host.eachGroup(sync);
    paintDialog();
  }

  wire(host.root);
  wire(host.dialogBody);
  window.visualViewport?.addEventListener("resize", () => {
    const active = document.activeElement;
    if (active?.matches?.(THREAD_FIELDS) && active.closest("[data-line-block]")) active.scrollIntoView({ block: "nearest" });
  }, { signal });

  return {
    sync,
    paint,
    press,
    reveal,
    compose,
    openDialog,
    // offer hears what the forge takes now: a change pulls the forge's
    // threads again, whose cards offer the reply and the resolve by it.
    offer(key) {
      if (key === offerKey) return;
      offerKey = key;
      paint();
    },
  };
}
