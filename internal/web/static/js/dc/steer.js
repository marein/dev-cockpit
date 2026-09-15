import { confirm, fire } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

const JOBS_URL = "/assistants/jobs";
const ASSISTANTS_URL = "/assistants/instances";
const EMPTY_HINT =
  "Optional: what has to be true for this job to count as done. " +
  "Left empty, the assistant checks against the task the session itself is on.";

// A job belongs to one assistant: its checks wake that one and its reports land
// in that thread. With a single assistant there is nothing to ask, so the
// dialog stays the one field it always was and the server picks the only one
// there is. With several, the dialog asks, because otherwise the reports land
// somewhere the user did not choose.
export async function steerCoder({ terminal, name, prefill = "" }) {
  const assistants = await liveAssistants();
  const result = assistants.length > 1
    ? await askWithAssistant(name, prefill, assistants)
    : await askPlain(name, prefill);
  if (!result) return false;
  try {
    const response = await postForm(JOBS_URL, {
      form: "steer",
      terminal,
      assistant: result.assistant,
      done_when: result.doneWhen,
    });
    await ensureOk(response, "Could not steer the coder.");
    notifySuccess(`Steering "${name}".`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

async function askPlain(name, prefill) {
  const result = await fire({
    title: `Steer "${name}"`,
    input: "textarea",
    inputValue: prefill,
    inputPlaceholder: EMPTY_HINT,
    inputAttributes: { "aria-label": "Done when" },
    showCancelButton: true,
    confirmButtonText: "Steer",
    cancelButtonText: "Cancel",
    reverseButtons: true,
  });
  if (!result.isConfirmed) return null;
  return { assistant: "", doneWhen: (result.value || "").trim() };
}

// The list arrives in the order the user sorted it, so the first row is the
// preselected one. There is no assistant the nav opens any more, the entry
// opens the list, so the dialog names its choice instead of following a
// position nothing else in the page shows.
async function askWithAssistant(name, prefill, assistants) {
  const options = assistants
    .map((one) => `<option value="${escapeAttr(one.id)}">${escapeText(one.title)}</option>`)
    .join("");
  const result = await fire({
    title: `Steer "${name}"`,
    html:
      `<label class="form-label d-block text-start" for="dc-steer-assistant">Reports go to</label>` +
      `<select id="dc-steer-assistant" class="form-select mb-3">${options}</select>` +
      `<label class="form-label d-block text-start" for="dc-steer-done-when">Done when</label>` +
      `<textarea id="dc-steer-done-when" class="form-control" rows="4" placeholder="${escapeAttr(EMPTY_HINT)}"></textarea>`,
    didOpen: () => {
      const box = document.getElementById("dc-steer-done-when");
      if (box) box.value = prefill;
    },
    showCancelButton: true,
    confirmButtonText: "Steer",
    cancelButtonText: "Cancel",
    reverseButtons: true,
    preConfirm: () => ({
      assistant: document.getElementById("dc-steer-assistant")?.value || "",
      doneWhen: (document.getElementById("dc-steer-done-when")?.value || "").trim(),
    }),
  });
  if (!result.isConfirmed) return null;
  return result.value;
}

// The assistants that exist right now. A failed read answers with none, and the
// dialog then asks nothing and lets the server decide, which is what it does
// with one assistant anyway.
async function liveAssistants() {
  try {
    const response = await fetch(ASSISTANTS_URL, { headers: { Accept: "application/json" } });
    if (!response.ok) return [];
    const payload = await response.json();
    return Array.isArray(payload?.assistants) ? payload.assistants : [];
  } catch {
    return [];
  }
}

function escapeText(value) {
  const node = document.createElement("span");
  node.textContent = value ?? "";
  return node.innerHTML;
}

function escapeAttr(value) {
  return escapeText(value).replaceAll('"', "&quot;");
}

export async function releaseCoder({ terminal, name }) {
  const ok = await confirm({
    title: `Release "${name}"?`,
    text: "The coder keeps working, nothing checks on it any more. It is yours again.",
    confirmText: "Release",
  });
  if (!ok) return false;
  try {
    const response = await postForm(JOBS_URL, { form: "release", terminal });
    await ensureOk(response, "Could not release the coder.");
    notifySuccess(`"${name}" is released.`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}
