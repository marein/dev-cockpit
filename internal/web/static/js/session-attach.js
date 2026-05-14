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

  const term = new Terminal({
    allowTransparency: true,
    cursorBlink: true,
    // Render a solid cursor even while unfocused (the terminal is read-only and
    // never takes focus). Without this the inactive cursor is a faint outline.
    cursorInactiveStyle: "block",
    disableStdin: true,
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, monospace",
    fontSize: DEFAULT_FONT_SIZE,
    scrollback: 0,
    theme: {
      background: "#111827",
      foreground: "#f9fafb",
    },
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(terminalElement);

  let followOutput = true;
  let resizeTimer = null;
  let source = null;
  let fontSizeOverride = Number(fontSizeSetting?.value) || DEFAULT_FONT_SIZE;
  let rowsOverride = Number(rowsSetting?.value) || DEFAULT_ROWS;
  let measureElementObserver = null;
  let lastClientCols = 0;
  let lastClientRows = rowsOverride;

  const isOffscreenMeasureElement = (element) => (
    element instanceof HTMLDivElement
    && element.parentElement === document.body
    && element.style.position === "absolute"
    && element.style.top === "-50000px"
    && element.style.width === "50000px"
    && element.style.whiteSpace === "pre"
  );

  const hostMeasureElement = (element) => {
    if (!isOffscreenMeasureElement(element)) {
      return;
    }
    terminalElement.appendChild(element);
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

  const scrollPanelToBottom = () => {
    terminalElement.scrollTop = terminalElement.scrollHeight;
  };

  const scrollZoneElement = terminalElement?.querySelector("session-scroll-zone");
  const eventFromScrollZone = (event) => (
    Boolean(scrollZoneElement)
    && typeof event.composedPath === "function"
    && event.composedPath().includes(scrollZoneElement)
  );

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

  const renderSnapshot = (encodedChunk) => {
    const shouldFollow = followOutput;
    term.reset();
    const chunk = decodeChunk(encodedChunk);
    if (chunk.length > 0) {
      term.write(chunk, () => {
        if (shouldFollow) {
          scrollPanelToBottom();
        }
      });
      return;
    }
    if (shouldFollow) {
      scrollPanelToBottom();
    }
  };

  const renderAppend = (encodedChunk) => {
    const shouldFollow = followOutput;
    const chunk = decodeChunk(encodedChunk);
    if (chunk.length === 0) {
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
      renderSnapshot(event.data);
      status.textContent = "Connected";
    });
    source.addEventListener("delta", (event) => {
      renderAppend(event.data);
    });
    source.addEventListener("session-error", (event) => {
      void event;
      status.textContent = "Disconnected";
      source?.close();
    });
    source.onerror = () => {
      status.textContent = "Disconnected";
    };
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
    let responsePayload = {};
    if (responseText) {
      responsePayload = JSON.parse(responseText);
    }
    if (!response.ok) {
      throw new Error(responsePayload.error || responseText || "Failed to resize terminal.");
    }
  };

  const scheduleResize = () => {
    if (resizeTimer !== null) {
      window.clearTimeout(resizeTimer);
    }
    resizeTimer = window.setTimeout(() => {
      resizeTimer = null;
      void performResize().catch((resizeError) => {
        void resizeError;
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

  terminalElement.addEventListener("scroll", syncFollowOutput);
  const xtermEventNames = [
    "beforeinput", "input", "keydown", "keyup", "keypress",
    "compositionstart", "compositionupdate", "compositionend",
    "pointerdown", "pointermove", "pointerup", "pointercancel",
    "mousedown", "mousemove", "mouseup", "dblclick",
    "touchstart", "touchmove", "touchend", "touchcancel",
    "wheel",
  ];
  for (const eventName of xtermEventNames) {
    terminalElement.addEventListener(eventName, (event) => {
      if (eventFromScrollZone(event)) {
        return;
      }
      event.stopPropagation();
    }, true);
  }
  for (const button of streamRefreshButtons) {
    button.addEventListener("click", (event) => {
      event.preventDefault();
      void refreshStream().catch((refreshError) => {
        void refreshError;
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
  void performResize().catch((resizeError) => {
    void resizeError;
    status.textContent = "Disconnected";
  });
})();
