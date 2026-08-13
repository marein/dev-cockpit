import { confirm } from "@dc/dialog";
import { ensureOk, postForm } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

export function navigate(url) {
  if (window.app?.navigate) window.app.navigate(url);
  else window.location.href = url;
}

export async function lifecycleAction(id, action, name) {
  try {
    const response = await postForm(`/docker/${id}/${action}`, {});
    await ensureOk(response, `Could not ${action} container "${name}".`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

export async function openShell(id, kind, name) {
  try {
    const response = await postForm(`/docker/${id}/${kind}`, {});
    await ensureOk(response, `Could not open a shell for "${name}".`);
    return await response.json();
  } catch (error) {
    notifyError(error.message);
    return null;
  }
}

// composeLogs opens a terminal following a whole stack, every service of it in
// one stream. Logs are a terminal everywhere, never a dialog: a dialog holds a
// dead copy of a few hundred lines, a terminal keeps talking and is a tab like
// any other.
export async function composeLogs(project, stack, name) {
  try {
    const response = await postForm(
      `/projects/${encodeURIComponent(project)}/docker/logs`,
      { stack: stack || "" },
    );
    await ensureOk(response, `Could not open the logs of "${name || project}".`);
    return await response.json();
  } catch (error) {
    notifyError(error.message);
    return null;
  }
}

export async function compose(project, stack, action) {
  const label = action.label || action.id;
  if (action.confirm) {
    const ok = await confirm({
      title: `Run "${label}"?`,
      text: `${action.command} in ${stack ? stack : project}`,
      confirmText: "Run",
    });
    if (!ok) return false;
  }
  try {
    const response = await postForm(
      `/projects/${encodeURIComponent(project)}/docker/compose`,
      { stack: stack || "", action: action.id },
    );
    await ensureOk(response, `Could not start "${label}".`);
    notifySuccess(`${label} started.`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

export async function restoreActions() {
  try {
    const response = await postForm("/docker/actions/restore", {});
    await ensureOk(response, "Could not restore the default actions.");
    notifySuccess("The default compose actions are back.");
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

// restoreLinkRules is the same way back for the rules that read a container's
// own address out of its labels: one route, and it clears the setting instead
// of storing today's defaults into it.
export async function restoreLinkRules() {
  try {
    const response = await postForm("/docker/link-rules/restore", {});
    await ensureOk(response, "Could not restore the default link rules.");
    notifySuccess("The default link rules are back.");
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}

// linkUrl builds what a link opens. Only the browser knows two of the three
// parts: a published port is reached on the host this page was reached on, and
// a proxy route that names no scheme of its own is reached over the scheme
// this page was reached over, so it is opened protocol relative. The server
// cannot answer either one, its own X-Forwarded-Proto is what the proxy's
// entrypoint is, not what the person in front of it typed.
function linkUrl(link) {
  const host = link.host || window.location.hostname;
  const port = link.port ? `:${link.port}` : "";
  const prefix = link.scheme ? `${link.scheme}:` : "";
  return `${prefix}//${host}${port}${link.path || ""}`;
}

// linkAddress is the address as a person reads it, the same shape the settings
// preview shows: a route is its host and path, a published port is the port.
function linkAddress(link) {
  return link.host ? `${link.host}${link.path || ""}` : `:${link.port}`;
}

// LABEL_TAIL caps how much of an address never shrinks. A path can be long,
// and a tail that takes the whole row would starve the head it is supposed to
// complete.
const LABEL_TAIL = 24;

// addressLabel splits an address so the ellipsis lands in the middle. What
// tells the sibling hosts of one container apart is their end, so the end is
// the part that must survive: the tail starts at the last dot of the host and runs
// to the end, path included, and the head takes whatever is left. An address
// whose tail alone would be too long is cut a fixed distance from its end
// instead, which keeps the same promise.
function addressLabel(address) {
  const found = /\.[^./]+(\/.*)?$/.exec(address);
  let cut = found ? found.index : -1;
  if (cut < 0 || address.length - cut > LABEL_TAIL) cut = Math.max(0, address.length - LABEL_TAIL);
  return { head: `Open ${address.slice(0, cut)}`, tail: address.slice(cut) };
}

// linkItems are the addresses one container answers on, as menu entries: the
// routes first, where one exists it is the address a person wants, then a
// divider and the published ports in their numeric order. The same address
// twice is one entry.
function linkItems(links) {
  const seen = new Set();
  const routes = [];
  const ports = [];
  for (const link of links || []) {
    const key = `${link.scheme || ""}//${link.host || ""}:${link.port || 0}${link.path || ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    (link.host ? routes : ports).push(link);
  }
  ports.sort((a, b) => a.port - b.port);
  const items = routes.map(linkItem);
  if (routes.length && ports.length) items.push({ divider: true });
  return items.concat(ports.map(linkItem));
}

function linkItem(link) {
  const address = linkAddress(link);
  return {
    label: addressLabel(address),
    title: `Open ${address}`,
    icon: "ti-external-link",
    action: () => window.open(linkUrl(link), "_blank", "noopener"),
  };
}

export function containerMenuItems(info, { onShell } = {}) {
  const items = [];
  // No status line: what the daemon calls the uptime is a snapshot of the last
  // cache refresh and ages badly, and the icon color already says running,
  // stopped or unwell. What the ports are is worth the line.
  if (info.portsLabel) {
    items.push({ label: info.portsLabel, icon: "ti-plug", disabled: true });
    items.push({ divider: true });
  }
  items.push(...linkItems(info.links));
  if (info.running && info.cli !== false && onShell) {
    items.push({ label: "Shell", icon: "ti-terminal-2", action: () => void onShell(info, "shell") });
    items.push({ label: "Logs", icon: "ti-file-text", action: () => void onShell(info, "logs-shell") });
  }
  items.push({ divider: true });
  if (info.running) {
    items.push({ label: "Restart", icon: "ti-refresh", warn: true, action: () => void lifecycleAction(info.id, "restart", info.name) });
    items.push({ label: "Stop", icon: "ti-player-stop", warn: true, action: () => void lifecycleAction(info.id, "stop", info.name) });
  } else {
    items.push({ label: "Start", icon: "ti-player-play", action: () => void lifecycleAction(info.id, "start", info.name) });
  }
  return items;
}

// projectMenuItems is the project's own docker menu, the same list on the
// projects page and in the editor: where to reach what runs, then per stack
// its logs, its last run and the configured commands.
//
// **The project menu answers which container, the container's own menu answers
// which address.** A project can run a dozen containers and one of them can be
// routed under a dozen host names; all of them in one list is a wall in front
// of the one entry somebody wants. So a container gets at most one entry here.
// With exactly one address that entry is the address itself, the most
// informative thing it can say. With several it names the container and how
// many there are, and opening it opens the same menu again with that
// container's addresses and a way back (onDrill). Picking the first of a
// dozen hosts for somebody is precisely what the cockpit cannot know.
export function projectMenuItems({ project, containers = [], stacks = [], actions = [], onLogs, onDrill } = {}) {
  const items = [];
  containers.forEach((container) => {
    const links = container.links || [];
    if (!links.length) return;
    if (links.length === 1) {
      items.push(linkItem(links[0]));
      return;
    }
    // A surface that cannot drill in lists them rather than picking one.
    if (!onDrill) {
      items.push(...linkItems(links));
      return;
    }
    items.push({
      label: `${container.name} (${links.length} addresses)`,
      title: `${links.length} addresses of ${container.name}`,
      icon: "ti-external-link",
      action: () => onDrill([
        { label: "Back", icon: "ti-arrow-left", action: () => onDrill(null) },
        { divider: true },
        ...linkItems(links),
      ]),
    });
  });
  stacks.forEach((stack) => {
    if (items.length) items.push({ divider: true });
    items.push(...stackMenuItems(stack, project, actions, onLogs));
  });
  return items;
}

function stackMenuItems(stack, project, actions, onLogs) {
  const items = [];
  const suffix = stack.label ? ` (${stack.label})` : "";
  items.push({
    label: (stack.total ? `${stack.running} of ${stack.total} running` : "Nothing running") + suffix,
    icon: "ti-stack-2",
    disabled: true,
  });
  if (stack.total && onLogs) {
    items.push({ label: `Logs${suffix}`, icon: "ti-file-text", action: () => void onLogs(stack) });
  }
  if (stack.run) {
    items.push({
      label: stack.run.running ? `${stack.run.action} is running…` : `Output of ${stack.run.action}`,
      icon: stack.run.running ? "ti-loader-2" : "ti-file-description",
      iconClass: stack.run.running ? "dc-spin" : "",
      action: () => navigate(stack.run.url),
    });
  }
  if (stack.busy) return items;
  if (!actions.length) {
    items.push({ label: "No compose actions configured.", icon: "ti-plug-off", disabled: true });
    items.push({ label: "Restore the default actions", icon: "ti-restore", action: () => void restoreActions() });
    return items;
  }
  actions.forEach((action) => {
    items.push({
      label: `${action.label}${suffix}`,
      icon: action.icon,
      warn: Boolean(action.confirm),
      action: () => void compose(project, stack.label, action),
    });
  });
  return items;
}
