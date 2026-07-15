import { onServerEvent } from "@dc/events";
import { getJSON, postForm } from "@dc/http";
import { playNotification } from "@dc/jingle";

// Notification bell + center. The element renders a bell with an unread badge
// and a dropdown listing recent "coder finished" / "needs attention" events.
// The header mounts one instance per breakpoint, so the toast, the title counter
// and the shared unread state live in a module level channel shared by all
// instances; each element only mirrors the channel state into its own DOM. The
// server stream itself is the app-wide one in @dc/events; the channel just
// subscribes to its "notifications" events.

const channel = {
  unread: null,
  targets: [],
  held: new Map(),
  listeners: new Set(),
  readUrl: "/notifications/read",

  addListener(listener, readUrl) {
    if (readUrl) this.readUrl = readUrl;
    this.listeners.add(listener);
    if (this.unread !== null) listener({ unread: this.unread });
  },

  removeListener(listener) {
    this.listeners.delete(listener);
  },

  receive(payload) {
    if (!payload) return;
    this.targets = payload.targets || [];
    // A toast already on screen and the own visible page act on the real
    // server state right away, independent of the grace window below.
    dismissReadToast(this.targets);
    reconcileOwnTarget(this.targets);
    // Every brand new unread enters the grace window instead of showing at
    // once; see the graceDelay note for why. The own visible page is held too:
    // its automatic read lands inside the window and drops the hold, so the
    // badge never even flashes on the page you are looking at.
    if (payload.added) this.hold(payload.added);
    this.reconcileHolds();
    this.render();
  },

  // hold puts one target's news into the grace window: nothing about it shows
  // (no badge, dot, toast or jingle) until the timer fires. A read landing
  // first clears it in reconcileHolds, so the whole notification stays silent.
  hold(added) {
    const sid = added.targetId;
    const existing = this.held.get(sid);
    if (existing) clearTimeout(existing.timer);
    const timer = setTimeout(() => {
      this.held.delete(sid);
      this.render();
      // If the grace window elapsed with the target's own page visibly open
      // (a slow read), surface it quietly: no toast, no jingle.
      if (ownVisibleTarget() !== sid) toast(added);
    }, graceDelay);
    this.held.set(sid, { timer, added });
  },

  // reconcileHolds drops a held target the server no longer reports as
  // unread: it was read within the grace window, so its news never surfaces.
  reconcileHolds() {
    const unread = new Set(this.targets);
    this.held.forEach((entry, sid) => {
      if (unread.has(sid)) return;
      clearTimeout(entry.timer);
      this.held.delete(sid);
    });
  },

  // render applies the visible state minus whatever is still held. Because a
  // target holds at most one unread entry, the shown count is just the number
  // of shown targets.
  render() {
    const shown = this.targets.filter((id) => !this.held.has(id));
    const count = shown.length;
    const pulse = this.unread !== null && count > this.unread;
    this.unread = count;
    updateTitle(count);
    updateCountBadges(count);
    decorateNews(shown);
    this.listeners.forEach((listener) => listener({ unread: count, pulse }));
  },
};

// The app-wide stream (see @dc/events) delivers every server push; the channel
// only handles the notification ones. It resends a notification snapshot on
// every reconnect, so a woken background tab reconciles from here too.
onServerEvent("notifications", (event) => channel.receive(event.detail));

// On becoming visible again, re-check whether the target whose page is open
// should mark itself read. The stream reconnect is owned by @dc/events.
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  reconcileOwnTarget(channel.targets);
});

function updateTitle(unread) {
  const base = document.title.replace(/^\(\d+\+?\)\s/, "");
  document.title = unread > 0 ? `(${unread > 99 ? "99+" : unread}) ${base}` : base;
}

// Targets hold at most one unread entry, so the unread count doubles as the
// number of targets with news. Any element opting in via [data-notify-count]
// (e.g. the quick nav toggle) mirrors it.
function updateCountBadges(unread) {
  document.querySelectorAll("[data-notify-count]").forEach((badge) => {
    badge.textContent = unread > 99 ? "99+" : String(unread);
    badge.classList.toggle("d-none", unread === 0);
  });
}

