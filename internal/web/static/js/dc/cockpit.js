// The phone's Cockpit tab says in its name what its icon and its dot show:
// Cockpit, the server's state while a reading passed a threshold, and news
// while the dot stands. Two elements paint those, host-status.js the gauge
// and notifications.js the dot, and both call this after they did, so the
// name is read off what stands on the page and never composed twice.
export function labelCockpitTab() {
  document.querySelectorAll('.dc-tabbar [data-ctx-area="cockpit"]').forEach((tab) => {
    const parts = ["Cockpit"];
    const icon = tab.querySelector("[data-host-worst-icon]");
    if (icon?.classList.contains("text-red")) parts.push("server critical");
    else if (icon?.classList.contains("text-yellow")) parts.push("server busy");
    const dot = tab.querySelector("[data-cockpit-dot]");
    if (dot && !dot.classList.contains("d-none")) parts.push("news");
    if (parts.length > 1) tab.setAttribute("aria-label", parts.join(", "));
    else tab.removeAttribute("aria-label");
  });
}
