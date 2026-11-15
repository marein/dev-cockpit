import * as dialog from "@dc/dialog";
import { escapeHtml } from "@dc/dom";
import { formView } from "@dc/prompt-view";
import { errorText } from "@dc/toast";
import { csrfHeaders } from "@dc/http";
import { getJSON, setJSON } from "@dc/store";

const INTERVAL = 5 * 60 * 1000;
const PROMPT_INTERVAL = 24 * 60 * 60 * 1000;
const KEY = "dc-update";

// Footer update indicator and apply flow. Polls /update/check on an interval
// (shared across tabs via localStorage + the storage event), reflects the
// result in the footer link, the rail's update button and the Cockpit sheet's
// update action, and drives
// the download/restart from the confirm dialog. The syscall.Exec restart on the
// server is unaffected; this only owns the UI.
// paintTile words the Cockpit sheet's update tile after a check. A build that
// cannot update itself never reaches here, its tile keeps the Up to date it
// renders with.
function paintTile(tile, status) {
  const label = tile.querySelector("[data-cockpit-update-label]");
  const icon = tile.querySelector("[data-cockpit-update-icon]");
  tile.classList.toggle("text-primary", Boolean(status.available));
  if (label) label.textContent = status.available ? `Update to ${status.latest}` : "Up to date";
  if (icon) icon.className = `ti ${status.available ? "ti-arrow-up-circle" : "ti-circle-check"}`;
}

function changelog(releases) {
  return releases.map((rel) => {
    const named = rel.name && rel.name.replace(/^v/, "") !== rel.version ? ` · ${escapeHtml(rel.name)}` : "";
    const date = rel.date ? ` <span class="text-secondary">· ${escapeHtml(rel.date.slice(0, 10))}</span>` : "";
    const notes = rel.notesHtml || `<p class="dc-prompt-text">${escapeHtml((rel.notes || "").trim() || "No release notes.")}</p>`;
    return `<p><strong>${escapeHtml(rel.version)}</strong>${named}${date}</p>${notes}`;
  }).join("<hr>");
}

function upToDate(version) {
  return dialog.notice({ kind: "success", title: "Up to date", text: `Running version ${version}.` });
}

function failed(text) {
  return dialog.notice({ kind: "error", title: "Update failed", text });
}

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

    // A fragment put in later (the phone's Cockpit sheet) carries its update
    // action in the default state, so it takes the known status on arrival.
    document.addEventListener("dc:rendered", () => {
      if (this.status && this.status.supported) this.renderFlags(this.status);
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
      node.title = "Update to " + status.latest;
    });
    // The surfaces word it themselves, the header button as the bare version and
    // the menu row as a sentence; both take the number here.
    document.querySelectorAll(".js-update-version").forEach((node) => {
      node.textContent = status.latest;
    });
    document.querySelectorAll("[data-cockpit-update]").forEach((tile) => paintTile(tile, status));
  }

  openDialog() {
    dialog.loading({ title: "Checking for updates…", icon: "refresh" });
    this.doCheck(true).then((status) => {
      const current = status || this.status;
      if (!current || !current.supported) {
        dialog.notice({ kind: "error", title: "Could not check for updates" });
      } else if (current.available) {
        this.confirmUpdate(current);
      } else {
        upToDate(current.current);
      }
    });
  }

  confirmUpdate(status) {
    const releases = status.releases || [];
    const view = formView({
      title: `Update to ${status.latest}?`,
      details: [
        { label: "Running", value: status.current, as: "mono" },
        ...(releases.length ? [{ as: "long", label: "Release notes", html: changelog(releases) }] : []),
        ...(status.writable ? [] : [{ as: "warning", value: "The binary location is not writable here, the update will fail." }]),
      ],
      icon: "arrow-up-circle",
      confirmText: "Update & restart",
    });
    dialog.fire({ ...view, width: "42rem" }).then((result) => {
      if (result.isConfirmed) this.apply(status);
    });
  }

  apply(status) {
    dialog.loading({ title: "Updating…", text: "Downloading and restarting.", icon: "arrow-up-circle" });
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
        await response.json().catch(() => ({}));
        this.waitForRestart(status.latest);
      })
      .catch((error) => failed(error.message));
  }

  async applyRejected(response) {
    const data = await response.json().catch(() => null);
    const fresh = data && data.status;
    if (!fresh || !fresh.supported) {
      failed((data && data.error) || "Update failed.");
      return;
    }
    this.saveState({ status: fresh });
    this.renderFooter(fresh);
    this.renderFlags(fresh);
    if (fresh.available) {
      this.confirmUpdate(fresh);
    } else {
      upToDate(fresh.current);
    }
  }

  // The server restarts straight away: nothing it runs has to be waited for.
  waitForRestart(target) {
    const deadline = Date.now() + 60000;
    const poll = () => {
      fetch("/update/check", { headers: { Accept: "application/json" }, cache: "no-store" })
        .then((response) => (response.ok ? response.json() : null))
        .then((status) => {
          if (status && status.current === target) {
            dialog.notice({ kind: "success", title: `Updated to ${target}`, timer: 2000 }).then(() => location.reload());
            return;
          }
          retry();
        })
        .catch(retry);
    };
    const retry = () => {
      if (Date.now() > deadline) {
        dialog.notice({ title: "Restart is taking a while", text: "Reload the page in a moment." });
        return;
      }
      setTimeout(poll, 1500);
    };
    setTimeout(poll, 2000);
  }
}

customElements.define("dc-update-check", UpdateCheck);
