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

// popupHost is where a popup has to open: inside the modal that stands, if one
// does. Bootstrap holds the focus inside an open modal, so a popup that hangs
// elsewhere in the document loses the focus again the moment it takes it, and
// its field cannot be typed into (the git passphrase question above all).
// Inside the modal the focus trap is satisfied: the popup keeps the focus while
// it stands and the modal has it back when it closes. The topmost modal wins,
// and a caller that names its own target keeps it.
function popupHost() {
  const modals = document.querySelectorAll(".modal.show");
  return modals.length ? modals[modals.length - 1] : undefined;
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
  const host = options.target || popupHost();
  if (!host) return window.Swal.fire(options);
  const settings = { ...options, target: host };
  // heightAuto is about the page behind the popup and means nothing with a
  // target of its own; SweetAlert warns about the pair.
  if (settings.heightAuto === undefined) settings.heightAuto = false;
  return window.Swal.fire(settings);
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
