// The fields of a binding by hand only count while it is picked: otherwise
// they stand disabled, so they show what the binding by hand would be and
// the form sends none of them.
class ForgeBinding extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const sync = () => {
      const hand = this.querySelector('input[name="how"][value="hand"]');
      const fields = this.querySelector("[data-forge-hand]");
      if (hand && fields) fields.disabled = !hand.checked;
    };
    this.addEventListener("change", sync, { signal: this.ac.signal });
    sync();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }
}

customElements.define("dc-forge-binding", ForgeBinding);
