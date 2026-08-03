import { onServerEvent } from "@dc/events";

// Server status: how busy the machine is, how much memory is in use, how full
// the disk under the projects is. The server renders the first reading into the
// page and sends every one after it on the event stream, on connect and on each
// heartbeat, so this element only paints.
//
// The layout mounts one instance per header breakpoint, the way it mounts the
// notification bell twice, so a reading paints every surface at once rather than
// each instance owning its own subtree.

// Mirrors hostinfo.Warn and hostinfo.Crit, the thresholds the first paint uses.
const WARN = 80;
const CRIT = 95;

const barClass = (value) => (value >= CRIT ? "bg-red" : value >= WARN ? "bg-yellow" : "bg-green");

class HostStatus extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    onServerEvent("host", (event) => this.paint(event.detail), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  paint(stats) {
    if (!stats) return;
    const metrics = [
      { key: "cpu", has: stats.hasCpu, value: stats.cpu, label: stats.cpuLabel },
      { key: "mem", has: stats.hasMem, value: stats.mem, label: stats.memLabel },
      { key: "disk", has: stats.hasDisk, value: stats.disk, label: stats.diskLabel },
    ];
    for (const metric of metrics) {
      for (const node of document.querySelectorAll(`[data-host-row="${metric.key}"]`)) {
        node.hidden = !metric.has;
        if (!metric.has) continue;
        this.paintMetric(node, metric);
      }
    }
    const level = stats.level === "crit" || stats.level === "warn" ? stats.level : "";
    for (const icon of document.querySelectorAll(".js-host-icon")) {
      icon.classList.remove("text-yellow", "text-red");
      if (level) icon.classList.add(level === "crit" ? "text-red" : "text-yellow");
    }
    const any = metrics.some((metric) => metric.has);
    for (const surface of document.querySelectorAll("dc-host-status")) {
      surface.hidden = !any;
    }
  }

  paintMetric(node, metric) {
    const value = Number(metric.value) || 0;
    const text = node.querySelector(".js-host-value");
    if (text) text.textContent = `${value}%`;
    const bar = node.querySelector(".js-host-bar");
    if (bar) {
      bar.style.width = `${Math.max(0, Math.min(100, value))}%`;
      bar.classList.remove("bg-green", "bg-yellow", "bg-red");
      bar.classList.add(barClass(value));
      bar.setAttribute("aria-valuenow", String(value));
    }
    const label = node.querySelector(".js-host-label");
    if (label && metric.label) label.textContent = metric.label;
  }
}

customElements.define("dc-host-status", HostStatus);
