import { get, remove, set } from "@dc/store";

const KEY = "dc-theme";
const media = window.matchMedia("(prefers-color-scheme: dark)");
const metas = [...document.querySelectorAll('meta[name="theme-color"]')].map((meta) => ({
  meta,
  scheme: (meta.getAttribute("media") || "").includes("dark") ? "dark" : "light",
  content: meta.getAttribute("content") || "",
}));

export function themePreference() {
  const value = get(KEY, "auto");
  return value === "light" || value === "dark" ? value : "auto";
}

export function isDark() {
  const preference = themePreference();
  return preference === "auto" ? media.matches : preference === "dark";
}

function paint() {
  const preference = themePreference();
  document.documentElement.setAttribute("data-bs-theme", isDark() ? "dark" : "light");
  const source = metas.find((entry) => entry.scheme === preference);
  for (const entry of metas) {
    entry.meta.setAttribute("content", source ? source.content : entry.content);
  }
}

let settleTimer;
function softFlip() {
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  document.documentElement.classList.add("dc-theme-flip");
  clearTimeout(settleTimer);
  settleTimer = setTimeout(() => document.documentElement.classList.remove("dc-theme-flip"), 350);
}

function apply() {
  paint();
  document.dispatchEvent(new CustomEvent("dc:theme", {
    detail: { preference: themePreference(), dark: isDark() },
  }));
}

export function setThemePreference(preference) {
  if (preference === "light" || preference === "dark") set(KEY, preference);
  else remove(KEY);
  softFlip();
  apply();
}

media.addEventListener("change", () => {
  if (themePreference() === "auto") apply();
});

window.addEventListener("storage", (event) => {
  if (event.key !== KEY) return;
  softFlip();
  apply();
});

paint();
