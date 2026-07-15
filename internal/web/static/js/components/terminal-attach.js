import { notifyError, notifySuccess, notifyInfo } from "@dc/toast";
import { postForm, postJSON } from "@dc/http";
import { get, set } from "@dc/store";

const TERMINAL_THEMES = {
  dark: {
    frame: "#0b0f19",
    theme: {
      background: "#111827",
      foreground: "#f9fafb",
      selectionBackground: "#3b82f6",
      selectionForeground: "#f9fafb",
    },
  },
  light: {
    frame: "#ffffff",
    theme: {
      background: "#ffffff",
      foreground: "#1f2937",
      cursor: "#1f2937",
      cursorAccent: "#ffffff",
      selectionBackground: "#3b82f6",
      black: "#000000",
      red: "#990000",
      green: "#00a600",
      yellow: "#999900",
      blue: "#0000b2",
      magenta: "#b200b2",
      cyan: "#00a6b2",
      white: "#555555",
      brightBlack: "#666666",
      brightRed: "#e50000",
      brightGreen: "#00d900",
      brightYellow: "#e5e500",
      brightBlue: "#0000ff",
      brightMagenta: "#e500e5",
      brightCyan: "#00e5e5",
      brightWhite: "#a5a5a5",
    },
  },
  "solarized-dark": {
    frame: "#002b36",
    theme: {
      background: "#002b36",
      foreground: "#839496",
      cursor: "#839496",
      cursorAccent: "#002b36",
      selectionBackground: "#073642",
      selectionForeground: "#93a1a1",
      black: "#073642",
      red: "#dc322f",
      green: "#859900",
      yellow: "#b58900",
      blue: "#268bd2",
      magenta: "#d33682",
      cyan: "#2aa198",
      white: "#eee8d5",
      brightBlack: "#586e75",
      brightRed: "#cb4b16",
      brightGreen: "#859900",
      brightYellow: "#b58900",
      brightBlue: "#268bd2",
      brightMagenta: "#6c71c4",
      brightCyan: "#2aa198",
      brightWhite: "#fdf6e3",
    },
  },
  "solarized-light": {
    frame: "#fdf6e3",
    theme: {
      background: "#fdf6e3",
      foreground: "#657b83",
      cursor: "#657b83",
      cursorAccent: "#fdf6e3",
      selectionBackground: "#93a1a1",
      black: "#073642",
      red: "#dc322f",
      green: "#859900",
      yellow: "#b58900",
      blue: "#268bd2",
      magenta: "#d33682",
      cyan: "#2aa198",
      white: "#586e75",
      brightBlack: "#93a1a1",
      brightRed: "#cb4b16",
      brightGreen: "#859900",
      brightYellow: "#b58900",
      brightBlue: "#268bd2",
      brightMagenta: "#6c71c4",
      brightCyan: "#2aa198",
      brightWhite: "#657b83",
    },
  },
  "gruvbox-dark": {
    frame: "#282828",
    theme: {
      background: "#282828",
      foreground: "#ebdbb2",
      cursor: "#ebdbb2",
      cursorAccent: "#282828",
      selectionBackground: "#504945",
      selectionForeground: "#ebdbb2",
      black: "#282828",
      red: "#cc241d",
      green: "#98971a",
      yellow: "#d79921",
      blue: "#458588",
      magenta: "#b16286",
      cyan: "#689d6a",
      white: "#a89984",
      brightBlack: "#928374",
      brightRed: "#fb4934",
      brightGreen: "#b8bb26",
      brightYellow: "#fabd2f",
      brightBlue: "#83a598",
      brightMagenta: "#d3869b",
      brightCyan: "#8ec07c",
      brightWhite: "#ebdbb2",
    },
  },
  "gruvbox-light": {
    frame: "#fbf1c7",
    theme: {
      background: "#fbf1c7",
      foreground: "#3c3836",
      cursor: "#3c3836",
      cursorAccent: "#fbf1c7",
      selectionBackground: "#d5c4a1",
      black: "#fbf1c7",
      red: "#cc241d",
      green: "#98971a",
      yellow: "#d79921",
      blue: "#458588",
      magenta: "#b16286",
      cyan: "#689d6a",
      white: "#7c6f64",
      brightBlack: "#928374",
      brightRed: "#9d0006",
      brightGreen: "#79740e",
      brightYellow: "#b57614",
      brightBlue: "#076678",
      brightMagenta: "#8f3f71",
      brightCyan: "#427b58",
      brightWhite: "#3c3836",
    },
  },
  "catppuccin-mocha": {
    frame: "#1e1e2e",
    theme: {
      background: "#1e1e2e",
      foreground: "#cdd6f4",
      cursor: "#f5e0dc",
      cursorAccent: "#1e1e2e",
      selectionBackground: "#585b70",
      selectionForeground: "#cdd6f4",
      black: "#45475a",
      red: "#f38ba8",
      green: "#a6e3a1",
      yellow: "#f9e2af",
      blue: "#89b4fa",
      magenta: "#f5c2e7",
      cyan: "#94e2d5",
      white: "#bac2de",
      brightBlack: "#585b70",
      brightRed: "#f38ba8",
      brightGreen: "#a6e3a1",
      brightYellow: "#f9e2af",
      brightBlue: "#89b4fa",
      brightMagenta: "#f5c2e7",
      brightCyan: "#94e2d5",
      brightWhite: "#a6adc8",
    },
  },
  "catppuccin-latte": {
    frame: "#eff1f5",
    theme: {
      background: "#eff1f5",
      foreground: "#4c4f69",
      cursor: "#dc8a78",
      cursorAccent: "#eff1f5",
      selectionBackground: "#acb0be",
      black: "#5c5f77",
      red: "#d20f39",
      green: "#40a02b",
      yellow: "#df8e1d",
      blue: "#1e66f5",
      magenta: "#ea76cb",
      cyan: "#179299",
      white: "#6c6f85",
      brightBlack: "#6c6f85",
      brightRed: "#d20f39",
      brightGreen: "#40a02b",
      brightYellow: "#df8e1d",
      brightBlue: "#1e66f5",
      brightMagenta: "#ea76cb",
      brightCyan: "#179299",
      brightWhite: "#4c4f69",
    },
  },
  "one-dark": {
    frame: "#282c34",
    theme: {
      background: "#282c34",
      foreground: "#abb2bf",
      cursor: "#528bff",
      cursorAccent: "#282c34",
      selectionBackground: "#3e4451",
      selectionForeground: "#abb2bf",
      black: "#282c34",
      red: "#e06c75",
      green: "#98c379",
      yellow: "#e5c07b",
      blue: "#61afef",
      magenta: "#c678dd",
      cyan: "#56b6c2",
      white: "#abb2bf",
      brightBlack: "#5c6370",
      brightRed: "#e06c75",
      brightGreen: "#98c379",
      brightYellow: "#e5c07b",
      brightBlue: "#61afef",
      brightMagenta: "#c678dd",
      brightCyan: "#56b6c2",
      brightWhite: "#ffffff",
    },
  },
  "one-light": {
    frame: "#fafafa",
    theme: {
      background: "#fafafa",
      foreground: "#383a42",
      cursor: "#526fff",
      cursorAccent: "#fafafa",
      selectionBackground: "#e5e5e6",
      black: "#383a42",
      red: "#e45649",
      green: "#50a14f",
      yellow: "#c18401",
      blue: "#4078f2",
      magenta: "#a626a4",
      cyan: "#0184bc",
      white: "#696c77",
      brightBlack: "#a0a1a7",
      brightRed: "#e45649",
      brightGreen: "#50a14f",
      brightYellow: "#c18401",
      brightBlue: "#4078f2",
      brightMagenta: "#a626a4",
      brightCyan: "#0184bc",
      brightWhite: "#383a42",
    },
  },
  "tokyonight-dark": {
    frame: "#1a1b26",
    theme: {
      background: "#1a1b26",
      foreground: "#c0caf5",
      cursor: "#c0caf5",
      cursorAccent: "#1a1b26",
      selectionBackground: "#33467c",
      selectionForeground: "#c0caf5",
      black: "#15161e",
      red: "#f7768e",
      green: "#9ece6a",
      yellow: "#e0af68",
      blue: "#7aa2f7",
      magenta: "#bb9af7",
      cyan: "#7dcfff",
      white: "#a9b1d6",
      brightBlack: "#414868",
      brightRed: "#f7768e",
      brightGreen: "#9ece6a",
      brightYellow: "#e0af68",
      brightBlue: "#7aa2f7",
      brightMagenta: "#bb9af7",
      brightCyan: "#7dcfff",
      brightWhite: "#c0caf5",
    },
  },
  "tokyonight-day": {
    frame: "#e1e2e7",
    theme: {
      background: "#e1e2e7",
      foreground: "#3760bf",
      cursor: "#3760bf",
      cursorAccent: "#e1e2e7",
      selectionBackground: "#b7c1e3",
      black: "#b4b5b9",
      red: "#f52a65",
      green: "#587539",
      yellow: "#8c6c3e",
      blue: "#2e7de9",
      magenta: "#9854f1",
      cyan: "#007197",
      white: "#6172b0",
      brightBlack: "#a1a6c5",
      brightRed: "#f52a65",
      brightGreen: "#587539",
      brightYellow: "#8c6c3e",
      brightBlue: "#2e7de9",
      brightMagenta: "#9854f1",
      brightCyan: "#007197",
      brightWhite: "#3760bf",
    },
  },
  "everforest-dark": {
    frame: "#2d353b",
    theme: {
      background: "#2d353b",
      foreground: "#d3c6aa",
      cursor: "#d3c6aa",
      cursorAccent: "#2d353b",
      selectionBackground: "#4f5b58",
      selectionForeground: "#d3c6aa",
      black: "#475258",
      red: "#e67e80",
      green: "#a7c080",
      yellow: "#dbbc7f",
      blue: "#7fbbb3",
      magenta: "#d699b6",
      cyan: "#83c092",
      white: "#d3c6aa",
      brightBlack: "#5c6a72",
      brightRed: "#e67e80",
      brightGreen: "#a7c080",
      brightYellow: "#dbbc7f",
      brightBlue: "#7fbbb3",
      brightMagenta: "#d699b6",
      brightCyan: "#83c092",
      brightWhite: "#d3c6aa",
    },
  },
  "everforest-light": {
    frame: "#fdf6e3",
    theme: {
      background: "#fdf6e3",
      foreground: "#5c6a72",
      cursor: "#5c6a72",
      cursorAccent: "#fdf6e3",
      selectionBackground: "#e0dcc7",
      black: "#5c6a72",
      red: "#f85552",
      green: "#8da101",
      yellow: "#dfa000",
      blue: "#3a94c5",
      magenta: "#df69ba",
      cyan: "#35a77c",
      white: "#708089",
      brightBlack: "#939f91",
      brightRed: "#f85552",
      brightGreen: "#8da101",
      brightYellow: "#dfa000",
      brightBlue: "#3a94c5",
      brightMagenta: "#df69ba",
      brightCyan: "#35a77c",
      brightWhite: "#5c6a72",
    },
  },
  "rosepine-dark": {
    frame: "#191724",
    theme: {
      background: "#191724",
      foreground: "#e0def4",
      cursor: "#e0def4",
      cursorAccent: "#191724",
      selectionBackground: "#403d52",
      selectionForeground: "#e0def4",
      black: "#26233a",
      red: "#eb6f92",
      green: "#31748f",
      yellow: "#f6c177",
      blue: "#9ccfd8",
      magenta: "#c4a7e7",
      cyan: "#ebbcba",
      white: "#e0def4",
      brightBlack: "#6e6a86",
      brightRed: "#eb6f92",
      brightGreen: "#31748f",
      brightYellow: "#f6c177",
      brightBlue: "#9ccfd8",
      brightMagenta: "#c4a7e7",
      brightCyan: "#ebbcba",
      brightWhite: "#e0def4",
    },
  },
  "rosepine-dawn": {
    frame: "#faf4ed",
    theme: {
      background: "#faf4ed",
      foreground: "#575279",
      cursor: "#575279",
      cursorAccent: "#faf4ed",
      selectionBackground: "#dfdad9",
      black: "#f2e9e1",
      red: "#b4637a",
      green: "#286983",
      yellow: "#ea9d34",
      blue: "#56949f",
      magenta: "#907aa9",
      cyan: "#d7827e",
      white: "#575279",
      brightBlack: "#9893a5",
      brightRed: "#b4637a",
      brightGreen: "#286983",
      brightYellow: "#ea9d34",
      brightBlue: "#56949f",
      brightMagenta: "#907aa9",
      brightCyan: "#d7827e",
      brightWhite: "#575279",
    },
  },
  "ayu-dark": {
    frame: "#0d1017",
    theme: {
      background: "#0d1017",
      foreground: "#bfbdb6",
      cursor: "#e6b450",
      cursorAccent: "#0d1017",
      selectionBackground: "#1b3a5b",
      selectionForeground: "#bfbdb6",
      black: "#131721",
      red: "#ea6c73",
      green: "#7fd962",
      yellow: "#f9af4f",
      blue: "#53bdfa",
      magenta: "#cda1fa",
      cyan: "#90e1c6",
      white: "#c7c7c7",
      brightBlack: "#686868",
      brightRed: "#f07178",
      brightGreen: "#aad94c",
      brightYellow: "#ffb454",
      brightBlue: "#59c2ff",
      brightMagenta: "#d2a6ff",
      brightCyan: "#95e6cb",
      brightWhite: "#ffffff",
    },
  },
  "ayu-light": {
    frame: "#fcfcfc",
    theme: {
      background: "#fcfcfc",
      foreground: "#5c6166",
      cursor: "#ffaa33",
      cursorAccent: "#fcfcfc",
      selectionBackground: "#d1e4f4",
      black: "#010101",
      red: "#f07171",
      green: "#86b300",
      yellow: "#f2ae49",
      blue: "#399ee6",
      magenta: "#a37acc",
      cyan: "#4cbf99",
      white: "#5c6166",
      brightBlack: "#8a9199",
      brightRed: "#f07171",
      brightGreen: "#86b300",
      brightYellow: "#f2ae49",
      brightBlue: "#399ee6",
      brightMagenta: "#a37acc",
      brightCyan: "#4cbf99",
      brightWhite: "#5c6166",
    },
  },
};

