import { postForm } from "@dc/http";
import { errorText } from "@dc/toast";

// One dialog for the create forms, so starting a coder or a shell never leaves
// the page you are on. It fetches the very page a link points at, with modal=1,
// and the server answers the same form alone; the marker rides the form's
// action back out through the POST, so the path a form posts to is still the
// path that rendered it. The pages stay pages: a deep link, a login with a
// return and a browser without JS all still get them, and so does this dialog
// whenever anything about the fetch is not what it expects.
//
// A Bootstrap modal, deliberately not SweetAlert, like the editor's line
// comment dialog: this is server rendered markup with selects, custom elements
// and a POST target of its own, and the app's pe.js glue already closes open
// modals and drops their backdrop on every boosted navigation.
const PATHS = ["/coders/new", "/shells/new", "/projects/new"];

// A touch keyboard must not jump up over the dialog the moment it opens, so
// only a fine pointer gets the field focused, the same rule the editor's
// filters and palettes use.
const POINTER = window.matchMedia("(hover: hover) and (pointer: fine)");

// opensAsModal reports whether a URL is one of the create forms, which is what
// every JS driven way to one asks before it navigates.
export function opensAsModal(url) {
  if (!url) return false;
  let target;
  try {
    target = new URL(url, window.location.href);
  } catch {
    return false;
  }
  return target.origin === window.location.origin && PATHS.includes(target.pathname);
}

// openFormModal opens the create dialog for a URL and reports whether it took
// it. False means this URL is not a create form, or there is no dialog on this
// page, and the caller navigates the way it always did.
export function openFormModal(url, trigger = null) {
  if (!opensAsModal(url)) return false;
  const host = document.querySelector("dc-form-modal");
  if (!host || !host.openForm) return false;
  host.openForm(url, trigger);
  return true;
}

function withMarker(url) {
  const target = new URL(url, window.location.href);
  target.searchParams.set("modal", "1");
  return target.pathname + target.search;
}

// A dialog over an open menu reads as two surfaces at once, and the menu would
// sit under the backdrop anyway.
function closeMenus() {
  if (!window.bootstrap) return;
  for (const menu of document.querySelectorAll(".dropdown-menu.show")) {
    const toggle = menu.parentElement?.querySelector('[data-bs-toggle="dropdown"]');
    if (toggle) window.bootstrap.Dropdown.getOrCreateInstance(toggle).hide();
  }
}

function navigate(url) {
  if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
  else window.location.href = url;
}

