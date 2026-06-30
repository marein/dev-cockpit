import { applyFold } from "@dc/fold";

const LIMIT = 5;
const TOGGLE_CLASS =
  "list-group-item list-group-item-action text-center text-secondary small py-1 bg-transparent border-0";

// Collapses a session/shell list to the first few entries with a "Show N more"
// toggle. Re-folds when its card changes (ajax delete/stop) via the
// "dc:rendered" event, so the toggle and counts stay correct as rows come and go.
class CollapseList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.expanded = this.dataset.collapseExpanded === "1";
    document.addEventListener("dc:rendered", (event) => {
      const root = event.detail && event.detail.root;
      if (root && (root === this || root.contains(this))) {
        this.fold();
      }
    }, { signal: this.ac.signal });
    this.fold();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  fold() {
    applyFold(this, {
      limit: LIMIT,
      items: Array.from(this.children).filter((node) => node.classList.contains("list-group-item")),
      expanded: this.expanded,
      toggleAttr: "data-collapse-toggle",
      toggleClass: TOGGLE_CLASS,
      signal: this.ac?.signal,
      onToggle: (event, next) => {
        this.expanded = next;
        this.dataset.collapseExpanded = next ? "1" : "";
        this.fold();
      },
    });
  }
}

customElements.define("dc-collapse-list", CollapseList);