// The auto families follow the OS scheme, picking a light or dark member.
const AUTO_THEMES = {
  auto: { light: "light", dark: "dark" },
  "solarized-auto": { light: "solarized-light", dark: "solarized-dark" },
  "gruvbox-auto": { light: "gruvbox-light", dark: "gruvbox-dark" },
  "catppuccin-auto": { light: "catppuccin-latte", dark: "catppuccin-mocha" },
  "one-auto": { light: "one-light", dark: "one-dark" },
  "tokyonight-auto": { light: "tokyonight-day", dark: "tokyonight-dark" },
  "everforest-auto": { light: "everforest-light", dark: "everforest-dark" },
  "rosepine-auto": { light: "rosepine-dawn", dark: "rosepine-dark" },
  "ayu-auto": { light: "ayu-light", dark: "ayu-dark" },
};

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
  const themeSetting = document.querySelector('terminal-setting-select[setting="theme"]');

  const DEFAULT_FONT_SIZE = 14;
  const DEFAULT_ROWS = 30;
  const DEFAULT_THEME = "auto";
  const darkSchemeMedia = window.matchMedia("(prefers-color-scheme: dark)");
  let themeOverride = themeSetting
    ? (get(themeSetting.getAttribute("storage-key") || "", "") || themeSetting.getAttribute("default-value") || DEFAULT_THEME)
    : DEFAULT_THEME;
  const resolveTheme = () => {
    const auto = AUTO_THEMES[themeOverride];
    const name = auto ? auto[darkSchemeMedia.matches ? "dark" : "light"] : themeOverride;
    return TERMINAL_THEMES[name] || TERMINAL_THEMES.dark;
  };
  // The current scheme colors ride every server request (theme POST, resize
  // POST, stream connect), so the session picks up this client's scheme on
  // every path, not only the dedicated POST.
  const themeColors = () => {
    const t = resolveTheme().theme;
    return { bg: t.background, fg: t.foreground };
  };

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
    theme: resolveTheme().theme,
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(terminalElement);

  const applyTheme = () => {
    const entry = resolveTheme();
    term.options.theme = entry.theme;
    terminalElement.style.background = entry.frame;
    terminalElement.style.setProperty("--dc-terminal-contrast", entry.theme.foreground);
  };
  // A live theme change (menu, OS flip) has no resize or stream connect to ride,
  // so it pushes the colors itself. On mount and reconnect the stream connect
  // carries them, so no post is needed there.
  const pushTheme = () => {
    const { bg, fg } = themeColors();
    postJSON("/terminal-theme", { bg, fg }).catch(() => {});
  };
  applyTheme();

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
  // so they scroll identically — pixel travel (finger or trackpad) maps to steps
  // at the same per-program rate, legacy line and page wheel deltas step per notch.
  {
    const WHEEL_LINES_PER_NOTCH = 3;
    const WHEEL_GESTURE_GAP_MS = 300;
    let wheelAccum = 0;
    let wheelPixels = 0;
    let wheelPixelsAt = 0;

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

    // One step moves one line of content, so a line step costs one cell height
    // of travel; the page-per-step pager (copilot) moves a screen per step and
    // costs half a screen of travel per page.
    const scrollStepPx = () => {
      const screen = terminalElement.querySelector(".xterm-screen");
      const cell = screen && term.rows >= 1 ? screen.clientHeight / term.rows : 17;
      const alt = serverModes.altScreen || term.buffer.active.type === "alternate";
      if (!mouse.tracking && alt && !scrollHistory && screen) {
        return Math.max(cell, screen.clientHeight / 2);
      }
      return Math.max(cell, 4);
    };

    if (interactiveInput) {
      listen(terminalElement, "wheel", (event) => {
        if (event.ctrlKey) {
          return; // leave pinch-zoom to the browser
        }
        event.preventDefault();
        event.stopPropagation();
        if (event.deltaMode === 0) {
          if (event.timeStamp - wheelPixelsAt > WHEEL_GESTURE_GAP_MS) {
            wheelPixels = 0;
          }
          wheelPixelsAt = event.timeStamp;
          wheelPixels += event.deltaY;
          const stepPx = scrollStepPx();
          while (wheelPixels >= stepPx) {
            wheelPixels -= stepPx;
            scrollStep(true, event.clientX, event.clientY);
          }
          while (wheelPixels <= -stepPx) {
            wheelPixels += stepPx;
            scrollStep(false, event.clientX, event.clientY);
          }
          return;
        }
        wheelAccum += event.deltaMode === 1 ? event.deltaY / WHEEL_LINES_PER_NOTCH : event.deltaY;
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
    // per-program steps so content tracks the finger 1:1.
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
      const stepPx = scrollStepPx();
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

  let fullscreen = interactiveInput && get("dc-terminal-fullscreen", "") === "1";
  const fullscreenButtons = document.querySelectorAll("[data-terminal-fullscreen]");
  const paintFullscreen = () => {
    document.documentElement.classList.toggle("dc-terminal-fullscreen", fullscreen);
    for (const button of fullscreenButtons) {
      button.setAttribute("aria-pressed", fullscreen ? "true" : "false");
      button.setAttribute("aria-label", fullscreen ? "Exit fullscreen" : "Fullscreen");
      button.title = (fullscreen ? "Exit fullscreen" : "Fullscreen") + " (Ctrl+Shift+F)";
      const icon = button.querySelector("i");
      if (icon) icon.className = fullscreen ? "ti ti-minimize" : "ti ti-maximize";
    }
  };
  const setFullscreen = (on) => {
    if (!interactiveInput || fullscreen === on) {
      return;
    }
    fullscreen = on;
    set("dc-terminal-fullscreen", on ? "1" : "");
    paintFullscreen();
    scheduleResize();
    term.focus();
  };
  paintFullscreen();

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
    const rows = fullscreen
      ? Math.max(2, (dims && dims.rows) ? dims.rows : lastClientRows || DEFAULT_ROWS)
      : Math.max(2, rowsOverride || lastClientRows || DEFAULT_ROWS);
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

  const sessionPath = streamUrl.replace(/\/stream$/, "");
  let userEndedAt = 0;
  listen(window, "pe:form", (event) => {
    const path = new URL(event.detail.form.action, window.location.origin).pathname;
    if (path === sessionPath + "/stop" || path === sessionPath + "/delete") {
      userEndedAt = Date.now();
    }
  });
  listen(window, "dc:terminal-closing", (event) => {
    if (sessionPath.endsWith("/" + event.detail.id)) {
      userEndedAt = Date.now();
    }
  });

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
    const { bg, fg } = themeColors();
    if (bg && fg) {
      stream.searchParams.set("bg", bg);
      stream.searchParams.set("fg", fg);
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
    source.addEventListener("terminal-ended", (event) => {
      ended = true;
      clearRefreshTimer();
      source?.close();
      if (Date.now() - userEndedAt > 15000) {
        notifyInfo(event.data || "Terminal has ended.");
      }
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
    const { bg, fg } = themeColors();
    const response = await postForm(resizeUrl, {
      cols: String(Math.max(term.cols, 2)),
      rows: String(Math.max(term.rows, 2)),
      bg,
      fg,
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

  for (const button of fullscreenButtons) {
    listen(button, "click", (event) => {
      event.preventDefault();
      setFullscreen(!fullscreen);
    });
  }
  if (interactiveInput) {
    listen(document, "keydown", (event) => {
      if ((event.key === "F" || event.key === "f" || event.key === "Enter") && (event.ctrlKey || event.metaKey)
        && event.shiftKey && !event.altKey && !event.repeat) {
        event.preventDefault();
        event.stopPropagation();
        setFullscreen(!fullscreen);
      }
    }, { capture: true });
    listen(document, "dblclick", (event) => {
      if (event.target.closest("[data-tabs-strip]") && !event.target.closest(".terminal-tab")) {
        setFullscreen(!fullscreen);
      }
    });
  }

  listen(window, "resize", scheduleResize);
  listen(darkSchemeMedia, "change", () => {
    if (AUTO_THEMES[themeOverride]) {
      applyTheme();
      pushTheme();
    }
  });
  listen(document, "terminal-setting-change", (event) => {
    if (event.detail?.setting === "theme") {
      themeOverride = String(event.detail.value || DEFAULT_THEME);
      applyTheme();
      pushTheme();
      return;
    }
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
    document.documentElement.classList.remove("dc-terminal-fullscreen");
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
