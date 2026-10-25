import { ChatStream, appendLive, showTool, writeLive } from "@dc/chat-stream";
import { ChatFollow } from "@dc/chat-follow";
import { resolveHostlessLinks } from "@dc/docker";
import { markWorking } from "@dc/dom";

// A bubble that stands keeps what the guest unfolded in it until it moves:
// its state, or the questions it asks, which end while its state holds.
function settled(bubble) {
  return `${bubble.dataset.state}:${bubble.querySelector("[data-choices-state]")?.dataset.choicesState || ""}`;
}

class ShareChat extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.scroller = this.querySelector("[data-share-scroll]");
    this.thread = this.querySelector("[data-share-thread]");
    this.headIcon = this.querySelector("[data-share-head-icon]");
    if (!this.scroller || !this.thread) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.live = null;
    this.follow = new ChatFollow(this.scroller, { button: this.querySelector("[data-chat-end]"), signal });
    resolveHostlessLinks(this.thread);
    this.follow.toEnd();
    this.stream = new ChatStream(this.getAttribute("stream-url"), {
      onFrame: (frame) => this.onFrame(frame),
      onOpen: () => {
        this.live = null;
        void this.pull();
      },
      onClosed: () => this.end(),
    });
    this.stream.open();
    document.addEventListener("visibilitychange", () => this.stream?.check(), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    this.stream?.close();
    this.stream = null;
  }

  onFrame(frame) {
    switch (frame.kind) {
      case "start":
        this.live = { id: frame.messageId, html: frame.html || "", text: frame.text || "" };
        if (this.headIcon) markWorking(this.headIcon, Number(frame.since || this.headIcon.dataset.workingSince) || 0);
        this.paint(true);
        break;
      case "html":
        if (this.live?.id !== frame.messageId) break;
        this.live.html = frame.html || "";
        this.live.text = "";
        this.paint(true);
        break;
      case "delta": {
        if (this.live?.id !== frame.messageId) break;
        const text = frame.text || "";
        this.live.text += text;
        const body = this.liveBody();
        if (body) appendLive(body, text);
        break;
      }
      case "tool":
        showTool(this.thread.querySelector(`[data-message-id="${CSS.escape(frame.messageId)}"]`), frame.text);
        break;
      case "end":
        this.live = null;
        if (this.headIcon) markWorking(this.headIcon, 0);
        void this.pull();
        break;
      case "message":
      case "gone":
        void this.pull();
        break;
    }
  }

  liveBody() {
    return this.thread.querySelector(`[data-message-id="${CSS.escape(this.live.id)}"] [data-assistant-text]`);
  }

  paint(pullMissing) {
    if (!this.live) return;
    const body = this.liveBody();
    if (body) {
      writeLive(body, this.live.html, this.live.text);
      resolveHostlessLinks(body);
    } else if (pullMissing) {
      void this.pull();
    }
  }

  async pull() {
    if (this.pulling) {
      this.again = true;
      return;
    }
    this.pulling = true;
    try {
      const response = await fetch(this.getAttribute("page-url"), { cache: "no-store", headers: { Accept: "text/html" }, signal: this.ac?.signal });
      if (!response.ok) {
        this.end();
        return;
      }
      const html = await response.text();
      if (!this.ac) return;
      this.apply(html);
      this.paint(false);
    } catch (error) {
      void error;
    } finally {
      this.pulling = false;
      if (this.again && this.ac) {
        this.again = false;
        void this.pull();
      }
    }
  }

  apply(html) {
    const doc = new DOMParser().parseFromString(html, "text/html");
    const fresh = doc.querySelector("[data-share-log]");
    const log = this.thread.querySelector("[data-share-log]");
    if (!fresh || !log) return;
    const standing = new Map([...log.children].map((node) => [node.dataset.messageId, node]));
    const nodes = [...fresh.children].map((node) => {
      const old = standing.get(node.dataset.messageId);
      return old && settled(old) === settled(node) ? old : document.adoptNode(node);
    });
    log.replaceChildren(...nodes);
    resolveHostlessLinks(log);
    this.thread.querySelector("[data-share-empty]")?.toggleAttribute("hidden", nodes.length > 0);
    const head = doc.querySelector("[data-share-head-icon]");
    if (this.headIcon && head) markWorking(this.headIcon, head.classList.contains("working") ? Number(head.dataset.workingSince) || 0 : 0);
  }

  end() {
    this.stream?.close();
    this.stream = null;
    this.querySelector("[data-share-ended]")?.removeAttribute("hidden");
  }
}

customElements.define("dc-share-chat", ShareChat);
