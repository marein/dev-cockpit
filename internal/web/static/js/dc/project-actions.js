import * as docker from "@dc/docker";
import { openMenu } from "@dc/contextmenu";
import { ensureOk, postForm, postJSON } from "@dc/http";
import { notifyError, notifyInfo, notifySuccess } from "@dc/toast";

// A project's own actions, shared by every surface that offers them: the
// projects page row and the quick nav's project detail. The markup carries the
// facts (the button's data attributes, one hidden span per compose stack, per
// configured command and per address a container answers on), so a surface
// renders the same attributes and gets the same menus without repeating how
// they are built. The menu bodies themselves already live in @dc/docker.

// chipLinks reads the addresses a container row carries, one hidden span per
// address: a proxy route names a host and maybe a path, a published port names
// only the port, and an empty scheme means the one this page was reached over.
export function chipLinks(chip) {
  return Array.from(chip.querySelectorAll("[data-docker-link]")).map((span) => ({
    scheme: span.dataset.linkScheme || "",
    host: span.dataset.linkHost || "",
    port: Number(span.dataset.linkPort || 0),
    path: span.dataset.linkPath || "",
  }));
}

export function containerInfo(chip) {
  return {
    id: chip.dataset.chipId,
    name: chip.dataset.chipName || "",
    portsLabel: chip.dataset.dockerPorts || "",
    links: chipLinks(chip),
    running: chip.dataset.dockerRunning === "1",
  };
}

export async function openContainerShell(info, kind, filter) {
  const data = await docker.openShell(info.id, kind, info.name, filter);
  if (data && data.url) docker.navigate(data.url);
}

export async function openLogTerminal(id, name) {
  await openContainerShell({ id, name }, "logs-shell");
}

export function openDockerMenu(chip, x, y, { signal } = {}) {
  const items = docker.containerMenuItems(containerInfo(chip), {
    onShell: (target, kind, filter) => openContainerShell(target, kind, filter),
  });
  openMenu({ x, y, items, signal });
}

// The project's menu says which container to reach, so one is reachable
// without finding its row first, plus the logs of the whole stack and the
// compose actions. A container with several addresses drills in: the same
// menu, at the same place, with that container's addresses and a way back.
// section is where the container rows stand, which differs per surface.
// composeMenuItems builds the entries out of the holder's data attributes
// and hidden spans, so the entries can stand in a menu of their own or inside
// a row's menu; onDrill(items) is how the drill in reopens the menu, and
// onDrill(null) is the way back.
export function composeMenuItems(holder, { section, onDrill } = {}) {
  const project = holder.dataset.dockerProject || "";
  const stacks = Array.from(holder.querySelectorAll("[data-docker-stack]")).map((span) => ({
    label: span.dataset.stackLabel || "",
    running: Number(span.dataset.stackRunning || 0),
    total: Number(span.dataset.stackTotal || 0),
    busy: Boolean(span.dataset.stackBusy),
    run: span.dataset.stackRun
      ? {
        id: span.dataset.stackRun,
        action: span.dataset.stackRunAction || "",
        running: Boolean(span.dataset.stackRunGoing),
        url: `/projects/${encodeURIComponent(project)}/docker/runs/${span.dataset.stackRun}`,
      }
      : null,
  }));
  const actions = Array.from(holder.querySelectorAll("[data-docker-action]")).map((span) => ({
    id: span.dataset.actionId || "",
    icon: span.dataset.actionIcon || "",
    label: span.dataset.actionLabel || "",
    command: span.dataset.actionCommand || "",
    confirm: Boolean(span.dataset.actionConfirm),
  }));
  // The containers in the order the surface renders them, which is the order
  // every docker surface stands in: unwell first, then running, then the rest.
  const containers = Array.from(section?.querySelectorAll('[data-chip-kind="docker"]') || []).map((chip) => ({
    name: chip.dataset.chipName || "",
    links: chipLinks(chip),
  }));
  return docker.projectMenuItems({
    project,
    stacks,
    containers,
    actions,
    onLogs: async (stack, filter) => {
      const data = await docker.composeLogs(project, stack.label, stack.label || project, filter);
      if (data && data.url) docker.navigate(data.url);
    },
    onDrill,
  });
}

export function openComposeMenu(button, { section, signal } = {}) {
  const rect = button.getBoundingClientRect();
  const x = Math.round(rect.left);
  const y = Math.round(rect.bottom);
  // onDrill(null) is the way back, which is this menu built again.
  const open = (items) => {
    const list = items || composeMenuItems(button, { section, onDrill: open });
    if (!list.length) return;
    openMenu({ x, y, items: list, signal });
  };
  open(null);
}

// gitMenuItems reads the destinations the server rendered onto the holder,
// the git button or the index row; icon is what pulses while a fetch runs,
// the holder's first icon unless the surface names another.
export function gitMenuItems(holder, { icon } = {}) {
  const items = [];
  if (holder.dataset.gitWorktree) {
    items.push({ label: "New worktree", icon: "ti-git-fork", href: holder.dataset.gitWorktree });
  }
  items.push({ label: "Fetch", icon: "ti-refresh", action: () => void fetchProject(holder, icon) });
  items.push({ label: "Commit changes", icon: "ti-git-commit", href: holder.dataset.gitCommit });
  items.push({ label: "Compare revisions", icon: "ti-git-compare", href: holder.dataset.gitCompare });
  return items;
}

export function openGitMenu(button, { signal } = {}) {
  const rect = button.getBoundingClientRect();
  openMenu({ x: Math.round(rect.left), y: Math.round(rect.bottom), items: gitMenuItems(button), signal });
}

export async function fetchProject(holder, icon = holder.querySelector(".ti")) {
  if (holder.getAttribute("aria-busy") === "true") return;
  const project = holder.dataset.gitProject || "";
  holder.disabled = true;
  holder.setAttribute("aria-busy", "true");
  icon?.classList.add("dc-git-working");
  try {
    const response = await postJSON(holder.dataset.gitFetch, {});
    await ensureOk(response, `Could not fetch "${project}".`);
    const data = await response.json();
    const message = data.message || (data.fetched ? `Fetched "${project}".` : `"${project}" has no remote to fetch from.`);
    if (data.fetched) notifySuccess(message);
    else notifyInfo(message);
  } catch (error) {
    notifyError(error.message);
  } finally {
    holder.disabled = false;
    holder.removeAttribute("aria-busy");
    icon?.classList.remove("dc-git-working");
  }
}

// deleteProject posts the deletion and says what happened. A project with
// containers is deleted in the background, its stacks go down first, and its
// row then renders as working until the projects event says it is gone, so
// that answer only says what was started. Every surface listens for that
// event, none of them reloads. Answers whether the server took it.
export async function deleteProject(name) {
  try {
    const response = await postForm("/projects/delete", { project: name });
    await ensureOk(response, "Could not delete the project.");
    const data = await response.json().catch(() => null);
    if (data && data.deleting) notifyInfo(`Deleting project "${name}"…`);
    else notifySuccess(`Project "${name}" deleted.`);
    return true;
  } catch (error) {
    notifyError(error.message);
    return false;
  }
}
