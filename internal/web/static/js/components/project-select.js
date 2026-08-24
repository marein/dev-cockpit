import * as projectSort from "@dc/project-sort";

// The project select of the create forms. The server renders the options
// alphabetically with the sort data attributes on them, and this puts them into
// the order the projects page stands in, out of the same stored mode through
// @dc/project-sort. Reordering options resets a select's selection, so the
// preselection is set again afterwards: the project the form was opened from
// when the server marked one, else the first option of the new order, which is
// the rule the alphabetical list had. It runs once, a reconnect must not throw
// away what the user picked in the meantime.
class ProjectSelect extends HTMLElement {
  connectedCallback() {
    if (this.sorted) return;
    this.sorted = true;
    const select = this.querySelector("select");
    if (!select) return;
    const pinned = select.querySelector("option[selected]");
    projectSort.sort(select);
    if (pinned) select.value = pinned.value;
    else select.selectedIndex = 0;
  }
}

customElements.define("dc-project-select", ProjectSelect);