// Server-rendered target lists opt into live news marks: a [data-notify-target]
// coder or shell icon turns blue and dances while it has news, and
// [data-notify-project-dot] shows while any target inside the same project
// container has news.
function decorateNews(targetIds) {
  const ids = new Set(targetIds || []);
  document.querySelectorAll("[data-notify-target]").forEach((icon) => {
    icon.classList.toggle("news", ids.has(icon.getAttribute("data-notify-target")));
  });
  document.querySelectorAll("[data-notify-project-dot]").forEach((dot) => {
    const scope = dot.closest("[data-project-name]");
    if (!scope) return;
    const any = [...scope.querySelectorAll("[data-notify-target]")]
      .some((el) => ids.has(el.getAttribute("data-notify-target")));
    dot.classList.toggle("d-none", !any);
  });
}

function targetURL(notification) {
  return notification.url || "/coders/" + encodeURIComponent(notification.targetId);
}

function openTarget(notification) {
  postForm(channel.readUrl, { id: notification.id })
    .catch(() => {})
    .finally(() => {
      window.location.href = targetURL(notification);
    });
}

// ownVisibleTarget returns the coder or shell id when the current page is
// its attach page in a visible tab, else null.
function ownVisibleTarget() {
  if (document.visibilityState !== "visible") return null;
  const match = window.location.pathname.match(/^\/(?:coders|shells)\/([^/]+)$/);
  return match ? decodeURIComponent(match[1]) : null;
}

// Unread news for the target whose page is visibly open is news the user is
// already looking at: mark the whole target read quietly. Runs on every
// event including the initial one, so a page frozen in the background (a
// locked phone) reconciles itself right after its SSE stream reconnects, and
// again when the tab becomes visible.
let markingOwn = false;
function reconcileOwnTarget(targets) {
  const own = ownVisibleTarget();
  if (!own || markingOwn || !(targets || []).includes(own)) return;
  markingOwn = true;
  postForm(channel.readUrl, { target: own })
    .catch(() => {})
    .finally(() => { markingOwn = false; });
}

// The whole notification (badge, title counter, list dots, toast, jingle)
// waits out a short grace period before it shows.
//
// Why: the server pushes a new unread event to every connected tab and device
// at once. When one tab has the target's own page open and visible, that tab
// marks the news read automatically, but that is a round trip (POST, then a
// read event back to everyone). Without the delay every other tab would have
// already bumped its badge, popped a toast and rung the jingle for news the
// user is, in that moment, actively looking at somewhere else. The grace
// window lets the read land first, so the news that was seen surfaces nowhere.
// The price we accepted: every notification appears about 0.75s later, even
// when nobody is looking, which is imperceptible for "coder done" or "command
// finished" and worth it to stop a phone ringing for a target open on the
// desktop.
const graceDelay = 750;

let shownToastTarget = null;

// A toast for a target that got read on another device is handled news:
// take it down instead of letting the timer run out.
function dismissReadToast(unreadTargets) {
  if (!shownToastTarget) return;
  if ((unreadTargets || []).includes(shownToastTarget)) return;
  shownToastTarget = null;
  window.Swal?.close();
}

function toast(added) {
  playNotification().catch(() => {});
  if (!window.Swal) return;

  let project;
  if (added.project) {
    project = document.createElement("div");
    project.className = "text-secondary small text-start text-break";
    const folder = document.createElement("i");
    folder.className = "ti ti-folder me-1";
    project.append(folder, document.createTextNode(added.project));
  }

  window.Swal.fire({
    toast: true,
    position: "top-end",
    icon: "info",
    title: `Something new in "${added.targetName}".`,
    html: project,
    customClass: { title: "text-break" },
    showConfirmButton: false,
    showCloseButton: true,
    timer: 8000,
    timerProgressBar: true,
    didOpen: (popup) => {
      popup.style.cursor = "pointer";
      popup.addEventListener("click", (event) => {
        if (event.target.closest(".swal2-close")) return;
        openTarget(added);
      });
    },
    didClose: () => {
      if (shownToastTarget === added.targetId) shownToastTarget = null;
    },
  });
  shownToastTarget = added.targetId;
}

function relativeTime(iso) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const seconds = Math.max(0, (Date.now() - t) / 1000);
  if (seconds < 45) return "just now";
  if (seconds < 3600) return Math.round(seconds / 60) + "m ago";
  if (seconds < 86400) return Math.round(seconds / 3600) + "h ago";
  return Math.round(seconds / 86400) + "d ago";
}

