import { notifyError, notifySuccess, notifyInfo } from "@dc/toast";
import { postForm } from "@dc/http";
import { get } from "@dc/store";

function initTerminalAttach(host) {
  const streamUrl = host.getAttribute("stream-url");
  const resizeUrl = host.getAttribute("resize-url");
  const scrollHistory = host.hasAttribute("scroll-history");

  const ac = new AbortController();
  const signal = ac.signal;
  const listen = (target, type, handler, opts) => {
    if (target) target.addEventListener(type, handler, { ...opts, signal });
  };

  const terminalElement = host;
  const streamRefreshButtons = document.querySelectorAll("[data-terminal-refresh]");

  const fontSizeSetting = document.querySelector('terminal-setting-select[setting="font-size"]');
  const rowsSetting = document.querySelector('terminal-setting-select[setting="rows"]');

  const DEFAULT_FONT_SIZE = 14;
  const DEFAULT_ROWS = 30;
  const FOREGROUND = "#f9fafb";

  // Touch clients keep the read-only mirror: input comes from the on-screen
  // controls and the prompt box, so stdin stays disabled and the terminal never
  // takes focus. Pointer-precise (desktop) clients type straight into the
  // terminal: xterm encodes every keystroke, paste and composed accent into the
  // exact terminal byte stream, which we forward through the controls module.
  const interactiveInput = !window.matchMedia("(pointer: coarse)").matches;
  const term = new Terminal({
    allowTransparency: true,
    disableStdin: !interactiveInput,
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, monospace",
    fontSize: DEFAULT_FONT_SIZE,
    scrollback: 0,
    theme: {
      background: "#111827",
      foreground: FOREGROUND,
      selectionBackground: "#3b82f6",
      selectionForeground: "#f9fafb",
    },
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(terminalElement);

  // ---- Direct keyboard input (desktop) ---------------------------------------
  // xterm.onData yields the terminal byte sequence the program expects for every
  // key, modifier combo, paste and dead-key/IME accent (é, ñ, …). We post it as
  // raw input; the server injects the bytes verbatim via `tmux send-keys -H`.
  if (interactiveInput) {
    // The read-only stylesheet hides xterm's helper textarea; un-hide it (via a
    // class) so the terminal can take focus and receive keystrokes.
    terminalElement.classList.add("attach-terminal-interactive");
    // Ctrl/Cmd+C with a selection copies through the browser instead of sending
    // ^C; everything else goes to the program (Ctrl+C with no selection stays
    // SIGINT, and paste keeps flowing through xterm's own paste handling).
    term.attachCustomKeyEventHandler((event) => {
      if (event.type === "keydown" && (event.ctrlKey || event.metaKey)
        && (event.key === "c" || event.key === "C") && term.hasSelection()) {
        return false;
      }
      return true;
    });
    term.onData((data) => {
      if (data) {
        document.dispatchEvent(new CustomEvent("terminal-input", { detail: { raw: data } }));
      }
    });
    term.focus();
  }

  const cursorMetrics = () => {
    const textarea = terminalElement.querySelector(".xterm-helper-textarea");
    const textareaRect = textarea ? textarea.getBoundingClientRect() : null;
    if (textareaRect && textareaRect.height > 0) {
      return { top: textareaRect.top, bottom: textareaRect.bottom, cell: textareaRect.height };
    }
    const screen = terminalElement.querySelector(".xterm-screen");
    if (screen && term.rows >= 1) {
      const rect = screen.getBoundingClientRect();
      const cell = rect.height / term.rows;
      const top = rect.top + term.buffer.active.cursorY * cell;
      return { top, bottom: top + cell, cell };
    }
    return null;
  };
  const visibleBand = () => {
    let top = 0;
    let bottom = window.innerHeight;
    const tabs = document.querySelector("terminal-tabs");
    if (tabs && tabs.offsetHeight > 0) {
      const position = window.getComputedStyle(tabs).position;
      if (position === "sticky" || position === "fixed") {
        top = tabs.offsetHeight;
      }
    }
    const footer = document.querySelector(".attach-footer");
    if (footer && footer.offsetHeight > 0) {
      const position = window.getComputedStyle(footer).position;
      if (position === "sticky" || position === "fixed") {
        bottom = window.innerHeight - footer.offsetHeight;
      }
    }
    return { top, bottom };
  };
  const isTerminalFocused = () => Boolean(terminalElement.querySelector(".xterm.focus"));
  const followCursor = () => {
    const cursor = cursorMetrics();
    if (!cursor) {
      return;
    }
    const band = visibleBand();
    const margin = cursor.cell * 2;
    let delta = 0;
    if (cursor.bottom + margin > band.bottom) {
      delta = cursor.bottom + margin - band.bottom;
    } else if (cursor.top - margin < band.top) {
      delta = cursor.top - margin - band.top;
    }
    if (delta !== 0) {
      window.scrollBy(0, delta);
    }
  };
  let followScheduled = false;
  term.onCursorMove(() => {
    if (followScheduled || !isTerminalFocused()) {
      return;
    }
    followScheduled = true;
    window.requestAnimationFrame(() => {
      followScheduled = false;
      followCursor();
    });
  });

  // Terminal modes the wheel handler needs. xterm tracks them live once it sees
  // the program's escape sequences, but those are absent from a screen snapshot,
  // so when attaching to an already-running program the server fills these in
  // from tmux (see the terminal-size event). We OR the two at use time.
  const serverModes = { altScreen: false, appCursor: false };

  // Canvas renderer: box-drawing and block glyphs are drawn from xterm's
  // built-in customGlyphs (pixel-aligned, seamless) instead of the browser font,
  // which renders them with gaps/offsets that differ between browsers. Renderer
  // addons must be loaded after term.open; fall back to the DOM renderer.
  try {
    if (window.CanvasAddon && window.CanvasAddon.CanvasAddon) {
      term.loadAddon(new window.CanvasAddon.CanvasAddon());
    }
  } catch (rendererError) {
    void rendererError;
  }

  const openWebLink = (event, uri) => {
    event.preventDefault();
    window.open(uri, "_blank", "noopener,noreferrer");
  };
  if (window.WebLinksAddon && window.WebLinksAddon.WebLinksAddon) {
    term.loadAddon(new window.WebLinksAddon.WebLinksAddon(openWebLink));
  }

  // ---- Mouse reporting -------------------------------------------------------
  // When a full-screen CLI enables mouse tracking (DECSET ?1000/1002/1003 and
  // friends, which tmux forwards because its own `mouse` option is off), xterm
  // would start capturing mouse-down and wheel events — breaking text selection
  // and turning the cursor into a pointer. We swallow the mouse-mode set/reset
  // sequences so xterm never activates mouse reporting, keeping the mirror
  // selectable. But we record the program's intent: a program that asked for
  // mouse reporting (e.g. claude) scrolls via wheel events, so the wheel handler
  // below synthesizes those itself instead of sending cursor keys.
  const MOUSE_MODES = new Set([1000, 1001, 1002, 1003, 1004, 1005, 1006, 1015, 1016]);
  const mouse = { tracking: false, sgr: false };
  const handleMouseMode = (on) => (params) => {
    if (!(params.length > 0
      && params.every((param) => MOUSE_MODES.has(Array.isArray(param) ? param[0] : param)))) {
      return false; // not purely a mouse-mode sequence; let xterm handle it
    }
    for (const param of params) {
      const code = Array.isArray(param) ? param[0] : param;
      if (code === 1000 || code === 1002 || code === 1003) {
        mouse.tracking = on;
      } else if (code === 1006) {
        mouse.sgr = on;
      }
    }
    return true; // swallow so xterm stays passive (selection preserved)
  };
  term.parser.registerCsiHandler({ prefix: "?", final: "h" }, handleMouseMode(true));
  term.parser.registerCsiHandler({ prefix: "?", final: "l" }, handleMouseMode(false));

  // ---- Scrolling (desktop wheel + mobile swipe) ------------------------------
  // We own scrolling and reproduce, per step, exactly what the program would
  // receive from a terminal emulator over SSH:
  //   - mouse-reporting programs (claude): a mouse-wheel event (one line);
  //   - other alternate-screen TUIs: a cursor key (vim/less) or PageUp/PageDown
  //     (copilot, which uses the cursor keys for prompt history);
  //   - a shell prompt: drive the tmux history (arrow keys would walk history).
  // The desktop wheel and the mobile swipe both feed the same per-program step,
  // so they scroll identically — the swipe just maps finger travel to steps.
  {
    const WHEEL_PIXELS_PER_NOTCH = 100;
    const WHEEL_LINES_PER_NOTCH = 3;
    let wheelAccum = 0;

    const sendRaw = (seq) =>
      document.dispatchEvent(new CustomEvent("terminal-input", { detail: { raw: seq } }));
    const sendControl = (control) =>
      document.dispatchEvent(new CustomEvent("terminal-control", { detail: { control } }));

    const scrollCell = (clientX, clientY) => {
      const screen = terminalElement.querySelector(".xterm-screen");
      if (!screen || term.cols < 1 || term.rows < 1 || typeof clientX !== "number" || typeof clientY !== "number") {
        return { col: 1, row: 1 };
      }
      const rect = screen.getBoundingClientRect();
      const col = Math.min(term.cols, Math.max(1, Math.floor((clientX - rect.left) / (rect.width / term.cols)) + 1));
      const row = Math.min(term.rows, Math.max(1, Math.floor((clientY - rect.top) / (rect.height / term.rows)) + 1));
      return { col, row };
    };

    const scrollStep = (down, clientX, clientY) => {
      if (mouse.tracking) {
        const { col, row } = scrollCell(clientX, clientY);
        const button = down ? 65 : 64; // SGR/legacy wheel-down / wheel-up
        const seq = mouse.sgr
          ? "\x1b[<" + button + ";" + col + ";" + row + "M"
          : "\x1b[M" + String.fromCharCode(32 + button, 32 + Math.min(col, 223), 32 + Math.min(row, 223));
        sendRaw(seq);
        return;
      }
      const alt = serverModes.altScreen || term.buffer.active.type === "alternate";
      if (alt) {
        if (scrollHistory) {
          const app = serverModes.appCursor || Boolean(term.modes && term.modes.applicationCursorKeysMode);
          sendRaw(down ? (app ? "\x1bOB" : "\x1b[B") : (app ? "\x1bOA" : "\x1b[A"));
        } else {
          sendRaw(down ? "\x1b[6~" : "\x1b[5~");
        }
        return;
      }
      if (scrollHistory) {
        sendControl(down ? "scroll-line-down" : "scroll-line-up");
      }
    };

    if (interactiveInput) {
      listen(terminalElement, "wheel", (event) => {
        if (event.ctrlKey) {
          return; // leave pinch-zoom to the browser
        }
        event.preventDefault();
        event.stopPropagation();
        let notches;
        if (event.deltaMode === 1) {
          notches = event.deltaY / WHEEL_LINES_PER_NOTCH;
        } else if (event.deltaMode === 2) {
          notches = event.deltaY;
        } else {
          notches = event.deltaY / WHEEL_PIXELS_PER_NOTCH;
        }
        wheelAccum += notches;
        while (wheelAccum >= 1) {
          wheelAccum -= 1;
          scrollStep(true, event.clientX, event.clientY);
        }
        while (wheelAccum <= -1) {
          wheelAccum += 1;
          scrollStep(false, event.clientX, event.clientY);
        }
      }, { capture: true, passive: false });
    }

    // Mobile swipe (terminal-scroll-zone): the zone streams finger travel as
    // pixel deltas (drag and fling alike), accumulated here and converted into
    // per-program steps so content tracks the finger 1:1. One step moves one
    // line of content, so a line step costs one cell height of travel; the
    // page-per-step pager (copilot) moves a screen per step and costs half a
    // screen of travel per page.
    // Anchor the synthesized wheel at the content centre rather than the finger,
    // so mouse-reporting programs (claude) always get it over their scrollable
    // area like a desktop wheel — a finger near the input row would otherwise
    // land the first reversal event on a non-scrolling cell and swallow it.
    const contentCentre = () => {
      const screen = terminalElement.querySelector(".xterm-screen");
      if (!screen) {
        return null;
      }
      const rect = screen.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
    };
    const swipeStepPx = () => {
      const screen = terminalElement.querySelector(".xterm-screen");
      const cell = screen && term.rows >= 1 ? screen.clientHeight / term.rows : 17;
      const alt = serverModes.altScreen || term.buffer.active.type === "alternate";
      if (!mouse.tracking && alt && !scrollHistory && screen) {
        return Math.max(cell, screen.clientHeight / 2);
      }
      return Math.max(cell, 4);
    };
    let swipeAccum = 0;
    listen(document, "terminal-scroll", (event) => {
      const detail = event.detail || {};
      if (detail.begin) {
        swipeAccum = 0;
      }
      const dy = Number(detail.dy) || 0;
      if (dy === 0) {
        return;
      }
      swipeAccum -= dy; // finger up = toward newer output = scroll down
      const stepPx = swipeStepPx();
      const centre = contentCentre() || { x: detail.clientX, y: detail.clientY };
      while (swipeAccum >= stepPx) {
        swipeAccum -= stepPx;
        scrollStep(true, centre.x, centre.y);
      }
      while (swipeAccum <= -stepPx) {
        swipeAccum += stepPx;
        scrollStep(false, centre.x, centre.y);
      }
    });
  }

  // ---- Cursor ----------------------------------------------------------------
  // The canvas renderer only paints the terminal cursor while the element is
  // focused, which the read-only mirror never is. Both supported CLIs use the
  // hardware cursor (they never draw their own), so we render a single block
  // that mirrors xterm's cursor cell. The server positions the cursor in every
  // snapshot, and the program's own escapes keep it current in the live stream.
  // When a desktop client focuses the terminal to type, xterm paints the real
  // cursor and the stylesheet hides this overlay so the two never double up.
  const cursorOverlay = document.createElement("div");
  cursorOverlay.className = "attach-cursor";
  const syncCursor = () => {
    const screen = terminalElement.querySelector(".xterm-screen");
    if (!screen || term.cols < 1 || term.rows < 1) {
      return;
    }
    if (cursorOverlay.parentElement !== screen) {
      screen.appendChild(cursorOverlay);
    }
    const cellWidth = screen.clientWidth / term.cols;
    const cellHeight = screen.clientHeight / term.rows;
    cursorOverlay.style.width = cellWidth + "px";
    cursorOverlay.style.height = cellHeight + "px";
    cursorOverlay.style.transform =
      "translate(" + term.buffer.active.cursorX * cellWidth + "px, " +
      term.buffer.active.cursorY * cellHeight + "px)";
  };
  term.onCursorMove(syncCursor);
  term.onRender(syncCursor);

  let onInitialSnapshot = null;
  if (!interactiveInput) {
    const cursorInput = document.createElement("input");
    cursorInput.type = "text";
    cursorInput.id = "terminal-cursor-input";
    cursorInput.className = "attach-cursor-input";
    cursorInput.setAttribute("autocomplete", "off");
    cursorInput.setAttribute("autocorrect", "off");
    cursorInput.setAttribute("autocapitalize", "none");
    cursorInput.setAttribute("spellcheck", "false");
    cursorInput.setAttribute("aria-hidden", "true");
    cursorInput.tabIndex = -1;

    const placeCursorInput = () => {
      const screen = terminalElement.querySelector(".xterm-screen");
      if (!screen || term.cols < 1 || term.rows < 1) {
        return;
      }
      if (cursorInput.parentElement !== screen) {
        screen.appendChild(cursorInput);
      }
      const cellWidth = screen.clientWidth / term.cols;
      const cellHeight = screen.clientHeight / term.rows;
      const footer = document.querySelector(".attach-footer");
      cursorInput.style.width = cellWidth + "px";
      cursorInput.style.height = cellHeight + "px";
      cursorInput.style.left = term.buffer.active.cursorX * cellWidth + "px";
      cursorInput.style.top = (term.buffer.active.cursorY + 2) * cellHeight + "px";
      cursorInput.style.scrollMarginBottom = (footer ? footer.offsetHeight : 0) + "px";
    };
    placeCursorInput();
    term.onRender(placeCursorInput);

    let anchorScheduled = false;
    const scheduleAnchorIntoView = () => {
      if (anchorScheduled || document.activeElement !== cursorInput) {
        return;
      }
      anchorScheduled = true;
      window.requestAnimationFrame(() => {
        anchorScheduled = false;
        cursorInput.scrollIntoView({ block: "nearest", behavior: "instant" });
      });
    };
    term.onCursorMove(() => {
      placeCursorInput();
      scheduleAnchorIntoView();
    });
    if (window.visualViewport) {
      listen(window.visualViewport, "resize", scheduleAnchorIntoView);
    }

    listen(cursorInput, "focus", () => {
      placeCursorInput();
      scheduleAnchorIntoView();
    });

    let initialScrolled = false;
    onInitialSnapshot = () => {
      if (initialScrolled) {
        return;
      }
      initialScrolled = true;
      window.requestAnimationFrame(() => {
        placeCursorInput();
        cursorInput.scrollIntoView({ block: "nearest", behavior: "instant" });
      });
    };
  }

  // ---- Selection layer -------------------------------------------------------
  // The canvas renderer draws glyphs to a bitmap, so there is no selectable DOM
  // text: touch devices have nothing to long-press and never raise the native
  // copy menu. We mirror the visible buffer into a transparent text layer that
  // sits over the canvas and follows the cell grid (line height per row,
  // letter-spacing so each glyph occupies one cell). It is inert on the desktop
  // (xterm's own mouse selection keeps working) and only becomes selectable in
  // the mobile copy mode, where it yields a native selection and copy menu.
  const selectionLayer = document.createElement("div");
  selectionLayer.className = "attach-selection";
  selectionLayer.setAttribute("aria-hidden", "true");
  let advanceWidth = 0;
  let advanceFontSize = 0;
  const measureAdvance = (fontSize) => {
    if (advanceFontSize === fontSize && advanceWidth > 0) {
      return advanceWidth;
    }
    const probe = document.createElement("span");
    probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;" +
      "letter-spacing:0;font-size:" + fontSize + "px;";
    probe.style.fontFamily = term.options.fontFamily;
    probe.textContent = "0".repeat(64);
    terminalElement.appendChild(probe);
    advanceWidth = probe.getBoundingClientRect().width / 64;
    advanceFontSize = fontSize;
    probe.remove();
    return advanceWidth;
  };
  const syncSelection = () => {
    const screen = terminalElement.querySelector(".xterm-screen");
    if (!screen || term.cols < 1 || term.rows < 1) {
      return;
    }
    if (selectionLayer.parentElement !== screen) {
      screen.appendChild(selectionLayer);
    }
    const cellWidth = screen.clientWidth / term.cols;
    const cellHeight = screen.clientHeight / term.rows;
    const fontSize = term.options.fontSize;
    selectionLayer.style.fontFamily = term.options.fontFamily;
    selectionLayer.style.fontSize = fontSize + "px";
    selectionLayer.style.lineHeight = cellHeight + "px";
    selectionLayer.style.letterSpacing = (cellWidth - measureAdvance(fontSize)) + "px";
    // Never rewrite the text while it holds the user's active selection, or the
    // next frame of output would discard the range they are about to copy.
    const selection = window.getSelection ? window.getSelection() : null;
    if (selection && !selection.isCollapsed && selectionLayer.contains(selection.anchorNode)) {
      return;
    }
    const buffer = term.buffer.active;
    const lines = [];
    for (let row = 0; row < term.rows; row += 1) {
      const line = buffer.getLine(buffer.viewportY + row);
      lines.push(line ? line.translateToString(true) : "");
    }
    const text = lines.join("\n");
    if (selectionLayer.textContent !== text) {
      selectionLayer.textContent = text;
    }
  };
  term.onRender(syncSelection);

  // ---- Copy ------------------------------------------------------------------
  const terminalSelection = () => {
    if (!term.hasSelection()) {
      return "";
    }
    const domSelection = window.getSelection ? window.getSelection().toString() : "";
    if (domSelection) {
      return "";
    }
    return term.getSelection();
  };

  listen(document, "copy", (event) => {
    const selection = terminalSelection();
    if (!selection) {
      return;
    }
    event.clipboardData?.setData("text/plain", selection);
    event.preventDefault();
  });

  listen(terminalElement, "contextmenu", (event) => {
    const selection = terminalSelection();
    if (!selection || !navigator.clipboard) {
      return;
    }
    void navigator.clipboard.writeText(selection).catch(() => {});
    event.preventDefault();
  });

  // ---- Sizing ----------------------------------------------------------------
  // Read the persisted value straight from storage: terminal-setting-select is
  // lazy loaded and may not have upgraded yet, so its .value getter can be absent.
  const settingValue = (el, fallback) => {
    if (!el) return fallback;
    return (parseInt(get(el.getAttribute("storage-key") || "", ""), 10)
      || parseInt(el.getAttribute("default-value") || "", 10)
      || fallback);
  };
  let fontSizeOverride = settingValue(fontSizeSetting, DEFAULT_FONT_SIZE);
  let rowsOverride = settingValue(rowsSetting, DEFAULT_ROWS);
  let lastClientCols = 0;
  let lastClientRows = rowsOverride;
  let measureElementObserver = null;

  // xterm appends offscreen measurement <div>s to document.body; reparent them
  // into the terminal so they inherit its font and measure cells correctly.
  const isOffscreenMeasureElement = (element) => (
    element instanceof HTMLDivElement
    && element.parentElement === document.body
    && element.style.position === "absolute"
    && element.style.top === "-50000px"
    && element.style.width === "50000px"
    && element.style.whiteSpace === "pre"
  );
  const hostMeasureElement = (element) => {
    if (isOffscreenMeasureElement(element)) {
      terminalElement.appendChild(element);
    }
  };
  const hostMeasureElements = () => {
    for (const child of document.body.children) {
      hostMeasureElement(child);
    }
  };

  const waitForLayout = async () => {
    if (document.fonts?.ready) {
      try {
        await Promise.race([
          document.fonts.ready,
          new Promise((resolve) => window.setTimeout(resolve, 250)),
        ]);
      } catch (error) {
        void error;
      }
    }
    await new Promise((resolve) => window.requestAnimationFrame(() => window.requestAnimationFrame(resolve)));
  };

  const resolveClientSize = async () => {
    term.options.fontSize = fontSizeOverride;
    await waitForLayout();
    const dims = fitAddon.proposeDimensions();
    const cols = Math.max(2, (dims && dims.cols) ? dims.cols : lastClientCols || term.cols || 2);
    const rows = Math.max(2, rowsOverride || lastClientRows || DEFAULT_ROWS);
    lastClientCols = cols;
    lastClientRows = rows;
    return { cols, rows };
  };

  // ---- Stream ----------------------------------------------------------------
  let followOutput = true;
  let source = null;
  let resizeTimer = null;
  let refreshTimer = null;
  let followSettingResize = 0;
  const followAfterSettingSnapshot = () => {
    if (!followSettingResize) {
      return;
    }
    const fresh = Date.now() - followSettingResize <= 5000;
    followSettingResize = 0;
    if (fresh) {
      followCursor();
    }
  };
  const setRefreshing = (on) => {
    for (const button of streamRefreshButtons) {
      if (on) {
        button.dataset.refreshing = "true";
      } else {
        delete button.dataset.refreshing;
      }
    }
  };
  const clearRefreshTimer = () => {
    if (refreshTimer !== null) {
      window.clearTimeout(refreshTimer);
      refreshTimer = null;
    }
    setRefreshing(false);
  };

  const scrollPanelToBottom = () => {
    terminalElement.scrollTop = terminalElement.scrollHeight;
  };
  const syncFollowOutput = () => {
    followOutput = terminalElement.scrollTop + terminalElement.clientHeight >= terminalElement.scrollHeight - 4;
  };

  const decodeChunk = (encodedChunk) => {
    if (!encodedChunk) {
      return new Uint8Array();
    }
    const binary = atob(encodedChunk);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }
    return bytes;
  };

  const writeChunk = (encodedChunk, reset) => {
    const shouldFollow = followOutput;
    if (reset) {
      term.reset();
    }
    const chunk = decodeChunk(encodedChunk);
    if (chunk.length === 0) {
      if (shouldFollow) {
        scrollPanelToBottom();
      }
      if (reset) {
        onInitialSnapshot?.();
        followAfterSettingSnapshot();
      }
      return;
    }
    term.write(chunk, () => {
      if (shouldFollow) {
        scrollPanelToBottom();
      }
      if (reset) {
        onInitialSnapshot?.();
        followAfterSettingSnapshot();
      }
    });
  };

  const connectStream = (cols, rows, userRefresh) => {
    if (source) {
      source.close();
    }
    clearRefreshTimer();
    const stream = new URL(streamUrl, window.location.href);
    if (cols && rows) {
      stream.searchParams.set("cols", String(cols));
      stream.searchParams.set("rows", String(rows));
    }
    source = new EventSource(stream);
    let ended = false; // the server told us the session is gone; suppress the follow-up onerror
    let connected = false; // a first successful open happened; later opens are reconnects
    let lostConnection = false; // currently in a dropped/reconnecting state, toast pending recovery
    const armReconnectTimer = (stillFailing, message) => {
      clearRefreshTimer();
      setRefreshing(true);
      refreshTimer = window.setTimeout(() => {
        refreshTimer = null;
        setRefreshing(false);
        if (stillFailing()) {
          notifyError(message);
        }
      }, 8000);
    };
    if (userRefresh) {
      armReconnectTimer(() => !connected, "Refresh failed. Could not reconnect to the terminal.");
    }
    source.onopen = () => {
      clearRefreshTimer();
      if (userRefresh && !connected) {
        notifySuccess("Terminal reconnected.");
      } else if (lostConnection) {
        lostConnection = false;
        notifySuccess("Reconnected to the terminal.");
      }
      connected = true;
    };
    source.addEventListener("terminal-size", (event) => {
      const size = JSON.parse(event.data);
      if (size.cols && size.rows && (term.cols !== size.cols || term.rows !== size.rows)) {
        term.resize(size.cols, size.rows);
      }
      // The program's modes from tmux (covers attaching to an already running
      // session, where we never saw the enable sequences); xterm's own parser
      // keeps the live state current for changes while attached.
      if (typeof size.mouseTracking === "boolean") {
        mouse.tracking = size.mouseTracking;
        mouse.sgr = size.mouseSgr;
        serverModes.altScreen = size.altScreen;
        serverModes.appCursor = size.appCursor;
      }
    });
    source.addEventListener("snapshot", (event) => {
      writeChunk(event.data, true);
    });
    source.addEventListener("delta", (event) => {
      writeChunk(event.data, false);
    });
    // The server signals a gone/ended session (e.g. "Session has ended.").
    source.addEventListener("terminal-error", (event) => {
      ended = true;
      clearRefreshTimer();
      source?.close();
      notifyError(event.data || "The terminal connection was lost.");
    });
    source.onerror = () => {
      // Skip when the session already ended cleanly (reported via terminal-error).
      if (ended || !source) {
        return;
      }
      // A permanent failure (CLOSED: session gone or auth expired) won't reconnect.
      if (source.readyState === EventSource.CLOSED) {
        clearRefreshTimer();
        lostConnection = false;
        notifyError("Lost connection to the terminal. Use the refresh button to reconnect.");
        return;
      }
      // CONNECTING means the browser is auto-reconnecting after an unclean drop;
      // flag it once so onopen can confirm recovery, and onerror can't spam.
      if (connected && !lostConnection) {
        lostConnection = true;
        notifyError("Connection to the terminal lost. Reconnecting…");
        armReconnectTimer(() => lostConnection, "Could not reconnect to the terminal. Use the refresh button to retry.");
      }
    };
  };

  const performResize = async () => {
    const { cols, rows } = await resolveClientSize();
    const hadSource = Boolean(source);
    if (term.cols !== cols || term.rows !== rows) {
      term.resize(cols, rows);
    }
    if (!hadSource) {
      connectStream(Math.max(term.cols, 2), Math.max(term.rows, 2));
      return;
    }
    const response = await postForm(resizeUrl, {
      cols: String(Math.max(term.cols, 2)),
      rows: String(Math.max(term.rows, 2)),
    });
    const responseText = await response.text();
    if (!response.ok) {
      let payload = {};
      if (responseText) {
        try {
          payload = JSON.parse(responseText);
        } catch (parseError) {
          void parseError;
        }
      }
      throw new Error(payload.error || responseText || "Failed to resize terminal.");
    }
  };

  const scheduleResize = () => {
    if (resizeTimer !== null) {
      window.clearTimeout(resizeTimer);
    }
    resizeTimer = window.setTimeout(() => {
      resizeTimer = null;
      // Best-effort: a failed resize means the session is gone, which the stream
      // itself reports — no need for a second toast on every window resize.
      void performResize().catch(() => {}).finally(() => {
        if (isTerminalFocused()) {
          followCursor();
        }
      });
    }, 120);
  };

  const refreshStream = async () => {
    const { cols, rows } = await resolveClientSize();
    if (term.cols !== cols || term.rows !== rows) {
      term.resize(cols, rows);
    }
    connectStream(cols, rows, true);
  };

  // ---- Wiring ----------------------------------------------------------------
  listen(terminalElement, "scroll", syncFollowOutput);

  for (const button of streamRefreshButtons) {
    listen(button, "click", (event) => {
      event.preventDefault();
      setRefreshing(true);
      notifyInfo("Reconnecting to the terminal…");
      void refreshStream().catch(() => {
        setRefreshing(false);
        notifyError("Could not reconnect to the terminal.");
      });
    });
  }

  listen(window, "resize", scheduleResize);
  listen(document, "terminal-setting-change", (event) => {
    if (event.detail?.setting === "font-size") {
      fontSizeOverride = Number(event.detail.value) || DEFAULT_FONT_SIZE;
    } else if (event.detail?.setting === "rows") {
      rowsOverride = Number(event.detail.value) || DEFAULT_ROWS;
    } else {
      return;
    }
    followSettingResize = Date.now();
    scheduleResize();
  });

  measureElementObserver = new MutationObserver((records) => {
    for (const record of records) {
      for (const node of record.addedNodes) {
        hostMeasureElement(node);
      }
    }
  });
  measureElementObserver.observe(document.body, { childList: true });
  hostMeasureElements();

  listen(window, "beforeunload", () => {
    source?.close();
    if (resizeTimer !== null) {
      window.clearTimeout(resizeTimer);
    }
    measureElementObserver?.disconnect();
  });

  syncFollowOutput();
  // Initial connect; a real failure surfaces through the stream's own handlers.
  void performResize().catch(() => {});

  return () => {
    ac.abort();
    source?.close();
    measureElementObserver?.disconnect();
    if (resizeTimer !== null) window.clearTimeout(resizeTimer);
    if (refreshTimer !== null) window.clearTimeout(refreshTimer);
    term.dispose();
  };
}

class TerminalAttach extends HTMLElement {
  connectedCallback() {
    if (this.inited) return;
    this.inited = true;
    this.teardown = initTerminalAttach(this);
  }

  disconnectedCallback() {
    this.teardown?.();
    this.teardown = null;
    this.inited = false;
  }
}

customElements.define("terminal-attach", TerminalAttach);
