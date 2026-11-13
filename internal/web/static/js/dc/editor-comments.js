import { labelNodes } from "@dc/contextmenu";
import { el, escapeHtml, growTextarea } from "@dc/dom";
import { ensureOk, postForm, postJSON } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

export function sortComments(list) {
  return [...list].sort((a, b) => {
    if (!!a.project !== !!b.project) return a.project ? -1 : 1;
    return a.path === b.path ? a.line - b.line : a.path < b.path ? -1 : 1;
  });
}

const PROJECT_PLACE = "Project comment";

export const placeOf = (comment) => (comment.project ? PROJECT_PLACE : `${comment.path}:${comment.line}`);

const flatLine = (text) => String(text || "").replace(/[\u0000-\u0008\u000a-\u001f\u007f-\u009f\u2028\u2029]/g, " ");

const noteLines = (c) => c.text.split("\n").map((line) => (c.author ? `${flatLine(c.author)}: ${flatLine(line)}` : flatLine(line)).trimEnd());

export function findUniqueQuoteLine(doc, quote) {
  if (!quote) return 0;
  let at = 0;
  for (let n = 1; n <= doc.lines; n++) {
    if (doc.line(n).text === quote) {
      if (at) return 0;
      at = n;
    }
  }
  return at;
}

const SHORT_ESCAPES = { "\t": "\\t", "\n": "\\n", "\r": "\\r", "\f": "\\f", "\v": "\\v", "\b": "\\b", "\u0007": "\\a" };

function escapeOf(char) {
  const code = char.charCodeAt(0);
  return SHORT_ESCAPES[char] || (code < 0x100 ? `\\x${code.toString(16).padStart(2, "0")}` : `\\u${code.toString(16).padStart(4, "0")}`);
}

// visibleLine is a value that stands on one line, a path or a quoted code
// line, as nodes that keep it there: a control character or a line or
// paragraph separator shows as its escape in a code chip. WebKit breaks a line
// at the separators even where nothing may wrap.
function visibleLine(text) {
  const value = String(text || "");
  const nodes = [];
  let at = 0;
  for (const match of value.matchAll(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g)) {
    if (match.index > at) nodes.push(value.slice(at, match.index));
    nodes.push(el("code", {}, escapeOf(match[0])));
    at = match.index + 1;
  }
  if (at < value.length) nodes.push(value.slice(at));
  return nodes;
}

export const placeNodes = (path, line) => labelNodes({ head: visibleLine(path), tail: `:${line}` });

export function noteStart(text, max) {
  const line = flatLine(text).replace(/\s+/g, " ").trim();
  return [...line].length > max ? `${[...line].slice(0, max - 1).join("")}…` : line;
}

export const resolved = (comment) => !!comment.resolved;

const repliesOf = (comment) => comment.replies || [];

export const replyCount = (n) => (n === 1 ? "1 reply" : `${n} replies`);

// commentRow is what one entry of the Line comments sheet shows, as the
// sheet's own row takes it: the path on one line, or Project comment, with
// the number of replies behind it and the first note flattened to one, so
// nothing a comment holds breaks out of its entry, and under an outdated one
// the line it was made on.
export function commentRow(comment) {
  const note = flatLine(comment.text);
  const quote = String(comment.lineText || "").trim();
  const outdated = quote ? `Outdated · was: ${flatLine(quote)}` : "Outdated";
  const replies = repliesOf(comment).length;
  const row = {
    icon: "ti-message-circle",
    iconClass: comment.outdated ? "text-orange" : resolved(comment) ? "text-green" : undefined,
    label: comment.project
      ? { head: PROJECT_PLACE, tail: replies ? ` · ${replyCount(replies)}` : "" }
      : { head: visibleLine(comment.path), tail: `:${comment.line}${replies ? ` · ${replyCount(replies)}` : ""}` },
    title: [...noteLines(comment), ...repliesOf(comment).flatMap(noteLines), ...(resolved(comment) ? ["Resolved"] : []), ...(comment.outdated ? [outdated] : [])].join("\n"),
  };
  if (!comment.outdated) return { ...row, sub: note };
  const old = el("span", { class: "text-orange text-truncate" }, quote ? ["Outdated · was: ", ...visibleLine(quote)] : outdated);
  return { ...row, subNodes: [el("span", { class: "d-flex flex-column min-w-0" }, el("span", { class: "text-truncate" }, note), old)] };
}

