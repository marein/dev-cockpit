import { syncAnimations } from "@dc/dom";
import { missedServerEvent, onServerEvent } from "@dc/events";
import { getText, postForm, postJSON, ensureOk } from "@dc/http";
import { confirmForm } from "@dc/dialog";
import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { typingIn } from "@dc/editor-comments";
import { retryPipeline } from "@dc/project-actions";
import * as assistantActions from "@dc/assistant-actions";
import { assistantPlace, keepPlaces, unfold } from "@dc/assistant-place";
import { projectThreads } from "@dc/assistant-project-threads";
import { renameAssistant } from "@dc/rename";
import { RowDrag } from "@dc/rowdrag";
import { notifyError } from "@dc/toast";

const MENUS = ".terminal-switcher, .dropdown-menu.show, [data-mention-panel]:not([hidden])";

// A self-refreshing assistant list: the projects and the steered jobs of an
// assistant, the assistants in the list column, and the memory. The list swaps
// its [data-assistant-body] on the assistant event, so a check that finished
// or an assistant that answered elsewhere changes what is on screen without
// anybody pulling on a timer, and an action acts in place. With the history
// attribute the rows carry a context menu (right click, long press, and the
// kebab button) for what a row does not need a page for; with the memory
// attribute a row edits in place.
class AssistantList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.url = this.getAttribute("list-url");
    if (!this.url) return;
    this.pulling = false;
    this.dirty = false;
    // A quiet list refreshes only after its own actions: the memory list holds
    // open forms the user types into, a background swap would throw them away.
    if (!this.hasAttribute("quiet")) {
      onServerEvent("assistant", () => void this.refresh(), { signal: this.ac.signal });
    }
    // A list of coders draws their sessions, which move by their own signals.
    const follows = (this.getAttribute("follow") || "").split(" ").filter(Boolean);
    for (const type of follows) onServerEvent(type, () => void this.refresh(), { signal: this.ac.signal });
    if (follows.some(missedServerEvent)) void this.refresh();
    this.addEventListener("submit", (event) => void this.onAction(event), { signal: this.ac.signal });
    this.addEventListener("click", (event) => {
      const older = event.target.closest("[data-assistant-older]");
      if (!older) return;
      event.preventDefault();
      this.showOlder(older);
    }, { signal: this.ac.signal });

    if (this.hasAttribute("history")) {
      wireRowMenus(this, "[data-assistant-instance]", (row, x, y) => this.openRowMenu(row, x, y), { signal: this.ac.signal });
      this.addEventListener("click", (event) => {
        if (event.target.closest("[data-assistant-grip]")) {
          event.preventDefault();
          event.stopPropagation();
          return;
        }
        const button = event.target.closest("[data-assistant-menu]");
        if (!button) return;
        event.preventDefault();
        event.stopPropagation();
        const rect = button.getBoundingClientRect();
        this.openRowMenu(button.closest("[data-assistant-instance]"), rect.left, rect.bottom + 4);
      }, { signal: this.ac.signal });
      this.wireDrag(this.ac.signal);
      this.confirming = false;
      document.addEventListener("keydown", (event) => this.onPageKeydown(event), { signal: this.ac.signal, capture: true });
    }

    if (this.hasAttribute("memory")) this.wireMemory(this.ac.signal);

    // Opening is the moment where being current matters most; closing drops the
    // work nobody is looking at. A list inside a sheet renders empty and pulls
    // itself every time the sheet opens.
    this.modal = this.querySelector(".modal");
    this.modal?.addEventListener("show.bs.modal", () => void this.refresh(), { signal: this.ac.signal });
    this.modal?.addEventListener("hidden.bs.modal", () => { this.dirty = false; }, { signal: this.ac.signal });
    this.sheet = this.closest(".offcanvas");
    this.sheet?.addEventListener("show.bs.offcanvas", () => void this.refresh(), { signal: this.ac.signal });
    if (this.hasAttribute("watch-url")) this.wireProjects(this.ac.signal);

    this.revealActive(true);
    this.dropGonePlaces();
    if (this.hasAttribute("pane")) {
      this.place = assistantPlace(this, {
        older: () => this.foldOlder(true),
        project: (back) => this.openProject(back.name, back.section, back),
      });
    }
  }

  // The cards show git state, and git publishes what moves in a working copy
  // only while somebody watches the project. The list watches its projects
  // while it is on screen and catches up once it is back, the window may have
  // lapsed meanwhile. Opening the sheet pulls already, see connectedCallback.
  // What the forge poller read and the line comments move a card by their
  // own signals, Retry is the project row's run of the failed jobs again. A
  // failed pipeline brings a folded card up, so a folded project's forge
  // signal pulls too. A card's review and changes lines open the project in
  // place of the cards on that section, which follows the same signals of
  // its project while it stands. A coder starting or stopping repaints its
  // row.
  wireProjects(signal) {
    this.watchTimer = 0;
    this.watchGen = 0;
    this.watchLapsed = false;
    this.viewPulling = false;
    this.viewDirty = false;
    this.threads = projectThreads({
      root: this,
      signal,
      view: () => this.viewBox(),
      project: () => this.openProjectName(),
      pull: () => this.refreshView(),
      hold: () => { this.viewDirty = true; },
      resume: () => { if (this.viewDirty) void this.refreshView(); },
    });
    const pull = (folded) => (event) => {
      const name = event.detail?.project;
      const named = name && CSS.escape(name);
      if (!name || this.querySelector(`[data-assistant-project="${named}"]`) || (folded && this.querySelector(`[data-assistant-project-folded="${named}"]`))) void this.refresh();
      if (!name || name === this.openProjectName()) void this.refreshView();
    };
    onServerEvent("git", pull(false), { signal });
    onServerEvent("linecomments", pull(false), { signal });
    onServerEvent("terminals", (event) => {
      const name = event.detail?.project;
      if (!name || this.querySelector(`[data-assistant-project="${CSS.escape(name)}"]`)) void this.refresh();
    }, { signal });
    onServerEvent("forge", pull(true), { signal });
    onServerEvent("projects", () => {
      void this.refresh();
      void this.refreshView();
    }, { signal });
    onServerEvent("assistant", () => void this.refreshView(), { signal });
    this.addEventListener("click", (event) => {
      const retry = event.target.closest("[data-assistant-project-retry]");
      if (retry) {
        event.preventDefault();
        void retryPipeline(retry);
        return;
      }
      const open = event.target.closest("[data-assistant-project-open]");
      if (open) {
        event.preventDefault();
        void this.openProject(open.dataset.assistantProjectOpen, open.dataset.assistantProjectSection);
        return;
      }
      if (event.target.closest("[data-assistant-project-back]")) {
        event.preventDefault();
        this.closeProject();
      }
    }, { signal });
    this.addEventListener("shown.bs.tab", (event) => {
      const section = event.target.closest?.("[data-assistant-project-switch]")?.dataset.assistantProjectSwitch;
      const view = this.viewBox();
      if (section && view) view.dataset.section = section;
    }, { signal });
    this.addEventListener("show.bs.collapse", (event) => {
      if (event.target.dataset?.assistantRest) this.unfoldRest(event.target, true);
    }, { signal });
    this.addEventListener("hide.bs.collapse", (event) => {
      if (event.target.dataset?.assistantRest) this.unfoldRest(event.target, false);
    }, { signal });
    const back = (pull) => {
      if (!this.watchLapsed || !this.onScreen()) return;
      if (pull) void this.refresh();
      void this.renewWatch();
    };
    document.addEventListener("visibilitychange", () => back(true), { signal });
    document.addEventListener("shown.bs.tab", () => back(true), { signal });
    this.sheet?.addEventListener("shown.bs.offcanvas", () => back(false), { signal });
    signal.addEventListener("abort", () => clearTimeout(this.watchTimer));
    if (["assistant", "git", "projects", "forge", "linecomments", "terminals"].some(missedServerEvent)) {
      void this.refresh();
      void this.refreshView();
    }
    void this.renewWatch();
    void this.threads.draw();
  }

  // The open project lives in the markup, the view's own attributes, so it
  // stands when the aside moves to a new page body and the element connects
  // again.
  viewBox() {
    return this.querySelector(":scope > [data-assistant-project-view]");
  }

  openProjectName() {
    const view = this.viewBox();
    return view && !view.hidden ? view.dataset.project || "" : "";
  }

  // The nearest box that scrolls the tab: the aside's body, beside the
  // thread and in the sheet alike.
  scroller() {
    for (let node = this.parentElement; node; node = node.parentElement) {
      const overflow = getComputedStyle(node).overflowY;
      if (overflow === "auto" || overflow === "scroll") return node;
    }
    return document.scrollingElement;
  }

  async pullView(name, section, more = "") {
    const url = new URL(this.getAttribute("view-url"), window.location.href);
    url.searchParams.set("project", name);
    if (section) url.searchParams.set("section", section);
    for (const list of more.split(" ").filter(Boolean)) url.searchParams.append("more", list);
    const holder = document.createElement("div");
    holder.innerHTML = await getText(url.pathname + url.search);
    const fresh = holder.querySelector("[data-assistant-project-view-body]");
    if (!fresh) throw new Error("The project could not be read.");
    await window.app?.loadElements?.(fresh);
    return fresh;
  }

  // A second tap while the first one still pulls opens nothing more: the
  // list's scroll is taken once, before the view stands in its place. The
  // cards are looked up after the pull, a refresh may have swapped them. A
  // place put back (back) opens the view as the reader left it, the lists
  // and rows unfolded and the diffs drawn, with the scroll the cards had,
  // and a project gone since stays quietly shut. It answers whether the
  // view stands. The tabs share the aside's scroller, a list that is not the
  // open tab leaves it to the one that is.
  async openProject(name, section, back = null) {
    const view = this.viewBox();
    if (!view || !name || this.opening) return false;
    this.opening = true;
    let fresh;
    try {
      fresh = await this.pullView(name, section, back?.more);
    } catch (err) {
      if (!back) notifyError(err?.message || "The project could not be read.");
      return false;
    } finally {
      this.opening = false;
    }
    const list = this.querySelector(":scope > [data-assistant-body]");
    if (!this.ac || !list) return false;
    const scroller = this.scroller();
    view.dataset.listScroll = String(back ? back.list : scroller.scrollTop);
    view.dataset.project = name;
    view.dataset.section = fresh.dataset.section || "";
    view.dataset.more = back ? [...fresh.querySelectorAll("[data-assistant-rest].show")].map((fold) => fold.dataset.assistantRest).sort().join(" ") : "";
    if (back) unfold(fresh, back.folds);
    this.threads.reset();
    view.replaceChildren(fresh);
    list.hidden = true;
    view.hidden = false;
    if (this.classList.contains("active")) scroller.scrollTop = 0;
    this.place?.save();
    if (back) await this.threads.draw();
    return true;
  }

  closeProject() {
    const view = this.viewBox();
    const list = this.querySelector(":scope > [data-assistant-body]");
    if (!view || view.hidden) return;
    view.hidden = true;
    this.threads.reset();
    view.replaceChildren();
    delete view.dataset.project;
    delete view.dataset.section;
    delete view.dataset.more;
    this.viewDirty = false;
    if (list) list.hidden = false;
    if (this.classList.contains("active")) this.scroller().scrollTop = Number(view.dataset.listScroll) || 0;
    this.place?.save();
  }

  // A reply or a note being edited in Reviews holds the view still: a swap
  // would take the words from under the hand. The pull it held back runs once the form
  // closes, a rest unfolded meanwhile takes its rows alone. An open diff and
  // the field of a new project comment go over into the swapped view with
  // what is typed in them, the field keeping the focus the move takes. A
  // section picked or a list unfolded or folded while a pull ran makes that
  // pull stale, it runs again.
  replying() {
    return Array.from(this.viewBox()?.querySelectorAll("[data-line-thread]") || []).some((card) => !card.closest("[data-assistant-diff]") && typingIn(card));
  }

  waitingRests() {
    const view = this.viewBox();
    const lists = new Set((view?.dataset.more || "").split(" ").filter(Boolean));
    return Array.from(view?.querySelectorAll("[data-assistant-rest]") || []).filter((fold) => lists.has(fold.dataset.assistantRest) && fold.querySelector(":scope > [data-assistant-rest-wait]"));
  }

  async refreshView() {
    const name = this.openProjectName();
    if (!name) return;
    if (this.viewPulling || (this.replying() && !this.waitingRests().length)) {
      this.viewDirty = true;
      return;
    }
    this.viewPulling = true;
    this.viewDirty = false;
    try {
      const section = this.viewBox()?.dataset.section || "";
      const more = this.viewBox()?.dataset.more || "";
      const fresh = await this.pullView(name, section, more);
      const view = this.viewBox();
      if (!view || this.openProjectName() !== name) return;
      if ((view.dataset.section || "") !== section || (view.dataset.more || "") !== more) {
        this.viewDirty = true;
        return;
      }
      if (this.replying()) {
        for (const fold of this.waitingRests()) {
          const rows = fresh.querySelector(`#${CSS.escape(fold.id)}`);
          if (rows && !rows.querySelector(":scope > [data-assistant-rest-wait]")) fold.replaceChildren(...rows.childNodes);
        }
        this.viewDirty = true;
        return;
      }
      const current = view.querySelector("[data-assistant-project-view-body]");
      if (current) this.keepUnfolded(current, fresh);
      const field = current?.querySelector("[data-assistant-project-comment]");
      const typing = this.threads.recordOf(document.activeElement) || field?.contains(document.activeElement) ? document.activeElement : null;
      const caret = typing && typeof typing.setSelectionRange === "function" ? [typing.selectionStart, typing.selectionEnd] : null;
      if (field) fresh.querySelector("[data-assistant-project-comment]")?.replaceWith(field);
      this.threads.carry(fresh);
      view.dataset.section = fresh.dataset.section || "";
      view.replaceChildren(fresh);
      void this.threads.draw();
      this.threads.sync();
      if (typing?.isConnected && document.activeElement !== typing) {
        typing.focus({ preventScroll: true });
        if (caret) typing.setSelectionRange(...caret);
      }
      this.threads.land();
    } catch (err) {
      if (err?.status === 404 && this.openProjectName() === name) this.closeProject();
    } finally {
      this.viewPulling = false;
      if (this.viewDirty && !this.replying()) void this.refreshView();
    }
  }

  // A list of Changes unfolds the rows past its first ones in place: the view
  // holds which lists stand unfolded and asks for their rest with every pull,
  // the server renders it then. A fold that waits for its rows pulls the view
  // once. Folded again, the diffs open in it fold too, and the next pull
  // leaves the rows out. Both count from the tap, so a pull already out
  // turns stale and cannot unfold the rest again.
  unfoldRest(fold, open) {
    const view = this.viewBox();
    if (!view) return;
    const lists = new Set((view.dataset.more || "").split(" ").filter(Boolean));
    if (open) lists.add(fold.dataset.assistantRest);
    else lists.delete(fold.dataset.assistantRest);
    view.dataset.more = [...lists].sort().join(" ");
    if (open) {
      if (fold.querySelector("[data-assistant-rest-wait]")) void this.refreshView();
      return;
    }
    for (const inner of fold.querySelectorAll("[data-assistant-fold].show")) {
      inner.classList.remove("show");
      fold.querySelector(`[aria-controls="${CSS.escape(inner.id)}"]`)?.setAttribute("aria-expanded", "false");
      this.threads.drop(inner.id, true);
    }
  }

  onScreen() {
    return !document.hidden && (this.checkVisibility?.({ visibilityProperty: true }) ?? true);
  }

  async renewWatch() {
    clearTimeout(this.watchTimer);
    const gen = ++this.watchGen;
    if (!this.ac) return;
    if (!this.onScreen()) {
      this.watchLapsed = true;
      return;
    }
    this.watchLapsed = false;
    let next = 15000;
    try {
      const data = await (await postForm(this.getAttribute("watch-url"), {})).json();
      if (!data.watching) return;
      next = Math.max(5000, (Number(data.seconds) || 30) * 500);
    } catch {
      void 0;
    }
    if (!this.ac || this.watchLapsed || gen !== this.watchGen) return;
    this.watchTimer = setTimeout(() => void this.renewWatch(), next);
  }

  // The Coders and Triggers tabs keep the older rows until the page is opened
  // again. A control that carries aria-expanded folds them again too and
  // turns at the tap, so a pull still out under the old address is dropped,
  // and the aside's place keeps them.
  showOlder(control) {
    const show = control.getAttribute("aria-expanded") !== "true";
    if (control.hasAttribute("aria-expanded")) control.setAttribute("aria-expanded", String(show));
    void this.foldOlder(show);
    this.place?.save();
  }

  foldOlder(show) {
    this.url = withAll(this.url, show);
    this.setAttribute("list-url", this.url);
    const pulled = this.refresh();
    if (this.hasAttribute("watch-url")) {
      this.setAttribute("watch-url", withAll(this.getAttribute("watch-url"), show));
      void this.renewWatch();
    }
    return pulled;
  }

  dropGonePlaces() {
    const living = this.querySelector("[data-assistant-living]");
    if (living) keepPlaces(Array.from(living.querySelectorAll("[data-assistant-instance]"), (row) => row.dataset.assistantInstance));
  }

  revealActive(center = false) {
    const row = this.querySelector("[data-assistant-instance].active");
    if (!row) return;
    row.scrollIntoView({ block: center ? "center" : "nearest" });
  }

  // A memory row edits where it stands: the pencil hides the reading half and
  // shows the row's own form in its place, so nothing repeats itself and
  // nothing jumps. Only one row is open at a time. Cancel resets the form back
  // to the rendered values, so a reopened row starts from what is stored, and
  // Escape cancels the edit instead of closing the whole overlay.
  wireMemory(signal) {
    this.addEventListener("click", (event) => {
      const open = event.target.closest("[data-memory-edit-open]");
      if (open) {
        event.preventDefault();
        this.openMemoryEdit(open.closest("[data-memory-entry]"));
        return;
      }
      const cancel = event.target.closest("[data-memory-edit-cancel]");
      if (cancel) {
        event.preventDefault();
        this.closeMemoryEdit(cancel.closest("[data-memory-entry]"));
      }
    }, { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      const entry = event.target.closest("[data-memory-entry]");
      if (!entry || entry.querySelector("[data-memory-edit]")?.hidden !== false) return;
      event.stopPropagation();
      this.closeMemoryEdit(entry);
    }, { signal });
  }

  openMemoryEdit(entry) {
    if (!entry) return;
    for (const other of this.querySelectorAll("[data-memory-entry]")) {
      if (other !== entry) this.closeMemoryEdit(other);
    }
    entry.querySelector("[data-memory-view]").hidden = true;
    const form = entry.querySelector("[data-memory-edit]");
    form.hidden = false;
    const title = form.querySelector('input[name="title"]');
    title?.focus();
    title?.setSelectionRange(title.value.length, title.value.length);
  }

  closeMemoryEdit(entry) {
    const form = entry?.querySelector("[data-memory-edit]");
    if (!form || form.hidden) return;
    form.reset();
    form.hidden = true;
    entry.querySelector("[data-memory-view]").hidden = false;
  }

  // An assistant's row menu: open it, give it a name of its own, share it, or
  // delete it. Renaming is here and not on the page because with several of
  // them the name is what the list is read by, and this is the list. Share
  // opens the sharing settings with the assistant picked, like a project's.
  openRowMenu(row, x, y) {
    if (!row) return null;
    // A long press on the grip is both a drag and a menu; the menu wins.
    this.rowDrag?.cancel();
    const { url, name } = this.target(row);
    return openMenu({
      x,
      y,
      signal: this.ac.signal,
      items: [
        {
          label: "Open",
          icon: "ti-message",
          href: url,
          action: () => {
            if (window.app?.navigate) window.app.navigate(url);
            else window.location.assign(url);
          },
        },
        { label: "Rename", icon: "ti-pencil", action: () => void this.renameAssistant(url, name) },
        assistantActions.shareItem(row.dataset.assistantInstance),
        { label: "Delete", icon: "ti-trash", danger: true, action: () => void this.deleteAssistant(url, name) },
      ],
    });
  }

  target(row) {
    return { url: row.dataset.assistantUrl || "/assistants/" + row.dataset.assistantInstance, name: row.dataset.assistantName || "" };
  }

  // The terminals page's keys (terminal-tabs onKeydown). The phone's sheet
  // holds a second copy of this column, only the page's own one answers.
  onPageKeydown(event) {
    if (this.confirming || event.altKey || event.ctrlKey === event.metaKey || !this.matches(".dc-app > .dc-ctx")) return;
    const key = event.key.toLowerCase();
    if (key !== "t" && key !== "x") return;
    const open = Array.from(document.querySelectorAll(MENUS));
    if (key === "t") {
      const toggle = this.querySelector(".dc-ctx-head [data-assistant-new-label]");
      if (!toggle?.offsetParent) return;
      const menu = toggle.matches('[data-bs-toggle="dropdown"]') ? toggle.nextElementSibling : null;
      if (open.some((node) => node !== menu)) return;
      event.preventDefault();
      event.stopPropagation();
      this.openNew(toggle, menu);
      return;
    }
    if (!event.shiftKey || event.repeat || open.length) return;
    const row = this.querySelector("[data-assistant-instance].active");
    if (!row) return;
    event.preventDefault();
    event.stopPropagation();
    void this.deleteOpen(row);
  }

  // Bootstrap focuses the toggle after its hidden event, so the way back to
  // where the keyboard was waits for that.
  openNew(toggle, menu) {
    if (!menu) {
      toggle.focus();
      return;
    }
    if (menu.classList.contains("show")) return;
    const back = document.activeElement;
    toggle.parentElement.addEventListener("hidden.bs.dropdown", () => queueMicrotask(() => {
      if (document.activeElement === toggle && back?.isConnected) back.focus({ preventScroll: true });
    }), { once: true, signal: this.ac.signal });
    window.bootstrap?.Dropdown.getOrCreateInstance(toggle).show();
  }

  async deleteOpen(row) {
    const { url, name } = this.target(row);
    this.confirming = true;
    try {
      await this.deleteAssistant(url, name);
    } finally {
      this.confirming = false;
    }
  }

  async renameAssistant(url, name) {
    if (await renameAssistant(url, name)) await this.refresh();
  }

  async deleteAssistant(url, name) {
    if (!(await assistantActions.deleteAssistant(url, name))) return;
    // The page of the deleted assistant is gone with it, the bare address opens
    // whichever one is left; every other page just sees the row go.
    if (window.location.pathname === url) {
      if (window.app?.navigate) window.app.navigate("/assistants");
      else window.location.assign("/assistants");
      return;
    }
    await this.refresh();
  }

  // The list is sorted by hand, so the rows are dragged into place and the
  // order is posted to the server, which is where it lives: it comes back on
  // the next reload, on this device and on every other one. The gesture is the
  // tab strip's own, @dc/rowdrag, and this is one list with no groups in it, so
  // a row only ever changes seats. The one deviation is the capture, taken when
  // the drag begins, so the carried row sits under the pointer from the press.
  wireDrag(signal) {
    this.rowDrag = new RowDrag(this, {
      rowSelector: "[data-assistant-instance]",
      gripSelector: "[data-assistant-grip]",
      ignoreSelector: "[data-assistant-menu]",
      capture: "drag",
      classes: { list: "dc-rows-dragging", row: "dc-row-dragging" },
      rows: () => this.rows(),
      scroller: () => this.querySelector(".dc-ctx-body") || this,
      onDrop: () => this.persistOrder(),
      onEnd: () => { if (this.dirty) void this.refresh(); },
    });
    this.rowDrag.wire(signal);
  }

  rows() {
    return Array.from(this.querySelectorAll("[data-assistant-instance]"));
  }

  persistOrder() {
    const ids = this.rows().map((row) => row.dataset.assistantInstance).filter(Boolean);
    postJSON("/assistants/order", { ids })
      .then((response) => ensureOk(response, "Could not save the order."))
      .catch((error) => notifyError(error.message));
  }

  disconnectedCallback() {
    this.rowDrag?.cancel();
    this.ac?.abort();
    this.ac = null;
  }

  // A job action is a normal form, posting the same route the page and the
  // command line post to. Everything that ends something carries data-confirm,
  // and that is the cockpit's one confirmation, from @dc/dialog with its native
  // fallback.
  async onAction(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || form.dataset.ajaxRefresh === undefined) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (form.dataset.confirm && !(await confirmForm(form, {
      target: this.modal?.classList.contains("show") ? this.modal : undefined,
      heightAuto: false,
    }))) return;
    const buttons = Array.from(form.querySelectorAll("button[type=submit]"));
    buttons.forEach((button) => { button.disabled = true; });
    try {
      const response = await fetch(form.action, {
        method: "POST",
        headers: { Accept: "application/json" },
        body: new URLSearchParams(new FormData(form)),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload.error || "The cockpit refused that.");
      }
    } catch (err) {
      notifyError(err?.message || "Could not reach the cockpit.");
    } finally {
      buttons.forEach((button) => { button.disabled = false; });
      await this.refresh();
    }
  }

  // A row the reader unfolded stays unfolded over a swap: the ids that stand
  // open are carried onto the rows that arrive, as the class and the state of
  // the control, which is what Bootstrap reads before it ever instantiates a
  // collapse. Without it every event of the assistant closes what somebody is
  // reading. A fold still opening is open: its control says so from the click
  // on, the class only once the animation ended.
  keepUnfolded(current, fresh) {
    for (const fold of current.querySelectorAll("[data-assistant-fold]")) {
      const opening = current.querySelector(`[aria-controls="${CSS.escape(fold.id)}"]`)?.getAttribute("aria-expanded") === "true";
      if (!fold.classList.contains("show") && !opening) continue;
      const next = fresh.querySelector(`#${CSS.escape(fold.id)}[data-assistant-fold]`);
      if (!next) continue;
      next.classList.add("show");
      fresh.querySelector(`[aria-controls="${CSS.escape(fold.id)}"]`)?.setAttribute("aria-expanded", "true");
    }
  }

  // A pull asked for while one is out runs once that one is done, and both
  // answer once the list stands as its address says.
  refresh() {
    // A swap in the middle of a drag would take the row out from under the
    // pointer, so the pull waits for the gesture and runs when it ends.
    if (this.rowDrag?.busy) {
      this.dirty = true;
      return Promise.resolve();
    }
    if (this.pulling) {
      this.dirty = true;
      return this.pulling;
    }
    this.dirty = false;
    this.pulling = this.pull().finally(() => {
      this.pulling = null;
      if (this.dirty) return this.refresh();
    });
    return this.pulling;
  }

  async pull() {
    try {
      const url = this.url;
      const html = await getText(url);
      if (url !== this.url) {
        this.dirty = true;
        return;
      }
      const holder = document.createElement("div");
      holder.innerHTML = html;
      const fresh = holder.querySelector("[data-assistant-body]");
      const current = this.querySelector("[data-assistant-body]");
      if (fresh && current) {
        this.keepUnfolded(current, fresh);
        fresh.hidden = current.hidden;
        current.replaceWith(fresh);
        syncAnimations(fresh);
        this.revealActive();
        this.dropGonePlaces();
        document.dispatchEvent(new CustomEvent("dc:assistant-counts"));
      }
    } catch {
      void 0;
    }
  }
}

function withAll(path, all) {
  const url = new URL(path, window.location.href);
  if (all) url.searchParams.set("all", "1");
  else url.searchParams.delete("all");
  return url.pathname + url.search;
}

customElements.define("dc-assistant-list", AssistantList);
