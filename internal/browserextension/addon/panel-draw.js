import { api, el } from "./api.js";
import { label, readImage, readMarks } from "./browser-context.js";
import { keepPicture, readPicture } from "./pictures.js";

const TOOLS = ["pen", "arrow", "rect", "text"];
const COLORS = ["red", "orange", "green", "blue", "purple"];

function stamp(time) {
  const at = new Date(time);
  const two = (n) => String(n).padStart(2, "0");
  return `${at.getFullYear()}${two(at.getMonth() + 1)}${two(at.getDate())}-${two(at.getHours())}${two(at.getMinutes())}${two(at.getSeconds())}`;
}

export function pictureName(url, time) {
  let host = "page";
  try {
    host = new URL(url).host.replace(/[^a-z0-9.]+/gi, "-").replace(/^-+|-+$/g, "") || "page";
  } catch {
    host = "page";
  }
  return `drawing-${host}-${stamp(time)}.png`;
}

function save(blob, name) {
  const href = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = href;
  link.download = name;
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(href), 60000);
}

function newKey() {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}

export class DrawBar {
  constructor(panel) {
    this.panel = panel;
    this.toggle = document.querySelector("[data-draw]");
    this.bar = document.querySelector("[data-draw-bar]");
    this.undo = this.bar.querySelector("[data-undo]");
    this.redo = this.bar.querySelector("[data-redo]");
    this.clear = this.bar.querySelector("[data-draw-clear]");
    this.saveButton = this.bar.querySelector("[data-picture-save]");
    this.addButton = this.bar.querySelector("[data-picture-add]");
    this.preview = document.querySelector("[data-preview]");
    this.previewText = this.preview.querySelector("[data-preview-text]");
    this.state = { on: false, count: 0, undo: false, redo: false };
    this.tools = { tool: "pen", color: "red" };
    this.busy = "";
    this.shown = null;
    this.toggle.addEventListener("click", () => void panel.ask("draw", { on: !this.state.on }));
    this.bar.addEventListener("click", (event) => this.onClick(event));
    this.preview.querySelector("[data-preview-save]").addEventListener("click", () => this.saveShown());
    this.preview.querySelector("[data-preview-close]").addEventListener("click", () => this.closePreview());
    api.storage.local.get("draw-tools").then((kept) => this.setTools(kept["draw-tools"])).catch(() => {});
    api.storage.onChanged.addListener((changes, area) => {
      if (area === "local" && changes["draw-tools"]) this.setTools(changes["draw-tools"].newValue);
    });
  }

  setTools(raw) {
    this.tools = { tool: TOOLS.includes(raw?.tool) ? raw.tool : "pen", color: COLORS.includes(raw?.color) ? raw.color : "red" };
    this.render();
  }

  setState(raw) {
    this.state = { on: raw?.on === true, count: Number.isSafeInteger(raw?.count) ? raw.count : 0, undo: raw?.undo === true, redo: raw?.redo === true };
    this.render();
    this.panel.changed();
  }

  onClick(event) {
    const button = event.target instanceof Element ? event.target.closest("button") : null;
    if (!button || button.disabled) return;
    const data = button.dataset;
    if (data.tool) void this.panel.ask("tools", { tool: data.tool });
    else if (data.color) void this.panel.ask("tools", { color: data.color });
    else if ("undo" in data) void this.panel.ask("draw-step", { step: -1 });
    else if ("redo" in data) void this.panel.ask("draw-step", { step: 1 });
    else if ("drawClear" in data) void this.panel.ask("draw-clear");
    else if ("pictureSave" in data) void this.picture("save");
    else if ("pictureAdd" in data) void this.picture("add");
  }

  render() {
    this.toggle.setAttribute("aria-pressed", this.state.on ? "true" : "false");
    this.toggle.textContent = this.state.on ? "Drawing" : "Draw";
    this.bar.hidden = !this.state.on && !this.state.count;
    for (const node of this.bar.querySelectorAll("[data-tool]")) node.setAttribute("aria-pressed", node.dataset.tool === this.tools.tool ? "true" : "false");
    for (const node of this.bar.querySelectorAll("[data-color]")) node.setAttribute("aria-pressed", node.dataset.color === this.tools.color ? "true" : "false");
    this.undo.disabled = !this.state.undo;
    this.redo.disabled = !this.state.redo;
    this.clear.disabled = !this.state.count;
    this.saveButton.disabled = Boolean(this.busy);
    this.addButton.disabled = Boolean(this.busy);
    this.saveButton.textContent = this.busy === "save" ? "Taking…" : "Download PNG";
    this.addButton.textContent = this.busy === "add" ? "Taking…" : "Add to the tray";
  }

  async picture(use) {
    const tab = this.panel.tabId;
    if (this.busy || tab < 0) return;
    this.busy = use;
    this.render();
    this.panel.say("", { tab });
    try {
      const ready = await this.panel.ask("draw-prepare", { tabId: tab });
      if (!ready?.page) throw new Error(ready?.error || "The page did not answer. Try again once it has loaded.");
      let shot;
      try {
        shot = await api.tabs.captureVisibleTab(this.panel.windowId, { format: "png" });
      } catch {
        throw new Error("The browser gave no picture of this page. Browser pages and the extension stores stay closed to it.");
      }
      const blob = await (await fetch(shot)).blob();
      const bitmap = await createImageBitmap(blob);
      const width = bitmap.width;
      bitmap.close();
      const page = ready.page;
      const w = Number(page.viewport?.w) || 0;
      const item = readImage({ kind: "image", url: page.url, title: page.title, viewport: page.viewport, width: w, height: Number(page.viewport?.h) || 0, ratio: w ? Math.round((width / w) * 100) / 100 : 1, pixels: true, marks: readMarks(page.marks), at: Date.now() });
      if (!item) throw new Error("The picture could not be read.");
      if (use === "save") {
        save(blob, pictureName(item.url, item.at));
        this.panel.say("Downloaded the picture.", { tab });
        return;
      }
      const key = newKey();
      await keepPicture(key, blob);
      const added = await this.panel.ask("add-image", { key, item, tabId: tab });
      if (!added?.added) throw new Error(added?.error || "The picture could not be added.");
      this.panel.say("The drawing is in the tray.", { tab });
    } catch (error) {
      this.panel.say(error.message, { error: true, tab });
    } finally {
      this.busy = "";
      this.render();
    }
  }

  async show(entry) {
    const tab = this.panel.tabId;
    const blob = await readPicture(entry.key);
    if (tab !== this.panel.tabId) return;
    this.closePreview();
    if (!blob) {
      this.panel.say("The picture is gone from this browser, its marks stay in the tray.", { error: true });
      return;
    }
    const href = URL.createObjectURL(blob);
    this.shown = { entry, blob, href, image: el("img", { src: href, alt: label(entry.item), dataset: { previewImage: "" } }) };
    this.preview.prepend(this.shown.image);
    this.previewText.textContent = label(entry.item);
    this.preview.hidden = false;
  }

  saveShown() {
    if (this.shown) save(this.shown.blob, pictureName(this.shown.entry.item.url, this.shown.entry.item.at));
  }

  closePreview() {
    if (this.shown) {
      this.shown.image.remove();
      URL.revokeObjectURL(this.shown.href);
    }
    this.shown = null;
    this.preview.hidden = true;
  }

  follow(items) {
    if (this.shown && !items.some((one) => one.key === this.shown.entry.key)) this.closePreview();
  }
}
