class DcTime extends HTMLElement {
  connectedCallback() {
    const stamp = new Date(this.getAttribute("datetime") || "");
    if (Number.isNaN(stamp.getTime())) return;
    this.textContent = stamp.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  }
}

customElements.define("dc-time", DcTime);
