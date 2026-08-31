export function relativeTime(value) {
  const t = value instanceof Date ? value.getTime() : Date.parse(value);
  if (Number.isNaN(t)) return "";
  const seconds = Math.max(0, (Date.now() - t) / 1000);
  if (seconds < 45) return "just now";
  if (seconds < 3600) return Math.round(seconds / 60) + "m ago";
  if (seconds < 86400) return Math.round(seconds / 3600) + "h ago";
  return Math.round(seconds / 86400) + "d ago";
}

class DcTime extends HTMLElement {
  connectedCallback() {
    const stamp = new Date(this.getAttribute("datetime") || "");
    if (Number.isNaN(stamp.getTime())) return;
    if (this.hasAttribute("relative")) {
      this.textContent = relativeTime(stamp);
      this.title = stamp.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
      return;
    }
    this.textContent = stamp.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  }
}

customElements.define("dc-time", DcTime);