// authorHead gives the entry of a comment a guest wrote one more line at its
// head, the person icon and the guest's name, and one of a review thread on
// the forge the forge's icon, the author there or the forge's name, and
// whether the thread is resolved.
export function authorHead(row, comment) {
  const forge = comment.forge;
  if (!comment.author && !forge) return row;
  const own = el("span", { class: "d-flex align-items-center gap-2 min-w-0" }, ...row.childNodes);
  row.classList.replace("align-items-center", "align-items-stretch");
  row.classList.replace("gap-2", "gap-1");
  row.classList.add("flex-column");
  row.replaceChildren(el(
    "span",
    { class: "d-flex align-items-center gap-2 min-w-0 text-secondary", "data-comment-author": "" },
    el("i", { class: `ti ${forge ? forge.icon : "ti-user"}`, "aria-hidden": "true" }),
    el("span", { class: "d-flex min-w-0" }, ...labelNodes({ head: visibleLine(comment.author || forge.label) })),
    resolved(comment) ? el("span", { class: "text-green text-nowrap", "data-comment-resolved": "" }, "Resolved") : null,
  ), own);
  return row;
}

// writeLineComment posts one write of a line comment thread to its route of
// the editor's line comments and answers what came back, null after it handed
// the refusal to refused, a toast unless the caller shows it at its field. An
// answer with gone is a thread another device deleted meanwhile.
export async function writeLineComment(url, payload, failed, refused = notifyError) {
  try {
    const response = await postJSON(url, payload);
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || failed);
    return data;
  } catch (err) {
    refused(err.message || failed);
    return null;
  }
}

// mayDelete is whether the reader may delete a thread, as the server judged
// it by the rule its delete takes.
export const mayDelete = (comment) => !!comment.deletes;

const noteConfirm = (comment, note) => `${el("div", {}, ...visibleLine(placeOf(comment))).outerHTML}${note.author ? `<div>By ${escapeHtml(note.author)}</div>` : ""}<div class="text-secondary">${escapeHtml(note.text)}</div>`;

// deleteCommentQuestion is the confirm before a thread is deleted, which
// takes its replies along.
export function deleteCommentQuestion(comment) {
  const replies = repliesOf(comment).length;
  return {
    title: "Delete this comment?",
    html: `${noteConfirm(comment, comment)}${replies ? `<div>It takes ${replies === 1 ? "its reply" : `its ${replyCount(replies)}`} along.</div>` : ""}`,
    confirmText: "Delete",
  };
}

// deleteReplyQuestion is the confirm before one reply of a thread is deleted.
export const deleteReplyQuestion = (comment, reply) => ({ title: "Delete this reply?", html: noteConfirm(comment, reply), confirmText: "Delete" });

// deleteLineComments posts a delete of line comment threads to the delete
// route of a project's line comments and answers whether it went, false
// after it toasted the refusal.
export async function deleteLineComments(url, body) {
  try {
    const response = await postJSON(url, body);
    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new Error(data.error || "The comment could not be deleted.");
    }
    return true;
  } catch (err) {
    notifyError(err.message || "The comment could not be deleted.");
    return false;
  }
}

