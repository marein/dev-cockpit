import { confirm } from "@dc/dialog";
import { notifyError } from "@dc/toast";
import { watchFileDrop } from "@dc/filedrop";
import { flash } from "@dc/dom";

const finePointer = window.matchMedia("(pointer: fine)");

class CoderFileUpload extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.button = this.querySelector("[data-coder-files-toggle]");
    const modalSelector = this.button?.getAttribute("data-bs-target");
    this.modal = modalSelector ? document.querySelector(modalSelector) : null;
    // The desktop layout renders its own Files button outside this element;
    // both open the same modal, so drive the upload indicator on every button
    // that targets it, not just the toggle inside the component.
    this.buttons = modalSelector
      ? Array.from(document.querySelectorAll(`.coder-files-button[data-bs-target="${modalSelector}"]`))
      : [];
    this.form = this.modal?.querySelector("[data-coder-file-upload-form]");
    this.input = this.modal?.querySelector('input[type="file"][name="files"]');
    this.submitButton = this.modal?.querySelector('button[type="submit"]');
    this.progress = this.modal?.querySelector("[data-coder-file-upload-progress]");
    this.content = this.modal?.querySelector("[data-coder-files-content]");
    this.modalContent = this.modal?.querySelector(".modal-content");
    this.batchBar = this.modal?.querySelector("[data-coder-files-batch]");
    this.batch = null;
    this.inserted = false;
    this.opening = false;
    this.checks = new WeakMap();
    this.activeUploads = 0;
    this.uploadRanWhileClosed = false;
    this.refreshController = null;
    this.refreshSpinner = document.createElement("div");
    this.refreshSpinner.className = "coder-files-refresh";
    this.refreshSpinner.innerHTML =
      '<span class="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>';
    if (!this.button || !this.modal || !this.form || !this.input || !this.progress || !this.content) {
      return;
    }

    const signal = this.ac.signal;
    for (const button of this.buttons) {
      button.addEventListener("click", () => this.resetButton(), { signal });
    }
    this.modal.addEventListener("show.bs.modal", () => {
      this.opening = true;
      this.resetButton();
      this.refreshFiles();
    }, { signal });
    this.modal.addEventListener("shown.bs.modal", () => {
      this.opening = false;
    }, { signal });
    this.modal.addEventListener("hide.bs.modal", () => this.returnFocus(), { signal });
    this.modal.addEventListener("hidden.bs.modal", () => {
      this.clearStatusMessages();
      this.updateButtonState();
      this.inserted = false;
      if (this.activeUploads === 0) {
        this.progress.hidden = true;
        this.batch = null;
        this.markBatch();
      }
    }, { signal });
    this.form.addEventListener("submit", (event) => this.submitFiles(event), { signal });
    this.input.addEventListener("change", () => this.onPicked(), { signal });
    this.modal.addEventListener("submit", (event) => this.submitDelete(event), { signal });
    this.modal.addEventListener("click", (event) => this.copyFilePath(event), { signal });
    this.modal.addEventListener("click", (event) => this.insertFilePath(event), { signal });
    this.batchBar?.querySelector("[data-insert-all-files]")?.addEventListener("click", () => this.insertBatch(), { signal });
    this.modal.addEventListener("paste", (event) => this.onPaste(event), { signal });
    watchFileDrop(this.modal, (files) => this.uploadFiles(files), { signal });

    this.setupTerminalDropZone();
  }

  disconnectedCallback() {
    this.cancelRefresh();
    this.ac?.abort();
    this.ac = null;
  }

  setupTerminalDropZone() {
    const footer = this.closest("[data-terminal-footer]");
    this.terminal = footer
      ? document.querySelector(`terminal-attach[terminal-id="${CSS.escape(footer.getAttribute("data-terminal-footer"))}"]`)
      : document.getElementById("terminal");
    if (!this.terminal) {
      return;
    }
    const signal = this.ac.signal;
    watchFileDrop(this.terminal, (files) => this.uploadFiles(files), { signal });
    this.terminal.addEventListener("paste", (event) => this.onPaste(event), { signal, capture: true });
  }

  onPaste(event) {
    const files = Array.from(event.clipboardData?.files || []);
    if (files.length === 0) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    this.uploadFiles(files);
  }

  onPicked() {
    if ((this.input.files || []).length === 0) {
      return;
    }
    this.submitFiles({ preventDefault() {} });
  }

  uploadFiles(files) {
    this.assignFiles(files);
    this.openModal();
    this.submitFiles({ preventDefault() {} }, 700);
  }

  assignFiles(files) {
    const data = new DataTransfer();
    for (const file of files) {
      data.items.add(file);
    }
    this.input.files = data.files;
  }

  openModal() {
    if (window.bootstrap) {
      window.bootstrap.Modal.getOrCreateInstance(this.modal).show();
    }
  }

  submitFiles(event, startDelayMs = 0) {
    event.preventDefault();

    const files = Array.from(this.input.files || []);
    if (files.length === 0) {
      this.progress.hidden = true;
      this.input.click();
      return;
    }

    const batch = { names: [] };
    this.batch = batch;
    this.markBatch();
    this.activeUploads = files.length;
    this.uploadRanWhileClosed = false;
    this.cancelRefresh();
    this.resetButton();
    this.input.disabled = true;
    if (this.submitButton) {
      this.submitButton.disabled = true;
    }
    this.progress.hidden = false;
    this.progress.replaceChildren(...files.map((file, index) => this.progressItem(file, index)));

    // Render the queued progress bars first, then start the transfer after a
    // short beat, so a drag-and-drop upload that finishes instantly still shows
    // the bar instead of flashing past while the modal is still opening.
    const startUploads = () =>
      Promise.allSettled(files.map((file, index) => this.uploadFile(file, index, batch))).then((results) => {
      this.activeUploads = 0;
      this.input.disabled = false;
      this.input.value = "";
      if (this.submitButton) {
        this.submitButton.disabled = false;
      }
      const allSucceeded = results.every((result) => result.status === "fulfilled");
      const shouldNotify = this.uploadRanWhileClosed && !this.isModalOpen();
      if (allSucceeded) {
        this.progress.hidden = true;
        if (shouldNotify) {
          this.setUploadState("done");
        }
      } else {
        if (shouldNotify) {
          this.setUploadState("error");
        }
      }
      this.updateButtonState();
      const listed = allSucceeded && this.isModalOpen() ? this.refreshFiles() : Promise.resolve();
      listed.then(() => {
        if (this.batch === batch) {
          batch.done = true;
          this.markBatch();
          this.pointAtBatch();
        }
      });
    });

    if (startDelayMs > 0) {
      window.setTimeout(startUploads, startDelayMs);
    } else {
      startUploads();
    }
  }

  submitDelete(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("[data-coder-file-delete]")) {
      return;
    }
    event.preventDefault();
    const message = form.getAttribute("data-terminal-confirm");
    const sendDelete = () => {
      this.cancelRefresh();
      return this.request("POST", form.action, new FormData(form)).catch(() => {});
    };
    if (!message) {
      sendDelete();
      return;
    }
    // Render inside the open Bootstrap modal so its focus trap treats the
    // dialog as in scope; otherwise it pulls focus back to the modal and Enter
    // hits the modal close button instead of confirming the delete.
    confirm({ title: message, confirmText: "Delete", target: this.modal, heightAuto: false }).then((ok) => {
      if (ok) {
        sendDelete();
      }
    });
  }

  copyFilePath(event) {
    const button = event.target.closest("[data-copy-file-path]");
    if (!(button instanceof HTMLButtonElement)) {
      return;
    }
    const path = button.getAttribute("data-copy-file-path") || "";
    if (!path || !navigator.clipboard) {
      return;
    }
    navigator.clipboard.writeText(path).then(() => this.showCheck(button));
  }

  showCheck(button) {
    const icon = button.querySelector("i.ti");
    if (!icon) {
      return;
    }
    if (!this.checks.has(button)) {
      this.checks.set(button, { icon: icon.className });
    }
    const check = this.checks.get(button);
    window.clearTimeout(check.timer);
    icon.className = "ti ti-check";
    check.timer = window.setTimeout(() => {
      icon.className = check.icon;
      this.checks.delete(button);
    }, 1200);
  }

  insertFilePath(event) {
    const button = event.target.closest("[data-insert-file-path]");
    const word = button?.getAttribute("data-insert-file-path") || "";
    if (!word || !this.isModalOpen() || !this.insert([word])) {
      return;
    }
    this.showCheck(button);
  }

  insertBatch() {
    const words = this.batchRows().map((row) => row.querySelector("[data-insert-file-path]")?.getAttribute("data-insert-file-path") || "").filter(Boolean);
    if (words.length === 0 || !this.isModalOpen() || !this.insert(words)) {
      return;
    }
    window.bootstrap?.Modal.getOrCreateInstance(this.modal).hide();
  }

  insert(words) {
    if (!this.terminal) {
      return false;
    }
    this.terminal.dispatchEvent(new CustomEvent("terminal-input", { bubbles: true, detail: { paste: ` ${words.join(" ")} ` } }));
    this.inserted = true;
    return true;
  }

  // Bootstrap's focus trap holds the focus until the hide has run, so the
  // terminal takes it right after, still inside the tap that closed the dialog
  // (a phone opens its keyboard only then), and again once the dialog is gone,
  // since Bootstrap hands it back to the button that opened it. On a phone the
  // keyboard only comes up when a path went in.
  returnFocus() {
    if (!this.terminal || !(finePointer.matches || this.inserted)) {
      return;
    }
    queueMicrotask(() => this.focusTerminal());
    this.modal.addEventListener("hidden.bs.modal", () => this.focusTerminal(), { once: true, signal: this.ac.signal });
  }

  batchRows() {
    const names = this.batch?.names.filter(Boolean) || [];
    const rows = new Map(Array.from(this.content.querySelectorAll("[data-coder-file]"), (row) => [row.getAttribute("data-coder-file"), row]));
    return names.map((name) => rows.get(name)).filter(Boolean);
  }

  markBatch() {
    const batch = this.batchRows();
    const rows = new Set(batch);
    for (const row of this.content.querySelectorAll("[data-coder-file]")) {
      row.toggleAttribute("data-coder-file-new", rows.has(row));
    }
    batch[0]?.parentElement.prepend(...batch);
    if (!this.batchBar) {
      return;
    }
    const count = this.batch?.done ? rows.size : 0;
    this.batchBar.hidden = count < 2;
    this.batchBar.querySelector("[data-coder-files-batch-label]").textContent = `Insert all ${count}`;
  }

  pointAtBatch() {
    const rows = this.batchRows();
    if (rows.length === 0 || !this.isModalOpen()) {
      return;
    }
    rows.forEach((row) => flash(row, this.ac.signal));
    const target = rows.length === 1 ? rows[0].querySelector("[data-insert-file-path]") : this.batchBar.querySelector("[data-insert-all-files]");
    (rows.length === 1 ? rows[0] : this.batchBar).scrollIntoView({ block: "nearest" });
    if (!finePointer.matches || !target) {
      return;
    }
    // Bootstrap focuses the dialog itself once it has faded in.
    if (this.opening) {
      this.modal.addEventListener("shown.bs.modal", () => target.focus({ preventScroll: true }), { once: true, signal: this.ac.signal });
    } else {
      target.focus({ preventScroll: true });
    }
  }

  focusTerminal() {
    const id = this.terminal.getAttribute("terminal-id");
    if (id) {
      document.dispatchEvent(new CustomEvent("dc:activate-pane", { detail: { id } }));
    }
    this.terminal.querySelector("#terminal-cursor-input")?.focus();
  }

  uploadFile(file, index, batch) {
    const item = this.progress.querySelector(`[data-file-index="${index}"]`);
    const bar = item.querySelector(".progress-bar");
    const progressOuter = item.querySelector(".progress");
    const status = item.querySelector("[data-file-status]");
    status.textContent = "Uploading";

    const data = new FormData();
    new FormData(this.form).forEach((value, key) => {
      if (key !== "files") {
        data.append(key, value);
      }
    });
    data.append("files", file, file.name);

    return this.request("POST", this.form.action, data, (progressEvent) => {
      if (!progressEvent.lengthComputable) {
        return;
      }
      const percent = Math.round((progressEvent.loaded / progressEvent.total) * 100);
      bar.style.width = `${percent}%`;
      progressOuter.setAttribute("aria-valuenow", String(percent));
      status.textContent = `${percent}%`;
    }).then((xhr) => {
      bar.style.width = "100%";
      progressOuter.setAttribute("aria-valuenow", "100");
      status.textContent = "Done";
      item.dataset.done = "true";
      batch.names[index] = decodeURIComponent((xhr.getResponseHeader("X-Coder-Files-Saved") || "").split("/")[0]);
      this.markBatch();
    }).catch((error) => {
      // Keep the per-file status short so it never overflows its cell; the
      // full reason is surfaced as a toast (see request()).
      status.textContent = "Failed";
      item.dataset.error = "true";
      throw error;
    }).finally(() => {
      this.activeUploads = Math.max(0, this.activeUploads - 1);
      this.updateButtonState();
    });
  }

  request(method, url, body, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open(method, url);
      xhr.setRequestHeader("Accept", "text/html");
      xhr.setRequestHeader("X-Requested-With", "XMLHttpRequest");
      if (onProgress) {
        xhr.upload.addEventListener("progress", onProgress);
      }
      xhr.addEventListener("load", () => {
        // Middleware errors (CSRF, body limit, panic) answer XHRs with a JSON
        // {error}; surface that as a toast instead of injecting a full page
        // into the file list. The handler's own errors return the list
        // fragment, which we render inline as before.
        const type = xhr.getResponseHeader("Content-Type") || "";
        if (type.includes("application/json")) {
          let message = "";
          try {
            message = (JSON.parse(xhr.responseText) || {}).error || "";
          } catch (_) {
            message = "";
          }
          message = message || xhr.statusText || `HTTP ${xhr.status}`;
          notifyError(message);
          reject(new Error(message));
          return;
        }
        if (xhr.responseText) {
          this.replaceContent(xhr.responseText);
        }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(xhr);
          return;
        }
        reject(new Error(xhr.statusText || `HTTP ${xhr.status}`));
      });
      xhr.addEventListener("error", () => reject(new Error("Request failed")));
      xhr.addEventListener("abort", () => reject(new Error("Request canceled")));
      xhr.send(body);
    });
  }

  replaceContent(html) {
    this.content.innerHTML = html;
    this.markBatch();
  }

  refreshFiles() {
    // Show the already-loaded list immediately (it is in the DOM), then fetch
    // the current list in the background and swap it in, like the quick
    // nav. An in-flight upload owns the content, so skip then.
    if (this.activeUploads > 0) {
      return Promise.resolve();
    }
    this.cancelRefresh();
    const controller = new AbortController();
    this.refreshController = controller;
    if (this.modalContent && this.refreshSpinner.parentElement !== this.modalContent) {
      this.modalContent.appendChild(this.refreshSpinner);
    }
    return fetch(this.form.action, {
      headers: { Accept: "text/html" },
      cache: "no-store",
      signal: controller.signal,
    })
      .then((response) => (response.ok ? response.text() : Promise.reject()))
      .then((html) => {
        if (this.activeUploads > 0) {
          return;
        }
        this.replaceContent(html);
      })
      .catch(() => {})
      .finally(() => {
        if (this.refreshController === controller) {
          this.refreshController = null;
        }
        this.refreshSpinner.remove();
      });
  }

  cancelRefresh() {
    if (this.refreshController) {
      this.refreshController.abort();
      this.refreshController = null;
    }
  }

  clearStatusMessages() {
    this.content.querySelectorAll(".alert").forEach((message) => message.remove());
  }

  setUploadState(state) {
    for (const button of this.buttons) {
      if (state) {
        button.dataset.uploadState = state;
      } else {
        delete button.dataset.uploadState;
      }
    }
  }

  resetButton() {
    this.setUploadState(null);
    this.uploadRanWhileClosed = false;
  }

  updateButtonState() {
    if (this.isModalOpen()) {
      return;
    }
    if (this.activeUploads > 0) {
      this.setUploadState("running");
      this.uploadRanWhileClosed = true;
    }
  }

  isModalOpen() {
    return this.modal.classList.contains("show");
  }

  progressItem(file, index) {
    const item = document.createElement("div");
    item.className = "coder-file-upload-item";
    item.dataset.fileIndex = String(index);

    const header = document.createElement("div");
    header.className = "d-flex justify-content-between gap-2 small mb-1";

    const name = document.createElement("div");
    name.className = "text-truncate";
    name.textContent = file.name;

    const status = document.createElement("div");
    status.className = "text-secondary text-nowrap";
    status.dataset.fileStatus = "";
    status.textContent = "Queued";

    const progressOuter = document.createElement("div");
    progressOuter.className = "progress";
    progressOuter.setAttribute("role", "progressbar");
    progressOuter.setAttribute("aria-valuemin", "0");
    progressOuter.setAttribute("aria-valuemax", "100");
    progressOuter.setAttribute("aria-valuenow", "0");

    const bar = document.createElement("div");
    bar.className = "progress-bar";
    bar.style.width = "0%";

    header.append(name, status);
    progressOuter.append(bar);
    item.append(header, progressOuter);
    return item;
  }
}

customElements.define("coder-file-upload", CoderFileUpload);
