import { api, el, EVERY_PAGE, everyPage, POPUP } from "./api.js";
import { call, CockpitError, cockpitOrigin, DOWNLOAD_PATH, dropSetup, fit, HANDSHAKE, pattern, readSetup, saveSetup, setupSaved, VERSION } from "./cockpit.js";
import { BROWSER } from "./version.js";

// Every page is never dropped here: Chrome would drop the cockpit's address
// with it.

const form = document.querySelector("[data-setup-form]");
const address = document.querySelector("[data-address]");
const token = document.querySelector("[data-token]");
const tokenHint = document.querySelector("[data-token-hint]");
const forget = document.querySelector("[data-forget]");
const every = document.querySelector("[data-every-page]");
const save = document.querySelector("[data-save]");
const result = document.querySelector("[data-result]");

let saved = null;
let round = 0;

function say(parts, error = false) {
  result.classList.toggle("error", error);
  result.replaceChildren(...[].concat(parts));
}

async function show(fresh) {
  saved = await readSetup().catch(() => null);
  if (saved && (fresh || !address.value)) address.value = saved.origin;
  if (fresh) token.value = "";
  token.placeholder = saved ? "Saved. Paste a new token to replace it." : "";
  tokenHint.textContent = saved ? "Left empty, the saved token stays." : "Paste the token the cockpit showed you once.";
  forget.hidden = !saved;
  const granted = await everyPage();
  every.disabled = granted;
  if (granted || saved) every.checked = granted;
}

function words(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

function release(origin) {
  if (saved && pattern(saved.origin) === pattern(origin)) return Promise.resolve();
  return api.permissions.remove({ origins: [pattern(origin)] }).catch(() => {});
}

async function finish(origin, secret, asked, wide) {
  round += 1;
  const mine = round;
  say(`Allow the extension to reach ${origin}${wide ? " and every page" : ""} in the browser's prompt.`);
  let granted = false;
  try {
    granted = await asked;
  } catch {
    granted = false;
  }
  if (mine !== round) return;
  if (!granted) {
    say(wide ? `The browser did not grant the access. Save again and allow it, or untick Access to every page to allow ${origin} alone.` : `The browser did not grant access to ${origin}. Save again and allow it.`, true);
    return;
  }
  save.disabled = true;
  say("Checking…");
  try {
    const handshake = await call({ origin, token: secret }, "GET", HANDSHAKE);
    const before = saved;
    await saveSetup(origin, secret);
    setupSaved();
    if (before && pattern(before.origin) !== pattern(origin)) await api.permissions.remove({ origins: [pattern(before.origin)] }).catch(() => {});
    await show(true);
    const fits = fit(handshake);
    const download = fits.text ? [" ", el("a", { href: new URL(DOWNLOAD_PATH, origin).href, target: "_blank", rel: "noopener" }, "Download")] : [];
    if (fits.blocks) {
      say(["Connected. ", fits.text, ...download], true);
      return;
    }
    const answer = await call({ origin, token: secret }, "GET", "/api/browser-extension/targets");
    const assistants = Array.isArray(answer.assistants) ? answer.assistants.length : 0;
    const coders = Array.isArray(answer.coders) ? answer.coders.length : 0;
    say([`Connected. The cockpit lists ${words(assistants, "assistant", "assistants")} and ${words(coders, "running coder", "running coders")}.`, ...(fits.text ? [" ", fits.text, ...download] : [])]);
  } catch (error) {
    await release(origin);
    say(error instanceof CockpitError ? error.message : "The check failed.", true);
  } finally {
    save.disabled = false;
  }
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  const origin = cockpitOrigin(address.value);
  if (!origin) {
    say("Enter the cockpit's http or https address, like https://cockpit.example.com:3000.", true);
    address.focus();
    return;
  }
  const typed = token.value.trim();
  const secret = typed || (saved && saved.origin === origin ? saved.token : "");
  if (!secret) {
    say(`Paste the token of the cockpit at ${origin}.`, true);
    token.focus();
    return;
  }
  // The browser grants access only within the user's click, so it is asked first.
  const wide = every.checked && !every.disabled;
  const asked = api.permissions.request({ origins: wide ? [pattern(origin), ...EVERY_PAGE] : [pattern(origin)] });
  void finish(origin, secret, asked, wide);
});

forget.addEventListener("click", async () => {
  if (!saved) return;
  const origin = saved.origin;
  await dropSetup();
  setupSaved();
  await api.permissions.remove({ origins: [pattern(origin)] }).catch(() => {});
  address.value = "";
  await show(true);
  say("The address and the token are forgotten.");
});

document.querySelector("[data-version]").textContent = `Version ${VERSION}`;
document.querySelector("[data-every-safari]").hidden = BROWSER !== "safari";
for (const use of document.querySelectorAll("[data-use]")) use.hidden = use.dataset.use !== (POPUP ? "popup" : "panel");
api.commands?.getAll?.().then((commands) => {
  const key = commands.find((one) => one.name === "_execute_action")?.shortcut;
  if (key) for (const text of document.querySelectorAll("[data-key-text]")) text.textContent = ` or press ${key}`;
}).catch(() => {});
api.permissions.onAdded.addListener(() => void show(false));
api.permissions.onRemoved.addListener(() => void show(false));
void show(false);
