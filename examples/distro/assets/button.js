// Pulse's button in the projects column, a link to its page with the beat
// count in its title.
export default class PulseButton extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    fetch(this.dataset.beatsUrl, { credentials: "same-origin", signal: this.ac.signal })
      .then((response) => (response.ok ? response.json() : null))
      .then((body) => {
        if (!body || !this.ac) return;
        const link = this.querySelector("[data-pulse-button]");
        link.title = `Pulse, ${body.beats} beats`;
        link.dataset.pulseReady = "";
      })
      .catch(() => {});
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }
}
