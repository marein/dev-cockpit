// SweetAlert2 wrappers. Falls back to native confirm/prompt when SweetAlert
// is unavailable, so flows still work. Theming happens in style.css through
// the --swal2-* custom properties, so open popups follow live theme flips.
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
  return window.Swal.fire(options);
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
  value,
  confirmText = "Create",
  validatorMessage = "Please enter a value.",
  allowEmpty = false,
} = {}) {
  if (!window.Swal) {
    const answer = window.prompt(title || "", value || "");
    if (answer === null) return null;
    const trimmed = answer.trim();
    return trimmed || allowEmpty ? trimmed : null;
  }
  const result = await fire({
    title,
    html,
    input: "text",
    inputPlaceholder: placeholder,
    inputValue: value || "",
    showCancelButton: true,
    confirmButtonText: confirmText,
    cancelButtonText: "Cancel",
    reverseButtons: true,
    inputValidator: allowEmpty
      ? undefined
      : (input) => (input && input.trim() ? undefined : validatorMessage),
  });
  if (!result.isConfirmed) return null;
  const trimmed = (result.value || "").trim();
  return trimmed || allowEmpty ? trimmed : null;
}
