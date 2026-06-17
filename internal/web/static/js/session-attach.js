(() => {
  const config = window.__SESSION_ATTACH_CONFIG__ || {};
  const streamUrl = config.streamUrl;
  const resizeUrl = config.resizeUrl;
  const csrfToken = config.csrfToken || "";

  const terminalElement = document.getElementById("session-terminal");
  const status = document.getElementById("session-stream-status");
  const streamRefreshButtons = document.querySelectorAll("[data-session-refresh]");
  const fontSizeSetting = document.querySelector('session-terminal-setting-select[setting="font-size"]');
  const rowsSetting = document.querySelector('session-terminal-setting-select[setting="rows"]');

  const DEFAULT_FONT_SIZE = 14;
  const DEFAULT_ROWS = 30;
  const FOREGROUND = "#f9fafb";

  // The terminal is a read-only viewport: input is sent through the controls
  // module, so stdin stays disabled and the element never takes focus.
  const term = new Terminal({
    allowTransparency: true,
    disableStdin: true,
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

  // ---- Mouse reporting -------------------------------------------------------
  // This viewport is a read-only mirror: stdin is disabled and keystrokes are
  // sent through the controls module, so xterm has no program to report mouse
  // events to. When a full-screen CLI enables mouse tracking (DECSET ?1000/1002/
  // 1003 and friends, which tmux forwards because its own `mouse` option is off),
  // xterm would start capturing mouse-down and wheel events — breaking text
  // selection, swallowing page scroll over the terminal, and turning the cursor
  // into a pointer. Swallow the mouse-mode set/reset sequences so the renderer
  // never activates mouse reporting, leaving the mirror selectable and scrollable.
  const MOUSE_MODES = new Set([1000, 1001, 1002, 1003, 1004, 1005, 1006, 1015, 1016]);
  const isMouseModeSequence = (params) => params.length > 0
    && params.every((param) => MOUSE_MODES.has(Array.isArray(param) ? param[0] : param));
  term.parser.registerCsiHandler({ prefix: "?", final: "h" }, isMouseModeSequence);
  term.parser.registerCsiHandler({ prefix: "?", final: "l" }, isMouseModeSequence);

  // ---- Cursor ----------------------------------------------------------------
  // The canvas renderer only paints the terminal cursor while the element is
  // focused, and this read-only viewport never is. Both supported CLIs use the
  // hardware cursor (they never draw their own), so we render a single block
  // that mirrors xterm's cursor cell. The server positions the cursor in every
  // snapshot, and the program's own escapes keep it current in the live stream.
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

  document.addEventListener("copy", (event) => {
    const selection = terminalSelection();
    if (!selection) {
      return;
    }
    event.clipboardData?.setData("text/plain", selection);
    event.preventDefault();
  });

  terminalElement.addEventListener("contextmenu", (event) => {
    const selection = terminalSelection();
    if (!selection || !navigator.clipboard) {
      return;
    }
    void navigator.clipboard.writeText(selection).catch(() => {});
    event.preventDefault();
  });

  // ---- Sizing ----------------------------------------------------------------
  let fontSizeOverride = Number(fontSizeSetting?.value) || DEFAULT_FONT_SIZE;
  let rowsOverride = Number(rowsSetting?.value) || DEFAULT_ROWS;
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
      return;
    }
    term.write(chunk, () => {
      if (shouldFollow) {
        scrollPanelToBottom();
      }
    });
  };

  const connectStream = (cols, rows) => {
    if (source) {
      source.close();
    }
    const stream = new URL(streamUrl, window.location.href);
    if (cols && rows) {
      stream.searchParams.set("cols", String(cols));
      stream.searchParams.set("rows", String(rows));
    }
    source = new EventSource(stream);
    source.addEventListener("terminal-size", (event) => {
      const size = JSON.parse(event.data);
      if (size.cols && size.rows && (term.cols !== size.cols || term.rows !== size.rows)) {
        term.resize(size.cols, size.rows);
      }
    });
    source.addEventListener("snapshot", (event) => {
      writeChunk(event.data, true);
      status.textContent = "Connected";
    });
    source.addEventListener("delta", (event) => {
      writeChunk(event.data, false);
    });
    source.addEventListener("session-error", () => {
      status.textContent = "Disconnected";
      source?.close();
    });
    source.onerror = () => {
      status.textContent = "Disconnected";
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
    const response = await fetch(resizeUrl, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
      },
      body: new URLSearchParams({
        cols: String(Math.max(term.cols, 2)),
        rows: String(Math.max(term.rows, 2)),
      }),
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
      void performResize().catch(() => {
        status.textContent = "Disconnected";
      });
    }, 120);
  };

  const refreshStream = async () => {
    const { cols, rows } = await resolveClientSize();
    if (term.cols !== cols || term.rows !== rows) {
      term.resize(cols, rows);
    }
    connectStream(cols, rows);
  };

  // ---- Wiring ----------------------------------------------------------------
  terminalElement.addEventListener("scroll", syncFollowOutput);

  for (const button of streamRefreshButtons) {
    button.addEventListener("click", (event) => {
      event.preventDefault();
      void refreshStream().catch(() => {
        status.textContent = "Disconnected";
      });
    });
  }

  window.addEventListener("resize", scheduleResize);
  fontSizeSetting?.addEventListener("session-terminal-setting-change", (event) => {
    fontSizeOverride = Number(event.detail?.value) || DEFAULT_FONT_SIZE;
    scheduleResize();
  });
  rowsSetting?.addEventListener("session-terminal-setting-change", (event) => {
    rowsOverride = Number(event.detail?.value) || DEFAULT_ROWS;
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

  window.addEventListener("beforeunload", () => {
    source?.close();
    if (resizeTimer !== null) {
      window.clearTimeout(resizeTimer);
    }
    measureElementObserver?.disconnect();
  });

  status.textContent = "Disconnected";
  syncFollowOutput();
  void performResize().catch(() => {
    status.textContent = "Disconnected";
  });
})();
