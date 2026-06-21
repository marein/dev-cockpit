(() => {
  const config = window.__SESSION_ATTACH_CONFIG__ || {};
  const inputUrl = config.inputUrl;
  const csrfToken = config.csrfToken || "";
  const scrollHistory = Boolean(config.scrollHistory);

  const SCROLL_CONTROLS = {
    "page-up": "scroll-up",
    "page-down": "scroll-down",
    "page-top": "scroll-top",
    "page-bottom": "scroll-bottom",
  };
  const mapControl = (control) =>
    scrollHistory && SCROLL_CONTROLS[control] ? SCROLL_CONTROLS[control] : control;

  const input = document.getElementById("session-prompt");
  const promptModalElement = document.getElementById("session-prompt-modal");
  const promptModalOpenButtons = document.querySelectorAll("[data-session-prompt-modal-open]");
  const promptModalForm = document.getElementById("session-prompt-modal-form");
  const promptModalTextarea = document.getElementById("session-prompt-modal-text");
  const controlButtons = document.querySelectorAll("[data-session-control]");
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
    ctrlToggle.addEventListener("click", () => setCtrlArmed(!ctrlArmed));
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

  const performSessionInput = async (items) => {
    const headers = { "Content-Type": "application/json", Accept: "application/json" };
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }
    const response = await fetch(inputUrl, {
      method: "POST",
      headers,
      body: JSON.stringify({ items }),
    });
    if (!response.ok) {
      const fallback = "Could not send input to the terminal.";
      throw new Error(window.errorText ? await window.errorText(response, fallback) : fallback);
    }
  };

  const pumpSessionInputs = async () => {
    if (inputInFlight) {
      return;
    }
    inputInFlight = true;
    try {
      while (pendingInputs.length > 0) {
        const items = pendingInputs;
        pendingInputs = [];
        try {
          await performSessionInput(items);
        } catch (requestError) {
          // Surface a clean message (e.g. "Your session has expired…") through
          // the shared toast channel; dedup collapses a burst of keystrokes.
          if (window.notifyError) {
            window.notifyError(requestError.message);
          }
        }
      }
    } finally {
      inputInFlight = false;
    }
  };

  const sendSessionInput = (payload) => {
    const last = pendingInputs[pendingInputs.length - 1];
    if (payload.text && last && last.text) {
      last.text += payload.text;
    } else if (payload.raw && last && last.raw) {
      last.raw += payload.raw;
    } else {
      pendingInputs.push(payload);
    }
    void pumpSessionInputs();
  };

  if (input) {
    input.addEventListener("keydown", (event) => {
      if (event.key === "Tab" && event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        void sendSessionInput({ control: "shift-tab" });
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
        void sendSessionInput({ control: `${modifiers.join("-")}-${key}` });
        return;
      }
      const control = keyMap[event.key];
      if (control) {
        event.preventDefault();
        const mapped = mapControl(control);
        void sendSessionInput({ control: event.altKey && mapped === control ? `alt-${control}` : mapped });
      }
    });

    input.addEventListener("beforeinput", (event) => {
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
          void sendSessionInput({ control: "ctrl-" + ch });
        } else {
          void sendSessionInput({ text: event.data[0] });
        }
        const rest = event.data.slice(1);
        if (rest) {
          void sendSessionInput({ text: rest });
        }
        return;
      }
      void sendSessionInput({ text: event.data });
    });

    input.addEventListener("compositionend", (event) => {
      input.value = "";
      if (event.data) {
        void sendSessionInput({ text: event.data });
      }
    });

    input.addEventListener("paste", (event) => {
      const pastedText = event.clipboardData?.getData("text") ?? "";
      if (!pastedText) {
        return;
      }
      event.preventDefault();
      void sendSessionInput({ paste: pastedText });
    });
  }

  if (promptModalElement && promptModalTextarea) {
    promptModalElement.addEventListener("shown.bs.modal", () => {
      promptModalTextarea.focus();
      promptModalTextarea.select();
    });
    promptModalElement.addEventListener("hidden.bs.modal", () => {
      promptModalTextarea.value = "";
    });
  }

  for (const button of promptModalOpenButtons) {
    button.addEventListener("click", () => {
      window.setTimeout(() => {
        promptModalTextarea?.focus();
      }, 0);
    });
  }

  promptModalForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const prompt = promptModalTextarea?.value ?? "";
    if (prompt === "") {
      promptModalTextarea?.focus();
      return;
    }
    void sendSessionInput({ prompt });
    if (promptModalElement) {
      window.bootstrap?.Modal.getOrCreateInstance(promptModalElement).hide();
    }
  });

  promptModalTextarea?.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault();
      promptModalForm?.requestSubmit();
    }
  });

  for (const button of controlButtons) {
    const control = button.dataset.sessionControl;
    if (!control) {
      continue;
    }
    let suppressClick = false;
    const fire = () => {
      void sendSessionInput({ control });
    };
    const repeater = window.createRepeater(fire, REPEAT_INITIAL_DELAY, REPEAT_INTERVAL);
    const start = (event) => {
      if (event.button !== undefined && event.button !== 0) {
        return;
      }
      event.preventDefault();
      repeater.start();
      suppressClick = true;
    };
    button.addEventListener("pointerdown", start);
    button.addEventListener("pointerup", repeater.stop);
    button.addEventListener("pointerleave", repeater.stop);
    button.addEventListener("pointercancel", repeater.stop);
    button.addEventListener("blur", repeater.stop);
    button.addEventListener("click", (event) => {
      if (suppressClick) {
        event.preventDefault();
        suppressClick = false;
        return;
      }
      fire();
    });
    button.addEventListener("keydown", (event) => {
      if (event.repeat) {
        return;
      }
      if (event.key === " " || event.key === "Enter") {
        event.preventDefault();
        repeater.start();
        suppressClick = true;
      }
    });
    button.addEventListener("keyup", (event) => {
      if (event.key === " " || event.key === "Enter") {
        repeater.stop();
      }
    });
  }

  document.addEventListener("session-control", (event) => {
    const control = event.detail?.control;
    if (typeof control === "string" && control !== "") {
      void sendSessionInput({ control });
    }
  });

  // Raw terminal bytes emitted by xterm when a desktop client types straight
  // into the terminal (see session-attach.js).
  document.addEventListener("session-input", (event) => {
    const raw = event.detail?.raw;
    if (typeof raw === "string" && raw !== "") {
      void sendSessionInput({ raw });
    }
  });
})();