class Notifications extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;

    if (!this.querySelector(".dc-notify-bell")) {
      this.innerHTML = `
      <div class="dropdown">
        <button type="button" class="btn btn-icon dc-notify-bell" data-bs-toggle="dropdown" data-bs-auto-close="outside" aria-label="Notifications">
          <i class="ti ti-bell fs-2"></i>
          <span class="dc-notify-badge d-none" aria-hidden="true">0</span>
        </button>
        <div class="dropdown-menu dropdown-menu-end dc-notify-menu shadow">
          <div class="d-flex align-items-center gap-2 px-3 py-2 border-bottom">
            <span class="fw-bold">Notifications</span>
            <button type="button" class="btn btn-link btn-sm ms-auto p-0 dc-notify-read-all">Mark all read</button>
          </div>
          <div class="list-group list-group-flush dc-notify-list"></div>
        </div>
      </div>`;
    }

    this.bell = this.querySelector(".dc-notify-bell");
    this.badge = this.querySelector(".dc-notify-badge");
    this.menu = this.querySelector(".dc-notify-menu");
    this.list = this.querySelector(".dc-notify-list");
    this.renderEmpty("Nothing yet.");

    this.listener = (state) => this.apply(state);
    channel.addListener(this.listener, this.getAttribute("read-url"));

    this.addEventListener("show.bs.dropdown", () => this.refresh(), { signal });

    this.querySelector(".dc-notify-read-all").addEventListener("click", () => {
      postForm(channel.readUrl, { all: "1" })
        .catch(() => {})
        .finally(() => this.refresh());
    }, { signal });

    this.list.addEventListener("click", (event) => {
      const item = event.target.closest("a[data-notify-id]");
      if (!item) return;
      event.preventDefault();
      openTarget({ id: item.dataset.notifyId, targetId: item.dataset.notifyTarget, url: item.getAttribute("href") });
    }, { signal });
  }

  disconnectedCallback() {
    channel.removeListener(this.listener);
    this.ac?.abort();
    this.ac = null;
  }

  apply(state) {
    const unread = state.unread || 0;
    this.badge.textContent = unread > 99 ? "99+" : String(unread);
    this.badge.classList.toggle("d-none", unread === 0);
    if (state.pulse) {
      this.bell.classList.remove("dc-notify-ring");
      void this.bell.offsetWidth;
      this.bell.classList.add("dc-notify-ring");
    }
    if (this.menu.classList.contains("show")) this.refresh();
  }

  refresh() {
    const listUrl = this.getAttribute("list-url") || "/notifications";
    getJSON(listUrl)
      .then((data) => this.renderList(data.notifications || []))
      .catch(() => this.renderEmpty("Could not load notifications."));
  }

  renderEmpty(text) {
    const empty = document.createElement("div");
    empty.className = "text-center text-secondary py-4 px-3";
    const icon = document.createElement("i");
    icon.className = "ti ti-bell-off fs-1 d-block mb-2";
    const label = document.createElement("div");
    label.textContent = text;
    empty.append(icon, label);
    this.list.replaceChildren(empty);
  }

  renderList(items) {
    if (!items.length) {
      this.renderEmpty("Nothing yet.");
      return;
    }
    this.list.replaceChildren(...items.map((n) => this.renderItem(n)));
  }

  renderItem(n) {
    const item = document.createElement("a");
    item.href = targetURL(n);
    item.dataset.notifyId = n.id;
    item.dataset.notifyTarget = n.targetId;
    item.className = "list-group-item list-group-item-action d-flex align-items-start gap-3 dc-notify-item" + (n.read ? "" : " dc-notify-unread");

    const icon = document.createElement("i");
    icon.className = "ti ti-bell-ringing text-primary fs-2 dc-notify-icon";

    const body = document.createElement("div");
    body.className = "d-flex flex-column min-w-0 flex-fill";

    const name = document.createElement("span");
    name.className = "text-truncate";
    name.textContent = n.targetName;
    body.append(name);
    if (n.project) {
      const project = document.createElement("span");
      project.className = "text-secondary small text-truncate";
      const folder = document.createElement("i");
      folder.className = "ti ti-folder me-1";
      project.append(folder, document.createTextNode(n.project));
      body.append(project);
    }

    const time = document.createElement("div");
    time.className = "text-secondary small mt-1";
    time.textContent = relativeTime(n.createdAt);

    body.append(time);
    item.append(icon, body);

    if (!n.read) {
      const dot = document.createElement("span");
      dot.className = "status-dot status-dot-animated bg-blue mt-2 flex-shrink-0";
      item.append(dot);
    }
    return item;
  }
}

customElements.define("dc-notifications", Notifications);
