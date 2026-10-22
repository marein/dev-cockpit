import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { confirm, promptText } from "@dc/dialog";
import { el, syncAnimations } from "@dc/dom";
import { DoubleTap } from "@dc/doubletap";
import { onServerEvent } from "@dc/events";
import { ensureOk, getText, pagePath, postForm, postJSON } from "@dc/http";
import { splitCreateItems } from "@dc/split";
import { renameCoder, renameShell } from "@dc/rename";
import { RowDrag } from "@dc/rowdrag";
import { get } from "@dc/store";
import { notifyError, notifySuccess } from "@dc/toast";
import { alsoDropped, releaseCoder, steerCoder } from "@dc/steer";
import { NewMenu } from "@dc/terminal-new-menu";
import { Switcher } from "@dc/terminal-switcher";
import { openFormModal } from "dc-form-modal";

class TerminalTabs extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.strip = this.querySelector("[data-tabs-strip]");
    if (!this.strip) return;
    this.switcherOnly = this.hasAttribute("hidden");
    this.vertical = this.hasAttribute("data-tabs-vertical");
    this.ac = new AbortController();
    this.switcher = null;
    this.confirming = false;
    this.inFlight = false;
    this.dirty = false;
    this.pendingIndex = null;
    this.tap = new DoubleTap();
    const signal = this.ac.signal;

    this.revealActive(this.vertical);
    const menu = this.querySelector(".terminal-tabs-new-menu");
    this.newMenu = menu ? new NewMenu(menu, signal) : null;
    this.strip.addEventListener("wheel", (event) => this.onWheel(event), { signal, passive: false });
    if (!this.hasAttribute("data-tabs-guest")) this.wireDrag(signal);
    this.strip.addEventListener("click", (event) => this.onClick(event), { signal, capture: true });
    // The row menu on the right click and on the touch long press, the shared
    // gesture every list column carries.
    if (!this.hasAttribute("data-tabs-guest")) wireRowMenus(this.strip, ".terminal-tab, .terminal-tab-member", (row, x, y) => {
      if (!row) return false;
      this.openRowMenu(row, x, y);
      return true;
    }, { signal });
    document.addEventListener("keydown", (event) => this.onKeydown(event), { signal, capture: true });
    document.addEventListener("keyup", (event) => this.onKeyup(event), { signal, capture: true });
    // A modifier click is not a bare modifier tap.
    document.addEventListener("pointerdown", () => this.tap.reset(), { signal, capture: true });
    document.addEventListener("dc-renamed", (event) => this.onRenamed(event.detail || {}), { signal });
    // A coder or shell started, stopped, was renamed or reordered somewhere (this
    // device or another one), or a project the switcher and the resume groups
    // list came or went: pull the fresh strip.
    for (const type of ["terminals", "assistant", "projects"]) onServerEvent(type, () => this.refresh(), { signal });
    // The + menu's create entries and the editor link carry the page's
    // project context, which on a split page is the focused member's. The
    // fragment pull already reports the active island as ?focus; what was
    // missing is the trigger, activating a pane is client side only, so the
    // menu kept the context of the last terminals event. The pull is only
    // paid when the context actually moved: a solo page, a re-activation and
    // the activation right after a render all match what the links carry.
    document.addEventListener("dc:terminal-activated", () => this.onPaneActivated(), { signal });
  }

  onPaneActivated() {
    const active = document.querySelector("terminal-attach[active][split-group]");
    const id = active?.getAttribute("terminal-id") || "";
    const link = this.querySelector('[data-tabs-new="shell"]');
    if (!id || !link) return;
    const ret = new URL(link.getAttribute("href") || "", window.location.href).searchParams.get("return") || "";
    const focus = new URL(ret, window.location.href).searchParams.get("focus") || "";
    if (focus === id) return;
    this.refresh();
  }

  onRenamed({ url, name }) {
    if (!url || !name) return;
    const segments = new URL(url, window.location.href).pathname.split("/");
    const tab = this.tabs().find((candidate) => segments.includes(candidate.dataset.tabId));
    if (!tab) return;
    tab.dataset.tabName = name;
    const label = tab.querySelector(".terminal-tab-name");
    if (label) label.textContent = name;
    const project = tab.dataset.tabProject;
    tab.setAttribute("title", name + (project ? " (" + project + ")" : ""));
    const close = tab.querySelector("[data-tab-close]");
    if (close) {
      close.setAttribute("aria-label", (tab.dataset.tabKind === "coder" ? "Stop coder " : "Delete shell ") + name);
    }
  }

  disconnectedCallback() {
    this.closeSwitcher();
    this.rowDrag?.cancel();
    this.ac?.abort();
    this.ac = null;
  }

  tabs() {
    return Array.from(this.strip.querySelectorAll(".terminal-tab"));
  }

  memberIds(tab) {
    return (tab.dataset.tabMembers || tab.dataset.tabId || "").split(" ").filter(Boolean);
  }

  persistMemberOrder(group) {
    const ids = Array.from(this.strip.querySelectorAll(`.terminal-tab-member[data-tab-group="${group}"]`)).map((row) => row.dataset.tabId);
    postJSON("/terminal-tabs/group", { ids })
      .then((response) => ensureOk(response, "Could not save the pane order."))
      .catch((error) => notifyError(error.message));
  }

  persistOrder() {
    const ids = this.tabs().flatMap((tab) => this.memberIds(tab));
    postJSON("/terminal-tabs/order", { ids })
      .then((response) => ensureOk(response, "Could not save the tab order."))
      .catch((error) => notifyError(error.message));
  }

  revealTab(tab, center = false) {
    if (!tab) return;
    if (this.vertical) {
      tab.scrollIntoView({ block: center ? "center" : "nearest" });
      return;
    }
    const strip = this.strip;
    if (tab.offsetLeft < strip.scrollLeft
      || tab.offsetLeft + tab.offsetWidth > strip.scrollLeft + strip.clientWidth) {
      strip.scrollLeft = tab.offsetLeft - (strip.clientWidth - tab.offsetWidth) / 2;
    }
  }

  revealActive(center = false) {
    const member = this.strip.querySelector(".terminal-tab-member.active");
    const tab = member && member.getClientRects().length ? member : this.strip.querySelector(".terminal-tab.active");
    this.revealTab(tab, center);
  }

  unitRows(tab) {
    if (tab.dataset.tabKind !== "split") return [tab];
    return [tab, ...this.strip.querySelectorAll(`.terminal-tab-member[data-tab-group="${CSS.escape(tab.dataset.tabId)}"]`)];
  }

  markPending(tab) {
    for (const other of this.strip.querySelectorAll(".terminal-tab-pending")) {
      if (other !== tab) other.classList.remove("terminal-tab-pending");
    }
    if (tab) {
      tab.classList.add("terminal-tab-pending");
      this.revealTab(tab);
    }
  }

  onWheel(event) {
    if (this.vertical || !event.deltaY || event.deltaX) return;
    if (this.strip.scrollWidth <= this.strip.clientWidth) return;
    event.preventDefault();
    this.strip.scrollLeft += event.deltaY;
  }

  onClick(event) {
    const close = event.target.closest("[data-tab-close]");
    if (close) {
      event.preventDefault();
      event.stopPropagation();
      const tab = close.closest(".terminal-tab");
      if (tab) void this.closeTarget(tab.dataset);
      return;
    }
    const menu = event.target.closest("[data-tab-menu]");
    if (menu) {
      event.preventDefault();
      event.stopPropagation();
      const tab = menu.closest(".terminal-tab, .terminal-tab-member");
      const rect = menu.getBoundingClientRect();
      if (tab) this.openRowMenu(tab, rect.right, rect.bottom);
      return;
    }
    const grip = event.target.closest("[data-tab-grip]");
    if (grip) {
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    const split = event.target.closest(".terminal-tab-split");
    if (split) this.aimSplit(split);
  }

  aimSplit(tab) {
    const focus = get("dc-split-active-" + tab.dataset.tabId, "");
    if (!focus || !this.memberIds(tab).includes(focus)) return;
    const href = tab.getAttribute("href") || "";
    tab.setAttribute("href", href.split("?")[0] + "?focus=" + encodeURIComponent(focus));
    window.setTimeout(() => tab.setAttribute("href", href), 0);
  }

  openRowMenu(tab, x, y) {
    this.rowDrag?.cancel();
    const dataset = { ...tab.dataset };
    const split = dataset.tabKind === "split";
    const items = [];
    if (dataset.tabKind === "shell" || dataset.tabRenames) {
      items.push({ label: "Rename", icon: "ti-pencil", action: () => void this.renameTab(dataset) });
    }
    if (split) {
      items.push({ label: "Rename split view", icon: "ti-pencil", action: () => void this.renameSplit(dataset) });
    }
    let newsTargets = [...tab.querySelectorAll("[data-notify-target].news")]
      .map((icon) => icon.getAttribute("data-notify-target")).filter(Boolean);
    if (split && !newsTargets.length && tab.querySelector("[data-tab-icon].news")) {
      newsTargets = this.memberIds(tab);
    }
    if (newsTargets.length) {
      items.push({
        label: "Mark read",
        icon: "ti-eye-check",
        action: () => newsTargets.forEach((id) => void this.markRead(id)),
      });
    }
    if (dataset.tabKind === "coder") {
      items.push(dataset.tabSteered
        ? {
          label: "Release",
          icon: "ti-steering-wheel-off",
          purple: true,
          action: () => void releaseCoder({ terminal: dataset.tabId, name: dataset.tabName }).then(() => this.tryRefresh()),
        }
        : {
          label: "Steer",
          icon: "ti-steering-wheel",
          purple: true,
          action: () => void steerCoder({
            terminal: dataset.tabId,
            name: dataset.tabName,
            prefill: dataset.tabSteerPrefill || "",
          }).then(() => this.tryRefresh()),
        });
    }
    if (split) {
      // The whole split is the context here, so the new pane opens a column of
      // its own; the project follows the focused member, like the quick nav's
      // project context row does.
      items.push({ divider: true });
      const focused = document.querySelector(`terminal-attach[active][split-group="${CSS.escape(dataset.tabId)}"]`)
        ?.closest(".attach-split-pane");
      items.push(...splitCreateItems({
        group: dataset.tabId,
        project: focused?.dataset.paneProject || dataset.tabProject || "",
      }));
    }
    if (dataset.tabProject) {
      items.push({ divider: true });
      items.push({ label: "Open project", icon: "ti-folder", href: "/projects#project-" + dataset.tabProject });
      items.push({
        label: "Open editor",
        icon: "ti-code",
        href: "/projects/" + encodeURIComponent(dataset.tabProject) + "/editor",
      });
    }
    items.push({ divider: true });
    if (dataset.tabGroup) {
      items.push({ label: "Remove from split view", icon: "ti-layout-off", action: () => void this.removeFromSplit(dataset) });
    }
    if (split) {
      items.push({ label: "Ungroup split view", icon: "ti-layout-off", action: () => void this.ungroupSplit(dataset) });
      items.push({
        label: "Close all terminals",
        icon: "ti-trash",
        danger: true,
        action: () => void this.closeSplitMembers(dataset),
      });
    } else {
      items.push({
        label: dataset.tabKind === "coder" ? "Stop" : "Delete",
        icon: dataset.tabKind === "coder" ? "ti-player-stop" : "ti-trash",
        danger: dataset.tabKind !== "coder",
        warn: dataset.tabKind === "coder",
        action: () => void this.closeTarget(dataset),
      });
      if (dataset.tabKind === "coder") {
        items.push({
          label: "Delete",
          icon: "ti-trash",
          danger: true,
          action: () => void this.closeTarget(dataset, true),
        });
      }
    }
    openMenu({ x, y, items, signal: this.ac.signal });
  }

  async groupTabs(base, added) {
    const involved = base.classList.contains("active") || added.classList.contains("active");
    const ids = [...this.memberIds(base), ...this.memberIds(added)];
    try {
      const response = await postJSON("/terminal-tabs/group", { ids });
      await ensureOk(response, "Could not create the split view.");
      const data = await response.json();
      if (involved && data.url) {
        if (data.url === window.location.pathname) {
          if (window.app?.navigate) Promise.resolve(window.app.navigate(data.url)).catch(() => {});
        } else {
          this.navigate(data.url);
        }
      }
    } catch (error) {
      notifyError(error.message);
    }
  }

  async renameSplit({ tabId, tabName }) {
    if (this.confirming) return;
    this.confirming = true;
    try {
      const name = await promptText({
        title: `Rename split view "${tabName}"`,
        value: tabName,
        confirmText: "Rename",
        allowEmpty: true,
      });
      if (name === null || name === tabName) return;
      const response = await postJSON("/terminal-tabs/group/name", { group: tabId, name });
      await ensureOk(response, "Could not rename the split view.");
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      this.tryRefresh();
    }
  }

  async removeFromSplit({ tabId, tabName }) {
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids: [tabId] });
      await ensureOk(response, "Could not change the split view.");
      notifySuccess(`"${tabName}" left the split view.`);
      this.tryRefresh();
    } catch (error) {
      notifyError(error.message);
    }
  }

  async ungroupSplit({ tabId, tabName }) {
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(tabId)}"]`);
    if (!tab) return;
    const current = tab.classList.contains("active");
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids: this.memberIds(tab) });
      await ensureOk(response, "Could not ungroup the split view.");
      const data = await response.json();
      notifySuccess(`Split view "${tabName}" ungrouped.`);
      if (current) this.navigate(data.url || "/terminals");
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.tryRefresh();
    }
  }

  async closeSplitMembers({ tabId, tabName }) {
    if (this.confirming) return;
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(tabId)}"]`);
    if (!tab) return;
    const ids = this.memberIds(tab);
    const kinds = (tab.dataset.tabMemberKinds || "").split(" ").filter(Boolean);
    const current = tab.classList.contains("active");
    this.confirming = true;
    try {
      const ok = await confirm({
        title: `Close all ${ids.length} terminals in "${tabName}"?`,
        confirmText: "Close all",
      });
      if (!ok) return;
      let failed = 0;
      for (let i = 0; i < ids.length; i += 1) {
        window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: ids[i] } }));
        const action = kinds[i] === "shell" ? `/shells/${ids[i]}/delete` : `/coders/${ids[i]}/stop`;
        try {
          const response = await postForm(action, {});
          await ensureOk(response, "Could not close the session.");
        } catch (memberError) {
          void memberError;
          failed += 1;
        }
      }
      if (failed) notifyError(`Could not close ${failed} of ${ids.length} terminals.`);
      else notifySuccess(`Split view "${tabName}" closed.`);
      this.removeTab(tabId);
      if (current) {
        const tabs = this.tabs();
        this.navigate(tabs[0]?.getAttribute("href") || "/terminals");
      }
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      this.tryRefresh();
    }
  }

  async renameTab({ tabId, tabKind, tabName, tabRenames, tabCoderLabel }) {
    if (this.confirming) return;
    this.confirming = true;
    await (tabKind === "shell" ? renameShell(tabId, tabName) : renameCoder(tabId, tabName, { mode: tabRenames, label: tabCoderLabel }));
    this.confirming = false;
    this.tryRefresh();
  }

  async markRead(id) {
    try {
      const response = await postForm("/notifications/read", { target: id });
      await ensureOk(response, "Could not mark the news read.");
    } catch (error) {
      notifyError(error.message);
    }
  }

  // The drag is @dc/rowdrag, the cockpit's one hand sorting gesture: a mouse
  // drags a row from anywhere, a finger from its grip, and the strip owns only
  // what is its own. A split row travels with its member rows, a member row is
  // dragged among its own siblings (no group target there, and its seat at the
  // end is before whatever follows the last member, not the end of the strip),
  // and a row resting on another one makes a split view of the two.
  wireDrag(signal) {
    this.rowDrag = new RowDrag(this.strip, {
      rowSelector: ".terminal-tab, .terminal-tab-member",
      ignoreSelector: "[data-tab-close], [data-tab-menu]",
      blocked: () => Boolean(this.switcher),
      vertical: () => this.vertical,
      scroller: () => (this.vertical ? this.strip.closest(".dc-ctx-body") || this.strip : this.strip),
      rows: (row) => (this.memberGroup(row)
        ? Array.from(this.strip.querySelectorAll(`.terminal-tab-member[data-tab-group="${this.memberGroup(row)}"]`))
        : this.tabs()),
      unitRows: (row) => (this.memberGroup(row) ? [row] : this.unitRows(row)),
      grouping: (row) => !this.memberGroup(row),
      endAnchor: (row, others) => (this.memberGroup(row) ? others[others.length - 1].nextSibling : null),
      onDrop: ({ row, groupRow }) => {
        if (groupRow) {
          void this.groupTabs(groupRow, row);
          return;
        }
        const group = this.memberGroup(row);
        if (group) this.persistMemberOrder(group);
        else this.persistOrder();
      },
      // Flush any event that arrived while the drag held the strip.
      onEnd: () => this.tryRefresh(),
    });
    this.rowDrag.wire(signal);
  }

  memberGroup(row) {
    return row.classList.contains("terminal-tab-member") ? row.dataset.tabGroup : "";
  }

  trackTap(event) {
    if (event.key !== "Control" && event.key !== "Meta") {
      this.tap.reset();
      return;
    }
    const otherModifiers = event.key === "Control"
      ? event.metaKey || event.altKey || event.shiftKey
      : event.ctrlKey || event.altKey || event.shiftKey;
    if (event.repeat || otherModifiers) {
      this.tap.reset();
      return;
    }
    this.tap.keydown(event.key);
  }

  onKeyup(event) {
    if (this.tap.keyup(event.key) && !this.confirming) {
      event.preventDefault();
      event.stopPropagation();
      if (!this.switcher) this.openSwitcher(1);
    }
  }

  onKeydown(event) {
    if (this.confirming) return;
    this.trackTap(event);
    if (event.key === "Tab" && event.ctrlKey && !event.altKey && !event.metaKey && !this.switcher) {
      if (this.switcherOnly) return;
      event.preventDefault();
      event.stopPropagation();
      this.switchTo(event.shiftKey ? -1 : 1);
      return;
    }
    if ((event.key === "t" || event.key === "T") && event.metaKey && !event.ctrlKey && !event.altKey && !this.switcher) {
      if (this.switcherOnly || !this.newMenu?.show()) return;
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    if ((event.key === "x" || event.key === "X") && (event.ctrlKey || event.metaKey)
      && event.shiftKey && !event.altKey && !event.repeat && !this.switcher) {
      if (this.switcherOnly || this.hasAttribute("data-tabs-guest")) return;
      const active = this.strip.querySelector(".terminal-tab.active");
      if (!active) return;
      event.preventDefault();
      event.stopPropagation();
      void this.closeTarget({ ...active.dataset });
      return;
    }
    if (this.newMenu?.open && !this.switcher && this.newMenu.onKeydown(event)) return;
    this.switcher?.onKeydown(event);
  }

  switchTo(direction) {
    const tabs = this.tabs();
    if (tabs.length < 2) return;
    const active = tabs.findIndex((tab) => tab.classList.contains("active"));
    const base = this.pendingIndex ?? (active === -1 ? 0 : active);
    const next = (base + direction + tabs.length) % tabs.length;
    this.pendingIndex = next;
    const url = tabs[next].getAttribute("href");
    if (!url || new URL(url, window.location.href).pathname === window.location.pathname) {
      window.pe?.abortController?.abort();
      this.markPending(null);
      return;
    }
    this.markPending(tabs[next]);
    if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
    else window.location.href = url;
  }

  openSwitcher(direction) {
    this.rowDrag?.cancel();
    this.switcher = Switcher.open(this, direction, this.ac.signal);
    if (this.switcher) this.tryRefresh();
  }

  // refresh marks the strip dirty and pulls the fresh strip and menu when nothing
  // blocks it. A `terminals` event that lands during a fetch, or while a close
  // confirm or a drag owns the strip, is coalesced into `dirty` and re-run once
  // the fetch settles or the gesture releases (tryRefresh from closeTarget /
  // and the drag's own onEnd), so no live change is ever dropped. The pull carries
  // this page's ?path so the active tab and the resume forms' CSRF stay right, and
  // keeps the + menu and switcher current so they need no refetch on open.
  refresh() {
    // The strip refreshes even while hidden on a coarse pointer (mobile): it is
    // invisible there, but terminal-swipe-nav reads its rows as the swipe
    // targets, so the order must stay live.
    this.dirty = true;
    if (this.switcherOnly && !this.switcher) return;
    this.tryRefresh();
  }

  tryRefresh() {
    if (!this.dirty || this.inFlight || this.confirming || this.rowDrag?.busy || !this.dataset.tabsUrl) return;
    this.dirty = false;
    this.inFlight = true;
    // Loading bar only in the + menu or switcher while one is open: the strip
    // refreshes constantly, a bar flashing over the tabs there just distracts.
    const hosts = [
      this.querySelector(".terminal-tabs-new-menu.show"),
      this.switcher?.overlay.querySelector(".terminal-switcher-panel"),
    ].filter(Boolean);
    const bars = hosts.map((host) => {
      const bar = el("div", { class: "dc-loading-bar", role: "status", "aria-label": "Refreshing" });
      host.prepend(bar);
      return bar;
    });
    // On a split page the path comes from the active island's server rendered
    // attributes, never from window.location: under a boosted navigation the
    // islands upgrade and activate on the DOM swap before pushState runs, so
    // the location can still be the previous page's, and a fragment pulled
    // with it paints the previous tab active (the activation triggered
    // refresh sits exactly in that window).
    const focused = document.querySelector("terminal-attach[active][split-group]");
    const path = focused
      ? "/splits/" + encodeURIComponent(focused.getAttribute("split-group") || "")
        + "?focus=" + encodeURIComponent(focused.getAttribute("terminal-id") || "")
      : pagePath();
    getText(this.dataset.tabsUrl + "?path=" + encodeURIComponent(path))
      .then((html) => {
        // A navigation may have swapped this strip away while the fragment was
        // in flight; painting a torn down element throws on its gone signal.
        if (!this.ac || !this.isConnected) return;
        const template = document.createElement("template");
        template.innerHTML = html;
        const fresh = template.content.querySelector("terminal-tabs");
        if (!fresh) return;
        const freshStrip = fresh.querySelector("[data-tabs-strip]");
        if (freshStrip) {
          this.rowDrag?.cancel();
          this.strip.innerHTML = freshStrip.innerHTML;
          syncAnimations(this.strip);
          this.revealActive();
        }
        this.newMenu?.swap(fresh);
        this.switcher?.rebuild();
      })
      .catch(() => {})
      .finally(() => {
        bars.forEach((bar) => bar.remove());
        this.inFlight = false;
        this.tryRefresh();
      });
  }

  closeSwitcher() {
    const switcher = this.switcher;
    if (!switcher) return;
    this.switcher = null;
    switcher.close();
  }

  // closeTarget stops a coder or deletes a shell. purge turns the coder path
  // into a delete, the server stops it first and drops its conversation.
  async closeTarget({ tabId, tabKind, tabName }, purge = false) {
    if (this.confirming) return;
    const id = tabId;
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(id)}"], .terminal-tab-member[data-tab-id="${CSS.escape(id)}"]`);
    if (!tab) return;
    const kind = tabKind || tab.dataset.tabKind;
    const name = tabName || tab.dataset.tabName;
    const current = tab.classList.contains("active");
    if (kind === "split") {
      void this.closeSplitMembers({ tabId: id, tabName: name });
      return;
    }
    const drop = purge && kind === "coder";
    this.confirming = true;
    try {
      const ok = await confirm({
        title: drop ? `Delete coder "${name}"?`
          : kind === "coder" ? `Stop coder "${name}"?` : `Delete shell "${name}"?`,
        text: drop ? "It is stopped first, its conversation cannot be resumed afterwards." : undefined,
        confirmText: kind === "coder" && !drop ? "Stop" : "Delete",
      });
      if (!ok) return;
      window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id } }));
      const action = drop ? `/coders/${id}/delete`
        : kind === "coder" ? `/coders/${id}/stop` : `/shells/${id}/delete`;
      const response = await postForm(action, {});
      await ensureOk(response, "Could not close the session.");
      const data = await response.json().catch(() => null);
      notifySuccess(alsoDropped(drop ? `Coder "${name}" deleted.`
        : kind === "coder" ? `Coder "${name}" stopped.` : `Shell "${name}" deleted.`, data));
      if (tab.dataset.tabGroup) {
        this.removeTab(id);
        if (window.location.pathname === "/splits/" + tab.dataset.tabGroup && window.app?.navigate) {
          Promise.resolve(window.app.navigate(window.location.pathname + window.location.search)).catch(() => {});
        }
        return;
      }
      if (current) {
        const tabs = this.tabs();
        const index = tabs.indexOf(tab);
        const neighbor = tabs[index + 1] || tabs[index - 1] || null;
        this.removeTab(id);
        this.navigate(neighbor?.getAttribute("href") || "/terminals");
        return;
      }
      this.removeTab(id);
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      // Flush any event that arrived while the confirm dialog was open.
      this.tryRefresh();
    }
  }

  removeTab(id) {
    this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(id)}"], .terminal-tab-member[data-tab-id="${CSS.escape(id)}"]`)?.remove();
  }

  navigate(url) {
    this.closeSwitcher();
    if (!url || url === window.location.pathname) return;
    // The + menu's and the switcher's create entries open the dialog instead of
    // the form page, by mouse and by keyboard alike: both commit through here.
    if (openFormModal(url)) return;
    if (window.app?.navigate) window.app.navigate(url);
    else window.location.href = url;
  }
}

customElements.define("terminal-tabs", TerminalTabs);
