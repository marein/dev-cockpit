import { CLOSED_EVENT, fire, isVisible } from "@dc/dialog";
import { onServerEvent } from "@dc/events";
import { getJSON, postJSON, postForm } from "@dc/http";
import { escapeHtml, windowSeen } from "@dc/dom";

let shownId = "";
let shownTarget = "";
let shownOpen = false;
let reconciling = false;
let queued = false;

function markSeen() {
  if (!shownTarget || !windowSeen()) return;
  void postForm("/notifications/read", { target: shownTarget }).catch(() => {});
}

function commandBlock({ command, dir }) {
  if (!command) return "";
  const lines = [];
  if (dir) lines.push(`cwd: ${escapeHtml(dir)}`);
  lines.push(`$ ${escapeHtml(command)}`);
  return `<pre class="mb-0 mt-2 p-2 small overflow-auto text-start dc-prompt-text dc-prompt-command"`
    + ` data-gitprompt-command>${lines.join("\n")}</pre>`;
}

function detailList(rows) {
  return rows
    .map((row) => `<dt class="col-4 fw-normal text-secondary">${escapeHtml(row.label)}</dt><dd class="col-8 mb-1 dc-prompt-text">${escapeHtml(row.value || "")}</dd>`)
    .join("");
}

function approvalBlock(question) {
  const rows = [{ label: "Asked by", value: question.assistant }, ...(question.details || [])];
  return `<div class="fw-bold mb-3 text-break" data-approval-what>${escapeHtml(question.action)}</div>`
    + `<dl class="row mb-0 text-start" data-approval-details>${detailList(rows)}</dl>`
    + commandBlock(question);
}

async function showSSHKey(question) {
  const unlock = !!question.unlock;
  const retry = question.retry
    ? `<div class="text-danger mb-2" data-ssh-key-retry>The passphrase did not open the key.</div>`
    : "";
  const input = unlock
    ? {
      input: "password",
      inputPlaceholder: "Passphrase",
      inputAttributes: { autocomplete: "off", autocorrect: "off", autocapitalize: "off", spellcheck: "false", "aria-label": "Passphrase" },
    }
    : {};
  const result = await fire({
    title: unlock ? "Open an SSH key" : "Sign with an SSH key",
    html: retry
      + `<dl class="row mb-0 text-start" data-ssh-key-details>${detailList(question.details || [])}</dl>`
      + (question.callers || []).map(commandBlock).join(""),
    footer: unlock
      ? "The key goes into the ssh-agent of every caller, for the time set under Settings, SSH agent."
      : "Denying fails this one signature.",
    ...input,
    showDenyButton: true,
    confirmButtonText: unlock ? "Open and sign" : "Allow",
    denyButtonText: "Deny",
    reverseButtons: true,
    allowOutsideClick: false,
  });
  const reason = window.Swal.DismissReason || {};
  const denied = result.isDenied || result.dismiss === reason.cancel || result.dismiss === reason.esc;
  await settle(question, result.isConfirmed || denied, {
    answer: unlock && result.isConfirmed ? result.value || "" : "",
    cancel: !result.isConfirmed,
  });
}

async function showApproval(question) {
  const result = await fire({
    title: "An assistant asks for approval",
    html: approvalBlock(question),
    footer: "It waits until you decide, for half an hour at most. Denying it tells the assistant.",
    input: "checkbox",
    inputValue: 0,
    inputPlaceholder: "Approve and don't ask again",
    showDenyButton: true,
    confirmButtonText: "Approve",
    denyButtonText: "Deny",
    reverseButtons: true,
    allowOutsideClick: false,
  });
  const reason = window.Swal.DismissReason || {};
  const denied = result.isDenied || result.dismiss === reason.cancel || result.dismiss === reason.esc;
  await settle(question, result.isConfirmed || denied, {
    approve: result.isConfirmed,
    remember: result.isConfirmed && result.value === 1,
  });
}

async function settle(question, answered, answer) {
  shownOpen = false;
  if (shownId !== question.id) return;
  if (answered) {
    try {
      await postJSON("/git/prompt", { key: question.key, project: question.project, id: question.id, ...answer });
    } catch (error) {
      void error;
    }
  }
  shownId = "";
  shownTarget = "";
  void reconcile();
}

async function show(question) {
  shownId = question.id;
  shownTarget = question.target || "";
  shownOpen = true;
  markSeen();
  if (question.kind === "approval") {
    await showApproval(question);
    return;
  }
  if (question.kind === "ssh-key") {
    await showSSHKey(question);
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
  const reason = window.Swal.DismissReason || {};
  const cancelled = result.dismiss === reason.cancel || result.dismiss === reason.esc;
  await settle(question, result.isConfirmed || cancelled, {
    answer: result.isConfirmed ? result.value || "" : "",
    cancel: !result.isConfirmed,
  });
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
      if (!shownOpen || questions.some((q) => q.id === shownId)) return;
      shownId = "";
      shownTarget = "";
      shownOpen = false;
      window.Swal.close();
      return;
    }
    if (questions.length && !isVisible()) void show(questions[0]);
  } finally {
    reconciling = false;
    if (queued) {
      queued = false;
      void reconcile();
    }
  }
}

onServerEvent("gitprompt", () => void reconcile());
document.addEventListener(CLOSED_EVENT, () => void reconcile());
document.addEventListener("visibilitychange", markSeen);
window.addEventListener("focus", markSeen);
void reconcile();
