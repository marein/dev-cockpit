import { postJSON, ensureOk } from "@dc/http";
import { assertionPayload, ceremonyError, requestOptions, supported } from "@dc/webauthn";

class PasskeyLogin extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.button = this.querySelector("[data-passkey-start]");
    this.status = this.querySelector("[data-passkey-status]");
    if (!supported()) {
      this.show("This browser cannot use passkeys. Choose another method below.");
      if (this.button) this.button.disabled = true;
      return;
    }
    this.button?.addEventListener("click", () => this.run(), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  show(text) {
    if (!this.status) return;
    this.status.textContent = text;
    this.status.classList.toggle("d-none", !text);
  }

  busy(state) {
    if (!this.button) return;
    this.button.disabled = state;
    this.button.classList.toggle("btn-loading", state);
  }

  verifyURL() {
    const url = this.getAttribute("verify-url");
    const next = this.getAttribute("next");
    return next && next !== "/" ? `${url}?next=${encodeURIComponent(next)}` : url;
  }

  async run() {
    this.busy(true);
    this.show("");
    try {
      const options = await ensureOk(
        await postJSON(this.getAttribute("options-url"), {}),
        "The passkey request could not be prepared.",
      ).then((response) => response.json());
      const credential = await navigator.credentials.get({ publicKey: requestOptions(options.publicKey) });
      const answer = await ensureOk(
        await postJSON(this.verifyURL(), assertionPayload(credential)),
        "The passkey was not accepted.",
      ).then((response) => response.json());
      window.location.assign(answer.location || "/");
    } catch (error) {
      this.show(ceremonyError(error));
      this.busy(false);
    }
  }
}

customElements.define("dc-passkey-login", PasskeyLogin);
