// Single server to client event stream. One EventSource to /events carries every
// server push as a {type, data} envelope under the SSE event name "dc"; each frame
// is re-dispatched as a dc:<type> CustomEvent on document, so any custom element
// subscribes with onServerEvent("terminals", handler, { signal }) and reacts live.
//
// The connection lives in module scope, so it survives pe.js page swaps untouched
// (only the elements around it re-mount) and never opens twice. The module loads
// on demand the first time a component imports it; the notification bell imports
// it and sits on every app page, so the stream is up wherever a coder or shell lives.

const STREAM_URL = "/events";
// A dead-but-not-closed socket (a frozen background tab, a silently dropped NAT
// mapping) never fires an error, so the watchdog treats the stream as stale when no
// frame arrives within STALE_MS. The server sends a "ping" frame every 15s, so a
// live stream refreshes lastFrameAt well inside the window; STALE_MS leaves room for
// one missed ping. WATCHDOG_MS is how often the timer re-checks.
const STALE_MS = 45000;
const WATCHDOG_MS = 15000;
let source = null;
let lastFrameAt = 0;

function open() {
  if (source) return;
  lastFrameAt = Date.now();
  source = new EventSource(STREAM_URL);
  source.addEventListener("open", () => { lastFrameAt = Date.now(); });
  source.addEventListener("dc", (event) => {
    lastFrameAt = Date.now();
    let message;
    try {
      message = JSON.parse(event.data);
    } catch (error) {
      void error;
      return;
    }
    // "ping" only exists to prove the stream is alive (see lastFrameAt above); it
    // carries no state, so it dispatches no DOM event.
    if (message && message.type && message.type !== "ping") {
      document.dispatchEvent(new CustomEvent("dc:" + message.type, { detail: message.data ?? null }));
    }
  });
}

function reconnect() {
  source?.close();
  source = null;
  open();
}

// EventSource reconnects on its own after a clean drop, but a tab frozen in the
// background (a locked phone) or a stream that went silently dead surfaces without an
// error. Reopen it on wake when the socket is closed or has gone stale, so the
// server's fresh snapshot lands and the page catches up.
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  if (!source || source.readyState === EventSource.CLOSED || Date.now() - lastFrameAt > STALE_MS) reconnect();
});

// Background watchdog: while the tab is visible, force a reconnect once the stream
// has been silent past STALE_MS. CONNECTING is skipped so a reconnect in progress
// gets time to land before another is triggered.
setInterval(() => {
  if (document.hidden || !source) return;
  if (source.readyState === EventSource.CONNECTING) return;
  if (Date.now() - lastFrameAt > STALE_MS) reconnect();
}, WATCHDOG_MS);

open();

// onServerEvent subscribes to one server event type. Pass an AbortController
// signal in options so the listener tears down with the element; the returned
// unsubscribe is there for callers that manage teardown by hand.
export function onServerEvent(type, handler, options) {
  const name = "dc:" + type;
  document.addEventListener(name, handler, options);
  return () => document.removeEventListener(name, handler, options);
}
