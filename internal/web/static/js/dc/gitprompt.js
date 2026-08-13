import { onServerEvent } from "@dc/events";
import { getJSON, postJSON, postForm } from "@dc/http";
import { escapeHtml, windowSeen } from "@dc/dom";

let shownId = "";
let shownTarget = "";
let reconciling = false;
let queued = false;

function markSeen() {
  if (!shownTarget || !windowSeen()) return;
  void postForm("/notifications/read", { target: shownTarget }).catch(() => {});
}

function commandBlock(question) {
  if (!question.command) return "";
  const lines = [];
  if (question.dir) lines.push(`cwd: ${escapeHtml(question.dir)}`);
  lines.push(`$ ${escapeHtml(question.command)}`);
  return `<pre class="mb-0 mt-2 p-2 small overflow-auto text-start"`
    + ` style="max-height: 12rem; white-space: pre-wrap; overflow-wrap: anywhere"`
    + ` data-gitprompt-command>${lines.join("\n")}</pre>`;
}

async function show(question) {
  shownId = question.id;
  shownTarget = question.target || "";
  markSeen();
  const line = question.prompt || "An answer is needed to continue.";
  const secret = /pass(word|phrase)|secret|token|pin\b/i.test(line);
  const context = question.command
    ? ""
    : `<div class="text-secondary" style="margin-bottom: .75rem">${escapeHtml(question.project)} &middot; ${escapeHtml(question.action)}</div>`;
  const result = await window.Swal.fire({
    title: "Git is asking",
    html: context
      + `<div style="white-space: pre-wrap; overflow-wrap: anywhere; text-align: start">${escapeHtml(line)}</div>`
      + commandBlock(question),
    footer: "This question comes from git or ssh. The cockpit only carries it and never keeps the answer.",
    input: secret ? "password" : "text",
    inputAttributes: { autocomplete: "off", autocorrect: "off", autocapitalize: "off", spellcheck: "false" },
    showCancelButton: true,
    confirmButtonText: "Send",
    cancelButtonText: "Cancel",
    reverseButtons: true,
    allowOutsideClick: false,
  });
  if (shownId !== question.id) return;
  shownId = "";
  shownTarget = "";
  const reason = window.Swal.DismissReason || {};
  const cancelled = result.dismiss === reason.cancel || result.dismiss === reason.esc;
  if (!result.isConfirmed && !cancelled) {
    void reconcile();
    return;
  }
  try {
    await postJSON("/git/prompt", {
      project: question.project,
      id: question.id,
      answer: result.isConfirmed ? result.value || "" : "",
      cancel: !result.isConfirmed,
    });
  } catch (error) {
    void error;
  }
  void reconcile();
}

async function reconcile() {
  if (reconciling) {
    queued = true;
    return;
  }
  reconciling = true;
  try {
    const data = await getJSON("/git/prompt").catch(() => null);
    if (!data || !window.Swal) return;
    const questions = data.questions || [];
    if (shownId) {
      if (questions.some((q) => q.id === shownId)) return;
      shownId = "";
      shownTarget = "";
      window.Swal.close();
    }
    if (questions.length) void show(questions[0]);
  } finally {
    reconciling = false;
    if (queued) {
      queued = false;
      void reconcile();
    }
  }
}

onServerEvent("gitprompt", () => void reconcile());
document.addEventListener("visibilitychange", markSeen);
window.addEventListener("focus", markSeen);
void reconcile();
