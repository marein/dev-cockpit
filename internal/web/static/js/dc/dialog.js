import { confirmView, formView, promptView } from "@dc/prompt-view";

// SweetAlert2 wrappers. Falls back to native confirm/prompt when SweetAlert
// is unavailable, so flows still work. Theming happens in dialog.css through
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

// A popup leaves its modal before the modal hides. SweetAlert removes a closed
// popup only when its close animation ends, and a hidden modal stops that
// animation, so an answered popup would stay in the hidden modal, still counted
// as open, and stand again the next time the modal shows. Out where it stands
// without a modal, it closes to the end whatever hides the modal, and one still
// unanswered goes with the modal it asked about.
document.addEventListener("hide.bs.modal", (event) => {
  const container = window.Swal?.getContainer();
  if (event.defaultPrevented || !container || !event.target.contains(container)) return;
  document.body.append(container);
  window.Swal.close();
});

export const CLOSED_EVENT = "dc:dialog-closed";

function announcingClose(options) {
  const own = options.didClose;
  return {
    ...options,
    didClose() {
      if (typeof own === "function") own.call(this);
      document.dispatchEvent(new CustomEvent(CLOSED_EVENT));
    },
  };
}

// SweetAlert reads a title as HTML, so a name in it would lose text or run as
// markup. A title is text, markup goes in html.
function plainTitle({ title, ...options }) {
  return { ...options, titleText: title };
}

export function fire({ native, ...options } = {}) {
  if (!window.Swal) {
    const message = nativeMessage(native || options);
    if (options.showCancelButton) {
      return Promise.resolve({ isConfirmed: window.confirm(message) });
    }
    if (message) window.alert(message);
    return Promise.resolve({ isConfirmed: true });
  }
  const host = options.target || popupHost();
  if (!host) return window.Swal.fire(announcingClose(plainTitle(options)));
  const settings = announcingClose({ ...plainTitle(options), target: host });
  // heightAuto is about the page behind the popup and means nothing with a
  // target of its own; SweetAlert warns about the pair.
  if (settings.heightAuto === undefined) settings.heightAuto = false;
  return window.Swal.fire(settings);
}

export async function confirm({
  title,
  text,
  warning,
  details,
  kind,
  confirmText,
  cancelText,
  target,
  heightAuto,
} = {}) {
  if (!window.Swal) {
    return window.confirm(title || text || "Are you sure?");
  }
  const view = confirmView({ title, text, warning, details, kind, confirmText, cancelText });
  const result = await fire({ ...view, target, heightAuto });
  return result.isConfirmed;
}

export function confirmForm(form, options = {}) {
  const ask = form.dataset;
  return confirm({
    title: ask.confirm,
    text: ask.confirmText,
    warning: ask.confirmWarning,
    kind: ask.confirmKind,
    confirmText: ask.confirmButton || "Confirm",
    ...options,
  });
}

export function notice({ title, text, details, kind = "info", confirmText = "OK", timer } = {}) {
  return fire({
    ...confirmView({ title, text, details, kind, confirmText }),
    showCancelButton: false,
    ...(timer && { timer, showConfirmButton: false }),
  });
}

// The loader starts in didRender: didOpen waits a task, and an answer that came
// quicker would already stand in the popup and get the loader instead.
export function loading({ title, text, icon } = {}) {
  if (!window.Swal) return Promise.resolve({ isConfirmed: false });
  return fire({
    ...formView({ title, text, icon, confirmText: "" }),
    showCancelButton: false,
    allowOutsideClick: false,
    allowEscapeKey: false,
    didRender: () => window.Swal.showLoading(),
  });
}

export async function promptText({
  title,
  text,
  details,
  icon,
  tone,
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
  const required = allowEmpty ? "" : validatorMessage;
  const result = await fire(promptView({ title, text, details, icon, tone, placeholder, value, required, confirmText }));
  if (!result.isConfirmed) return null;
  const trimmed = (result.value || "").trim();
  return trimmed || allowEmpty ? trimmed : null;
}
