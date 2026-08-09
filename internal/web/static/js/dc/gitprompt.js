import { onServerEvent } from "@dc/events";
import { getJSON, postJSON } from "@dc/http";
import { escapeHtml } from "@dc/dom";

let shownId = "";
let reconciling = false;
let queued = false;

async function show(question) {
  shownId = question.id;
  const line = question.prompt || "An answer is needed to continue.";
  const secret = /pass(word|phrase)|secret|token|pin\b/i.test(line);
  const result = await window.Swal.fire({
    title: "Git is asking",
    html: `<div class="text-secondary" style="margin-bottom: .75rem">${escapeHtml(question.project)} &middot; ${escapeHtml(question.action)}</div>`
      + `<div style="white-space: pre-wrap; overflow-wrap: anywhere; text-align: start">${escapeHtml(line)}</div>`,
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
void reconcile();
