import { confirm } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { notifyError } from "@dc/toast";

export function setContext(host, percent, pending) {
  const ring = host.querySelector("[data-assistant-ring]");
  const value = Number(percent);
  if (!ring || (!pending && (!Number.isFinite(value) || value <= 0))) return;
  const capped = pending ? 0 : Math.min(100, Math.round(value));
  for (const arc of ring.querySelectorAll("[data-assistant-ring-fill], [data-assistant-ring-halo]")) {
    arc.setAttribute("stroke-dasharray", `${capped} 100`);
  }
  ring.toggleAttribute("data-assistant-ring-pending", Boolean(pending));
  if (capped >= 95) ring.setAttribute("data-assistant-ring-level", "full");
  else if (capped >= 85) ring.setAttribute("data-assistant-ring-level", "high");
  else ring.removeAttribute("data-assistant-ring-level");
  const full = host.querySelector("[data-assistant-context-full]");
  const waiting = host.querySelector("[data-assistant-context-pending]");
  const shown = host.querySelector("[data-assistant-context-percent]");
  if (shown) shown.textContent = String(capped);
  if (full) full.hidden = Boolean(pending);
  if (waiting) waiting.hidden = !pending;
  const button = host.querySelector("[data-assistant-new-label]");
  if (!button) return;
  const words = pending ? waiting?.textContent.trim() : `Context ${capped} percent`;
  const label = words ? `${button.getAttribute("data-assistant-new-label")}. ${words}` : button.getAttribute("data-assistant-new-label");
  button.setAttribute("title", label);
  button.setAttribute("aria-label", label);
}

export function syncCompact(host, running) {
  const button = host.querySelector("[data-assistant-compact]");
  if (!button) return;
  const ready = button.hasAttribute("data-assistant-compact-ready");
  button.disabled = running || !ready;
  const hint = host.querySelector("[data-assistant-compact-hint]");
  if (hint) hint.hidden = !button.disabled;
  const first = host.querySelector("[data-assistant-compact-first]");
  if (first) first.hidden = running || ready;
  const busy = host.querySelector("[data-assistant-compact-busy]");
  if (busy) busy.hidden = !running;
}

export function compactReady(host) {
  host.querySelector("[data-assistant-compact]")?.setAttribute("data-assistant-compact-ready", "");
}

export async function compactConversation(host, postUrl) {
  const button = host.querySelector("[data-assistant-compact]");
  if (!button || button.disabled) return null;
  const toggle = button.closest(".dropdown")?.querySelector("[data-bs-toggle='dropdown']");
  if (toggle) window.bootstrap?.Dropdown.getInstance(toggle)?.hide();
  const ok = await confirm({
    title: "Compact this conversation?",
    text: "The assistant replaces the turns so far with a summary of them, so the next answers start with less context.",
    warning: "It costs about one answer and cannot be undone.",
    kind: "danger",
    confirmText: "Compact",
  });
  if (!ok) return null;
  button.disabled = true;
  try {
    const response = await postForm(postUrl, { form: "compact" });
    await ensureOk(response, "The conversation could not be compacted.");
    return await response.json().catch(() => ({}));
  } catch (error) {
    syncCompact(host, host.hasAttribute("running"));
    notifyError(error.message || "The conversation could not be compacted.");
    return null;
  }
}
