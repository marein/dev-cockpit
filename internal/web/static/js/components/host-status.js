import { labelCockpitTab } from "@dc/cockpit";
import { onServerEvent } from "@dc/events";

// Server status: how busy the machine is, how much memory is in use, how full
// the disk under the projects is. The server renders the first reading into the
// page and sends every one after it on the event stream, on connect and on the
// stream's own host beat, so this element only paints: the three bars in the
// status line and the detail rows of the dropup they open, the phone's
// Cockpit tab icon and the server row of the Cockpit sheet. A meter that carries a value
// and a label gets those written too.

// Mirrors hostinfo.Warn and hostinfo.Crit, the thresholds the first paint uses.
const WARN = 80;
const CRIT = 95;
const TONES = ["bg-green", "bg-yellow", "bg-red"];

const barClass = (value) => (value >= CRIT ? "bg-red" : value >= WARN ? "bg-yellow" : "bg-green");
const NAMES = { cpu: "CPU", mem: "RAM", disk: "Disk" };
const width = (value) => `${Math.max(0, Math.min(100, value))}%`;

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
      { key: "cpu", has: stats.hasCpu, value: Number(stats.cpu) || 0, label: stats.cpuLabel },
      { key: "mem", has: stats.hasMem, value: Number(stats.mem) || 0, label: stats.memLabel },
      { key: "disk", has: stats.hasDisk, value: Number(stats.disk) || 0, label: stats.diskLabel },
    ];
    for (const metric of metrics) {
      const meter = this.querySelector(`[data-host-meter="${metric.key}"]`);
      if (meter) {
        meter.hidden = !metric.has;
        if (metric.has) this.paintMeter(meter, metric);
      }
      const row = this.querySelector(`[data-host-row="${metric.key}"]`);
      if (row) {
        row.hidden = !metric.has;
        if (metric.has) this.paintRow(row, metric);
      }
    }
    const worst = this.querySelector("[data-host-worst-icon]");
    if (worst) this.paintWorst(worst, metrics);
    else this.hidden = !metrics.some((metric) => metric.has);
  }

  paintMeter(meter, metric) {
    const name = meter.dataset.hostName || metric.key;
    const reading = `${name} ${metric.value}%`;
    meter.setAttribute("aria-label", reading);
    meter.title = metric.label ? `${reading} · ${metric.label}` : reading;
    const bar = meter.querySelector(".js-host-bar");
    bar.style.width = width(metric.value);
    bar.classList.remove(...TONES);
    bar.classList.add(barClass(metric.value));
    const value = meter.querySelector(".js-host-value");
    if (value) value.textContent = `${metric.value}%`;
    const label = meter.querySelector(".js-host-label");
    if (label && metric.label) label.textContent = metric.label;
  }

  paintWorst(icon, metrics) {
    const top = metrics.filter((m) => m.has).reduce((a, m) => (!a || m.value > a.value ? m : a), null);
    const value = top ? top.value : -1;
    icon.classList.toggle("text-red", value >= CRIT);
    icon.classList.toggle("text-yellow", value >= WARN && value < CRIT);
    if (value >= WARN) icon.setAttribute("title", `Server ${value >= CRIT ? "critical" : "busy"}, ${NAMES[top.key]} ${value}%`);
    else icon.removeAttribute("title");
    labelCockpitTab();
  }

  paintRow(row, metric) {
    row.querySelector(".js-host-value").textContent = `${metric.value}%`;
    const bar = row.querySelector(".js-host-bar");
    bar.style.width = width(metric.value);
    bar.classList.remove(...TONES);
    bar.classList.add(barClass(metric.value));
    bar.setAttribute("aria-valuenow", String(metric.value));
    const label = row.querySelector(".js-host-label");
    if (metric.label) label.textContent = metric.label;
  }
}

customElements.define("dc-host-status", HostStatus);
