import { confirm, fire } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

const JOBS_URL = "/assistant/jobs";
const EMPTY_HINT =
  "Optional: what has to be true for this job to count as done. " +
  "Left empty, the assistant checks against the task the session itself is on.";

export async function steerCoder({ terminal, name, prefill = "" }) {
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
  if (!result.isConfirmed) return false;
  try {
    const response = await postForm(JOBS_URL, {
      form: "steer",
      terminal,
      done_when: (result.value || "").trim(),
    });
    await ensureOk(response, "Could not steer the coder.");
    notifySuccess(`Steering "${name}".`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

export async function releaseCoder({ terminal, name }) {
  const ok = await confirm({
    title: `Release "${name}"?`,
    text: "The coder keeps working, the assistant stops checking on it. It is yours again.",
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
