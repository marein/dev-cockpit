(() => {
  const config = window.__SESSION_ATTACH_CONFIG__ || {};
  const renameUrl = config.renameUrl;
  const csrfToken = config.csrfToken || "";

  const container = document.querySelector("[data-shell-rename]");
  if (!container || !renameUrl) {
    return;
  }
  const label = container.querySelector("[data-shell-name]");
  const input = container.querySelector("[data-shell-name-input]");
  if (!label || !input) {
    return;
  }

  let editing = false;
  let saving = false;

  const showLabel = () => {
    input.classList.add("d-none");
    label.classList.remove("d-none");
    editing = false;
  };

  const showInput = () => {
    if (editing) {
      return;
    }
    editing = true;
    input.value = label.textContent.trim();
    label.classList.add("d-none");
    input.classList.remove("d-none");
    input.focus();
    input.select();
  };

  const applyName = (name) => {
    label.textContent = name;
    input.value = name;
    const suffix = " - Dev Cockpit";
    document.title = name + suffix;
  };

  const save = async () => {
    if (saving) {
      return;
    }
    const name = input.value.trim();
    if (name === "" || name === label.textContent.trim()) {
      showLabel();
      return;
    }
    saving = true;
    try {
      const headers = {
        "Content-Type": "application/x-www-form-urlencoded",
        Accept: "application/json",
      };
      if (csrfToken) {
        headers["X-CSRF-Token"] = csrfToken;
      }
      const response = await fetch(renameUrl, {
        method: "POST",
        headers,
        body: new URLSearchParams({ name }),
      });
      if (!response.ok) {
        throw new Error(await window.errorText(response, "Could not rename shell."));
      }
      const payload = await response.json().catch(() => ({}));
      applyName(payload.name || name);
    } catch (error) {
      input.value = label.textContent.trim();
      if (window.notifyError) {
        window.notifyError(error.message || "Could not rename shell.");
      }
    } finally {
      saving = false;
      showLabel();
    }
  };

  label.addEventListener("click", showInput);
  label.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      showInput();
    }
  });

  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      void save();
    } else if (event.key === "Escape") {
      event.preventDefault();
      input.value = label.textContent.trim();
      showLabel();
    }
  });
  input.addEventListener("blur", () => {
    if (editing && !saving) {
      void save();
    }
  });
})();
