import * as projectActions from "@dc/project-actions";
import { menuJustClosed, openMenu, wireRowMenus } from "@dc/contextmenu";
import { confirm } from "@dc/dialog";
import { ensureOk, landingURL, postForm } from "@dc/http";
import { notifyError } from "@dc/toast";

// The project index, the list column beside the board and the phone's sheet:
// every row carries the project's menu behind its three dots, on a right click
// and on a touch long press. The column is swapped whole on every projects,
// terminals and docker event (@dc/ctx beside the board, dc-ctx-sheet in the
// sheet), and this element is the list itself, so every render wires its own
// rows. An open menu outlives that swap on purpose, the way the tab strip's
// does: it acts on the facts the row carried when it opened, and a navigation
// closes it like every other menu. The entries are built out of what the row
// carries, the same attributes and hidden spans the board's git and compose
// buttons carry, through @dc/project-actions.
class ProjectIndex extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    wireRowMenus(this, "[data-index-project]", (row, x, y) => this.openRowMenu(row, x, y), { signal });
    this.addEventListener("click", (event) => {
      const button = event.target.closest("[data-index-menu]");
      if (!button) return;
      // The dots sit inside the row's link: the click is neither the link's
      // nor, in the sheet, the tap that closes it.
      event.preventDefault();
      event.stopPropagation();
      if (menuJustClosed()) return;
      const rect = button.getBoundingClientRect();
      this.openRowMenu(button.closest("[data-index-project]"), Math.round(rect.right), Math.round(rect.bottom));
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // A drill in of the compose menu (a container with several addresses)
  // reopens the menu at the same place with those addresses, and its Back
  // entry is this menu built again.
  openRowMenu(row, x, y) {
    if (!row) return false;
    const open = (items) => openMenu({ x, y, items: items || this.rowItems(row, open) });
    open(null);
    return true;
  }

  rowItems(row, onDrill) {
    const name = row.dataset.indexProject || "";
    const project = encodeURIComponent(name);
    // The create forms return here on Cancel, the page this column stands on.
    const back = encodeURIComponent(window.location.pathname + window.location.search);
    const items = [
      { label: "Open project", icon: "ti-folder", href: row.getAttribute("href") },
      { label: "Open editor", icon: "ti-code", href: `/projects/${project}/editor` },
      { label: "New coder", icon: "ti-robot", href: `/coders/new?project=${project}&return=${back}` },
      { label: "New shell", icon: "ti-terminal-2", action: () => void this.startShell(row) },
      { divider: true },
    ];
    if (row.dataset.gitProject) {
      items.push(...projectActions.gitMenuItems(row, { icon: row.querySelector("[data-index-menu] .ti") }));
    }
    if (row.dataset.dockerProject) {
      // The container rows stand on the board, when the board is on this page.
      const section = document.getElementById(`project-${name}`);
      items.push({ divider: true });
      items.push(...projectActions.composeMenuItems(row, { section, onDrill }));
    }
    items.push({ divider: true });
    items.push({ label: "Delete project", icon: "ti-trash", danger: true, action: () => void this.deleteProject(row) });
    return items;
  }

  // The row knows its project, so the shell starts at once, the way the
  // board's chip starts one, and the page moves to it. A refusal is a toast.
  async startShell(row) {
    try {
      const response = await postForm("/shells/new", { project: row.dataset.indexPath || "" });
      await ensureOk(response, "Could not start the shell.");
      const url = await landingURL(response);
      if (!url) return;
      if (window.app?.navigate) window.app.navigate(url);
      else window.location.assign(url);
    } catch (error) {
      notifyError(error.message);
    }
  }

  // The board's confirm with the board's note, then the shared deletion: the
  // projects event takes the row off every surface, nothing reloads.
  async deleteProject(row) {
    const name = row.dataset.indexProject || "";
    const ok = await confirm({
      title: `Delete project "${name}"?`,
      text: row.dataset.indexDeleteNote,
      confirmText: "Delete",
    });
    if (!ok) return;
    await projectActions.deleteProject(name);
  }
}

customElements.define("dc-project-index", ProjectIndex);
