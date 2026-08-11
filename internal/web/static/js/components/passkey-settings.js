import { postJSON, ensureOk } from "@dc/http";
import { promptText } from "@dc/dialog";
import { notifyError } from "@dc/toast";
import { attestationPayload, ceremonyError, creationOptions, deviceLabel, supported } from "@dc/webauthn";

class PasskeySettings extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.button = this.querySelector("[data-passkey-add]");
    this.status = this.querySelector("[data-passkey-status]");
    if (!this.button) return;
    if (!supported()) {
      this.show("This browser cannot register passkeys.");
      this.button.disabled = true;
      return;
    }
    this.button.addEventListener("click", () => this.run(), { signal });
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
    this.button.disabled = state;
    this.button.classList.toggle("btn-loading", state);
  }

  async run() {
    const label = await promptText({
      title: "Name this passkey",
      placeholder: "iPhone",
      value: deviceLabel(),
      confirmText: "Continue",
      validatorMessage: "Please name the device this passkey lives on.",
    });
    if (!label) return;
    this.busy(true);
    this.show("");
    try {
      const options = await ensureOk(
        await postJSON(this.getAttribute("options-url"), {}),
        "The registration could not be prepared.",
      ).then((response) => response.json());
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options.publicKey) });
      const answer = await ensureOk(
        await postJSON(`${this.getAttribute("register-url")}?label=${encodeURIComponent(label)}`, attestationPayload(credential)),
        "The passkey was not accepted.",
      ).then((response) => response.json());
      window.app.navigate(answer.location || this.getAttribute("done-url"));
    } catch (error) {
      notifyError(ceremonyError(error));
      this.busy(false);
    }
  }
}

customElements.define("dc-passkey-settings", PasskeySettings);
