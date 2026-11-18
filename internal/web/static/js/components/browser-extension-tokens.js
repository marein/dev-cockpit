import { fire } from "@dc/dialog";
import { copyText } from "@dc/dom";
import { postForm } from "@dc/http";
import { formView, invalid, promptView } from "@dc/prompt-view";
import { errorText, notifyError, notifySuccess } from "@dc/toast";

const PAGE = "/settings/browser";

// The server keeps only a token's hash, so the token stands in this dialog
// alone: a click beside it does not close it, Copy leaves it open, and the
// list behind it is loaded again once it closes.
class BrowserExtensionTokens extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.querySelector("[data-browser-extension-token-new]")?.addEventListener("click", () => void this.make(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async make() {
    if (this.asking) return;
    this.asking = true;
    try {
      const made = await this.askName();
      if (made) await this.show(made);
    } finally {
      this.asking = false;
    }
  }

  async askName() {
    const view = promptView({
      title: "New token",
      text: "One token per browser, named after it.",
      icon: "key",
      placeholder: "Firefox on the laptop",
      confirmText: "Make token",
    });
    const result = await fire({
      ...view,
      inputAttributes: {
        ...view.inputAttributes,
        maxlength: this.getAttribute("name-max"),
        autocomplete: "off",
        autocorrect: "off",
        spellcheck: "false",
        enterkeyhint: "done",
      },
      showLoaderOnConfirm: true,
      allowOutsideClick: () => !window.Swal.isLoading(),
      preConfirm: async (name) => {
        try {
          const response = await postForm(PAGE, { form: "new-token", name });
          if (response.ok) return await response.json();
          window.Swal.showValidationMessage(invalid(await errorText(response, "The token could not be made.")));
        } catch {
          window.Swal.showValidationMessage(invalid("The cockpit could not be reached."));
        }
        return false;
      },
    });
    return result.isConfirmed ? result.value : null;
  }

  async show({ name, token }) {
    const view = formView({
      title: name,
      text: "Paste it into the extension's setup under Token.",
      details: [
        { as: "code", label: "Token", value: token },
        { as: "note", value: "Shown only now, the cockpit keeps only its hash." },
      ],
      icon: "key",
      confirmText: "Done",
    });
    await fire({
      ...view,
      showCancelButton: false,
      showDenyButton: true,
      denyButtonText: "Copy",
      focusDeny: true,
      allowOutsideClick: false,
      customClass: { ...view.customClass, denyButton: "btn" },
      preDeny: async () => {
        if (await copyText(token)) notifySuccess("Copied.");
        else notifyError("The clipboard is not available.");
        return false;
      },
    });
    Promise.resolve(window.app.navigate(`${PAGE}#settings-browser-tokens`)).catch(() => {});
  }
}

customElements.define("dc-browser-extension-tokens", BrowserExtensionTokens);