class FormModal extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.modal = this.querySelector("[data-form-modal]");
    this.content = this.querySelector("[data-form-modal-content]");
    this.titleEl = this.querySelector("[data-form-modal-title]");
    if (!this.modal || !this.content) return;
    // Capture, because pe.js boosts the same click on the way up and cancelling
    // its pe:click would hand the link back to the browser as a full load.
    document.addEventListener("click", (e) => this.intercept(e), { capture: true, signal });
    // The form's submit is stopped here, so pe.js never swaps the page under an
    // open dialog; what the server answers decides whether it stays.
    this.modal.addEventListener("submit", (e) => void this.send(e), { signal });
    this.modal.addEventListener("hidden.bs.modal", () => this.clear(), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  intercept(e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const link = e.target instanceof Element ? e.target.closest("a[href]") : null;
    if (!link || link.hasAttribute("target") || link.hasAttribute("download") || link.closest("[data-no-pe]")) return;
    if (!opensAsModal(link.href)) return;
    e.preventDefault();
    e.stopPropagation();
    this.openForm(link.href, link);
  }

  // openForm fetches a create form and shows it. Called again while the dialog
  // stands (the project form's choice select reshapes the form and asks the
  // server for the new shape) it swaps the form in place and the dialog keeps
  // the trigger it was opened from.
  async openForm(url, trigger = null) {
    if (!window.bootstrap || this.loading) return;
    this.loading = true;
    const standing = this.modal.classList.contains("show");
    if (trigger || !standing) this.trigger = trigger;
    closeMenus();
    const done = window.app?.showProgress ? window.app.showProgress() : null;
    let html = "";
    try {
      const response = await fetch(withMarker(url), { headers: { Accept: "text/html" }, cache: "no-store" });
      // Anything but the form itself, a login with a return above all, is the
      // page's answer and not the dialog's, so the browser follows it.
      if (response.redirected || !response.ok) {
        this.leave(response.url || url);
        return;
      }
      html = await response.text();
    } catch {
      navigate(url);
      return;
    } finally {
      this.loading = false;
      done?.();
    }
    this.reset();
    this.content.insertAdjacentHTML("beforeend", html);
    const form = this.content.querySelector("form");
    if (!form) {
      this.leave(url);
      return;
    }
    if (this.titleEl) this.titleEl.textContent = form.dataset.formTitle || "";
    await window.app?.loadElements?.(this.content);
    if (standing) {
      this.focusFirst(form);
      return;
    }
    this.modal.addEventListener("shown.bs.modal", () => this.focusFirst(form), { once: true });
    window.bootstrap.Modal.getOrCreateInstance(this.modal).show();
  }

  // The first field of the form takes the focus, and nothing scrolls doing it.
  focusFirst(form) {
    if (!POINTER.matches) return;
    form.querySelector("input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled])")
      ?.focus({ preventScroll: true });
  }

  // While a create runs the form gives way to a spinner and one line saying
  // what runs (data-form-wait). The form is hidden, not thrown away: a refusal
  // brings it back with everything that was typed.
  wait(form) {
    const view = document.createElement("div");
    view.className = "modal-body text-center py-5";
    view.dataset.formModalWait = "";
    const spinner = document.createElement("div");
    spinner.className = "spinner-border text-primary";
    spinner.setAttribute("role", "status");
    const line = document.createElement("div");
    line.className = "mt-3 text-secondary";
    line.textContent = form.dataset.formWait || "Working…";
    view.append(spinner, line);
    form.hidden = true;
    this.content.append(view);
    // The hidden form takes the focus with it, and Bootstrap listens for Escape
    // on the dialog itself.
    this.modal.focus({ preventScroll: true });
    return () => {
      view.remove();
      form.hidden = false;
    };
  }

  async send(e) {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !this.content.contains(form)) return;
    e.preventDefault();
    e.stopPropagation();
    if (this.busy) return;
    this.busy = true;
    const focused = document.activeElement;
    this.showError("");
    const restore = this.wait(form);
    try {
      const response = await postForm(form.action, new FormData(form));
      if (response.redirected) {
        this.leave(response.url);
        return;
      }
      if (!response.ok) {
        // A refusal keeps the dialog standing over what was typed.
        restore();
        this.showError(await errorText(response, "The form could not be sent."));
        return;
      }
      const data = await response.json().catch(() => null);
      this.leave(data?.location || response.url);
    } catch (error) {
      restore();
      this.showError(error.message || "The form could not be sent.");
    } finally {
      this.busy = false;
      if (!this.leaving && !this.modal.contains(document.activeElement)) {
        const back = this.modal.contains(focused) && focused.isConnected && !focused.hidden ? focused : this.modal;
        back.focus({ preventScroll: true });
      }
    }
  }

  showError(text) {
    let box = this.content.querySelector("[data-form-modal-error]");
    if (!text) {
      box?.remove();
      return;
    }
    if (!box) {
      box = document.createElement("div");
      box.className = "alert alert-danger";
      box.setAttribute("role", "alert");
      box.dataset.formModalError = "";
      (this.content.querySelector(".modal-body") || this.content).prepend(box);
    }
    box.textContent = text;
    box.scrollIntoView({ block: "nearest" });
  }

  // leave closes the dialog and follows the create where the form page would
  // have gone. The focus does not travel back to the trigger here, the page it
  // sat on is being replaced.
  leave(url) {
    this.leaving = true;
    window.bootstrap?.Modal.getInstance(this.modal)?.hide();
    navigate(url);
  }

  // The head stays, everything the server sent goes: a form may arrive wrapped
  // in an element of its own (the project form does, its custom element is the
  // wrapper), so this cannot pick the form out by name.
  reset() {
    for (const node of [...this.content.children]) {
      if (!node.classList.contains("modal-header")) node.remove();
    }
    this.showError("");
  }

  clear() {
    this.reset();
    const trigger = this.trigger;
    this.trigger = null;
    if (!this.leaving && trigger?.isConnected) trigger.focus({ preventScroll: true });
    this.leaving = false;
  }
}

customElements.define("dc-form-modal", FormModal);
