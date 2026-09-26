import { fire } from "@dc/dialog";
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
  return `<pre class="mb-0 mt-2 p-2 small overflow-auto text-start dc-prompt-text dc-prompt-command"`
    + ` data-gitprompt-command>${lines.join("\n")}</pre>`;
}

function approvalBlock(question) {
  const rows = [
    ["Assistant", question.assistant],
    ["Project", question.project],
    ["Stack", question.stack || "project root"],
    ["Command", question.action],
  ];
  const list = rows
    .map(([label, value]) => `<dt class="col-4 fw-normal text-secondary">${escapeHtml(label)}</dt><dd class="col-8 mb-1 text-break">${escapeHtml(value || "")}</dd>`)
    .join("");
  return `<dl class="row mb-0 text-start" data-approval-details>${list}</dl>` + commandBlock(question);
}

async function showApproval(question) {
  const result = await fire({
    title: "An assistant asks for approval",
    html: approvalBlock(question),
    footer: "The run waits until you decide, for half an hour at most. Denying it tells the assistant.",
    input: "checkbox",
    inputValue: 0,
    inputPlaceholder: "Approve and don't ask again",
    showDenyButton: true,
    confirmButtonText: "Approve",
    denyButtonText: "Deny",
    reverseButtons: true,
    allowOutsideClick: false,
  });
  if (shownId !== question.id) return;
  shownId = "";
  shownTarget = "";
  const reason = window.Swal.DismissReason || {};
  const denied = result.isDenied || result.dismiss === reason.cancel || result.dismiss === reason.esc;
  if (!result.isConfirmed && !denied) {
    void reconcile();
    return;
  }
  try {
    await postJSON("/git/prompt", {
      key: question.key,
      project: question.project,
      id: question.id,
      approve: result.isConfirmed,
      remember: result.isConfirmed && result.value === 1,
    });
  } catch (error) {
    void error;
  }
  void reconcile();
}

async function show(question) {
  shownId = question.id;
  shownTarget = question.target || "";
  markSeen();
  if (question.kind === "approval") {
    await showApproval(question);
    return;
  }
  const line = question.prompt || "An answer is needed to continue.";
  const secret = /pass(word|phrase)|secret|token|pin\b/i.test(line);
  const context = question.command
    ? ""
    : `<div class="text-secondary mb-3">${escapeHtml(question.project)} &middot; ${escapeHtml(question.action)}</div>`;
  // Through @dc/dialog, not around it: the question can stand over an open
  // modal (a create dialog, the editor's) and only a popup inside that modal
  // can be typed into.
  const result = await fire({
    title: "Git is asking",
    html: context
      + `<div class="text-start dc-prompt-text">${escapeHtml(line)}</div>`
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
      key: question.key,
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
