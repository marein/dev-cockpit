import { postForm, ensureOk, getText, getJSON, csrfHeaders } from "@dc/http";
import { notifyError, showToast } from "@dc/toast";
import { onServerEvent } from "@dc/events";
import { jumpTextEdge } from "@dc/dom";

const COARSE = window.matchMedia?.("(pointer: coarse)").matches ?? false;

const ATTACH_ICONS = { image: "ti-photo", video: "ti-video", audio: "ti-microphone", file: "ti-paperclip" };

// The conversation stream sends a ping frame every 15s, the same life sign
// /events carries, because the SSE keepalive is a comment and fires no event
// here. Past this the stream counts as dead, with room for one missed ping.
const STALE_MS = 45000;

// Voice mode is a per device choice, like the terminal theme: whether a
// finished answer is read aloud on this screen. The volume sits beside it,
// also per device, as a whole percent.
const VOICE_MODE_KEY = "dc-assistant-voice-mode";
const VOICE_VOLUME_KEY = "dc-assistant-voice-volume";
const VOICE_VOLUME_DEFAULT = 100;

// How long the send button has to be held before the press means talking
// instead of sending; a shorter press stays the plain send it always was.
const SEND_HOLD_MS = 250;

// How far left the holding finger or mouse has to slide to cancel the
// recording, the messenger gesture: past this nothing is transcribed and
// nothing is sent.
const TALK_CANCEL_PX = 80;

