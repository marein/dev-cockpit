import { createRepeater } from "@dc/repeater";
import { notifyError } from "@dc/toast";
import { postJSON, ensureOk } from "@dc/http";

function initTerminalInput(host) {
  const inputUrl = host.getAttribute("input-url");
  const scrollHistory = host.hasAttribute("scroll-history");

  const ac = new AbortController();
  const signal = ac.signal;
  const repeaters = [];
  const listen = (target, type, handler, opts) => {
    if (target) target.addEventListener(type, handler, { ...opts, signal });
  };

  const SCROLL_CONTROLS = {
    "page-up": "scroll-up",
    "page-down": "scroll-down",
    "page-top": "scroll-top",
    "page-bottom": "scroll-bottom",
  };
  const mapControl = (control) =>
    scrollHistory && SCROLL_CONTROLS[control] ? SCROLL_CONTROLS[control] : control;

  // The cursor input is created by terminal-attach after its async setup, so
  // resolving nodes here would race it (typing then dies silently). Delegated
  // document listeners match on the ids instead, binding to whichever input
  // exists at the moment the user types.
  const INPUT_IDS = new Set(["terminal-prompt", "terminal-cursor-input"]);
  const forTerminalInputs = (handler) => (event) => {
    if (event.target instanceof Element && INPUT_IDS.has(event.target.id)) {
      handler(event);
    }
  };
  const promptModalElement = document.getElementById("terminal-prompt-modal");
  const promptModalOpenButtons = document.querySelectorAll("[data-terminal-prompt-modal-open]");
  const promptModalForm = document.getElementById("terminal-prompt-modal-form");
  const promptModalTextarea = document.getElementById("terminal-prompt-modal-text");
  const controlButtons = document.querySelectorAll("[data-terminal-control]");
  const ctrlToggle = document.querySelector("[data-shell-ctrl]");

  let ctrlArmed = false;
  const setCtrlArmed = (armed) => {
    ctrlArmed = armed;
    if (!ctrlToggle) {
      return;
    }
    ctrlToggle.classList.toggle("active", armed);
    ctrlToggle.setAttribute("aria-pressed", armed ? "true" : "false");
  };
  if (ctrlToggle) {
    listen(ctrlToggle, "click", () => {
      setCtrlArmed(!ctrlArmed);
      // Arming means a letter comes next: focus the cursor input right here in
      // the tap handler so the on-screen keyboard opens (iOS requires the focus
      // inside the user gesture).
      if (ctrlArmed) {
        document.getElementById("terminal-cursor-input")?.focus();
      }
    });
  }

  const keyMap = {
    ArrowUp: "arrow-up",
    ArrowDown: "arrow-down",
    ArrowLeft: "arrow-left",
    ArrowRight: "arrow-right",
    Escape: "escape",
    Backspace: "backspace",
    Tab: "tab",
    Enter: "enter",
    PageUp: "page-up",
    PageDown: "page-down",
    Home: "page-top",
    End: "page-bottom",
  };
  const REPEAT_INITIAL_DELAY = 400;
  const REPEAT_INTERVAL = 60;

  // Input batching: the first payload from idle goes out immediately; anything
  // arriving while a request is in flight is buffered and flushed together in
  // the next request. Adjacent text payloads coalesce into one string; control,
  // paste and prompt payloads stay as discrete ordered items — the server is
  // the single place that maps them to tmux keys.
  let pendingInputs = [];
  let inputInFlight = false;

  const controlKeyName = (event) => {
    if (keyMap[event.key]) {
      return keyMap[event.key];
    }
    if (event.key === " ") {
      return "space";
    }
    if (event.key.length === 1) {
      return event.key.toLowerCase();
    }
    return "";
  };

  // Each successful POST reports its round trip; the swipe fling in
  // terminal-scroll-zone.js caps its velocity from this measurement.
  const performTerminalInput = async (items) => {
    const started = performance.now();
    await ensureOk(await postJSON(inputUrl, { items }), "Could not send input to the terminal.");
    document.dispatchEvent(new CustomEvent("terminal-input-latency", {
      detail: { ms: performance.now() - started },
    }));
  };

  const pumpTerminalInputs = async () => {
    if (inputInFlight) {
      return;
    }
    inputInFlight = true;
    try {
      while (pendingInputs.length > 0) {
        const items = pendingInputs;
        pendingInputs = [];
        try {
          await performTerminalInput(items);
        } catch (requestError) {
          // Surface a clean message (e.g. "Your session has expired…") through
          // the shared toast channel; dedup collapses a burst of keystrokes.
          notifyError(requestError.message);
        }
      }
    } finally {
      inputInFlight = false;
    }
  };

  const sendTerminalInput = (payload) => {
    const last = pendingInputs[pendingInputs.length - 1];
    if (payload.text && last && last.text) {
      last.text += payload.text;
    } else if (payload.raw && last && last.raw) {
      last.raw += payload.raw;
    } else {
      pendingInputs.push(payload);
    }
    void pumpTerminalInputs();
  };

  const bindInputs = () => {
    listen(document, "keydown", forTerminalInputs((event) => {
      if (event.key === "Tab" && event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        void sendTerminalInput({ control: "shift-tab" });
        return;
      }
      if (event.ctrlKey || event.metaKey) {
        if (event.key.toLowerCase() === "v") {
          return;
        }
        if (event.metaKey && event.key.toLowerCase() === "c") {
          return;
        }
        const key = controlKeyName(event);
        if (!key) {
          return;
        }
        const modifiers = [];
        if (event.ctrlKey) {
          modifiers.push("ctrl");
        }
        if (event.altKey) {
          modifiers.push("alt");
        }
        if (event.metaKey) {
          modifiers.push("meta");
        }
        event.preventDefault();
        void sendTerminalInput({ control: `${modifiers.join("-")}-${key}` });
        return;
      }
      const control = keyMap[event.key];
      if (control) {
        event.preventDefault();
        const mapped = mapControl(control);
        void sendTerminalInput({ control: event.altKey && mapped === control ? `alt-${control}` : mapped });
      }
    }));

    listen(document, "beforeinput", forTerminalInputs((event) => {
      if (event.isComposing) {
        return;
      }
      if (event.inputType !== "insertText" || !event.data) {
        return;
      }
      event.preventDefault();
      if (ctrlArmed) {
        const ch = event.data[0].toLowerCase();
        setCtrlArmed(false);
        if (/[a-z0-9]/.test(ch)) {
          void sendTerminalInput({ control: "ctrl-" + ch });
        } else {
          void sendTerminalInput({ text: event.data[0] });
        }
        const rest = event.data.slice(1);
        if (rest) {
          void sendTerminalInput({ text: rest });
        }
        return;
      }
      void sendTerminalInput({ text: event.data });
    }));

    listen(document, "compositionend", forTerminalInputs((event) => {
      event.target.value = "";
      if (event.data) {
        void sendTerminalInput({ text: event.data });
      }
    }));

    listen(document, "paste", forTerminalInputs((event) => {
      const pastedText = event.clipboardData?.getData("text") ?? "";
      if (!pastedText) {
        return;
      }
      event.preventDefault();
      void sendTerminalInput({ paste: pastedText });
    }));
  };
  bindInputs();

  if (promptModalElement && promptModalTextarea) {
    listen(promptModalElement, "shown.bs.modal", () => {
      promptModalTextarea.focus();
      promptModalTextarea.select();
    });
    listen(promptModalElement, "hidden.bs.modal", () => {
      promptModalTextarea.value = "";
    });
  }

  for (const button of promptModalOpenButtons) {
    listen(button, "click", () => {
      window.setTimeout(() => {
        promptModalTextarea?.focus();
      }, 0);
    });
  }

  listen(promptModalForm, "submit", (event) => {
    event.preventDefault();
    const prompt = promptModalTextarea?.value ?? "";
    if (prompt === "") {
      promptModalTextarea?.focus();
      return;
    }
    void sendTerminalInput({ prompt });
    if (promptModalElement) {
      window.bootstrap?.Modal.getOrCreateInstance(promptModalElement).hide();
    }
  });

  listen(promptModalTextarea, "keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault();
      promptModalForm?.requestSubmit();
    }
  });

  for (const button of controlButtons) {
    const control = button.dataset.terminalControl;
    if (!control) {
      continue;
    }
    let suppressClick = false;
    const fire = () => {
      void sendTerminalInput({ control });
    };
    const repeater = createRepeater(fire, REPEAT_INITIAL_DELAY, REPEAT_INTERVAL);
    repeaters.push(repeater);
    const start = (event) => {
      if (event.button !== undefined && event.button !== 0) {
        return;
      }
      event.preventDefault();
      repeater.start();
      suppressClick = true;
    };
    listen(button, "pointerdown", start);
    listen(button, "pointerup", repeater.stop);
    listen(button, "pointerleave", repeater.stop);
    listen(button, "pointercancel", repeater.stop);
    listen(button, "blur", repeater.stop);
    listen(button, "click", (event) => {
      if (suppressClick) {
        event.preventDefault();
        suppressClick = false;
        return;
      }
      fire();
    });
    listen(button, "keydown", (event) => {
      if (event.repeat) {
        return;
      }
      if (event.key === " " || event.key === "Enter") {
        event.preventDefault();
        repeater.start();
        suppressClick = true;
      }
    });
    listen(button, "keyup", (event) => {
      if (event.key === " " || event.key === "Enter") {
        repeater.stop();
      }
    });
  }

  const pasteFromClipboard = async () => {
    if (!navigator.clipboard?.readText) {
      notifyError("Clipboard paste is not available on this device.");
      return;
    }
    let text = "";
    try {
      text = await navigator.clipboard.readText();
    } catch (error) {
      void error;
      notifyError("Could not read the clipboard.");
      return;
    }
    if (text) {
      void sendTerminalInput({ paste: text });
    }
  };
  for (const button of document.querySelectorAll("[data-terminal-paste]")) {
    listen(button, "click", () => {
      void pasteFromClipboard();
    });
  }

  listen(document, "terminal-control", (event) => {
    const control = event.detail?.control;
    if (typeof control === "string" && control !== "") {
      void sendTerminalInput({ control });
    }
  });

  // Raw terminal bytes emitted by xterm when a desktop client types straight
  // into the terminal (see terminal-attach.js).
  listen(document, "terminal-input", (event) => {
    const raw = event.detail?.raw;
    if (typeof raw === "string" && raw !== "") {
      void sendTerminalInput({ raw });
    }
  });

  return () => {
    ac.abort();
    repeaters.forEach((repeater) => repeater.stop());
  };
}

class TerminalInput extends HTMLElement {
  connectedCallback() {
    if (this.inited) return;
    this.inited = true;
    this.teardown = initTerminalInput(this);
  }

  disconnectedCallback() {
    this.teardown?.();
    this.teardown = null;
    this.inited = false;
  }
}

customElements.define("terminal-input", TerminalInput);
