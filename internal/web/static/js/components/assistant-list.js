import { isModEnter, syncAnimations } from "@dc/dom";
import { missedServerEvent, onServerEvent } from "@dc/events";
import { getJSON, getText, postForm, postJSON, ensureOk } from "@dc/http";
import { confirm, fire } from "@dc/dialog";
import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { postForgeThread, publishComments, publishQuestion, writeLineComment } from "@dc/editor-comments";
import { inlineDiff } from "@dc/inline-diff";
import { retryPipeline } from "@dc/project-actions";
import { RowDrag } from "@dc/rowdrag";
import { notifyError, notifySuccess } from "@dc/toast";

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
    this.diffs = new Map();
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
        return;
      }
      const action = event.target.closest("[data-assistant-thread-reply-open], [data-assistant-thread-reply-cancel], [data-assistant-thread-resolve], [data-assistant-thread-reopen], [data-assistant-thread-handoff], [data-assistant-project-publish]");
      if (!action) return;
      event.preventDefault();
      void this.threadAction(action);
    }, { signal });
    this.addEventListener("submit", (event) => {
      const form = event.target instanceof HTMLFormElement && event.target.closest("[data-assistant-thread-reply]");
      if (!form) return;
      event.preventDefault();
      event.stopPropagation();
      void this.sendReply(form);
    }, { signal });
    this.addEventListener("input", (event) => {
      if (event.target.matches?.("[data-assistant-thread-reply-text]")) event.target.classList.remove("is-invalid");
    }, { signal });
    this.addEventListener("keydown", (event) => {
      if (!isModEnter(event) || !event.target.matches?.("[data-assistant-thread-reply-text]")) return;
      event.preventDefault();
      event.target.form?.querySelector("[data-assistant-thread-reply-send]")?.click();
    }, { signal });
    this.addEventListener("shown.bs.tab", (event) => {
      const section = event.target.closest?.("[data-assistant-project-switch]")?.dataset.assistantProjectSwitch;
      const view = this.viewBox();
      if (section && view) view.dataset.section = section;
    }, { signal });
    this.addEventListener("show.bs.collapse", (event) => {
      if (event.target.dataset?.assistantRest) {
        this.unfoldRest(event.target, true);
        return;
      }
      const box = event.target.querySelector?.("[data-assistant-diff]");
      if (box) this.loadDiff(event.target.id, box);
    }, { signal });
    this.addEventListener("hide.bs.collapse", (event) => {
      if (event.target.dataset?.assistantRest) this.unfoldRest(event.target, false);
    }, { signal });
    this.addEventListener("hidden.bs.collapse", (event) => {
      if (!event.target.dataset?.assistantRest) this.dropDiff(event.target.id, true);
    }, { signal });
    signal.addEventListener("abort", () => this.dropDiffs());
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
    this.drawOpenDiffs();
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
  // cards are looked up after the pull, a refresh may have swapped them.
  async openProject(name, section) {
    const view = this.viewBox();
    if (!view || !name || this.opening) return;
    this.opening = true;
    let fresh;
    try {
      fresh = await this.pullView(name, section);
    } catch (err) {
      notifyError(err?.message || "The project could not be read.");
      return;
    } finally {
      this.opening = false;
    }
    const list = this.querySelector(":scope > [data-assistant-body]");
    if (!this.ac || !list) return;
    const scroller = this.scroller();
    view.dataset.listScroll = String(scroller.scrollTop);
    view.dataset.project = name;
    view.dataset.section = fresh.dataset.section || "";
    view.dataset.more = "";
    this.dropDiffs();
    view.replaceChildren(fresh);
    list.hidden = true;
    view.hidden = false;
    scroller.scrollTop = 0;
  }

  closeProject() {
    const view = this.viewBox();
    const list = this.querySelector(":scope > [data-assistant-body]");
    if (!view || view.hidden) return;
    view.hidden = true;
    this.dropDiffs();
    view.replaceChildren();
    delete view.dataset.project;
    delete view.dataset.section;
    delete view.dataset.more;
    this.viewDirty = false;
    if (list) list.hidden = false;
    this.scroller().scrollTop = Number(view.dataset.listScroll) || 0;
  }

  // A reply being typed holds the view still: a swap would take the words
  // from under the hand. The pull it held back runs once the form closes, a
  // rest unfolded meanwhile takes its rows alone. A section picked or a list
  // unfolded or folded while a pull ran makes that pull stale, it runs again.
  replying() {
    return !!this.viewBox()?.querySelector("[data-assistant-thread-reply]:not([hidden])");
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
      this.carryDiffs(fresh);
      view.dataset.section = fresh.dataset.section || "";
      view.replaceChildren(fresh);
      this.drawOpenDiffs();
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
      this.dropDiff(inner.id, true);
    }
  }

  drawOpenDiffs() {
    for (const box of this.querySelectorAll("[data-assistant-diff]")) {
      const fold = box.closest("[data-assistant-fold]");
      if (!fold.classList.contains("show") || fold.parentElement.closest("[data-assistant-fold]:not(.show)")) continue;
      if (this.diffs.get(fold.id)?.box !== box) this.loadDiff(fold.id, box);
    }
  }

  carryDiffs(fresh) {
    for (const [id, record] of [...this.diffs]) {
      const next = fresh.querySelector(`#${CSS.escape(id)}.show [data-assistant-diff]`);
      if (!next) {
        this.dropDiff(id, false);
        continue;
      }
      next.replaceWith(record.box);
      void this.pullDiff(id, record);
    }
  }

  loadDiff(id, box) {
    this.dropDiff(id, false);
    const record = { box, handle: null, text: null, loading: false, again: false };
    this.diffs.set(id, record);
    showDiff(box, "wait");
    void this.pullDiff(id, record);
  }

  dropDiff(id, reset) {
    const record = this.diffs.get(id);
    if (!record) return;
    this.diffs.delete(id);
    record.handle?.destroy();
    if (reset) showDiff(record.box, "wait");
  }

  dropDiffs() {
    for (const id of [...(this.diffs?.keys() || [])]) this.dropDiff(id, false);
  }

  async pullDiff(id, record) {
    if (record.loading) {
      record.again = true;
      return;
    }
    record.loading = true;
    record.again = false;
    const { box } = record;
    const live = () => this.diffs.get(id) === record;
    try {
      const url = new URL(this.getAttribute("diff-url"), window.location.href);
      url.searchParams.set("project", this.openProjectName());
      url.searchParams.set("list", box.dataset.list);
      url.searchParams.set("path", box.dataset.path);
      const answer = await getJSON(url.pathname + url.search);
      if (!live()) return;
      if (answer.note) {
        record.handle?.destroy();
        record.handle = null;
        record.text = null;
        showDiff(box, "note", answer.note);
        return;
      }
      const text = [answer.original || "", answer.content || ""];
      if (record.handle && record.text[0] === text[0] && record.text[1] === text[1]) return;
      const handle = await inlineDiff(text[0], text[1]);
      if (!live()) {
        handle.destroy();
        return;
      }
      showDiff(box, "view");
      box.querySelector("[data-assistant-diff-view]").replaceChildren(handle.dom);
      record.handle?.destroy();
      record.handle = handle;
      record.text = text;
    } catch (err) {
      if (!live()) return;
      record.handle?.destroy();
      record.handle = null;
      record.text = null;
      showDiff(box, "note", err?.message || "The diff could not be read.");
    } finally {
      record.loading = false;
      if (record.again && live()) void this.pullDiff(id, record);
    }
  }

  async threadAction(button) {
    const thread = button.closest("[data-assistant-thread]");
    const form = thread?.querySelector("[data-assistant-thread-reply]");
    if (button.matches("[data-assistant-thread-reply-open]")) {
      if (!form) return;
      form.hidden = false;
      button.setAttribute("aria-expanded", "true");
      if (matchMedia("(pointer: fine)").matches) form.querySelector("textarea")?.focus();
      return;
    }
    if (button.matches("[data-assistant-thread-reply-cancel]")) {
      this.closeReply(form);
      return;
    }
    if (button.matches("[data-assistant-thread-handoff]")) {
      this.handOff(button.dataset.assistantThreadHandoff);
      return;
    }
    const reviews = button.closest("[data-assistant-project-reviews]");
    if (button.matches("[data-assistant-thread-resolve], [data-assistant-thread-reopen]")) {
      button.disabled = true;
      const resolve = button.matches("[data-assistant-thread-resolve]");
      if (thread.hasAttribute("data-assistant-thread-local")) await this.resolveLocal(reviews, thread, resolve);
      else await postForgeThread(reviews.dataset.threadUrl, { thread: thread.dataset.thread, branch: reviews.dataset.branch, resolve: true });
      button.disabled = false;
      void this.refreshView();
      return;
    }
    if (button.matches("[data-assistant-project-publish]")) {
      const ids = (button.dataset.ids || "").split(" ").filter(Boolean);
      const offer = { forge: button.dataset.forgePublishForge, noun: button.dataset.forgePublishNoun, ref: button.dataset.forgePublishRef };
      if (!ids.length || !(await confirm(publishQuestion(ids.length, Number(button.dataset.general) || 0, offer)))) return;
      button.disabled = true;
      await publishComments(button.dataset.assistantProjectPublish, ids);
      button.disabled = false;
      void this.refreshView();
    }
  }

  closeReply(form) {
    if (!form) return;
    form.reset();
    form.querySelector("textarea")?.classList.remove("is-invalid");
    form.hidden = true;
    form.closest("[data-assistant-thread]")?.querySelector("[data-assistant-thread-reply-open]")?.setAttribute("aria-expanded", "false");
    if (this.viewDirty) void this.refreshView();
  }

  async sendReply(form) {
    const text = form.querySelector("textarea");
    const body = text.value.trim();
    const toggle = form.querySelector("[data-assistant-thread-reply-resolve]");
    const toggles = !!toggle?.checked;
    if (!body && !toggles) {
      text.classList.add("is-invalid");
      text.focus();
      return;
    }
    const reviews = form.closest("[data-assistant-project-reviews]");
    const thread = form.closest("[data-assistant-thread]");
    const buttons = Array.from(form.querySelectorAll("button"));
    buttons.forEach((button) => { button.disabled = true; });
    const done = thread.hasAttribute("data-assistant-thread-local")
      ? await this.answerLocal(reviews, thread, body, toggles ? toggle.value === "1" : null)
      : await postForgeThread(reviews.dataset.threadUrl, { thread: form.dataset.thread, branch: reviews.dataset.branch, body, resolve: toggles });
    buttons.forEach((button) => { button.disabled = false; });
    if (!done) return;
    this.viewDirty = true;
    this.closeReply(form);
  }

  // A local thread is written through the editor's line comments, the way
  // its thread dialog writes it: the reply first, then the resolve or the
  // reopen. A toggle refused after the reply went still closes the form, a
  // second Reply would post the words twice.
  async answerLocal(reviews, thread, body, resolve) {
    if (body && !(await writeLineComment(`${reviews.dataset.commentsUrl}/reply`, { thread: thread.dataset.assistantThread, text: body }, "The reply could not be saved."))) return false;
    if (resolve !== null && !(await this.resolveLocal(reviews, thread, resolve))) return !!body;
    return true;
  }

  resolveLocal(reviews, thread, resolve) {
    return writeLineComment(`${reviews.dataset.commentsUrl}/resolve`, { id: thread.dataset.assistantThread, resolved: resolve }, resolve ? "The thread could not be resolved." : "The thread could not be reopened.");
  }

  // The composer of the page takes the draft, saves it the way a typed one is
  // saved and sends nothing. On a phone the sheet steps aside, so the box the
  // draft went into is what shows.
  handOff(text) {
    const taken = !this.dispatchEvent(new CustomEvent("dc-assistant-handoff", { bubbles: true, cancelable: true, detail: { text } }));
    if (!taken) {
      notifyError("This assistant is read only.");
      return;
    }
    const aside = this.closest("[data-assistant-aside]");
    if (aside?.classList.contains("show")) window.bootstrap?.Offcanvas.getOrCreateInstance(aside).hide();
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
  // turns at the tap, so a pull still out under the old address is dropped.
  showOlder(control) {
    const show = control.getAttribute("aria-expanded") !== "true";
    if (control.hasAttribute("aria-expanded")) control.setAttribute("aria-expanded", String(show));
    this.url = withAll(this.url, show);
    this.setAttribute("list-url", this.url);
    void this.refresh();
    if (!this.hasAttribute("watch-url")) return;
    this.setAttribute("watch-url", withAll(this.getAttribute("watch-url"), show));
    void this.renewWatch();
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
    const url = row.dataset.assistantUrl || "/assistants/" + row.dataset.assistantInstance;
    const name = row.dataset.assistantName || "";
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
        { label: "Share", icon: "ti-share", href: `/settings/sharing?assistant=${encodeURIComponent(row.dataset.assistantInstance)}` },
        { label: "Delete", icon: "ti-trash", danger: true, action: () => void this.deleteAssistant(url, name) },
      ],
    });
  }

  async renameAssistant(url, name) {
    const result = await fire({
      title: "Rename this assistant",
      input: "text",
      inputValue: name,
      inputAttributes: { "aria-label": "Name" },
      showCancelButton: true,
      confirmButtonText: "Rename",
      cancelButtonText: "Cancel",
      reverseButtons: true,
    });
    if (!result.isConfirmed) return;
    const title = (result.value || "").trim();
    if (!title || title === name) return;
    try {
      const response = await postForm(url, { form: "rename", title });
      await ensureOk(response, "The assistant could not be renamed.");
    } catch (err) {
      notifyError(err?.message || "The assistant could not be renamed.");
      return;
    }
    await this.refresh();
  }

  async deleteAssistant(url, name) {
    const ok = await confirm({
      title: name ? `Delete "${name}"?` : "Delete this assistant?",
      text: "Its thread goes with it. The coders it steers are released and yours again, the shared memory is kept.",
      confirmText: "Delete",
    });
    if (!ok) return;
    let released = "";
    try {
      const response = await postForm(url, { form: "delete" });
      await ensureOk(response, "The assistant could not be deleted.");
      // The answer names the coders that came back, and that is the one thing
      // the user cannot see for themselves once the row is gone.
      released = (await response.json().catch(() => ({})))?.released || "";
    } catch (err) {
      notifyError(err?.message || "The assistant could not be deleted.");
      return;
    }
    if (released) notifySuccess(released);
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
    if (form.dataset.confirm) {
      const ok = await confirm({
        title: form.dataset.confirm,
        confirmText: form.dataset.confirmButton || "Confirm",
        target: this.modal?.classList.contains("show") ? this.modal : undefined,
        heightAuto: false,
      });
      if (!ok) return;
    }
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

  async refresh() {
    // A swap in the middle of a drag would take the row out from under the
    // pointer, so the pull waits for the gesture and runs when it ends.
    if (this.rowDrag?.busy) {
      this.dirty = true;
      return;
    }
    if (this.pulling) {
      this.dirty = true;
      return;
    }
    this.pulling = true;
    this.dirty = false;
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
        document.dispatchEvent(new CustomEvent("dc:assistant-counts"));
      }
    } catch {
      void 0;
    } finally {
      this.pulling = false;
      if (this.dirty) void this.refresh();
    }
  }

}

function showDiff(box, part, note = "") {
  box.querySelector("[data-assistant-diff-wait]").hidden = part !== "wait";
  const line = box.querySelector("[data-assistant-diff-note]");
  line.hidden = part !== "note";
  line.textContent = note;
  const view = box.querySelector("[data-assistant-diff-view]");
  view.hidden = part !== "view";
  if (part !== "view") view.replaceChildren();
}

function withAll(path, all) {
  const url = new URL(path, window.location.href);
  if (all) url.searchParams.set("all", "1");
  else url.searchParams.delete("all");
  return url.pathname + url.search;
}

customElements.define("dc-assistant-list", AssistantList);
