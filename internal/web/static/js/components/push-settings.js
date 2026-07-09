import { postForm, postJSON, ensureOk } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

function base64ToBytes(value) {
  const padded = value + "=".repeat((4 - (value.length % 4)) % 4);
  const raw = atob(padded.replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (ch) => ch.charCodeAt(0));
}

function isIPad() {
  return /iPad/.test(navigator.userAgent)
    || (/Macintosh/.test(navigator.userAgent) && navigator.maxTouchPoints > 1);
}

function deviceLabel() {
  const ua = navigator.userAgent;
  const os = /iPhone/.test(ua) ? "iPhone"
    : isIPad() ? "iPad"
    : /Android/.test(ua) ? "Android"
    : /Mac/.test(ua) ? "Mac"
    : /Windows/.test(ua) ? "Windows"
    : /Linux/.test(ua) ? "Linux"
    : "Device";
  const browser = /Edg/.test(ua) ? "Edge"
    : /CriOS|Chrome/.test(ua) ? "Chrome"
    : /FxiOS|Firefox/.test(ua) ? "Firefox"
    : /Safari/.test(ua) ? "Safari"
    : "Browser";
  return os + ", " + browser;
}

class PushSettings extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;

    this.status = this.querySelector("[data-push-status]");
    this.enableBtn = this.querySelector("[data-push-enable]");
    this.disableBtn = this.querySelector("[data-push-disable]");
    this.testBtn = this.querySelector("[data-push-test]");

    this.enableBtn?.addEventListener("click", () => this.enable(), { signal });
    this.disableBtn?.addEventListener("click", () => this.disable(), { signal });
    this.testBtn?.addEventListener("click", () => this.test("webpush", this.testBtn), { signal });
    this.querySelectorAll("[data-webhook-test]").forEach((btn) => {
      btn.classList.remove("d-none");
      btn.addEventListener("click", () => this.test("webhook", btn, { id: btn.getAttribute("data-webhook-test") }), { signal });
    });

    this.reflect();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  supported() {
    return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
  }

  showStatus(text) {
    if (!this.status) return;
    this.status.textContent = text;
    this.status.classList.toggle("d-none", !text);
  }

  async subscription() {
    const registration = await navigator.serviceWorker.getRegistration();
    return registration ? registration.pushManager.getSubscription() : null;
  }

  knownEndpoints() {
    return [...this.querySelectorAll("[data-push-device]")].map((row) => row.getAttribute("data-push-device"));
  }

  async reflect() {
    if (this.knownEndpoints().length > 0) this.testBtn?.classList.remove("d-none");
    if (!this.supported()) {
      const ios = /iPhone|iPod/.test(navigator.userAgent) || isIPad();
      this.showStatus(window.isSecureContext === false
        ? "Web push needs HTTPS. Open the app over https, then enable it here."
        : ios
          ? "Add the app to the home screen first (share menu), then enable push from the installed app."
          : "This browser does not support web push.");
      return;
    }
    if (Notification.permission === "denied") {
      this.showStatus("Notifications are blocked for this app. Allow them in the browser or system settings first.");
      return;
    }
    const sub = await this.subscription().catch(() => null);
    const registered = Boolean(sub) && this.knownEndpoints().includes(sub.endpoint);
    this.enableBtn?.classList.toggle("d-none", registered);
    this.disableBtn?.classList.toggle("d-none", !registered);
    if (registered) {
      const row = this.querySelector(`[data-push-device="${CSS.escape(sub.endpoint)}"]`);
      row?.querySelector("[data-push-this-device]")?.classList.remove("d-none");
    }
  }

  setBusy(button, busy) {
    button.disabled = busy;
    button.classList.toggle("btn-loading", busy);
  }

  async freshSubscription(registration) {
    const options = {
      userVisibleOnly: true,
      applicationServerKey: base64ToBytes(this.getAttribute("vapid-key") || ""),
    };
    try {
      return await registration.pushManager.subscribe(options);
    } catch (error) {
      const stale = await registration.pushManager.getSubscription();
      if (!stale) throw error;
      await stale.unsubscribe();
      return registration.pushManager.subscribe(options);
    }
  }

  async enable() {
    this.setBusy(this.enableBtn, true);
    try {
      if (await Notification.requestPermission() !== "granted") {
        this.showStatus("Notifications were not allowed.");
        this.setBusy(this.enableBtn, false);
        return;
      }
      const registration = await navigator.serviceWorker.register("/sw.js");
      await navigator.serviceWorker.ready;
      const sub = await this.freshSubscription(registration);
      const keys = sub.toJSON().keys || {};
      await ensureOk(await postJSON(this.getAttribute("subscribe-url"), {
        endpoint: sub.endpoint,
        p256dh: keys.p256dh,
        auth: keys.auth,
        label: deviceLabel(),
      }), "Could not register the device.");
      window.app.navigate(this.getAttribute("done-url"));
    } catch (error) {
      notifyError(error?.message || "Could not enable push on this device.");
      this.setBusy(this.enableBtn, false);
    }
  }

  async disable() {
    this.setBusy(this.disableBtn, true);
    try {
      const sub = await this.subscription();
      const endpoint = sub ? sub.endpoint : "";
      if (sub) await sub.unsubscribe();
      if (endpoint) {
        await ensureOk(await postForm(this.getAttribute("unsubscribe-url"), { endpoint }), "Could not remove the device.");
      }
      window.app.navigate(this.getAttribute("done-url"));
    } catch (error) {
      notifyError(error?.message || "Could not disable push on this device.");
      this.setBusy(this.disableBtn, false);
    }
  }

  async test(channel, button, extra = {}) {
    this.setBusy(button, true);
    try {
      await ensureOk(await postForm(this.getAttribute("test-url"), { channel, ...extra }), "Sending failed.");
      notifySuccess(channel === "webhook" ? "Test message sent." : "Test push sent.");
    } catch (error) {
      notifyError(error?.message);
    } finally {
      this.setBusy(button, false);
    }
  }
}

customElements.define("dc-push-settings", PushSettings);
