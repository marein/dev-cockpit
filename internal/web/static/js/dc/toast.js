// Global client-side notification channel. Turns recoverable failures into a
// non-blocking Bootstrap toast (console when bootstrap is missing) and exposes
// helpers modules call directly. Toasts deliberately do not ride the SweetAlert
// singleton: a toast firing while a dialog stands must never close that dialog.
// installErrorHandler wires unhandled promise rejections, where most uncaught
// fetch failures land, into the same channel.

const kinds = {
  info: { icon: "ti-info-circle", color: "text-info", bar: "" },
  success: { icon: "ti-circle-check", color: "text-success", bar: "bg-success" },
  error: { icon: "ti-alert-circle", color: "text-danger", bar: "bg-danger" },
};

function toastContainer() {
  let el = document.querySelector("[data-dc-toasts]");
  if (!el) {
    el = document.createElement("div");
    el.className = "toast-container position-fixed top-0 end-0 p-3";
    el.setAttribute("data-dc-toasts", "");
    document.body.appendChild(el);
  }
  return el;
}

export function showToast({ icon, title, detail, timer, onClick, onHidden } = {}) {
  const kind = kinds[icon] || kinds.error;
  const duration = timer || 6000;

  if (!window.bootstrap) {
    const text = [title, typeof detail === "string" ? detail : ""].filter(Boolean).join("\n");
    if (kind === kinds.error) console.error(text);
    else console.log(text);
    if (onHidden) onHidden();
    return { close() {} };
  }

  const toast = document.createElement("div");
  toast.className = "toast dc-toast" + (onClick ? " dc-toast-clickable" : "");
  toast.setAttribute("role", "status");

  const row = document.createElement("div");
  row.className = "d-flex align-items-start p-3";

  const glyph = document.createElement("i");
  glyph.className = `ti ${kind.icon} ${kind.color} fs-2 me-2`;

  const body = document.createElement("div");
  body.className = "flex-fill min-w-0";
  if (title) {
    const heading = document.createElement("div");
    heading.className = "fw-bold text-break";
    heading.textContent = title;
    body.append(heading);
  }
  if (detail instanceof Node) {
    body.append(detail);
  } else if (detail) {
    const block = document.createElement("div");
    block.className = "dc-toast-detail small text-start";
    block.textContent = detail;
    body.append(block);
  }

  const close = document.createElement("button");
  close.type = "button";
  close.className = "btn-close ms-2";
  close.setAttribute("data-bs-dismiss", "toast");
  close.setAttribute("aria-label", "Close");

  row.append(glyph, body, close);

  const progress = document.createElement("div");
  progress.className = "dc-toast-progress" + (kind.bar ? ` ${kind.bar}` : "");
  progress.style.animationDuration = `${duration}ms`;

  toast.append(row, progress);

  if (onClick) {
    toast.addEventListener("click", (event) => {
      if (event.target.closest(".btn-close")) return;
      onClick(event);
    });
  }

  toastContainer().appendChild(toast);
  const instance = new window.bootstrap.Toast(toast, { delay: duration, autohide: true });
  toast.addEventListener("hidden.bs.toast", () => {
    instance.dispose();
    toast.remove();
    if (onHidden) onHidden();
  }, { once: true });
  instance.show();
  return {
    close() {
      if (toast.isConnected) instance.hide();
    },
  };
}

let last = { text: "", at: 0 };

function show(text, icon, timer, force) {
  const now = Date.now();
  if (!force && text === last.text && now - last.at < 4000) {
    return;
  }
  last = { text: text, at: now };

  // The message rides in the bounded, scrollable detail block for errors, so a
  // fat git refusal stays readable on a phone instead of overflowing the
  // screen. The line breaks are kept, because the messages this bound exists
  // for are git's, and git writes its refusal as an error line plus its hints.
  // Run into one paragraph they are exactly as unreadable as before, only
  // shorter. Success and info messages are short statements and read as the
  // bold line instead.
  if (icon === "success" || icon === "info") {
    showToast({ icon: icon, title: text, timer: timer });
  } else {
    showToast({ icon: "error", detail: text, timer: timer });
  }
}

function clean(value, fallback) {
  const text = value == null ? "" : String(value).trim();
  return text || fallback;
}

export function notifyError(message) {
  show(clean(message, "Something went wrong."));
}

export function notifySuccess(message) {
  show(clean(message, "Done."), "success", 3000);
}

export function notifyInfo(message) {
  show(clean(message, "Working…"), "info", 3000, true);
}

// Best-effort human message from a failed fetch Response: prefers the server's
// JSON {error} or plain-text body, and ignores HTML error pages (which are
// meant for navigations, not AJAX).
export async function errorText(response, fallback) {
  try {
    const type = response.headers.get("content-type") || "";
    if (type.includes("application/json")) {
      const data = await response.json();
      if (data && data.error) return String(data.error);
    } else if (type.includes("text/plain")) {
      const text = (await response.text()).trim();
      if (text) return text;
    }
  } catch (error) {
    void error;
  }
  return clean(fallback, "Something went wrong.");
}

let handlerInstalled = false;

export function installErrorHandler() {
  if (handlerInstalled) {
    return;
  }
  handlerInstalled = true;
  window.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason;
    const message = reason && reason.message ? reason.message : reason;
    show(clean(message, "An unexpected error occurred."));
  });
}
