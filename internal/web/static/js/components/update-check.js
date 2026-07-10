import * as dialog from "@dc/dialog";
import { errorText } from "@dc/toast";
import { csrfHeaders } from "@dc/http";
import { getJSON, setJSON } from "@dc/store";

const INTERVAL = 5 * 60 * 1000;
const PROMPT_INTERVAL = 24 * 60 * 60 * 1000;
const KEY = "dc-update";

// Footer update indicator and apply flow. Polls /update/check on an interval
// (shared across tabs via localStorage + the storage event), reflects the
// result in the footer link and the header "Update available" flags, and drives
// the download/restart from the confirm dialog. The syscall.Exec restart on the
// server is unaffected; this only owns the UI.
class UpdateCheck extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.runningVersion = this.dataset.version || "";
    this.status = null;
    this.timer = null;
    this.ac = new AbortController();

    document.addEventListener("click", (event) => {
      const trigger = event.target.closest("[data-update-open]");
      if (!trigger) return;
      event.preventDefault();
      this.openDialog();
    }, { signal: this.ac.signal });

    window.addEventListener("storage", (event) => {
      if (event.key !== KEY) return;
      const state = this.loadState();
      if (state.next && state.next > Date.now()) this.arm(state.next - Date.now());
      if (this.usable(state.status)) this.render(state.status);
    }, { signal: this.ac.signal });

    const cached = this.loadState().status;
    if (this.usable(cached)) {
      this.render(cached);
    } else {
      this.renderInitial();
    }
    this.scheduleCheck();
  }

  disconnectedCallback() {
    clearTimeout(this.timer);
    this.ac?.abort();
    this.ac = null;
  }

  loadState() {
    return getJSON(KEY, {}) || {};
  }

  saveState(patch) {
    const state = this.loadState();
    Object.assign(state, patch);
    setJSON(KEY, state);
  }

  arm(delayMs) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.doCheck(false), Math.max(0, Math.min(delayMs, INTERVAL)));
  }

  rearm() {
    this.saveState({ next: Date.now() + INTERVAL });
    this.arm(INTERVAL);
  }

  doCheck(force) {
    const promise = this.check(force);
    promise.then(() => this.rearm(), () => this.rearm());
    return promise;
  }

  scheduleCheck() {
    const next = this.loadState().next;
    const now = Date.now();
    if (!next || next <= now || next > now + INTERVAL) {
      this.doCheck(false);
    } else {
      this.arm(next - now);
    }
  }

  check(force) {
    const url = "/update/check" + (force ? "?force=1" : "");
    return fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" })
      .then((response) => (response.ok ? response.json() : null))
      .then((status) => {
        if (status) {
          this.saveState({ status });
          this.render(status);
        }
        return status;
      })
      .catch(() => null);
  }

  usable(status) {
    return status && status.supported && status.current === this.runningVersion;
  }

  render(status) {
    this.status = status;
    if (!status.supported) return;
    this.renderFooter(status);
    this.renderFlags(status);
    this.maybePrompt(status);
  }

  maybePrompt(status) {
    if (!status.available) return;
    if (dialog.isVisible()) return;
    const state = this.loadState();
    if (state.promptedVersion === status.latest && Date.now() - (state.prompted || 0) < PROMPT_INTERVAL) return;
    this.saveState({ prompted: Date.now(), promptedVersion: status.latest });
    this.confirmUpdate(status);
  }

  footerLink(className, text) {
    const link = document.createElement("a");
    link.href = "#";
    link.dataset.updateOpen = "";
    link.className = className + " text-decoration-none";
    link.textContent = text;
    this.replaceChildren(document.createTextNode("("), link, document.createTextNode(")"));
  }

  renderInitial() {
    this.footerLink("link-secondary", "Up to date");
  }

  renderFooter(status) {
    this.footerLink(
      status.available ? "link-primary" : "link-secondary",
      status.available ? "Update to " + status.latest : "Up to date",
    );
  }

  renderFlags(status) {
    document.querySelectorAll(".js-update-flag").forEach((node) => {
      node.classList.toggle("d-none", !status.available);
      const title = node.querySelector(".nav-link-title");
      if (title) title.textContent = "Update to " + status.latest;
    });
    document.querySelectorAll(".js-update-spacer").forEach((node) => {
      node.classList.toggle("d-none", status.available);
    });
  }

  openDialog() {
    dialog.loading({ title: "Checking for updates…" });
    this.doCheck(true).then((status) => {
      const current = status || this.status;
      if (!current || !current.supported) {
        dialog.fire({ icon: "error", title: "Could not check for updates" });
      } else if (current.available) {
        this.confirmUpdate(current);
      } else {
        dialog.fire({ icon: "success", title: "Up to date", text: "Running version " + current.current + "." });
      }
    });
  }

  changelog(releases) {
    const wrap = document.createElement("div");
    wrap.style.textAlign = "left";
    wrap.style.maxHeight = "50vh";
    wrap.style.overflowY = "auto";
    releases.forEach((rel) => {
      const head = document.createElement("div");
      head.className = "fw-bold mt-3";
      let title = rel.name && rel.name !== rel.version ? rel.version + " — " + rel.name : "Version " + rel.version;
      if (rel.date) title += "  (" + rel.date.slice(0, 10) + ")";
      head.textContent = title;

      const body = document.createElement("div");
      body.className = "small dc-md";
      if (rel.notesHtml) {
        body.innerHTML = rel.notesHtml;
        body.querySelectorAll("a[href]").forEach((anchor) => {
          anchor.target = "_blank";
          anchor.rel = "noopener noreferrer";
        });
      } else {
        body.textContent = (rel.notes || "").trim() || "No release notes.";
      }

      wrap.append(head, body);
    });
    return wrap;
  }

  confirmUpdate(status) {
    const options = {
      title: "Update to " + status.latest + "?",
      html: this.changelog(status.releases || []),
      showCancelButton: true,
      confirmButtonText: "Update & restart",
      cancelButtonText: "Cancel",
      reverseButtons: true,
      width: "44rem",
    };
    if (!status.writable) {
      options.footer = "⚠ The binary location is not writable here, the update will fail.";
    }
    dialog.fire(options).then((result) => {
      if (result.isConfirmed) this.apply(status);
    });
  }

  apply(status) {
    dialog.loading({ title: "Updating…", text: "Downloading and restarting." });
    fetch("/update/apply", {
      method: "POST",
      headers: csrfHeaders({ Accept: "application/json", "Content-Type": "application/json" }),
      body: JSON.stringify({ version: status.latest }),
    })
      .then(async (response) => {
        if (response.status === 409) {
          await this.applyRejected(response);
          return;
        }
        if (!response.ok) throw new Error(await errorText(response, "Update failed."));
        this.waitForRestart(status.latest);
      })
      .catch((error) => {
        dialog.fire({ icon: "error", title: "Update failed", text: error.message });
      });
  }

  async applyRejected(response) {
    const data = await response.json().catch(() => null);
    const fresh = data && data.status;
    if (!fresh || !fresh.supported) {
      dialog.fire({ icon: "error", title: "Update failed", text: (data && data.error) || "Update failed." });
      return;
    }
    this.saveState({ status: fresh });
    this.renderFooter(fresh);
    this.renderFlags(fresh);
    if (fresh.available) {
      this.confirmUpdate(fresh);
    } else {
      dialog.fire({ icon: "success", title: "Up to date", text: "Running version " + fresh.current + "." });
    }
  }

  waitForRestart(target) {
    const deadline = Date.now() + 60000;
    const poll = () => {
      fetch("/update/check", { headers: { Accept: "application/json" }, cache: "no-store" })
        .then((response) => (response.ok ? response.json() : null))
        .then((status) => {
          if (status && status.current === target) {
            dialog
              .fire({ icon: "success", title: "Updated to " + target, timer: 2000, showConfirmButton: false })
              .then(() => location.reload());
            return;
          }
          retry();
        })
        .catch(retry);
    };
    const retry = () => {
      if (Date.now() > deadline) {
        dialog.fire({ icon: "warning", title: "Restart is taking a while", text: "Reload the page in a moment." });
        return;
      }
      setTimeout(poll, 1500);
    };
    setTimeout(poll, 2000);
  }
}

customElements.define("dc-update-check", UpdateCheck);
