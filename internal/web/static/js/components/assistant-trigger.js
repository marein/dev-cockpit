import { growTextarea } from "@dc/dom";

// The one form that makes a trigger and the one that changes it, in the create
// dialog and on the page behind it. Every field arrives on the stand the server
// rendered into it, a new one on the defaults and a changed one on what stands,
// so nothing fills a form here and there is one reading of a stand.
//
// What is left is the switching and the zone list: the picked event decides
// which target field shows, the jobs of the assistant, the running coders or a cron schedule, and
// only the shown one posts. Both target fields take several terminals, so the
// mode beside them shows wherever they do and nowhere else: any of them fires
// it, or every one has to, and a compose run has no terminal. The batch window goes the other way round and stands for everything
// but a schedule, which has no window to fold anything into. The expiry's unit
// switches the same way: No expiry stands among the units, and picked it
// leaves the number nothing to measure, so the number goes out with it.
class AssistantTrigger extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.event = this.querySelector("[data-trigger-event]");
    this.mode = this.querySelector("[data-trigger-mode]");
    this.batch = this.querySelector("[data-trigger-batch]");
    this.task = this.querySelector("[data-trigger-task]");
    this.unit = this.querySelector("[data-trigger-unit]");
    this.event?.addEventListener("change", () => this.syncFields(), { signal });
    this.unit?.addEventListener("change", () => this.syncExpiry(), { signal });
    this.task?.addEventListener("input", () => this.grow(), { signal });
    this.fillZones();
    this.closest(".modal")?.addEventListener("shown.bs.modal", () => this.grow(), { signal });
    this.syncFields();
    this.syncExpiry();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // The zone field is a plain text input the server rendered its stand into,
  // and this only hands the browser's own list of zone names to its datalist:
  // the browser is where that list already lives, so nobody has to curate one
  // and nothing here decides which zones exist. A browser without the call
  // leaves the field exactly as it is, which is a field somebody types into,
  // and the server refuses a name it cannot load either way.
  fillZones() {
    const list = this.querySelector("[data-trigger-zones]");
    if (!list || list.childElementCount || typeof Intl?.supportedValuesOf !== "function") return;
    let zones;
    try {
      zones = Intl.supportedValuesOf("timeZone");
    } catch {
      return;
    }
    const options = document.createDocumentFragment();
    for (const zone of zones) {
      const option = document.createElement("option");
      option.value = zone;
      options.append(option);
    }
    list.append(options);
  }

  source() {
    return this.event?.selectedOptions?.[0]?.dataset.source || "";
  }

  // One target field at a time: the others are hidden and disabled, so only
  // the shown one posts under its name. The mode goes with the two terminal
  // fields, the batch window stands for every event but a schedule.
  syncFields() {
    const source = this.source();
    for (const box of this.querySelectorAll("[data-trigger-target]")) {
      this.showBox(box, box.dataset.triggerTarget === source);
    }
    this.showBox(this.mode, source === "job" || source === "coder");
    this.showBox(this.batch, source !== "cron");
    this.grow();
  }

  // No expiry greys the number out: there is nothing left for it to count, and
  // a disabled field whose reason stands in the select beside it explains
  // itself. It is switched here and not in the markup, because the select wins
  // on the server anyway and a page whose JS never ran has to be able to pick
  // a unit and type into a field that was never disabled.
  syncExpiry() {
    const number = this.querySelector("[data-trigger-span]");
    if (number) number.disabled = this.unit?.value === "never";
  }

  // A hidden field is disabled with it, so it posts nothing at all: a field
  // nobody named is one the server leaves alone, which on a change is what
  // keeps a bound this event has no use for from being written over it.
  showBox(box, on) {
    if (!box) return;
    box.hidden = !on;
    for (const field of box.querySelectorAll("select, input")) field.disabled = !on;
  }

  grow() {
    growTextarea(this.task, () => window.innerHeight);
  }
}

customElements.define("dc-assistant-trigger", AssistantTrigger);