// postForgeThread answers or resolves a review thread over the forge's thread
// route of a project and toasts what came of it, branch empty for the one HEAD
// is on. The line comments follow with the linecomments event once the forge
// is read again. It answers whether what sent it is done: a resolve refused
// after the reply went answers true too, a second Reply would post it twice.
export async function postForgeThread(url, { thread, branch = "", body = "", resolve = false }) {
  const fields = [["thread", thread]];
  if (branch) fields.push(["branch", branch]);
  if (body) fields.push(["body", body]);
  if (resolve) fields.push(["resolve", "1"]);
  let replied = false;
  try {
    const response = await postForm(url, fields);
    if (!response.ok) replied = (await response.clone().json().catch(() => ({}))).replied === true;
    await ensureOk(response, body ? "The reply could not be posted." : "The thread could not be resolved.");
    notifySuccess((await response.json()).message);
    return true;
  } catch (err) {
    notifyError(err.message);
    return replied;
  }
}

// publishWords names what a publish of count comments makes on the forge,
// general of them project comments, which become general comments.
export function publishWords(count, general) {
  if (general && general === count) return { each: "a general comment", aside: "" };
  return { each: "a review thread", aside: general ? ", a project comment a general one" : "" };
}

// publishQuestion is the confirm before count comments, general of them
// project comments, are published on the change request an offer names.
export function publishQuestion(count, general, offer) {
  const words = publishWords(count, general);
  const becomes = count === 1 ? `It becomes ${words.each}` : `Each becomes ${words.each} of its own`;
  return {
    title: count === 1 ? "Publish the comment?" : `Publish all ${count} comments?`,
    text: `${becomes} on ${offer.noun} ${offer.ref} on ${offer.forge}${words.aside}, for everybody who can read it there.`,
    confirmText: "Publish",
  };
}

// publishComments publishes the line comments with the ids over the publish
// route of a project. A published comment comes back as a note of the forge
// with the linecomments event, the toast only says it went.
export async function publishComments(url, ids) {
  try {
    const response = await postForm(url, ids.map((id) => ["id", id]));
    await ensureOk(response, "The comments could not be published.");
    notifySuccess((await response.json()).message);
  } catch (err) {
    notifyError(err.message);
  }
}

// The fields of a thread's card, its reply and a note being edited, and of a
// new comment under a line, which grow with their text up to a share of the
// window.
export const THREAD_FIELDS = "[data-line-thread-reply-text], [data-line-comment-text], [data-comment-note-field]";

export const growField = (text) => growTextarea(text, () => window.innerHeight);

const requiredText = new WeakMap();

// refuse marks a field and says why under it, the server's sentence or the
// one the field is required with.
export function refuse(text, sentence = "") {
  const feedback = text.parentElement?.querySelector(":scope > .invalid-feedback");
  if (feedback) {
    if (!requiredText.has(feedback)) requiredText.set(feedback, feedback.textContent);
    feedback.textContent = sentence || requiredText.get(feedback);
  }
  text.classList.add("is-invalid");
  text.focus();
}

export const replyOpen = (card) => !!card.querySelector("[data-line-thread-reply]:not([hidden])");

const OPEN_EDITS = "[data-comment-note-form]:not([hidden])";

// openEdits are the notes of a card whose text stands as its field.
export const openEdits = (card) => [...card.querySelectorAll(OPEN_EDITS)].map((form) => form.closest("[data-comment-note]"));

// typingIn is whether a card holds words a swap would take: its reply open
// or a note being edited.
export const typingIn = (card) => replyOpen(card) || !!card.querySelector(OPEN_EDITS);

// openNoteEdit puts the field of a note in place of its text, holding that
// text, sized to it and with the caret at its end. A field already open
// keeps what was typed there.
export function openNoteEdit(note) {
  const form = note?.querySelector(":scope > [data-comment-note-form]");
  const text = note?.querySelector(":scope > [data-comment-note-text]");
  if (!form || !text) return null;
  const field = form.querySelector("textarea");
  if (form.hidden) {
    form.dcFrom = text.textContent;
    field.value = form.dcFrom;
    field.classList.remove("is-invalid");
    text.hidden = true;
    form.hidden = false;
  }
  growField(field);
  field.focus();
  field.setSelectionRange(field.value.length, field.value.length);
  return form;
}

