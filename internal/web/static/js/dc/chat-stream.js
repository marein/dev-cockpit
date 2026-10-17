// The live side of an assistant's conversation, the owner's page and a
// guest's alike: the stream they follow and the answer written into its
// bubble while it grows.

// An assistant's stream sends a ping frame every 15s, the same life sign
// /events carries, because the SSE keepalive is a comment and fires no event
// here. Past this the stream counts as dead, with room for one missed ping.
const STALE_MS = 45000;

const RENDER_MARK = "⁠";

export class ChatStream {
  constructor(url, { onFrame, onOpen = () => {}, onClosed = () => {} }) {
    this.url = url;
    this.onFrame = onFrame;
    this.onOpen = onOpen;
    this.onClosed = onClosed;
    this.source = null;
    this.lastFrameAt = 0;
  }

  open() {
    if (!this.url || this.source) return;
    this.lastFrameAt = Date.now();
    const source = new EventSource(this.url);
    this.source = source;
    // Every connection that had to be established again is a break, whether
    // this stream built it or the browser retried on its own, and a break is
    // where a frame goes missing. That is the moment to catch up.
    source.addEventListener("open", () => {
      this.lastFrameAt = Date.now();
      this.onOpen();
    });
    source.addEventListener("assistant", (event) => {
      let frame;
      try {
        frame = JSON.parse(event.data);
      } catch {
        return;
      }
      if (!frame) return;
      this.lastFrameAt = Date.now();
      if (frame.kind !== "ping") this.onFrame(frame);
    });
    source.addEventListener("error", () => {
      if (source.readyState === EventSource.CLOSED) this.onClosed();
    });
  }

  close() {
    this.source?.close();
    this.source = null;
  }

  touch() {
    this.lastFrameAt = Date.now();
  }

  // A socket that hangs open and silent, the classic phone that just woke up,
  // never reports itself closed, and silence alone says nothing: an answer is
  // silent while the model thinks. The ping is what separates the two, so the
  // stream is only torn down and built again once that life sign stops.
  check() {
    if (!this.source) return;
    if (this.source.readyState === EventSource.CLOSED || Date.now() - this.lastFrameAt > STALE_MS) this.restart();
  }

  restart() {
    if (document.visibilityState !== "visible") return;
    this.close();
    this.open();
  }
}

// writeLive puts the answer so far into its bubble's body: the prefix the
// server rendered, and the text that arrived since where the renderer left
// RENDER_MARK, behind the prefix when a render swallowed the mark.
export function writeLive(body, html, text) {
  body.innerHTML = html || "";
  liveTail(body).textContent = text || "";
}

export function appendLive(body, text) {
  liveTail(body).append(document.createTextNode(text));
}

// showTool names the tool an answer works with under its bubble.
export function showTool(bubble, name) {
  const line = bubble?.querySelector("[data-assistant-tool]");
  if (!line) return false;
  line.classList.remove("d-none");
  const label = line.querySelector("[data-assistant-tool-name]");
  if (label) label.textContent = name || "";
  return true;
}

function liveTail(body) {
  let tail = body.querySelector("[data-assistant-tail]");
  if (!tail) {
    tail = document.createElement("span");
    tail.setAttribute("data-assistant-tail", "");
    const spot = takeRenderMark(body);
    if (spot) spot.parentNode.insertBefore(tail, spot);
    else body.append(tail);
  }
  return tail;
}

function takeRenderMark(root) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let node = null;
  let at = -1;
  for (let cursor = walker.nextNode(); cursor; cursor = walker.nextNode()) {
    const found = cursor.data.lastIndexOf(RENDER_MARK);
    if (found >= 0) {
      node = cursor;
      at = found;
    }
  }
  if (!node) return null;
  const rest = node.splitText(at);
  rest.data = rest.data.slice(RENDER_MARK.length);
  return rest;
}
