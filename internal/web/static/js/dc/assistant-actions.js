import { confirm } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

export function shareItem(id) {
  return { label: "Share", icon: "ti-share", href: `/settings/sharing?assistant=${encodeURIComponent(id)}` };
}

export async function deleteAssistant(url, name) {
  const ok = await confirm({
    title: name ? `Delete "${name}"?` : "Delete this assistant?",
    text: "Its thread goes with it. The coders it steers are released and yours again, the shared memory is kept.",
    confirmText: "Delete",
  });
  if (!ok) return false;
  let released = "";
  try {
    const response = await postForm(url, { form: "delete" });
    await ensureOk(response, "The assistant could not be deleted.");
    // The answer names the coders that came back, and that is the one thing
    // the user cannot see for themselves once the row is gone.
    released = (await response.json().catch(() => ({})))?.released || "";
  } catch (err) {
    notifyError(err?.message || "The assistant could not be deleted.");
    return false;
  }
  if (released) notifySuccess(released);
  return true;
}
