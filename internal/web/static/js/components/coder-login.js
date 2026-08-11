// Browser login for a coder CLI. The server runs the CLI's own login command
// and this element carries what it printed into a dialog: claude shows an
// oauth link and takes the authorization code back, copilot shows the device
// code and finishes on its own. The element polls the flow while its dialog
// stands, like the git prompt it never re-fires the dialog it already shows,
// and the pasted code goes straight to the server and nowhere else.
import { getJSON, postJSON, ensureOk } from "@dc/http";
import { available } from "@dc/dialog";
import { notifyError, notifySuccess } from "@dc/toast";
import { escapeHtml } from "@dc/dom";

const footer = "The CLI stores its own credentials. The cockpit never keeps the code.";

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch (error) {
    void error;
    return "the login page";
  }
}

class CoderLogin extends HTMLElement {
  connectedCallback() {
    if (this.abort) {
      return;
    }
    this.abort = new AbortController();
    this.url = this.getAttribute("login-url") || "";
    this.name = this.getAttribute("coder-name") || "Coder";
    this.shownKey = "";
    this.watching = false;
    this.timer = null;
    for (const button of this.querySelectorAll("[data-login-start]")) {
      button.addEventListener("click", () => void this.start(), { signal: this.abort.signal });
    }
  }

  disconnectedCallback() {
    this.abort?.abort();
    this.abort = null;
    this.stopWatching(true);
  }

  async start() {
    if (!available()) {
      notifyError("The login needs the dialog library, which did not load.");
      return;
    }
    try {
      const response = await postJSON(this.url, { action: "start" });
      await ensureOk(response, "The login could not start.");
      const description = await response.json();
      this.watch();
      this.render(description);
    } catch (error) {
      notifyError(error.message);
    }
  }

  watch() {
    if (this.watching) {
      return;
    }
    this.watching = true;
    this.timer = setInterval(() => void this.refresh(), 1000);
  }

  stopWatching(closeDialog) {
    if (this.timer) {
      clearInterval(this.timer);
    }
    this.timer = null;
    this.watching = false;
    if (closeDialog && this.shownKey && window.Swal) {
      window.Swal.close();
    }
    this.shownKey = "";
  }

  async refresh() {
    try {
      const description = await getJSON(this.url, { signal: this.abort?.signal });
      this.render(description);
    } catch (error) {
      void error;
    }
  }

  render(description) {
    if (!this.watching) {
      return;
    }
    const flow = description.flow;
    if (!flow || flow.state === "cancelled") {
      this.stopWatching(true);
      return;
    }
    if (flow.state === "done") {
      this.stopWatching(true);
      this.applyState(description);
      notifySuccess("Logged in.");
      return;
    }
    if (flow.state === "failed") {
      this.stopWatching(true);
      notifyError(flow.error || "The login failed.");
      return;
    }
    const key = [flow.state, flow.url || "", flow.code || "", flow.note || ""].join("|");
    if (key === this.shownKey) {
      return;
    }
    this.shownKey = key;
    this.show(flow, key);
  }

  show(flow, key) {
    const settle = (result) => {
      if (this.shownKey !== key || !this.watching) {
        return;
      }
      const reason = window.Swal.DismissReason || {};
      if (result.isConfirmed && flow.takesCode && flow.state === "waiting") {
        void this.answer(result.value || "");
        return;
      }
      if (result.dismiss === reason.cancel || result.dismiss === reason.esc) {
        void this.cancel();
      }
    };
    if (flow.state === "starting" || flow.state === "checking") {
      void window.Swal.fire({
        title: flow.state === "checking" ? "Checking the code…" : "Starting the login…",
        html: '<div class="spinner-border text-secondary" role="status"></div>',
        showConfirmButton: false,
        showCancelButton: true,
        cancelButtonText: "Cancel",
        allowOutsideClick: false,
      }).then(settle);
      return;
    }
    if (flow.takesCode) {
      const note = flow.note
        ? `<div class="text-danger mb-2" style="text-align: start">${escapeHtml(flow.note)}</div>`
        : "";
      void window.Swal.fire({
        title: `${this.name} login`,
        html: `<div style="text-align: start">${note}
          <p>Open the login page, sign in, and copy the code it hands you.</p>
          <a class="btn btn-primary w-100" target="_blank" rel="noreferrer" href="${escapeHtml(flow.url)}">Open ${escapeHtml(hostOf(flow.url))}</a>
        </div>`,
        input: "text",
        inputPlaceholder: "Paste the code here",
        inputAttributes: { autocomplete: "off", autocorrect: "off", autocapitalize: "off", spellcheck: "false" },
        inputValidator: (value) => (value && value.trim() ? undefined : "Paste the code from the login page."),
        showCancelButton: true,
        confirmButtonText: "Submit code",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        allowOutsideClick: false,
        footer,
      }).then(settle);
      return;
    }
    void window.Swal.fire({
      title: `${this.name} login`,
      html: `<div style="text-align: start">
        <p>Open the device page and enter this code there.</p>
        <div class="fs-1 fw-bold font-monospace text-center user-select-all my-2">${escapeHtml(flow.code)}</div>
        <a class="btn btn-primary w-100 mb-3" target="_blank" rel="noreferrer" href="${escapeHtml(flow.url)}">Open ${escapeHtml(hostOf(flow.url))}</a>
        <div class="text-secondary d-flex align-items-center gap-2"><span class="spinner-border spinner-border-sm" role="status"></span>Waiting for the authorization, this closes on its own.</div>
      </div>`,
      showConfirmButton: false,
      showCancelButton: true,
      cancelButtonText: "Cancel",
      allowOutsideClick: false,
      footer,
    }).then(settle);
  }

  async answer(code) {
    try {
      const response = await postJSON(this.url, { action: "answer", code: code.trim() });
      await ensureOk(response, "The code could not be sent.");
      this.render(await response.json());
    } catch (error) {
      notifyError(error.message);
      this.shownKey = "";
      void this.refresh();
    }
  }

  async cancel() {
    this.stopWatching(false);
    try {
      const response = await postJSON(this.url, { action: "cancel" });
      await ensureOk(response, "The login could not be cancelled.");
    } catch (error) {
      notifyError(error.message);
    }
  }

  applyState(description) {
    for (const block of this.querySelectorAll("[data-login-in]")) {
      block.hidden = !description.loggedIn;
    }
    for (const block of this.querySelectorAll("[data-login-out]")) {
      block.hidden = description.loggedIn;
    }
    const account = this.querySelector("[data-login-account]");
    if (account) {
      account.textContent = description.account || "";
    }
    const detail = this.querySelector("[data-login-detail]");
    if (detail) {
      detail.textContent = description.detail || "";
    }
  }
}

customElements.define("dc-coder-login", CoderLogin);
