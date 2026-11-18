import { confirm, fire } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { formView } from "@dc/prompt-view";
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
// somewhere the user did not choose. The list arrives in the order the user
// sorted it, so the first row is the preselected one. There is no assistant the
// nav opens any more, the entry opens the list, so the dialog names its choice
// instead of following a position nothing else in the page shows.
export async function steerCoder({ terminal, name, prefill = "" }) {
  const assistants = await liveAssistants();
  const several = assistants.length > 1;
  const result = await fire({
    ...formView({
      title: `Steer "${name}"`,
      icon: "steering-wheel",
      tone: "steer",
      details: [
        ...(several ? [{
          as: "field", control: "select", id: "dc-steer-assistant", label: "Reports go to",
          options: assistants.map((one) => ({ value: one.id, text: one.title })),
        }] : []),
        { as: "field", control: "textarea", id: "dc-steer-done-when", label: "Done when", value: prefill, focus: !several },
        { as: "note", value: EMPTY_HINT },
      ],
      confirmText: "Steer",
    }),
    preConfirm: () => ({
      assistant: document.getElementById("dc-steer-assistant")?.value || "",
      doneWhen: (document.getElementById("dc-steer-done-when")?.value || "").trim(),
    }),
  });
  if (!result.isConfirmed) return false;
  try {
    const response = await postForm(JOBS_URL, {
      form: "steer",
      terminal,
      assistant: result.value.assistant,
      done_when: result.value.doneWhen,
    });
    await ensureOk(response, "Could not steer the coder.");
    notifySuccess(`Steering "${name}".`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
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

// The server writes the steering assistant onto a coder's icon, and never
// onto a guest's, so the entry needs no request and a guest gets none.
export function steeredByItem(row) {
  const icon = row?.querySelector("[data-steered-url]");
  if (!icon) return null;
  return { label: icon.dataset.steeredBy, icon: "ti-sparkles", title: `Steered by ${icon.dataset.steeredBy}`, href: icon.dataset.steeredUrl };
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

// alsoDropped appends what a deletion took with it to the sentence a surface
// says about it. Deleting a coder closes its job and drops the triggers that
// could only ever have fired on that terminal, and the server answers the one
// wording for it (`dropped`), which the page's flash and the CLI print too, so
// no surface writes a second one. Without it the sentence stands as it was.
export function alsoDropped(message, answer) {
  const dropped = answer?.dropped;
  if (!dropped) return message;
  return `${message.replace(/\.$/, "")}, ${dropped}.`;
}