// closeNoteEdit puts the note's text back in place of its field, the words
// saved where it went.
export function closeNoteEdit(form, saved) {
  const text = form.closest("[data-comment-note]")?.querySelector(":scope > [data-comment-note-text]");
  if (text && typeof saved === "string") text.textContent = saved;
  form.hidden = true;
  form.querySelector("textarea")?.classList.remove("is-invalid");
  if (text) text.hidden = false;
}

// noteEdited is whether the field of a note being edited holds other words
// than the note.
export const noteEdited = (form) => !form.hidden && form.querySelector("textarea")?.value !== form.dcFrom;

// noteEditOf is what the edit of a note sends: the note's id, empty for the
// first note, and its words, which are the note's own where nothing changed,
// null after it refused an empty field.
export function noteEditOf(form) {
  const field = form.querySelector("textarea");
  const body = field.value.trim();
  if (!body) {
    refuse(field);
    return null;
  }
  return { id: form.closest("[data-comment-note]")?.dataset.commentNote || "", body, same: body === form.dcFrom };
}

// paintReply names what the reply form of a thread's card sends: Resolve or
// Reopen while its switch stands on and its field is empty, else Reply.
export function paintReply(form) {
  const send = form.querySelector("[data-line-thread-reply-send]");
  const toggle = form.querySelector("[data-line-thread-reply-resolve]");
  if (!send) return;
  const toggling = !!toggle?.checked && !form.querySelector("textarea")?.value.trim();
  send.textContent = toggling ? (toggle.value === "1" ? "Resolve" : "Reopen") : "Reply";
}

// openReply unfolds the reply form of a thread's card, its field taking the
// focus on a fine pointer, and answers the form.
export function openReply(card) {
  const form = card.querySelector("[data-line-thread-reply]");
  if (!form) return null;
  form.hidden = false;
  card.querySelector("[data-line-thread-reply-open]")?.setAttribute("aria-expanded", "true");
  const text = form.querySelector("textarea");
  growField(text);
  if (matchMedia("(pointer: fine)").matches) text?.focus();
  return form;
}

export function closeReply(form) {
  form.reset();
  form.querySelector("textarea")?.classList.remove("is-invalid");
  form.hidden = true;
  paintReply(form);
  form.closest("[data-line-thread]")?.querySelector("[data-line-thread-reply-open]")?.setAttribute("aria-expanded", "false");
}

// replyOf is what the reply form of a thread's card sends: its words and,
// where it offers the switch, whether that resolves or reopens the thread,
// null after it refused an empty form at its field.
export function replyOf(form) {
  const text = form.querySelector("textarea");
  const body = text.value.trim();
  const toggle = form.querySelector("[data-line-thread-reply-resolve]");
  if (!body && !toggle?.checked) {
    refuse(text);
    return null;
  }
  return { body, toggles: !!toggle?.checked, resolve: toggle?.checked ? toggle.value === "1" : null };
}

// answerLocal writes the answer to a local thread the way every place does:
// the reply first, then the resolve or the reopen. A switch refused after the
// reply went still counts as done, a second Reply would post the words twice.
export async function answerLocal({ body, resolve }, { reply, toggle }) {
  if (body && !(await reply(body))) return false;
  if (resolve !== null && !(await toggle(resolve))) return !!body;
  return true;
}

// placeChildren puts nodes into box in their order and takes the rest out,
// moving no node that stays in the order it stands, so a focused field keeps
// its focus and its caret.
export function placeChildren(box, nodes) {
  const stay = new Set(nodes);
  for (const child of [...box.children]) if (!stay.has(child)) child.remove();
  let at = box.firstElementChild;
  for (const node of nodes) {
    if (node === at) at = at.nextElementSibling;
    else box.insertBefore(node, at);
  }
}
