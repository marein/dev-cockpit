// Global client-side notification channel. Turns recoverable failures into a
// non-blocking toast (SweetAlert2 when present, console otherwise) and exposes
// helpers modules call directly. installErrorHandler wires unhandled promise
// rejections, where most uncaught fetch failures land, into the same channel.
let last = { text: "", at: 0 };

function show(text, icon, timer, force) {
  const now = Date.now();
  if (!force && text === last.text && now - last.at < 4000) {
    return;
  }
  last = { text: text, at: now };

  if (!window.Swal) {
    if (icon === "error") console.error(text);
    else console.log(text);
    return;
  }
  window.Swal.fire({
    toast: true,
    position: "top-end",
    icon: icon || "error",
    title: text,
    showConfirmButton: false,
    showCloseButton: true,
    timer: timer || 6000,
    timerProgressBar: true,
  });
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
