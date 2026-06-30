// Generic "show N more" fold. Collapses a container's children to the first
// `limit` entries and appends a toggle button. The caller owns the expanded
// state (dataset flag, in-memory Set, …) and re-invokes applyFold from onToggle.
export function applyFold(container, options = {}) {
  const {
    limit = 5,
    items = Array.from(container.children),
    expanded = false,
    hiddenClass = "d-none",
    toggleAttr = "data-fold-toggle",
    toggleClass = "",
    label = (count) => "Show " + count + " more",
    collapsedLabel = "Show less",
    onToggle = () => {},
    signal,
  } = options;

  const existing = container.querySelector(":scope > [" + toggleAttr + "]");
  if (existing) {
    existing.remove();
  }

  const real = items.filter((node) => !node.hasAttribute(toggleAttr));
  if (real.length <= limit) {
    real.forEach((node) => node.classList.remove(hiddenClass));
    return;
  }

  const hidden = real.slice(limit);
  real.slice(0, limit).forEach((node) => node.classList.remove(hiddenClass));
  hidden.forEach((node) => node.classList.toggle(hiddenClass, !expanded));

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.setAttribute(toggleAttr, "");
  toggle.className = toggleClass;
  toggle.textContent = expanded ? collapsedLabel : label(hidden.length);
  toggle.addEventListener("click", (event) => onToggle(event, !expanded), { signal });
  container.appendChild(toggle);
}
