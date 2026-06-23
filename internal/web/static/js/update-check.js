(function () {
  "use strict";

  const slot = document.getElementById("dc-update");
  if (!slot) return;

  const dark = { background: "#1f2937", color: "#f8fafc" };
  const csrf = slot.dataset.csrf || "";
  const runningVersion = slot.dataset.version || "";
  const INTERVAL = 5 * 60 * 1000;
  const KEY = "dcUpdate";
  let status = null;
  let timer = null;

  function loadState() {
    try {
      return JSON.parse(localStorage.getItem(KEY) || "null") || {};
    } catch (_) {
      return {};
    }
  }
  function saveState(patch) {
    const s = loadState();
    Object.assign(s, patch);
    try {
      localStorage.setItem(KEY, JSON.stringify(s));
    } catch (_) {}
  }

  function arm(delayMs) {
    clearTimeout(timer);
    timer = setTimeout(() => doCheck(false), Math.max(0, Math.min(delayMs, INTERVAL)));
  }

  function rearm() {
    saveState({ next: Date.now() + INTERVAL });
    arm(INTERVAL);
  }

  function doCheck(force) {
    const p = check(force);
    p.then(rearm, rearm);
    return p;
  }

  function scheduleCheck() {
    const next = loadState().next;
    const now = Date.now();
    if (!next || next <= now || next > now + INTERVAL) {
      doCheck(false);
    } else {
      arm(next - now);
    }
  }

  function check(force) {
    const url = "/update/check" + (force ? "?force=1" : "");
    return fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" })
      .then((r) => (r.ok ? r.json() : null))
      .then((s) => {
        if (s) {
          saveState({ status: s });
          render(s);
        }
        return s;
      })
      .catch(() => null);
  }

  function usable(s) {
    return s && s.supported && s.current === runningVersion;
  }

  function render(s) {
    status = s;
    if (!s.supported) return;
    renderFooter(s);
    renderFlags(s);
  }

  function footerLink(className, text) {
    const link = document.createElement("a");
    link.href = "#";
    link.dataset.updateOpen = "";
    link.className = className + " text-decoration-none";
    link.textContent = text;
    slot.replaceChildren(document.createTextNode("("), link, document.createTextNode(")"));
  }

  function renderInitial() {
    footerLink("link-secondary", "Up to date");
  }

  function renderFooter(s) {
    footerLink(s.available ? "link-primary" : "link-secondary", s.available ? "Update to " + s.latest : "Up to date");
  }

  function renderFlags(s) {
    document.querySelectorAll(".js-update-flag").forEach((el) => {
      el.classList.toggle("d-none", !s.available);
      const title = el.querySelector(".nav-link-title");
      if (title) title.textContent = "Update to " + s.latest;
    });
    document.querySelectorAll(".js-update-spacer").forEach((el) => {
      el.classList.toggle("d-none", s.available);
    });
  }

  function openDialog() {
    Swal.fire(
      Object.assign(
        { title: "Checking for updates…", allowOutsideClick: false, allowEscapeKey: false, didOpen: () => Swal.showLoading() },
        dark,
      ),
    );
    doCheck(true).then((s) => {
      const cur = s || status;
      if (!cur || !cur.supported) {
        Swal.fire(Object.assign({ icon: "error", title: "Could not check for updates" }, dark));
      } else if (cur.available) {
        confirmUpdate(cur);
      } else {
        Swal.fire(
          Object.assign({ icon: "success", title: "Up to date", text: "Running version " + cur.current + "." }, dark),
        );
      }
    });
  }

  function changelog(releases) {
    const wrap = document.createElement("div");
    wrap.style.textAlign = "left";
    wrap.style.maxHeight = "50vh";
    wrap.style.overflowY = "auto";
    releases.forEach((rel) => {
      const head = document.createElement("div");
      head.className = "fw-bold mt-3";
      let title = rel.name && rel.name !== rel.version ? rel.version + " — " + rel.name : "Version " + rel.version;
      if (rel.date) title += "  (" + rel.date.slice(0, 10) + ")";
      head.textContent = title;

      const body = document.createElement("div");
      body.className = "small dc-md";
      if (rel.notesHtml) {
        body.innerHTML = rel.notesHtml;
        body.querySelectorAll("a[href]").forEach((a) => {
          a.target = "_blank";
          a.rel = "noopener noreferrer";
        });
      } else {
        body.textContent = (rel.notes || "").trim() || "No release notes.";
      }

      wrap.append(head, body);
    });
    return wrap;
  }

  function confirmUpdate(s) {
    const opts = Object.assign(
      {
        title: "Update to " + s.latest + "?",
        html: changelog(s.releases || []),
        showCancelButton: true,
        confirmButtonText: "Update & restart",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        width: "44rem",
      },
      dark,
    );
    if (!s.writable) {
      opts.footer = "⚠ The binary location is not writable here, the update will fail.";
    }
    Swal.fire(opts).then((r) => {
      if (r.isConfirmed) apply(s);
    });
  }

  function apply(s) {
    Swal.fire(
      Object.assign(
        {
          title: "Updating…",
          text: "Downloading and restarting.",
          allowOutsideClick: false,
          allowEscapeKey: false,
          didOpen: () => Swal.showLoading(),
        },
        dark,
      ),
    );
    fetch("/update/apply", {
      method: "POST",
      headers: { "X-CSRF-Token": csrf, Accept: "application/json" },
    })
      .then(async (r) => {
        if (!r.ok) throw new Error(await window.errorText(r, "Update failed."));
        waitForRestart(s.latest);
      })
      .catch((e) => {
        Swal.fire(Object.assign({ icon: "error", title: "Update failed", text: e.message }, dark));
      });
  }

  function waitForRestart(target) {
    const deadline = Date.now() + 60000;
    const poll = () => {
      fetch("/update/check", { headers: { Accept: "application/json" }, cache: "no-store" })
        .then((r) => (r.ok ? r.json() : null))
        .then((s) => {
          if (s && s.current === target) {
            Swal.fire(
              Object.assign(
                { icon: "success", title: "Updated to " + target, timer: 2000, showConfirmButton: false },
                dark,
              ),
            ).then(() => location.reload());
            return;
          }
          retry();
        })
        .catch(retry);
    };
    const retry = () => {
      if (Date.now() > deadline) {
        Swal.fire(
          Object.assign(
            { icon: "warning", title: "Restart is taking a while", text: "Reload the page in a moment." },
            dark,
          ),
        );
        return;
      }
      setTimeout(poll, 1500);
    };
    setTimeout(poll, 2000);
  }

  document.addEventListener("click", (e) => {
    const trigger = e.target.closest("[data-update-open]");
    if (!trigger) return;
    e.preventDefault();
    openDialog();
  });

  window.addEventListener("storage", (e) => {
    if (e.key !== KEY) return;
    const st = loadState();
    if (st.next && st.next > Date.now()) arm(st.next - Date.now());
    if (usable(st.status)) render(st.status);
  });

  const cached = loadState().status;
  if (usable(cached)) {
    render(cached);
  } else {
    renderInitial();
  }
  scheduleCheck();
})();
