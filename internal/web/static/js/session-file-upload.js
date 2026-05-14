(() => {
  class SessionFileUpload extends HTMLElement {
    connectedCallback() {
      this.button = this.querySelector("[data-session-files-toggle]");
      const modalSelector = this.button?.getAttribute("data-bs-target");
      this.modal = modalSelector ? document.querySelector(modalSelector) : null;
      this.form = this.modal?.querySelector("[data-session-file-upload-form]");
      this.input = this.modal?.querySelector('input[type="file"][name="files"]');
      this.submitButton = this.modal?.querySelector('button[type="submit"]');
      this.progress = this.modal?.querySelector("[data-session-file-upload-progress]");
      this.content = this.modal?.querySelector("[data-session-files-content]");
      this.activeUploads = 0;
      this.uploadRanWhileClosed = false;
      if (!this.button || !this.modal || !this.form || !this.input || !this.progress || !this.content) {
        return;
      }

      this.button.addEventListener("click", () => this.resetButton());
      this.modal.addEventListener("show.bs.modal", () => this.resetButton());
      this.modal.addEventListener("hidden.bs.modal", () => {
        this.clearStatusMessages();
        this.updateButtonState();
      });
      this.form.addEventListener("submit", (event) => this.submitFiles(event));
      this.modal.addEventListener("submit", (event) => this.submitDelete(event));
      this.modal.addEventListener("click", (event) => this.copyFilePath(event));
    }

    submitFiles(event) {
      event.preventDefault();

      const files = Array.from(this.input.files || []);
      if (files.length === 0) {
        this.progress.hidden = true;
        this.request("POST", this.form.action, new FormData(this.form)).catch(() => {});
        return;
      }

      this.activeUploads = files.length;
      this.uploadRanWhileClosed = false;
      this.resetButton();
      this.input.disabled = true;
      if (this.submitButton) {
        this.submitButton.disabled = true;
      }
      this.progress.hidden = false;
      this.progress.replaceChildren(...files.map((file, index) => this.progressItem(file, index)));

      Promise.allSettled(files.map((file, index) => this.uploadFile(file, index))).then((results) => {
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
            this.button.dataset.uploadState = "done";
          }
        } else {
          if (shouldNotify) {
            this.button.dataset.uploadState = "error";
          }
        }
        this.updateButtonState();
      });
    }

    submitDelete(event) {
      const form = event.target;
      if (!(form instanceof HTMLFormElement) || !form.matches("[data-session-file-delete]")) {
        return;
      }
      event.preventDefault();
      const message = form.getAttribute("data-session-confirm");
      const sendDelete = () => this.request("POST", form.action, new FormData(form)).catch(() => {});
      if (!message) {
        sendDelete();
        return;
      }
      Swal.fire({
        title: message,
        icon: "warning",
        showCancelButton: true,
        confirmButtonText: "Delete",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        background: "#1f2937",
        color: "#f8fafc",
      }).then((result) => {
        if (result.isConfirmed) {
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
      const original = button.innerHTML;
      navigator.clipboard.writeText(path).then(() => {
        button.innerHTML = '<i class="ti ti-check me-1"></i>Copied';
        window.setTimeout(() => {
          button.innerHTML = original;
        }, 1200);
      });
    }

    uploadFile(file, index) {
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
      }).then(() => {
        bar.style.width = "100%";
        progressOuter.setAttribute("aria-valuenow", "100");
        status.textContent = "Done";
        item.dataset.done = "true";
      }).catch((error) => {
        status.textContent = error.message || "Upload failed";
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
          if (xhr.responseText) {
            this.replaceContent(xhr.responseText);
          }
          if (xhr.status >= 200 && xhr.status < 300) {
            resolve(xhr.responseText);
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
    }

    clearStatusMessages() {
      this.content.querySelectorAll(".alert").forEach((message) => message.remove());
    }

    resetButton() {
      delete this.button.dataset.uploadState;
      this.uploadRanWhileClosed = false;
    }

    updateButtonState() {
      if (this.isModalOpen()) {
        return;
      }
      if (this.activeUploads > 0) {
        this.button.dataset.uploadState = "running";
        this.uploadRanWhileClosed = true;
      }
    }

    isModalOpen() {
      return this.modal.classList.contains("show");
    }

    progressItem(file, index) {
      const item = document.createElement("div");
      item.className = "session-file-upload-item";
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

  customElements.define("session-file-upload", SessionFileUpload);
})();
