import { fire, promptText } from "@dc/dialog";
import { escapeHtml } from "@dc/dom";
import { ensureOk, postForm } from "@dc/http";
import { notifyError } from "@dc/toast";

// A coder's name lives in its CLI's own session record, so the server types
// the CLI's rename command into the session as is. Only the user can see
// whether that fits the coder's state, so the dialog says what happens.
const CODER_NOTE =
  `<p class="text-secondary text-start mb-0">This sends <code>/rename &lt;new name&gt;</code> into the running coder as is, ` +
  `regardless of what stands in its input line or which dialog is open. ` +
  `If that does not fit right now, cancel and type the command into the coder yourself.</p>`;

export const inactiveCoderRenameItem = {
  label: "Inactive coders cannot be renamed",
  icon: "ti-pencil-off",
  disabled: true,
};

let open = false;

// renameShell and renameCoder ask for the new name and post it. They answer
// the name the server took, or null when nothing was renamed. A coder whose
// CLI renames only in a dialog of its own (mode "manual") gets the way there
// instead, nothing is sent and nothing is posted.
export function renameShell(id, current) {
  return rename(`/shells/${id}/rename`, current, `Rename shell "${current}"`, undefined, "Could not rename the shell.");
}

export function renameCoder(id, current, { mode, label } = {}) {
  if (mode === "manual") return renameInside(current, label);
  return rename(`/coders/${id}/rename`, current, `Rename coder "${current}"`, CODER_NOTE, "Could not rename the coder.");
}

async function renameInside(current, label) {
  if (open) return null;
  open = true;
  try {
    await fire({
      title: `Rename coder "${current}"`,
      icon: "info",
      html:
        `<p class="text-secondary text-start mb-0">Renaming ${escapeHtml(label || "this coder")} from the cockpit is not possible. ` +
        `Run <code>/rename</code> directly in the coder.</p>`,
      confirmButtonText: "OK",
    });
  } finally {
    open = false;
  }
  return null;
}

async function rename(url, current, title, html, failure) {
  if (open) return null;
  open = true;
  try {
    const name = await promptText({
      title,
      html,
      value: current,
      confirmText: "Rename",
      validatorMessage: "Please enter a name.",
    });
    if (!name || name === current) return null;
    const response = await postForm(url, { name });
    await ensureOk(response, failure);
    const payload = await response.json().catch(() => ({}));
    return payload.name || name;
  } catch (error) {
    notifyError(error.message || failure);
    return null;
  } finally {
    open = false;
  }
}
