import { CLOSED_EVENT, fire, isVisible } from "@dc/dialog";
import { onServerEvent } from "@dc/events";
import { getJSON, postJSON, postForm } from "@dc/http";
import { windowSeen } from "@dc/dom";
import { approvalView, gitQuestionView } from "@dc/prompt-view";

let shownId = "";
let shownTarget = "";
let shownOpen = false;
let reconciling = false;
let queued = false;

function markSeen() {
  if (!shownTarget || !windowSeen()) return;
  void postForm("/notifications/read", { target: shownTarget }).catch(() => {});
}

async function showApproval(question) {
  const result = await fire({ ...approvalView(question), reverseButtons: true, allowOutsideClick: false });
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
  // Through @dc/dialog, not around it: the question can stand over an open
  // modal (a create dialog, the editor's) and only a popup inside that modal
  // can be typed into.
  const result = await fire({ ...gitQuestionView(question), reverseButtons: true, allowOutsideClick: false });
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