// One silent sample. Played muted inside the push to talk press or the voice
// mode toggle, the two user gestures, it unlocks the audio element for the
// programmatic play a finished answer asks for later.
const SILENT_WAV = "data:audio/wav;base64,UklGRigAAABXQVZFZm10IBIAAAABAAEARKwAAIhYAQACABAAAABkYXRhAgAAAAEA";

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
    this.ownTops = [];
    this.scrollQueued = false;

    this.form?.addEventListener("submit", (event) => {
      event.preventDefault();
      // Releasing a talk hold raises the button's ordinary click and with it
      // this submit, which would send the composer while the clip is still
      // transcribing; a submit right after a hold's release is that click
      // and is spent here.
      if (this.swallowSubmitUntil && Date.now() < this.swallowSubmitUntil) return;
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
      const speak = event.target.closest("[data-assistant-speak]");
      if (speak) {
        event.preventDefault();
        this.toggleSpeak(speak);
        return;
      }
      const discard = event.target.closest("[data-assistant-discard]");
      if (discard) {
        event.preventDefault();
        void this.discard(discard.getAttribute("data-assistant-discard"));
      }
    }, { signal });
    this.setupVoice(signal);
    document.addEventListener("visibilitychange", () => {
      this.checkStream();
      void this.catchUp();
    }, { signal });
    onServerEvent("draft", (event) => this.onDraftEvent(event.detail), { signal });
    // A speech engine's very first start builds its container and downloads
    // its model, minutes rather than seconds. The server announces such a
    // start the moment it begins, and the toast is what keeps that one wait
    // from reading as a hang.
    onServerEvent("voice-warming", (event) => this.onVoiceWarming(event.detail), { signal });
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

  // A machine move must not read as the reader scrolling away, and its
  // scroll event cannot be told apart by counting: the browser fires at most
  // one event per frame for all movement in it, and it also moves scrollTop
  // on its own, clamping to the bottom of a layout the streaming answer has
  // meanwhile changed again, so a counter drifts and then either swallows
  // the reader's real scroll or lets a stale event unpin a follower. What is
  // reliable is the place a machine move can land on: where a jump ended, or
  // the bottom the layout had when the machinery last looked. trackPin
  // ignores an event that reads back one of those places, because no reader
  // intent lies in it, and judges everything else.
  notePlace(top) {
    const now = Date.now();
    this.ownTops = this.ownTops.filter((entry) => now - entry.at < 500);
    this.ownTops.push({ top, at: now });
  }

  maskedJump(jump) {
    jump();
    this.notePlace(this.scrollPosition());
  }

  // focusMessage brings one message to the top of the transcript, the way a
  // notification's link lands on the answer it announced, and releases the
  // end pin so the settling page does not drag the reader away from it.
  focusMessage(messageId) {
    const node = this.bubble(messageId);
    if (!node || !this.scroller) return false;
    this.pinned = false;
    this.maskedJump(() => node.scrollIntoView({ block: "start", behavior: "instant" }));
    return true;
  }

  anchorTo(messageId, offset) {
    const node = this.bubble(messageId);
    if (!node || !this.scroller) return false;
    this.pinned = false;
    const shift = node.getBoundingClientRect().top - this.scroller.getBoundingClientRect().top - offset;
    this.maskedJump(() => this.scrollToPosition(this.scrollPosition() + shift));
    return true;
  }

  disconnectedCallback() {
    window.clearTimeout(this.draftTimer);
    window.clearTimeout(this.holdTimer);
    window.clearTimeout(this.hintTimer);
    this.holdTimer = null;
    this.holdFired = false;
    this.holdCancelled = false;
    this.hintLinger = false;
    this.removeAttribute("ready");
    this.sizer?.disconnect();
    this.sizer = null;
    this.stopWatchdog();
    this.stopTalk(true);
    this.disarmTalkEscape();
    this.speech?.pause();
    this.speech = null;
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
    this.lastFrameAt = Date.now();
    this.source = new EventSource(this.streamUrl);
    // Every connection that had to be established again is a break, whether
    // this element built it or the browser retried on its own, and a break is
    // where a frame goes missing. That is the moment to catch up, and the only
    // one besides a page coming back.
    this.source.addEventListener("open", () => {
      this.lastFrameAt = Date.now();
      void this.catchUp();
    });
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

  // A socket that hangs open and silent, the classic phone that just woke up,
  // never reports itself closed, and silence alone says nothing: an answer is
  // silent while the model thinks. The ping is what separates the two, so the
  // stream is only torn down and built again once that life sign stops.
  checkStream() {
    if (!this.source) return;
    if (this.source.readyState === EventSource.CLOSED || Date.now() - this.lastFrameAt > STALE_MS) this.restartStream();
  }

  restartStream() {
    if (document.visibilityState !== "visible") return;
    this.closeStream();
    this.openStream();
  }

  onFrame(frame) {
    if (!frame) return;
    this.lastFrameAt = Date.now();
    if (frame.kind === "ping") return;
    if (!frame.messageId) return;
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
        // Voice mode: a finished answer reads itself aloud. The play stands
        // on the unlock the push to talk press bought; a refused autoplay
        // stays quiet, the speaker on the answer is the way to it then.
        if ((frame.state || "complete") === "complete" && this.hasAttribute("tts") && this.voiceModeOn()) {
          void this.playSpeech(this.messageUrl + encodeURIComponent(frame.messageId) + "/audio");
        }
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
      // The fresh fragment's speaker renders in the resting state; while
      // this very answer is being spoken it has to show that instead.
      this.syncSpeakButtons();
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

  // The watchdog watches the socket, nothing else. Frames stopping is the
  // normal middle of an answer, thinking is complete silence and a tool run
  // emits one frame at its start, so silence must cost neither a rebuilt
  // stream nor a request: a half hour of tool work would otherwise be hundreds
  // of pulls whose answer is thrown away.
  resync() {
    if (!this.pendingMessageId) {
      this.stopWatchdog();
      return;
    }
    this.checkStream();
  }

  // The one pull that repairs a break, after the stream was established again
  // and when the page comes back in front. It may only ever finish a message:
  // the pull is a snapshot of the past and the store holds the answer once the
  // turn settled, so a fragment that still says streaming is the empty bubble,
  // and putting it on screen would wipe every streamed word.
  async catchUp() {
    const messageId = this.pendingMessageId;
    if (!messageId || this.catching) return;
    this.catching = true;
    try {
      const html = await getText(this.messageUrl + encodeURIComponent(messageId));
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.firstElementChild;
      if (!fresh || fresh.getAttribute("data-state") === "streaming") return;
      if (this.pendingMessageId !== messageId) return;
      const node = this.bubble(messageId);
      if (node) node.replaceWith(fresh);
      else this.log.append(fresh);
      window.app?.loadElements?.(fresh);
      this.pendingMessageId = null;
      this.stopWatchdog();
      this.setRunning(false);
      this.stickToEnd();
    } catch {
      void 0;
    } finally {
      this.catching = false;
    }
  }

  // Push to talk and the spoken answers. Holding the talk button records;
  // releasing transcribes and sends the result right away, existing composer
  // text prepended, so a spoken exchange costs no extra tap. The keyboard
  // way is the Alt Alt double tap, wired in the panel element because it
  // works with the overlay closed too; it lands here as toggleTalk. A
  // finished answer then reads itself aloud while voice mode is on, and the
  // speaker on every answer replays it.
  setupVoice(signal) {
    this.sttUrl = this.getAttribute("stt-url");
    this.voiceButton = this.querySelector("[data-assistant-voice]");
    this.autoplayInput = this.querySelector("[data-assistant-autoplay]");
    if (this.voiceButton) {
      this.applyVoiceMode(this.voiceModeOn());
    }
    // Switching it on is the gesture the audio element needs to be allowed to
    // start an answer by itself later, so the unlock is spent right here.
    this.autoplayInput?.addEventListener("change", () => {
      const on = this.autoplayInput.checked;
      try {
        localStorage.setItem(VOICE_MODE_KEY, on ? "on" : "off");
      } catch {
        void 0;
      }
      this.applyVoiceMode(on);
      if (on) this.unlockSpeech();
    }, { signal });
    // The volume follows the drag, so what you hear while an answer plays is
    // what the slider says. The slider stored it already, this only moves the
    // sound.
    this.addEventListener("dc-volume-change", (event) => {
      const value = event.detail?.value;
      if (Number.isFinite(value)) this.applyVoiceVolume(value);
    }, { signal });
    if (!this.sttUrl || !this.sendButton) return;
    if (!navigator.mediaDevices?.getUserMedia || !window.MediaRecorder) return;
    this.talkReady = true;
    this.talkHint = this.querySelector("[data-assistant-talk-hint]");
    // Push to talk lives on the send button: a press held past the threshold
    // records, its release stops and sends the transcript, and a shorter
    // press stays the plain send it always was, decided by nothing but the
    // clock. The capture keeps the release on the button wherever the finger
    // drifted, and the long press must never become a scroll, a text
    // selection or the context menu.
    this.sendButton.style.touchAction = "none";
    this.sendButton.addEventListener("contextmenu", (event) => event.preventDefault(), { signal });
    this.sendButton.addEventListener("pointerdown", (event) => {
      if (event.pointerType === "mouse" && event.button !== 0) return;
      this.sendButton.setPointerCapture?.(event.pointerId);
      this.holdStartX = event.clientX;
      window.clearTimeout(this.holdTimer);
      this.holdTimer = window.setTimeout(() => {
        this.holdTimer = null;
        this.holdFired = true;
        this.unlockSpeech();
        void this.startTalk();
      }, SEND_HOLD_MS);
    }, { signal });
    // The capture routes the moves here wherever the pointer drifts, finger
    // and mouse alike: sliding left past the threshold cancels the recording
    // mid hold, the messenger gesture, and the hint follows the slide so the
    // way out is readable before it is taken.
    this.sendButton.addEventListener("pointermove", (event) => {
      if (!this.holdFired || this.holdCancelled) return;
      const pull = Math.min(0, event.clientX - this.holdStartX);
      this.slideTalkHint(pull);
      if (pull <= -TALK_CANCEL_PX) this.cancelTalk();
    }, { signal });
    this.sendButton.addEventListener("pointerup", () => this.endHold(false), { signal });
    this.sendButton.addEventListener("pointercancel", () => this.endHold(true), { signal });
  }

  // endHold settles one press on the send button. A press the timer never
  // turned into a recording is a plain send and the following click carries
  // it; one that recorded ends the recording instead, and the click that
  // still follows the release is marked to be swallowed. A press that was
  // cancelled by the slide already ended its recording, so the release only
  // spends the click: without that the finger lifting off a cancelled hold
  // would send the typed text nobody asked to send.
  endHold(abort) {
    window.clearTimeout(this.holdTimer);
    this.holdTimer = null;
    if (this.holdCancelled) {
      this.holdCancelled = false;
      this.swallowSubmitUntil = Date.now() + 350;
      return;
    }
    if (!this.holdFired) return;
    this.holdFired = false;
    this.swallowSubmitUntil = Date.now() + 350;
    this.stopTalk(abort);
  }

  // cancelTalk ends a held recording without sending: the clip is thrown
  // away, the hint confirms it, and the cancel is final for this press, a
  // slide back to the right resumes nothing.
  cancelTalk() {
    if (!this.holdFired) return;
    this.holdFired = false;
    this.holdCancelled = true;
    this.stopTalk(true);
    this.confirmTalkCancel();
  }

  // The hint sits over the message box while a recording runs: an arrow and
  // the words, sliding along with the pull and thinning towards the
  // threshold, so the gesture reads before it triggers.
  slideTalkHint(pull) {
    const slide = this.talkHint?.querySelector("[data-assistant-talk-slide]");
    if (!slide) return;
    slide.style.transform = pull ? `translateX(${Math.round(pull / 2)}px)` : "";
    slide.style.opacity = pull ? String(Math.max(0.3, 1 + pull / (TALK_CANCEL_PX * 2))) : "";
  }

  showTalkHint(mode = "slide") {
    const hint = this.talkHint;
    if (!hint) return;
    window.clearTimeout(this.hintTimer);
    this.hintLinger = false;
    const slide = hint.querySelector("[data-assistant-talk-slide]");
    if (slide) {
      slide.classList.remove("text-danger");
      slide.style.transform = "";
      slide.style.opacity = "";
    }
    const icon = hint.querySelector("i");
    if (icon) icon.className = mode === "escape" ? "ti ti-keyboard" : "ti ti-arrow-left";
    const text = hint.querySelector("[data-assistant-talk-hint-text]");
    if (text) text.textContent = mode === "escape" ? "Esc to cancel" : "Slide to cancel";
    hint.classList.remove("d-none");
    hint.classList.add("d-flex");
  }

  // While a recording runs, Escape throws it away. The listener goes on the
  // document in the capture phase and is taken off again the moment the
  // recording ends, so nothing else in the app ever sees a press that meant
  // cancel, and every press outside a recording reaches whatever owns Escape
  // there, a dialog, the editor, the terminal.
  armTalkEscape() {
    if (this.talkEscape) return;
    this.talkEscape = (event) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      this.escapeTalk();
    };
    document.addEventListener("keydown", this.talkEscape, { capture: true });
  }

  disarmTalkEscape() {
    if (!this.talkEscape) return;
    document.removeEventListener("keydown", this.talkEscape, { capture: true });
    this.talkEscape = null;
  }

  // escapeTalk throws the clip away whichever way the recording was started.
  // A held press takes the slide's own path, so the release that still
  // follows only spends its click instead of sending; a hands free recording
  // ends right here and says so where the hint stands.
  escapeTalk() {
    if (this.holdFired) {
      this.cancelTalk();
      return;
    }
    if (!this.talkActive()) return;
    this.stopTalk(true);
    this.showTalkHint("escape");
    this.confirmTalkCancel();
  }

  // hideTalkHint steps aside while the cancel confirmation still stands: the
  // recorder's stop resets the button right away, but the person who slid
  // left has to read that the cancel took.
  hideTalkHint() {
    if (this.hintLinger) return;
    window.clearTimeout(this.hintTimer);
    this.talkHint?.classList.add("d-none");
    this.talkHint?.classList.remove("d-flex");
  }

  confirmTalkCancel() {
    const hint = this.talkHint;
    if (!hint) return;
    const slide = hint.querySelector("[data-assistant-talk-slide]");
    if (slide) {
      slide.classList.add("text-danger");
      slide.style.transform = "";
      slide.style.opacity = "";
    }
    const icon = hint.querySelector("i");
    if (icon) icon.className = "ti ti-x";
    const text = hint.querySelector("[data-assistant-talk-hint-text]");
    if (text) text.textContent = "Recording cancelled";
    this.hintLinger = true;
    this.hintTimer = window.setTimeout(() => {
      this.hintLinger = false;
      this.hideTalkHint();
    }, 900);
  }

  // talkActive reports a recording or a pending microphone grab; the panel's
  // Alt Alt gesture asks before it decides between start and stop.
  talkActive() {
    return Boolean(this.recorder || this.talkHeld);
  }

  // toggleTalk is push to talk without the hold, for the Alt Alt gesture:
  // the first call starts recording, the second stops and sends, the same
  // path the button's release takes. A call while the microphone permission
  // prompt still stands takes the grab back instead of recording into a
  // conversation nobody watches.
  toggleTalk() {
    if (!this.talkReady) return;
    if (this.talkActive()) {
      this.stopTalk(false);
      return;
    }
    this.unlockSpeech();
    void this.startTalk();
  }

  onVoiceWarming(data) {
    const engine = data?.engine === "piper" ? "text to speech" : "speech to text";
    showToast({
      icon: "info",
      title: "Preparing the " + engine + " engine",
      detail: "The first use builds its container and downloads the model, which can take a few minutes. Afterwards it answers in seconds.",
      timer: 15000,
    });
  }

  async startTalk() {
    if (this.talkHeld || this.recorder || this.transcribing) return;
    this.talkHeld = true;
    let stream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch {
      this.talkHeld = false;
      notifyError("The microphone is not available.");
      return;
    }
    // Released while the permission prompt stood: nothing to record.
    if (!this.talkHeld) {
      stream.getTracks().forEach((track) => track.stop());
      return;
    }
    // webm/opus where the browser has it; Safari records mp4/aac instead,
    // and the engine decodes either, so the default is good enough there.
    const preferred = "audio/webm;codecs=opus";
    let recorder;
    try {
      recorder = MediaRecorder.isTypeSupported?.(preferred)
        ? new MediaRecorder(stream, { mimeType: preferred })
        : new MediaRecorder(stream);
    } catch {
      stream.getTracks().forEach((track) => track.stop());
      this.talkHeld = false;
      notifyError("Recording is not supported here.");
      return;
    }
    const chunks = [];
    recorder.addEventListener("dataavailable", (event) => {
      if (event.data?.size) chunks.push(event.data);
    });
    recorder.addEventListener("stop", () => {
      stream.getTracks().forEach((track) => track.stop());
      const type = recorder.mimeType || "audio/webm";
      const send = this.talkSend;
      this.recorder = null;
      this.setTalkState("");
      const clip = new Blob(chunks, { type });
      if (!send || !clip.size) return;
      void this.transcribe(clip);
    });
    this.recorder = recorder;
    this.talkSend = false;
    recorder.start();
    this.setTalkState("recording");
  }

  stopTalk(abort) {
    this.talkHeld = false;
    if (!this.recorder) return;
    this.talkSend = !abort;
    if (this.recorder.state !== "inactive") this.recorder.stop();
  }

  // The recording state has to be readable at arm's length: the send button
  // goes solid red and wears the microphone while it records, the spinner
  // while the clip transcribes, and its plain blue send face otherwise. The
  // reset hands the disabled flag back to the length rule the composer's
  // input applies.
  setTalkState(state) {
    if (!this.sendButton) return;
    this.sendButton.classList.toggle("btn-danger", state === "recording");
    this.sendButton.classList.toggle("btn-primary", state !== "recording");
    this.sendButton.setAttribute("aria-pressed", state === "recording" ? "true" : "false");
    const icon = this.sendButton.querySelector("i");
    if (icon) icon.className = state === "recording" ? "ti ti-microphone" : state === "busy" ? "ti ti-send dc-icon-spinner" : "ti ti-send";
    // Each way of recording gets the way out it can actually take: a held
    // press slides, a hands free recording has no press to slide, so it is
    // told about Escape instead.
    if (state === "recording") this.showTalkHint(this.holdFired ? "slide" : "escape");
    else this.hideTalkHint();
    // Escape only means cancel while a recording runs. The listener lives
    // exactly as long as the recording, so the key keeps every other meaning
    // it has in the app the rest of the time.
    if (state === "recording") this.armTalkEscape();
    else this.disarmTalkEscape();
    if (state === "busy") this.sendButton.disabled = true;
    else this.onInput();
  }

  // The released clip becomes the message: transcribed, prepended with what
  // already stood in the box, and sent right away, so what was understood
  // stands in the transcript instead of waiting in the composer.
  async transcribe(clip) {
    this.transcribing = true;
    this.setTalkState("busy");
    try {
      const data = new FormData();
      data.append("audio", clip, clip.type.includes("mp4") ? "clip.mp4" : "clip.webm");
      const response = await fetch(this.sttUrl, {
        method: "POST",
        headers: csrfHeaders({ Accept: "application/json" }),
        body: data,
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || "The recording could not be transcribed.");
      const text = (payload.text || "").trim();
      if (!text) return;
      if (this.input) {
        const current = this.input.value.trim();
        this.input.value = current ? `${current} ${text}` : text;
      }
      await this.send();
    } catch (error) {
      notifyError(error.message || "The recording could not be transcribed.");
    } finally {
      this.transcribing = false;
      this.setTalkState("");
    }
  }

  voiceModeOn() {
    try {
      return localStorage.getItem(VOICE_MODE_KEY) === "on";
    } catch {
      return false;
    }
  }

  // The stored volume as a fraction for the audio element. Anything unreadable
  // or out of range reads as full, so a broken value never leaves a screen
  // silent with no way to tell.
  voiceVolume() {
    let stored = null;
    try {
      stored = localStorage.getItem(VOICE_VOLUME_KEY);
    } catch {
      stored = null;
    }
    const percent = Number(stored);
    if (stored === null || !Number.isFinite(percent) || percent < 0 || percent > 100) {
      return VOICE_VOLUME_DEFAULT / 100;
    }
    return percent / 100;
  }

  // The icon is the whole state display: a speaker while answers read
  // themselves aloud, a crossed one while they do not. No colour on the
  // button, the icon swap already says it.
  applyVoiceMode(on) {
    if (this.autoplayInput) this.autoplayInput.checked = on;
    if (!this.voiceButton) return;
    const icon = this.voiceButton.querySelector("i");
    if (icon) icon.className = on ? "ti ti-volume" : "ti ti-volume-off";
  }

  // The slider is `dc-assistant-volume`, which owns the row and the stored
  // value and says when it moved. Here only the sound follows. The row is the
  // element's own volume, no base under it: speech arrives peak normalised
  // (measured: peak 0 dB, mean -15 dB), so a full row is the file's own level.
  // The notification sound carries its own scale, scriptune keeps its master
  // gain at a tenth of the value it stores.
  applyVoiceVolume(fraction) {
    if (this.speech) this.speech.volume = fraction;
  }

  // The volume rides on the element's own volume, and deliberately not on a
  // gain node the way the notification jingles do: an element routed through
  // Web Audio only sounds through its graph, and iOS suspends an audio
  // context when the page goes to the background, so a spoken answer would
  // stop the moment the app is put away, while a plain element keeps playing.
  // A graph also runs a beat behind the element's clock and clips the first
  // and last words of an answer. The price is that iOS ignores a volume set
  // from script, the hardware buttons own it there, and the slider disables
  // itself on such devices. Should the slider ever have to work there, the
  // level belongs in the wav the server renders, not in a graph in front of
  // the speaker.
  ensureSpeech() {
    if (this.speech) return this.speech;
    this.speech = new Audio();
    this.speech.preload = "auto";
    this.speech.volume = this.voiceVolume();
    this.speech.addEventListener("ended", () => {
      this.speechUrl = null;
      this.syncSpeakButtons();
    });
    return this.speech;
  }

  // unlockSpeech spends a user gesture on the audio element: one muted play
  // inside the press, and the element may later start an answer on its own.
  unlockSpeech() {
    if (this.speechUnlocked || !this.hasAttribute("tts")) return;
    const speech = this.ensureSpeech();
    speech.muted = true;
    speech.src = SILENT_WAV;
    speech.play().then(() => {
      speech.pause();
      speech.muted = false;
      this.speechUnlocked = true;
    }).catch(() => {
      speech.muted = false;
    });
  }

  toggleSpeak(button) {
    const url = button.getAttribute("data-assistant-speak");
    if (!url) return;
    if (this.speech && this.speechUrl === url && !this.speech.paused) {
      this.stopSpeech();
      return;
    }
    void this.playSpeech(url, { announce: true });
  }

  // playSpeech starts one answer. The first ask renders the audio server
  // side, so the spinner may stand a while; play() resolves when sound
  // actually starts. An autoplay that the browser or the route refuses stays
  // quiet, a clicked speaker says what happened.
  async playSpeech(url, { announce } = {}) {
    const speech = this.ensureSpeech();
    // The volume is applied again here, so a slider moved while nothing
    // played still decides how loud this answer comes out.
    this.applyVoiceVolume(this.voiceVolume());
    speech.pause();
    this.speechUrl = url;
    this.speechLoading = url;
    this.syncSpeakButtons();
    speech.muted = false;
    speech.src = url;
    try {
      await speech.play();
      this.speechUnlocked = true;
    } catch {
      if (this.speechUrl === url) {
        this.speechUrl = null;
        if (announce) notifyError("The answer could not be spoken.");
      }
    } finally {
      if (this.speechLoading === url) this.speechLoading = null;
      this.syncSpeakButtons();
    }
  }

  stopSpeech() {
    if (!this.speech) return;
    this.speech.pause();
    this.speech.currentTime = 0;
    this.speechUrl = null;
    this.syncSpeakButtons();
  }

  // One painter for every speaker button, off the current playback state, so
  // a replaced message fragment falls back into the right picture too.
  syncSpeakButtons() {
    for (const button of this.querySelectorAll("[data-assistant-speak]")) {
      const url = button.getAttribute("data-assistant-speak");
      const icon = button.querySelector("i");
      if (!icon) continue;
      if (this.speechLoading && url === this.speechLoading) icon.className = "ti ti-volume dc-icon-spinner";
      else if (this.speech && url === this.speechUrl && !this.speech.paused) icon.className = "ti ti-player-stop";
      else icon.className = "ti ti-volume";
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
    const now = Date.now();
    this.ownTops = this.ownTops.filter((entry) => now - entry.at < 500);
    const top = this.scrollPosition();
    if (this.ownTops.some((entry) => Math.abs(top - entry.top) <= 1)) return;
    const remaining = this.scrollLength() - top - this.viewportHeight();
    this.pinned = remaining < 120;
  }

  // stickToEnd holds the transcript at its end while the reader is following
  // it, and nowhere at all once they scrolled away. The bottom is written
  // down before anything else, on every call: this is the moment the layout
  // may clamp the scroller there on its own, and that clamp must never read
  // as the reader.
  stickToEnd() {
    this.notePlace(this.scrollLength() - this.viewportHeight());
    if (!this.pinned) return;
    if (this.scrollQueued) return;
    this.scrollQueued = true;
    window.requestAnimationFrame(() => {
      this.scrollQueued = false;
      if (!this.pinned) return;
      // Scrolling smoothly never catches up with a growing answer: every
      // delta would start a new animation from where the last one got to.
      // Following the end of a transcript is a jump.
      this.maskedJump(() => this.scrollToPosition(this.scrollLength()));
    });
  }
}

customElements.define("dc-assistant", Assistant);
