import { postForm, ensureOk, getText, getJSON, csrfHeaders } from "@dc/http";
import { notifyError } from "@dc/toast";
import { onServerEvent } from "@dc/events";
import { jumpTextEdge } from "@dc/dom";

const COARSE = window.matchMedia?.("(pointer: coarse)").matches ?? false;

const ATTACH_ICONS = { image: "ti-photo", video: "ti-video", audio: "ti-microphone", file: "ti-paperclip" };

const MEDIA_EXT = {
  image: ["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "svg"],
  video: ["mp4", "webm", "ogv", "mov", "m4v"],
  audio: ["mp3", "m4a", "aac", "wav", "ogg", "oga", "opus", "flac"],
};

function mediaKind(name) {
  const ext = name.split(".").pop().toLowerCase();
  for (const [kind, list] of Object.entries(MEDIA_EXT)) {
    if (list.includes(ext)) return kind;
  }
  return "file";
}

function renderAttachment(file) {
  if (file.media === "image") {
    const link = document.createElement("a");
    link.href = file.url;
    link.target = "_blank";
    link.rel = "noopener";
    link.setAttribute("data-assistant-file", file.name);
    const image = document.createElement("img");
    image.src = file.url;
    image.alt = file.name;
    image.className = "dc-assistant-media";
    link.append(image);
    return link;
  }
  if (file.media === "video" || file.media === "audio") {
    const player = document.createElement(file.media);
    player.src = file.url;
    player.controls = true;
    player.preload = "metadata";
    player.className = file.media === "video" ? "dc-assistant-media" : "dc-assistant-audio";
    if (file.media === "video") player.playsInline = true;
    return player;
  }
  const link = document.createElement("a");
  link.href = `${file.url}?download=1`;
  link.setAttribute("download", "");
  link.setAttribute("data-assistant-file", file.name);
  link.className = "d-inline-flex align-items-center gap-1 text-break";
  link.textContent = file.name;
  return link;
}

// The one conversation surface. It renders only inside the assistant overlay
// and scrolls its own [data-assistant-scroll] region; the page behind the
// overlay never scrolls for it.
class Assistant extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.scroller = this.querySelector("[data-assistant-scroll]");
    this.postUrl = this.getAttribute("post-url");
    this.streamUrl = this.getAttribute("stream-url");
    this.messageUrl = this.getAttribute("message-url");
    this.uploadUrl = this.getAttribute("upload-url");
    this.draftUrl = this.getAttribute("draft-url");
    this.maxBytes = Number(this.getAttribute("max-bytes")) || 32768;
    this.maxFileBytes = Number(this.getAttribute("max-file-bytes")) || 0;
    this.log = this.querySelector("[data-assistant-log]");
    this.form = this.querySelector("[data-assistant-form]");
    this.footer = this.querySelector("[data-assistant-footer]");
    this.input = this.querySelector("[data-assistant-input]");
    this.sendButton = this.querySelector("[data-assistant-send]");
    this.cancelButton = this.querySelector("[data-assistant-cancel]");
    this.counter = this.querySelector("[data-assistant-count]");
    this.empty = this.querySelector("[data-assistant-empty]");
    this.attachButton = this.querySelector("[data-assistant-attach]");
    // The input, not a file of a message: a sent attachment carries the same
    // attribute and sits above the composer, so an unscoped lookup finds its
    // link and the paperclip stops working for the rest of the conversation.
    this.fileInput = this.querySelector("input[data-assistant-file]");
    this.attachTray = this.querySelector("[data-assistant-attachments]");
    this.attached = [];
    if (!this.log || !this.postUrl) return;
    this.restoreDraft();

    this.running = this.hasAttribute("running");
    this.pending = null;
    this.pinned = true;
    this.programmatic = 0;
    this.scrollQueued = false;

    this.form?.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.send();
    }, { signal });
    this.input?.addEventListener("keydown", (event) => this.onKeydown(event), { signal });
    this.input?.addEventListener("input", () => this.onInput(), { signal });
    this.cancelButton?.addEventListener("click", () => void this.cancel(), { signal });
    this.addEventListener("click", (event) => {
      const retry = event.target.closest("[data-assistant-retry]");
      if (retry) {
        event.preventDefault();
        void this.retry();
        return;
      }
      const discard = event.target.closest("[data-assistant-discard]");
      if (discard) {
        event.preventDefault();
        void this.discard(discard.getAttribute("data-assistant-discard"));
      }
    }, { signal });
    document.addEventListener("visibilitychange", () => this.checkStream(), { signal });
    onServerEvent("draft", (event) => this.onDraftEvent(event.detail), { signal });
    this.scroller?.addEventListener("scroll", () => this.trackPin(), { signal, passive: true });

    if (this.uploadUrl) {
      // A drop belongs to the overlay, the page under it may have drop
      // targets of its own (the editor tree does).
      const dropTarget = this;
      this.attachButton?.addEventListener("click", () => this.fileInput?.click(), { signal });
      this.fileInput?.addEventListener("change", () => {
        void this.upload(Array.from(this.fileInput.files || []));
        this.fileInput.value = "";
      }, { signal });
      this.input?.addEventListener("paste", (event) => {
        const files = Array.from(event.clipboardData?.files || []);
        if (!files.length) return;
        event.preventDefault();
        void this.upload(files);
      }, { signal });
      dropTarget.addEventListener("dragover", (event) => {
        if (!event.dataTransfer?.types?.includes("Files")) return;
        event.preventDefault();
        this.toggleAttribute("dropping", true);
      }, { signal });
      dropTarget.addEventListener("dragleave", (event) => {
        if (event.relatedTarget) return;
        this.removeAttribute("dropping");
      }, { signal });
      dropTarget.addEventListener("drop", (event) => {
        const files = Array.from(event.dataTransfer?.files || []);
        this.removeAttribute("dropping");
        if (!files.length) return;
        event.preventDefault();
        void this.upload(files);
      }, { signal });
      this.attachTray?.addEventListener("click", (event) => {
        const remove = event.target.closest("[data-assistant-attachment-remove]");
        if (!remove) return;
        this.attached = this.attached.filter((file) => file.name !== remove.dataset.assistantAttachmentRemove);
        this.renderAttachments();
        this.queueDraft();
      }, { signal });
    }
    // The log grows after this element is done: a picture in the transcript
    // decodes, a web font swaps, the composer wraps to a second line. Each of
    // those moves the bottom, so the pin is re-applied from an observer.
    if (window.ResizeObserver) {
      this.sizer = new ResizeObserver(() => this.stickToEnd());
      if (this.footer) this.sizer.observe(this.footer);
      this.sizer.observe(this.log);
    }

    this.setRunning(this.running);
    this.openStream();
    this.stickToEnd();
    this.onInput();
    this.autoGrow();
    this.setAttribute("ready", "");
  }

  viewportHeight() {
    return this.scroller ? this.scroller.clientHeight : window.innerHeight;
  }

  scrollPosition() {
    return this.scroller ? this.scroller.scrollTop : 0;
  }

  scrollLength() {
    return this.scroller ? this.scroller.scrollHeight : 0;
  }

  scrollToPosition(top) {
    this.scroller?.scrollTo({ top, behavior: "instant" });
  }

  // focusMessage brings one message to the top of the transcript, the way a
  // notification's link lands on the answer it announced, and releases the
  // end pin so the settling page does not drag the reader away from it.
  focusMessage(messageId) {
    const node = this.bubble(messageId);
    if (!node || !this.scroller) return false;
    this.pinned = false;
    this.programmatic += 1;
    node.scrollIntoView({ block: "start", behavior: "instant" });
    window.setTimeout(() => { this.programmatic = Math.max(0, this.programmatic - 1); }, 0);
    return true;
  }

  anchorTo(messageId, offset) {
    const node = this.bubble(messageId);
    if (!node || !this.scroller) return false;
    this.pinned = false;
    this.programmatic += 1;
    const shift = node.getBoundingClientRect().top - this.scroller.getBoundingClientRect().top - offset;
    this.scrollToPosition(this.scrollPosition() + shift);
    window.setTimeout(() => { this.programmatic = Math.max(0, this.programmatic - 1); }, 0);
    return true;
  }

  disconnectedCallback() {
    window.clearTimeout(this.draftTimer);
    this.removeAttribute("ready");
    this.sizer?.disconnect();
    this.sizer = null;
    this.stopWatchdog();
    this.ac?.abort();
    this.ac = null;
    this.closeStream();
  }

  onKeydown(event) {
    if (jumpTextEdge(event, this.input)) return;
    if (event.key !== "Enter") return;
    if (COARSE || event.shiftKey || event.isComposing) return;
    event.preventDefault();
    void this.send();
  }

  autoGrow() {
    if (!this.input) return;
    const wasPinned = this.pinned;
    this.input.style.height = "auto";
    const max = Math.round(this.viewportHeight() * 0.35);
    this.input.style.height = `${Math.min(this.input.scrollHeight, max)}px`;
    this.input.style.overflowY = this.input.scrollHeight > max ? "auto" : "hidden";
    if (wasPinned) this.stickToEnd();
  }

  // The unsent message belongs to the conversation, so the browser only holds
  // it until the next save: the page renders it back into the box, the chips
  // come from the files the draft carried.
  restoreDraft() {
    this.draftSaved = { text: this.input?.value || "", files: this.attachmentNames([]) };
    this.draftAt = "";
    let files = [];
    try {
      files = JSON.parse(this.getAttribute("draft-files") || "[]");
    } catch {
      files = [];
    }
    if (!Array.isArray(files) || !files.length) return;
    this.attached = files.map((file) => ({ ...file, progress: 1 }));
    this.renderAttachments();
    this.draftSaved.files = this.attachmentNames(this.attached);
  }

  // Another device saved a draft for this conversation. The event says which
  // conversation moved and nothing more, so the fresh state comes from the
  // server, the way the tab strip pulls its fragment. A bare signal (the
  // snapshot after a reconnect) means everyone catches up.
  onDraftEvent(detail) {
    const conversation = detail && typeof detail === "object" ? detail.conversation : "";
    if (conversation && conversation !== this.getAttribute("conversation-id")) return;
    void this.pullDraft();
  }

  // pullDraft never types over the user. A box with unsaved words keeps them
  // until its own debounce has run, and only a draft newer than the last one
  // this device wrote is applied, so two devices settle on the later writer
  // instead of fighting over every keystroke.
  async pullDraft() {
    if (!this.input || !this.draftUrl || this.hasAttribute("blocked")) return;
    // A save of this device is on its way: what the server answers now is older
    // than what it is about to hold, so the pull waits for it instead of
    // putting a draft back that is already overtaken.
    if (this.draftSaving) {
      this.draftMissed = true;
      return;
    }
    let draft;
    try {
      draft = await getJSON(this.draftUrl);
    } catch {
      return;
    }
    const at = draft.updatedAt || "";
    if (at && this.draftAt && at <= this.draftAt) return;
    const files = (draft.files || []).map((file) => ({ ...file, progress: 1 }));
    const names = this.attachmentNames(files);
    if (draft.text === this.input.value && names === this.attachmentNames(this.attached)) {
      this.draftAt = at;
      return;
    }
    const typing = !this.draftSaved
      || this.draftSaved.text !== this.input.value
      || this.draftSaved.files !== this.attachmentNames(this.attached);
    if (typing) {
      // Words in the box win until this device has written them. Once it has,
      // it looks again, so the two devices end on the same draft instead of
      // each keeping what the other one replaced.
      this.draftMissed = true;
      return;
    }
    this.draftAt = at;
    this.draftSaved = { text: draft.text || "", files: names };
    this.input.value = draft.text || "";
    this.attached = files;
    this.renderAttachments();
    this.autoGrow();
  }

  attachmentNames(files) {
    return files.filter((file) => file.progress >= 1).map((file) => file.name).join("\n");
  }

  // A draft is saved when the typing stops, not while it runs: a conversation
  // is one file, and a keystroke is not worth rewriting it. This is the only
  // path that saves, so there is nothing to reason about when a page goes away
  // or a phone locks: after the pause the words are already at the server.
  queueDraft() {
    if (!this.input || this.hasAttribute("blocked")) return;
    window.clearTimeout(this.draftTimer);
    this.draftTimer = window.setTimeout(() => void this.saveDraft(), 1000);
  }

  async saveDraft() {
    if (!this.input || this.hasAttribute("blocked")) return;
    window.clearTimeout(this.draftTimer);
    const ready = this.attached.filter((file) => file.progress >= 1);
    const text = this.input.value;
    const names = this.attachmentNames(ready);
    if (this.draftSaved && this.draftSaved.text === text && this.draftSaved.files === names) return;
    this.draftSaved = { text, files: names };
    const body = new URLSearchParams();
    body.set("form", "draft");
    body.set("message", text);
    ready.forEach((file) => body.append("attachment", file.name));
    this.draftSaving = true;
    try {
      const response = await fetch(this.postUrl, {
        method: "POST",
        headers: csrfHeaders({ "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" }),
        body: body.toString(),
      });
      const payload = await response.json().catch(() => ({}));
      // What this device wrote last. The draft that comes back from the server
      // is only applied when it is newer than this, so an echo of the own save
      // never lands back in the box.
      if (payload.updatedAt) this.draftAt = payload.updatedAt;
    } catch {
      this.draftSaved = null;
    } finally {
      this.draftSaving = false;
    }
    // Look once after every save: two devices that wrote at the same moment
    // both end on what the server kept, instead of each holding what the other
    // one replaced.
    this.draftMissed = false;
    await this.pullDraft();
  }

  onInput() {
    this.queueDraft();
    this.autoGrow();
    if (!this.input || !this.counter) return;
    const used = new TextEncoder().encode(this.input.value).length;
    const over = used > this.maxBytes;
    this.counter.classList.toggle("d-none", used < this.maxBytes * 0.8);
    this.counter.classList.toggle("text-danger", over);
    this.counter.textContent = `${Math.round(used / 1024)} of ${Math.round(this.maxBytes / 1024)} KB`;
    if (this.sendButton) this.sendButton.disabled = over;
  }

  setRunning(running) {
    this.running = running;
    this.toggleAttribute("running", running);
    this.cancelButton?.classList.toggle("d-none", !running);
    this.onInput();
  }

  openStream() {
    if (!this.streamUrl || this.source) return;
    this.source = new EventSource(this.streamUrl);
    this.source.addEventListener("assistant", (event) => {
      let frame;
      try {
        frame = JSON.parse(event.data);
      } catch {
        return;
      }
      this.onFrame(frame);
    });
    this.source.addEventListener("error", () => {
    });
  }

  closeStream() {
    this.source?.close();
    this.source = null;
  }

  checkStream() {
    if (document.visibilityState !== "visible") return;
    if (this.source && this.source.readyState === EventSource.CLOSED) {
      this.closeStream();
      this.openStream();
    }
  }

  onFrame(frame) {
    if (!frame || !frame.messageId) return;
    this.lastFrameAt = Date.now();
    switch (frame.kind) {
      case "start":
        this.pending = { runId: frame.runId, messageId: frame.messageId };
        this.setRunning(true);
        this.setText(frame.messageId, frame.html, frame.text);
        break;
      case "delta":
        this.appendText(frame.messageId, frame.text || "");
        break;
      case "html":
        this.setText(frame.messageId, frame.html, "");
        break;
      case "tool":
        this.showTool(frame.messageId, frame.text);
        break;
      case "message":
        // A message appeared outside a followed generation: a check wrote its
        // report, or a message queued while a turn ran. Pull that one message
        // and append or replace it: a chat answer may be streaming right now,
        // and this must not touch its state.
        void this.replaceMessage(frame.messageId, "complete");
        break;
      case "gone":
        this.bubble(frame.messageId)?.remove();
        break;
      case "end":
        this.setRunning(false);
        this.pending = null;
        this.pendingMessageId = null;
        this.stopWatchdog();
        this.setContext(frame.context);
        // The settled state arrives with the finished message, not before it:
        // the streamed text is still the partial one until the fragment lands,
        // and a bubble that says "complete" over half an answer is a lie.
        void this.replaceMessage(frame.messageId, frame.state || "complete");
        break;
    }
  }

  bubble(messageId) {
    return this.log.querySelector(`[data-message-id="${CSS.escape(messageId)}"]`);
  }

  ensureBubble(messageId) {
    this.empty?.remove();
    this.empty = null;
    let node = this.bubble(messageId);
    if (!node) {
      node = document.createElement("div");
      node.className = "dc-msg";
      node.id = "message-" + messageId;
      node.setAttribute("data-assistant-message", "");
      node.setAttribute("data-no-pe", "");
      node.setAttribute("data-role", "assistant");
      node.setAttribute("data-message-id", messageId);
      node.setAttribute("data-state", "streaming");
      node.innerHTML = `<div class="d-flex align-items-baseline gap-2 mb-1">
          <span class="text-secondary small fw-medium text-truncate" data-assistant-author></span>
          <span class="spinner-border spinner-border-sm text-secondary ms-auto flex-shrink-0" role="status" aria-label="Answering"></span>
        </div>
        <div class="text-break markdown" data-assistant-text></div>
        <div class="text-secondary small mt-2 d-none" data-assistant-tool><i class="ti ti-tool me-1"></i>Working<span data-assistant-tool-name class="ms-1 font-monospace"></span></div>`;
      const author = node.querySelector("[data-assistant-author]");
      if (author) author.textContent = this.getAttribute("coder-label") || "Coder";
      this.log.append(node);
    }
    return node;
  }

  body(messageId) {
    return this.ensureBubble(messageId).querySelector("[data-assistant-text]");
  }

  tail(body) {
    let tail = body.querySelector("[data-assistant-tail]");
    if (!tail) {
      tail = document.createElement("span");
      tail.setAttribute("data-assistant-tail", "");
      body.append(tail);
    }
    return tail;
  }

  setText(messageId, html, text) {
    const body = this.body(messageId);
    if (!body) return;
    body.innerHTML = html || "";
    this.tail(body).textContent = text || "";
    this.stickToEnd();
  }

  appendText(messageId, text) {
    const body = this.body(messageId);
    if (!body) return;
    this.tail(body).append(document.createTextNode(text));
    this.stickToEnd();
  }

  showTool(messageId, name) {
    const line = this.ensureBubble(messageId).querySelector("[data-assistant-tool]");
    if (!line) return;
    line.classList.remove("d-none");
    const label = line.querySelector("[data-assistant-tool-name]");
    if (label) label.textContent = name || "";
  }

  setContext(percent) {
    const ring = this.querySelector("[data-assistant-ring]");
    const value = Number(percent);
    if (!ring || !Number.isFinite(value) || value <= 0) return;
    const capped = Math.min(100, Math.round(value));
    for (const arc of ring.querySelectorAll("[data-assistant-ring-fill], [data-assistant-ring-halo]")) {
      arc.setAttribute("stroke-dasharray", `${capped} 100`);
    }
    if (capped >= 95) ring.setAttribute("data-assistant-ring-level", "full");
    else if (capped >= 85) ring.setAttribute("data-assistant-ring-level", "high");
    else ring.removeAttribute("data-assistant-ring-level");
    const button = this.querySelector("[data-assistant-new-label]");
    if (!button) return;
    const label = `${button.getAttribute("data-assistant-new-label")}. Context ${capped} percent`;
    button.setAttribute("title", label);
    button.setAttribute("aria-label", label);
  }

  // A pulled message keeps the place its frame gave it. The fetch takes a
  // moment, and the frames behind it do not wait: an answer that opens right
  // after the question was announced would otherwise stand above it.
  // The slot carries no [data-assistant-message], it is nothing yet, and it
  // takes up no room while it waits.
  reserveBubble(messageId) {
    const node = this.bubble(messageId);
    if (node) return node;
    this.empty?.remove();
    this.empty = null;
    const slot = document.createElement("div");
    slot.style.display = "contents";
    slot.setAttribute("data-assistant-slot", "");
    slot.setAttribute("data-message-id", messageId);
    this.log.append(slot);
    return slot;
  }

  // state is what the bubble falls back to when the finished message cannot be
  // pulled: the answer on screen is then the streamed one, but it no longer
  // claims to be running. The bubble is looked up after the fetch, because two
  // pulls of the same message can be in flight at once (the stream frame and
  // the send response race for a queued message), and a lookup from before the
  // await appends a duplicate next to what the other pull already put there.
  async replaceMessage(messageId, state) {
    this.reserveBubble(messageId);
    try {
      const html = await getText(this.messageUrl + encodeURIComponent(messageId));
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.firstElementChild;
      if (!fresh) throw new Error("empty message fragment");
      const node = this.bubble(messageId);
      if (node) node.replaceWith(fresh);
      else this.log.append(fresh);
      window.app?.loadElements?.(fresh);
      this.stickToEnd();
    } catch {
      const node = this.bubble(messageId);
      if (node?.hasAttribute("data-assistant-slot")) {
        node.remove();
        return;
      }
      node?.querySelector(".spinner-border")?.remove();
      if (state) node?.setAttribute("data-state", state);
    }
  }

  appendUserMessage(text, attachments = []) {
    this.empty?.remove();
    this.empty = null;
    const node = document.createElement("div");
    node.className = "dc-msg dc-msg-user";
    node.setAttribute("data-assistant-message", "");
    node.setAttribute("data-no-pe", "");
    node.setAttribute("data-role", "user");
    node.setAttribute("data-state", "complete");
    node.innerHTML = `<div class="d-flex align-items-baseline gap-2 mb-1">
        <span class="text-secondary small fw-medium text-truncate">You</span>
        <dc-time datetime="${new Date().toISOString()}" class="text-secondary small flex-shrink-0"></dc-time>
      </div>
      <div class="text-break" data-assistant-text></div>
      <div class="d-flex flex-column gap-2 mt-2 d-none" data-assistant-files></div>`;
    const body = node.querySelector("[data-assistant-text]");
    if (body) {
      text.split("\n").forEach((line, i) => {
        if (i) body.append(document.createElement("br"));
        body.append(line);
      });
    }
    const files = node.querySelector("[data-assistant-files]");
    if (files && attachments.length) {
      files.classList.remove("d-none");
      files.append(...attachments.map((file) => renderAttachment(file)));
    }
    this.log.append(node);
    window.app?.loadElements?.(node);
    // Asking something is asking to follow the answer, wherever the reader was.
    this.pinned = true;
    this.stickToEnd();
    return node;
  }

  startWatchdog(messageId) {
    this.stopWatchdog();
    this.pendingMessageId = messageId;
    this.lastFrameAt = Date.now();
    this.watchdog = window.setInterval(() => void this.resync(), 3000);
  }

  stopWatchdog() {
    if (this.watchdog) window.clearInterval(this.watchdog);
    this.watchdog = null;
  }

  async resync() {
    if (!this.pendingMessageId) {
      this.stopWatchdog();
      return;
    }
    this.checkStream();
    if (Date.now() - this.lastFrameAt < 3000) return;
    const messageId = this.pendingMessageId;
    try {
      const html = await getText(this.messageUrl + encodeURIComponent(messageId));
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.firstElementChild;
      if (!fresh) return;
      const settled = fresh.getAttribute("data-state") !== "streaming";
      const node = this.bubble(messageId);
      if (node) node.replaceWith(fresh);
      else this.log.append(fresh);
      window.app?.loadElements?.(fresh);
      if (settled) {
        this.pendingMessageId = null;
        this.stopWatchdog();
        this.setRunning(false);
      }
      this.stickToEnd();
    } catch {
      void 0;
    }
  }

  async upload(files) {
    if (!this.uploadUrl || !files.length) return;
    for (const file of files) {
      if (this.maxFileBytes && file.size > this.maxFileBytes) {
        notifyError(`${file.name} is too large.`);
        continue;
      }
      const pending = { name: file.name, media: mediaKind(file.name), size: file.size, progress: 0 };
      this.attached.push(pending);
      this.renderAttachments();
      try {
        const saved = await this.transfer(file, pending);
        Object.assign(pending, saved, { progress: 1 });
      } catch (error) {
        this.attached = this.attached.filter((entry) => entry !== pending);
        notifyError(error.message || "The file could not be attached.");
      }
      this.renderAttachments();
    }
    this.onInput();
  }

  transfer(file, pending) {
    return new Promise((resolve, reject) => {
      const data = new FormData();
      data.append("file", file, file.name);
      const xhr = new XMLHttpRequest();
      xhr.open("POST", this.uploadUrl);
      const headers = csrfHeaders({ Accept: "application/json" });
      Object.keys(headers).forEach((key) => xhr.setRequestHeader(key, headers[key]));
      xhr.upload.addEventListener("progress", (event) => {
        if (!event.lengthComputable) return;
        pending.progress = event.loaded / event.total;
        this.renderAttachments();
      });
      xhr.addEventListener("load", () => {
        let payload = {};
        try {
          payload = JSON.parse(xhr.responseText || "{}");
        } catch {
          payload = {};
        }
        if (xhr.status >= 200 && xhr.status < 300 && payload.files?.length) {
          resolve(payload.files[0]);
          return;
        }
        reject(new Error(payload.error || "The file could not be attached."));
      });
      xhr.addEventListener("error", () => reject(new Error("The file could not be attached.")));
      xhr.send(data);
    });
  }

  renderAttachments() {
    if (!this.attachTray) return;
    this.attachTray.classList.toggle("d-none", this.attached.length === 0);
    this.attachTray.replaceChildren(...this.attached.map((file) => {
      const chip = document.createElement("span");
      chip.className = "badge bg-secondary-lt d-inline-flex align-items-center gap-1";
      chip.setAttribute("data-assistant-attachment", file.name);
      const icon = document.createElement("i");
      icon.className = `ti ${ATTACH_ICONS[file.media] || "ti-paperclip"}`;
      const label = document.createElement("span");
      label.className = "text-truncate";
      label.style.maxWidth = "10rem";
      label.textContent = file.progress < 1 ? `${file.name} ${Math.round(file.progress * 100)}%` : file.name;
      chip.append(icon, label);
      if (file.progress >= 1) {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "btn-close btn-close-sm";
        remove.setAttribute("aria-label", `Remove ${file.name}`);
        remove.setAttribute("data-assistant-attachment-remove", file.name);
        chip.append(remove);
      }
      return chip;
    }));
  }

  async send() {
    if (!this.input) return;
    const text = this.input.value.trim();
    const ready = this.attached.filter((file) => file.progress >= 1);
    if (!text && !ready.length) return;
    if (new TextEncoder().encode(text).length > this.maxBytes) {
      notifyError("That message is too long.");
      return;
    }
    this.input.value = "";
    // The send takes the draft with it, server side and here: a save still
    // waiting on its timer would otherwise write the sent words back.
    window.clearTimeout(this.draftTimer);
    this.draftSaved = { text: "", files: "" };
    this.onInput();
    if (!COARSE) this.input.focus();
    const bubble = this.appendUserMessage(text, ready);
    this.attached = [];
    this.renderAttachments();
    try {
      const body = new URLSearchParams();
      body.set("form", "message");
      body.set("message", text);
      ready.forEach((file) => body.append("attachment", file.name));
      const response = await postForm(this.postUrl, body);
      await ensureOk(response, "The message could not be sent.");
      const payload = await response.json().catch(() => ({}));
      if (payload.queued) {
        if (this.adoptSent(bubble, payload.userMessageId, "queued")) {
          void this.replaceMessage(payload.userMessageId, "queued");
        }
        this.checkStream();
        return;
      }
      this.adoptSent(bubble, payload.userMessageId, "complete");
      this.setRunning(true);
      this.beginPending(payload.messageId);
    } catch (error) {
      bubble?.remove();
      this.input.value = text;
      this.attached = ready;
      this.renderAttachments();
      this.onInput();
      notifyError(error.message || "The message could not be sent.");
    }
  }

  // The sent message goes out on the stream too, for the other devices, so this
  // page can be about to hold it twice: the bubble it wrote when the words left
  // the box, and the one the announcement pulls. Whichever was there first
  // stays, the other one goes. Returns whether the written bubble is the one
  // that stayed, and it then carries the id, so the announcement replaces it
  // instead of appending next to it.
  adoptSent(bubble, messageId, state) {
    if (!messageId || !bubble) return false;
    if (this.bubble(messageId)) {
      bubble.remove();
      return false;
    }
    bubble.setAttribute("data-message-id", messageId);
    bubble.setAttribute("data-state", state);
    return true;
  }

  async retry() {
    if (this.running) return;
    this.setRunning(true);
    try {
      const response = await postForm(this.postUrl, { form: "retry" });
      await ensureOk(response, "The message could not be sent again.");
      const payload = await response.json().catch(() => ({}));
      this.bubble(payload.replacedId)?.remove();
      this.beginPending(payload.messageId);
    } catch (error) {
      this.setRunning(false);
      notifyError(error.message || "The message could not be sent again.");
    }
  }

  beginPending(messageId) {
    if (!messageId) return;
    this.ensureBubble(messageId);
    this.startWatchdog(messageId);
    this.checkStream();
  }

  async cancel() {
    try {
      const response = await postForm(this.postUrl, { form: "cancel" });
      await ensureOk(response, "The answer could not be stopped.");
    } catch (error) {
      notifyError(error.message || "The answer could not be stopped.");
    }
  }

  async discard(messageId) {
    if (!messageId) return;
    try {
      const response = await postForm(this.postUrl, { form: "discard", message_id: messageId });
      await ensureOk(response, "The message could not be taken back.");
      this.bubble(messageId)?.remove();
    } catch (error) {
      notifyError(error.message || "The message could not be taken back.");
    }
  }

  // A short page (the pictures in it have not decoded yet) reads as "already at
  // the end" from every scroll event, so an anchored page must not re-pin: the
  // pin would win the next time the transcript grows and take the page to the
  // bottom the reader never asked for.
  trackPin() {
    if (this.programmatic) return;
    const remaining = this.scrollLength() - this.scrollPosition() - this.viewportHeight();
    this.pinned = remaining < 120;
  }

  // stickToEnd holds the transcript at its end while the reader is following
  // it, and nowhere at all once they scrolled away.
  stickToEnd() {
    if (!this.pinned) return;
    if (this.scrollQueued) return;
    this.scrollQueued = true;
    window.requestAnimationFrame(() => {
      this.scrollQueued = false;
      if (!this.pinned) return;
      this.programmatic += 1;
      // Scrolling smoothly never catches up with a growing answer: every
      // delta would start a new animation from where the last one got to.
      // Following the end of a transcript is a jump.
      this.scrollToPosition(this.scrollLength());
      window.setTimeout(() => { this.programmatic = Math.max(0, this.programmatic - 1); }, 0);
    });
  }
}

customElements.define("dc-assistant", Assistant);
