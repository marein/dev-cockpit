(function () {
  "use strict";

  const menu = document.querySelector(".quicknav-menu[data-quicknav-url]");
  if (!menu) return;

  const root = menu.closest(".quicknav");
  const toggle = root.querySelector(".quicknav-toggle");
  const list = menu.querySelector(".quicknav-list");
  const url = menu.dataset.quicknavUrl;

  const spinner = document.createElement("div");
  spinner.className = "quicknav-refresh";
  spinner.setAttribute("role", "status");
  spinner.setAttribute("aria-label", "Loading");

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
