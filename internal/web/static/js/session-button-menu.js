(() => {
  const menus = document.querySelectorAll("[data-session-button-menu]");

  for (const root of menus) {
    const trigger = root.querySelector("[data-session-button-menu-trigger]");
    const menu = root.querySelector("[data-session-button-menu-menu]");
    if (!(trigger instanceof HTMLElement) || !(menu instanceof HTMLElement)) {
      continue;
    }

    const positionMenu = () => {
      const rect = trigger.getBoundingClientRect();
      menu.style.left = `${rect.left + rect.width / 2}px`;
      menu.style.top = `${rect.top - 8}px`;
    };

    const open = () => {
      root.setAttribute("data-open", "");
      trigger.setAttribute("aria-expanded", "true");
      positionMenu();
    };

    const close = () => {
      root.removeAttribute("data-open");
      trigger.setAttribute("aria-expanded", "false");
    };

    const isOpen = () => root.hasAttribute("data-open");

    const toggle = () => {
      if (isOpen()) {
        close();
      } else {
        open();
      }
    };

    const fireButton = (button) => {
      const control = button?.dataset?.sessionControl;
      if (!control) {
        return;
      }
      root.dispatchEvent(new CustomEvent("session-control", {
        bubbles: true,
        detail: { control },
      }));
    };

    trigger.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      toggle();
    });

    trigger.addEventListener("keydown", (event) => {
      if (event.key !== " " && event.key !== "Enter") {
        return;
      }
      event.preventDefault();
      open();
      menu.querySelector("[data-session-control]")?.focus();
    });

    menu.addEventListener("click", (event) => {
      const button = event.target?.closest?.("[data-session-control]");
      if (!(button instanceof HTMLElement) || !menu.contains(button)) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      fireButton(button);
      close();
      trigger.focus();
    });

    menu.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") {
        return;
      }
      event.preventDefault();
      close();
      trigger.focus();
    });

    root.addEventListener("focusout", (event) => {
      if (!root.contains(event.relatedTarget)) {
        close();
      }
    });

    document.addEventListener("pointerdown", (event) => {
      if (root.contains(event.target)) {
        return;
      }
      close();
    }, true);

    window.addEventListener("resize", () => {
      if (isOpen()) {
        positionMenu();
      }
    });
  }
})();
