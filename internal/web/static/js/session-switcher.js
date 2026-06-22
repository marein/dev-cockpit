(function () {
  "use strict";

  const menu = document.querySelector(".session-switcher-menu[data-switcher-url]");
  if (!menu) return;

  const root = menu.closest(".session-switcher");
  const toggle = root.querySelector(".session-switcher-toggle");
  const list = menu.querySelector(".session-switcher-list");
  const url = menu.dataset.switcherUrl;

  const spinner = document.createElement("div");
  spinner.className = "session-switcher-refresh";
  spinner.innerHTML =
    '<span class="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>';

  let inFlight = false;

  function reposition() {
    if (window.bootstrap) bootstrap.Dropdown.getOrCreateInstance(toggle).update();
  }

  function refresh() {
    if (inFlight) return;
    inFlight = true;
    menu.appendChild(spinner);
    fetch(url + "?path=" + encodeURIComponent(location.pathname), {
      headers: { Accept: "text/html" },
      cache: "no-store",
    })
      .then((r) => (r.ok ? r.text() : Promise.reject()))
      .then((html) => {
        list.innerHTML = html;
        reposition();
      })
      .catch(() => {})
      .finally(() => {
        spinner.remove();
        inFlight = false;
      });
  }

  root.addEventListener("show.bs.dropdown", refresh);
})();
