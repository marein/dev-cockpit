// SweetAlert2 wrappers with the app's dark preset. Falls back to native
// confirm/prompt when SweetAlert is unavailable, so flows still work.
const DARK = { background: "#1f2937", color: "#f8fafc" };

export function available() {
  return Boolean(window.Swal);
}

export function isVisible() {
  return Boolean(window.Swal && window.Swal.isVisible());
}

function nativeMessage({ title, text } = {}) {
  return [title, text].filter(Boolean).join("\n\n");
}

export function fire(options = {}) {
  if (!window.Swal) {
    const message = nativeMessage(options);
    if (options.showCancelButton) {
      return Promise.resolve({ isConfirmed: window.confirm(message) });
    }
    if (message) window.alert(message);
    return Promise.resolve({ isConfirmed: true });
  }
  return window.Swal.fire({ ...DARK, ...options });
}

export async function confirm({
  title,
  text,
  html,
  icon = "warning",
  confirmText = "Confirm",
  cancelText = "Cancel",
  target,
  heightAuto,
} = {}) {
  if (!window.Swal) {
    return window.confirm(title || text || "Are you sure?");
  }
  const result = await fire({
    title,
    text,
    html,
    icon,
    showCancelButton: true,
    confirmButtonText: confirmText,
    cancelButtonText: cancelText,
    reverseButtons: true,
    target,
    heightAuto,
  });
  return result.isConfirmed;
}

export function loading({ title, text } = {}) {
  if (!window.Swal) return Promise.resolve({ isConfirmed: false });
  return fire({
    title,
    text,
    allowOutsideClick: false,
    allowEscapeKey: false,
    didOpen: () => window.Swal.showLoading(),
  });
}

export async function promptText({
  title,
  html,
  placeholder,
  confirmText = "Create",
  validatorMessage = "Please enter a value.",
} = {}) {
  if (!window.Swal) {
    const value = window.prompt(title || "");
    return value && value.trim() ? value.trim() : null;
  }
  const result = await fire({
    title,
    html,
    input: "text",
    inputPlaceholder: placeholder,
    showCancelButton: true,
    confirmButtonText: confirmText,
    cancelButtonText: "Cancel",
    reverseButtons: true,
    inputValidator: (value) => (value && value.trim() ? undefined : validatorMessage),
  });
  return result.isConfirmed && result.value ? result.value.trim() : null;
}
