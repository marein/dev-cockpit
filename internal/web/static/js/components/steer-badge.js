// A count on the aside's furniture: how many coders this assistant steers, how
// many triggers still fire, or both together on the button that opens the
// aside. Which one it counts is the count attribute, one word or both.
//
// It reads the two lists that stand on the page instead of asking the server
// for them again: both are dc-assistant-list elements that refresh themselves
// on the assistant event and write the number onto the body they swap in, so
// the answer is already here and a fetch of its own would be the same request
// a third time. The lists say when they swapped, and every badge recounts.
const COUNTS = {
  jobs: ["[data-assistant-jobs-open]", "assistantJobsOpen"],
  triggers: ["[data-assistant-triggers-open]", "assistantTriggersOpen"],
};

class SteerBadge extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    document.addEventListener("dc:assistant-counts", () => this.refresh(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  refresh() {
    if (!this.ac) return;
    let open = 0;
    for (const name of (this.getAttribute("count") || "jobs").split(/\s+/)) {
      const count = COUNTS[name];
      if (!count) continue;
      open += Number(document.querySelector(count[0])?.dataset[count[1]]) || 0;
    }
    this.textContent = String(open);
    this.classList.toggle("d-none", open === 0);
  }
}

customElements.define("dc-steer-badge", SteerBadge);
