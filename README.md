# dev-cockpit

__Table of contents__

* [Overview](#overview): A brief introduction to the cockpit.
* [Features](#features): What the cockpit does, grouped by area.
* [Deployment Guide](#deployment-guide): Requirements, installation, running and updating.
* [Configuration](#configuration): Flags, settings and custom distributions.

## Overview

This is a web cockpit for a development machine, usable from a desktop browser and a phone.
It runs coders (Claude Code, GitHub Copilot CLI, OpenCode, Codex) and shells in tmux on the host,
and adds an editor with git, Docker Compose control, and assistants that hand work to coders and follow it through.

> This is a personal productivity tool and it is **100% vibe coded**. Coding agents write the code, test every
> feature in the browser, and run the integration tests. Architecture and security are reviewed from time to time,
> but not on every change. **Run it only on machines and networks you trust.**

## Features

Each area of the cockpit is described below.

<details>
  <summary>Projects</summary>

  ### Projects

  * **Management**: Create, open and delete projects, including git worktrees of an existing repository.
    Branch pickers search local and remote branches.
  * **Overview**: Every project row shows its coders, shells and containers, with a menu for the editor,
    new terminals, git and compose actions.
  * **Upstream**: Fetch from the projects page and see how far the branch is from its upstream.
</details>

<details>
  <summary>Terminals</summary>

  ### Terminals

  * **Coders and shells**: Start them in any project, attach from any device, and resume a stopped coder
    with its conversation. One instance serves every supported and installed coder CLI.
  * **Layout**: Reorder tabs, group terminals into split views with columns, and switch with keyboard shortcuts
    or swipes on a phone.
  * **Copy view**: Shows a coder's recorded conversation or a shell's scrollback as selectable text.
  * **Files**: Drop or paste files into a coder terminal and reference them in a prompt.
  * **Phone input**: On screen keys and direction pads.
  * **Git passphrase**: `dev-cockpit git` runs git through the cockpit, so an ssh passphrase is asked in the
    browser instead of blocking a terminal.
  * **Restore**: Optionally restores all terminals after a host reboot.
</details>

<details>
  <summary>Editor</summary>

  ### Editor

  * **Files**: File tree, tabs, quick open, find and replace in files, and previews for markdown, SVG, images,
    video and audio. Upload, download, move, copy, rename and extract archives.
  * **Saving**: Autosave. A save never overwrites what a coder or git wrote in the meantime, and open files
    follow changes on disk.
  * **Code navigation**: Go to definition and find usages for Go, PHP, TypeScript, JavaScript and Python, including
    dependency and standard library sources, through language servers in Docker.
  * **Git**: Change marks, diff against HEAD or any revision, blame, file history, commit selected files with
    amend and push, switch and create branches, push, pull, fetch, tags, revert, clone, and compare two revisions.
  * **Line comments**: Comments stay on their line while the file changes. Assistants can manage them too.
  * **Panels**: A terminal panel below the code and a Docker view per project.
</details>

<details>
  <summary>Assistants</summary>

  ### Assistants

  * **Conversations**: Any number of assistants, each with its own thread, running on a supported and installed coder CLI.
    They share one memory of what they were told.
  * **Awareness**: They see running coders, shells, projects and notifications.
  * **Jobs**: They start coders for a task and steer them as jobs, with a criterion that says when the task is
    done. Checks look at the coder when it stops, and only done or blocked is reported.
  * **Triggers**: An assistant reacts to a job ending, a coder signal or a cron schedule, and pushes the answer
    into its thread.
  * **Actions**: They run compose commands and delete projects, coders or assistants, with an approval step
    where configured.
  * **Media**: Attach pictures and files, speak by holding the microphone, and have answers read aloud.
    Speech runs locally in Docker.
  * **Models**: The model is set per assistant for chat, checks and triggers.
</details>

<details>
  <summary>Docker</summary>

  ### Docker

  * **Containers**: Compose containers show as chips on their project, live from the daemon.
    Open a shell or logs in a container, filter logs, start, stop and restart.
  * **Compose commands**: Configurable per project (up, down, rebuild and others), run in the background with
    their output on a page.
  * **Links**: To published ports and to hosts routed by a reverse proxy.
</details>

<details>
  <summary>Sharing</summary>

  ### Sharing

  * **Links**: Share projects and assistants with granular permissions, through a secure, expiring named link
    that needs no login.
  * **Projects**: A guest sees the files, git history, line comments, coders and shells live, and the
    containers with their logs. A link can allow comments, written under its name.
  * **Assistants**: A guest sees the conversation live, the jobs and triggers, and the coders the assistant steers.
</details>

<details>
  <summary>Notifications</summary>

  ### Notifications

  * **Sources**: A coder finishes or asks, a long shell command ends, a compose run ends, a worktree post script fails,
    git needs a passphrase, or an assistant answers or needs an approval.
  * **In the browser**: Bell, marks on terminals and projects, toasts and a jingle.
  * **Off the page**: Web Push to phones and desktops, and webhooks (Slack compatible).
</details>

<details>
  <summary>Costs</summary>

  ### Costs

  * **Scope**: Experimental, counts claude with Claude and Ollama Cloud models.
  * **Spend**: What coders and assistants spent, at API list price.
  * **Charts**: Stacked bar charts per project, assistant, coder and model over any period.
  * **Prices**: Price lists are refreshed daily from LiteLLM and ollama.com. Months to keep are set under Settings.
</details>

<details>
  <summary>Settings</summary>

  ### Settings

  Most of the app is configured here. The documentation is built in at `/docs`.
</details>

## Deployment Guide

### Requirements

* Linux or macOS.
* `tmux` on the host.
* At least one coder CLI installed and logged in: `claude`, `copilot`, `opencode` or `codex`. `claude` has to be 2.1.152 or newer, the first release that knows every hook the cockpit registers.
* Optional: `git` for the git features, Docker for compose stacks, code navigation and voice.

The server refuses to start without tmux or without any coder CLI. A coder whose CLI is missing is skipped.
The UI edits each coder's config under the home directory:

| Coder      | Instructions file                    | Agents directory           | Skills directory            |
|------------|--------------------------------------|----------------------------|-----------------------------|
| `claude`   | `~/.claude/CLAUDE.md`                | `~/.claude/agents`         | `~/.claude/skills`          |
| `copilot`  | `~/.copilot/copilot-instructions.md` | `~/.copilot/agents`        | `~/.copilot/skills`         |
| `opencode` | `~/.config/opencode/AGENTS.md`       | `~/.config/opencode/agent` | `~/.config/opencode/skills` |
| `codex`    | `~/.codex/AGENTS.md`                 | `~/.codex/agents`          | `~/.codex/skills`           |

Claude coders and assistants can run on models served by Ollama through `ollama launch`.
Pick `ollama/<name>` as the model. Needs ollama installed, and `ollama signin` for cloud models.

### Install

The following script resolves the latest release, downloads the archive for the platform and extracts the
`dev-cockpit` binary into `~/.local/bin`. To pin a version, replace the first line with `VERSION=1.6.0`.

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/marein/dev-cockpit/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac

mkdir -p ~/.local/bin
curl -fsSL "https://github.com/marein/dev-cockpit/releases/download/${VERSION}/dev-cockpit_${VERSION}_${os}_${arch}.tar.gz" \
  | tar -xzf - -C ~/.local/bin dev-cockpit
chmod +x ~/.local/bin/dev-cockpit
```

Alternatively, download the archive from the [releases](https://github.com/marein/dev-cockpit/releases)
and put the binary on the `PATH`.

> Keep the binary in a user writable directory like `~/.local/bin`, so the update can replace it without `sudo`.
> A root owned path like `/usr/local/bin` works for updates only when dev-cockpit runs as root.

> The macOS binary is unsigned. If Gatekeeper blocks it, run
> `xattr -d com.apple.quarantine ~/.local/bin/dev-cockpit` once.

### Run

```bash
dev-cockpit serve --addr 0.0.0.0:3000 --projects-dir ~/projects
```

Open the address in a browser and log in. The default `--addr` is `0.0.0.0:80`, which requires root.

The default login is `admin` / `password`. Change it before exposing the server. Generate a bcrypt hash with
`dev-cockpit hash-password` and pass it together with a random cookie key:

```bash
dev-cockpit serve --addr 0.0.0.0:3000 \
  --auth-user admin \
  --auth-password-hash '<hash>' \
  --session-cookie-key '<random-secret>'
```

For HTTPS, pass `--tls-cert-file` and `--tls-key-file`, or terminate TLS in a reverse proxy. In that case,
bind locally, for example `--addr 127.0.0.1:3000`, and set `--trusted-proxies` to the proxy's address.

> Web Push and the microphone require HTTPS.

### Update

The status line shows when a newer release exists. The update downloads the release for the platform,
verifies its checksum, replaces the binary and restarts in place. Running terminals keep running because they
live in tmux. Run `dev-cockpit --version` to print the version of a binary.

## Configuration

Run `dev-cockpit serve --help` to list every flag. The main ones are:

* **`--addr`**: Listen address, default `0.0.0.0:80`.
* **`--projects-dir`**: Root directory of the projects, default `~/projects`.
* **`--state-dir`**: Directory for the state, default `~/.local/state/dev-cockpit`.
* **`--auth-user`, `--auth-password-hash`, `--session-cookie-key`**: The login.
* **`--tls-cert-file`, `--tls-key-file`, `--trusted-proxies`**: HTTPS and proxies.
* **`--max-request-body-size`**: Upload limit in bytes, default 100 MB.

Everything else is set in the UI under Settings and stored in the state directory.

### Custom Distributions

A distribution ships its own version, source link and update feed. It is a separate Go module
(`go get github.com/marein/dev-cockpit`) with a `main.go`. An empty field keeps the default of a plain build.

```go
package main

import "github.com/marein/dev-cockpit/distro"

func main() {
	distro.Main(distro.Build{
		Version:          "1.2.3",
		RepoURL:          "https://example.com/you/your-distribution",
		UpdateFeedURL:    "https://gitlab.example.com/api/v4/projects/42/releases?per_page=100",
		UpdateFeedFormat: "gitlab",
	})
}
```

`UpdateFeedFormat` is either `github` or `gitlab`. The feed must follow the release conventions of this
repository: semver tags, `dev-cockpit_<version>_<os>_<arch>.tar.gz` containing `dev-cockpit`, and
`dev-cockpit_<version>_checksums.txt`.

> Plugins are experimental and not part of the stable contract. A distribution adds them through the
> `ServePlugins` field of `distro.Build`, see the [plugin package](https://github.com/marein/dev-cockpit/tree/master/plugin).
> A pair marked `Optional` ships switched off, see [Optional compiled plugins](#optional-compiled-plugins).
> [`examples/distro`](examples/distro) is a distribution with one plugin always on and one optional.

### Plugins

Plugins are experimental and not part of the stable contract. Compiled and external plugins reach the same
surface: every `Add` of `plugin.Serve`, `AddPage` included, is a manifest entry, and the `Serve` readers
(`StateDir`, `PluginStateDir`, `ProjectsDir`, `Projects`, `Assistants`) answer on the callback socket.

| `plugin.Serve` | External plugin |
| --- | --- |
| `AddRoutes` | Its process serves HTTP on its socket for everything below `/plugins/<id>/` |
| `AddAssets` | `assets`, a directory of the plugin, through the same hashed pipeline |
| `AddCustomElement` | `elements`, each a name and a module in `assets` |
| `AddSlotHTML` | `slots`, each a slot and a markup file |
| `AddPage` | `page`, title, icon, nav label and a markup file for `Page.HTML` |
| `Routes.Path` | `{{base}}` in the markup, `/plugins/<id>` |
| `AddSettings`, `AddAssistantMemory` | `settings`, `memory` |
| `StateDir`, `PluginStateDir`, `ProjectsDir` | Environment, and `GET /v1/plugin` on the callback socket |
| `Projects().Create`, `Project.Changed` | `POST /v1/projects`, `POST /v1/projects/<name>/changed` |
| `Assistants().Create`, `List`, `LastMessage`, `Coders` | `POST /v1/assistants`, `GET /v1/assistants`, `GET /v1/assistants/<id>/last-message`, `GET /v1/assistants/coders` |
| `AddEventHandler` | `events`, streamed from `GET /v1/events` |
| `Context`, ended at a switch off | `SIGTERM` to its process group, see [Process](#process) |

An external plugin is a directory named after its id in `<state-dir>/external-plugins`. Settings &rarr; Plugins
installs one from a public GitHub or GitLab repository (see [Releases](#releases)) or copies one from a
directory on the host, without `.git`. A directory dropped or linked into the folder is picked up too. A
plugin runs as the cockpit's user and its frontend code runs in the cockpit page, install only plugins you
trust. The page switches, restarts, removes plugins and shows their log, every open page follows live.
[`examples/plugins/hello`](examples/plugins/hello) is a complete plugin in Python.

#### Optional compiled plugins

A distribution marks a compiled plugin optional on its pair:

```go
ServePlugins: []plugin.Named[plugin.ServePlugin]{
	{ID: "stamp", Plugin: stamp{}},
	{ID: "pulse", Plugin: &pulse{}, Optional: true},
},
```

Settings &rarr; Plugins lists compiled plugins under Built in. An always on plugin has no action. An optional
one ships switched off and has Enable and Disable, no Remove, it updates with the binary. The switch survives
restarts and reaches every open page live, like an external plugin's.

* While it is off nothing of it runs or is written: no `ConfigureServe`, no state directory, no settings or
  memory, no events.
* Every switch on, and every start while it is on, calls `ConfigureServe` with a fresh `Serve`. Settings and
  memory are applied with their overwrite rules, its page, rail entry, slot markup, routes, assets and event
  handlers appear on every open page without a reload.
* Every switch off ends the `Serve`'s `Context` and removes what it added. Whatever `ConfigureServe` started
  ends with `Context().Done()`, a call of `Assistants` through that `Serve` answers an error. Its routes
  answer 404, its open page says it is not running, after a reload too. Written settings and memory files
  stay, edits included, and so does its state directory. The next switch on writes them only where the
  overwrite rule says so.
* An error or panic in `ConfigureServe` refuses the switch on and the plugin stays off. One that fails at a
  start while switched on shows as broken, the cockpit starts anyway.
* Ids are unique across compiled and external plugins, an optional plugin's id is taken while it is off.

The switches live in `<state-dir>/external-plugins.json` (`on`), beside those of external plugins (`disabled`).

#### Manifest

`plugin.json`, unknown fields are refused:

```json
{
  "protocol": 1,
  "id": "hello",
  "name": "Hello",
  "version": "1.0.0",
  "description": "One line for the Plugins page.",
  "run": "hello.py",
  "assets": "assets",
  "elements": [{"name": "board", "module": "board.js"}],
  "slots": [{"slot": "projects-actions", "html": "slot.html"}],
  "page": {"title": "Hello notes", "icon": "ti-notes", "nav_label": "Hello", "html": "page.html"},
  "settings": {"values": {"hello-greeting": "Hi"}, "overwrite": false},
  "memory": {"dir": "memory", "overwrite": false},
  "events": ["job", "coder", "compose"]
}
```

* `id`: lowercase words joined by dashes, at most 40 characters, equal to the directory name and to no
  compiled plugin's id. Elements are named `<id>-<name>`.
* `run`: an executable file in the directory, started with the directory as working directory.
* Paths are relative and stay inside the directory, symlinks included, else the plugin is broken.
* `slots`: `projects-actions` and `projects-empty-actions`, a slot may appear more than once.
* `html`: a markup file of at most 64 KiB UTF-8, what a compiled plugin passes to `AddSlotHTML` and
  `Page.HTML`. Every `{{base}}` becomes `/plugins/<id>`, like `Routes.Path`:
  `<hello-board data-notes-url="{{base}}/notes">`. It is read when the plugin loads, again on restart, and
  parsed as HTML, so an unbalanced tag stays inside its place and a `plaintext` element is dropped. It renders
  unescaped in the HTML response, before any module runs, while the process answers. Otherwise the slot stays
  empty and the page shows the plugin's state. A `script` in it runs on full page loads only, behavior
  belongs in an element's module.
* An element's module default exports its class, the cockpit defines it once the element is on the page. It
  may import the cockpit's modules, `@dc/http` sends the CSRF token.

#### Process

The cockpit starts `run` in its own process group with the cockpit's environment plus:

| Variable | Meaning |
| --- | --- |
| `DEV_COCKPIT_PROTOCOL` | `1` |
| `DEV_COCKPIT_PLUGIN_ID`, `DEV_COCKPIT_PLUGIN_DIR` | Id and directory |
| `DEV_COCKPIT_PLUGIN_BASE` | `/plugins/<id>`, the browser's path to it |
| `DEV_COCKPIT_PLUGIN_SOCKET` | The unix socket the plugin listens on, HTTP/1.1 |
| `DEV_COCKPIT_SOCKET`, `DEV_COCKPIT_TOKEN` | The callback socket and its bearer token, new on every start |
| `DEV_COCKPIT_PLUGIN_STATE_DIR` | `<state-dir>/plugins/<id>`, the plugin's own, not in a backup |
| `DEV_COCKPIT_STATE_DIR`, `DEV_COCKPIT_PROJECTS_DIR` | The instance's directories |

* The plugin runs once it answers HTTP on its socket, within 15 seconds. `GET /.dev-cockpit/probe` every 10
  seconds, any status, two misses end it. An ended plugin restarts after 1, 2, 4 up to 60 seconds.
* Stop, disable, remove and shutdown send SIGTERM to the group and SIGKILL to what is left after 3 seconds,
  they return as soon as the group is gone. A cockpit start ends groups an earlier cockpit left, so a plugin
  never runs twice.
* Requests reach the plugin with the path below `/plugins/<id>` and `X-Forwarded-Prefix`. The cockpit checks
  session and CSRF token and strips `Cookie`, `Authorization`, `X-CSRF-Token` and the `csrf_token` form
  field. `Set-Cookie` in a response is dropped. A response must start within 30 seconds.
* Its output is the log on the Plugins page, the last 200 lines.

#### Callback API

HTTP on `DEV_COCKPIT_SOCKET` with `Authorization: Bearer <DEV_COCKPIT_TOKEN>`, JSON in and out, errors as
`{"error": "..."}`:

* `GET /v1/plugin`: `id`, `base`, `state_dir`, `plugin_state_dir`, `projects_dir`.
* `POST /v1/projects` with `{"name": "..."}`: creates the project like the projects page, `201` with `name`
  and `dir`, `409` when a project of that name has content. Every open page shows it.
* `POST /v1/projects/<name>/changed`: announces that the project's content changed, `204`.
* `POST /v1/assistants` with `{"name": "...", "message": "...", "coder_id": "..."}`, each optional: starts an
  assistant like the New of the assistants page, `201` with `{"assistant": {...}}`, its fields as in the
  list. The message is answered as if the user had sent it, a paid turn that nobody approves: a plugin is
  trusted as installed. When the assistant was made but its first message could not go out, `error` stands
  beside `assistant`.
* `GET /v1/assistants`, optional `?contains=<word>` for the name or a message: `{"assistants": [...]}` in the
  list's order, each with `id`, `name`, `named`, `coder_id`, `model`, `check_model`, `trigger_model`,
  `created_at`, `last_message_at`, `message_count`, `preview`, `running`, `unfinished` and `url`.
* `GET /v1/assistants/<id>/last-message`: the newest message with `id`, `role` (`user`, `assistant` or
  `cockpit`), `text`, `headline` (a note's line), `time`, `state` (`complete`, `streaming`, `queued`,
  `cancelled`, `failed` or `interrupted`), `error` and `model`. Without messages `id` is empty. `404` for an
  id no assistant carries, deleted ones included.
* `GET /v1/assistants/coders`: `{"coders": [...]}`, the coders `coder_id` takes in the order the assistants
  page offers them, each with `id`, `name` as the cockpit shows it and `default`, set on the one a create
  without `coder_id` runs on. That one follows the assistant opened last.
* The JSON names are the Go fields of `plugin.Assistants`' inputs and outputs, and fields only get added. A
  field or query parameter these calls do not know answers `400`, so a plugin notices a cockpit older than
  the field.
* `GET /v1/events`: server sent events of the sources in `events`, `job` (a steered job closed), `coder` (a
  coder ended its turn or asks) and `compose` (a compose run ended). One JSON object per `data:` line with
  `source`, `kind`, `target`, `time`, `headline`, `body` and `project`, a comment line every 15 seconds.
  Events are dropped while a stream lags behind.

An answer may leave out a field whose value is empty, so a plugin reads every field with a default.

#### Releases

Settings &rarr; Plugins installs a plugin from a public repository and keeps it current. The dialog asks for
the forge, GitHub or GitLab, and the address on any https host: github.com, GitHub Enterprise, gitlab.com or
a GitLab of its own with groups and subgroups. The cockpit guesses nothing from the host. It reads releases
from `api.github.com` for github.com, `/api/v3` on other GitHub hosts and `/api/v4` on GitLab hosts. Releases
follow the conventions of the cockpit's own:

* A semver tag, `v1.2.3` or `1.2.3`. Skipped like in the cockpit's own update: drafts and prereleases on
  GitHub, upcoming releases on GitLab, releases whose files are not uploaded yet.
* `<id>_<version>_<os>_<arch>.tar.gz` per platform, like `hello_1.2.3_linux_amd64.tar.gz`, or one
  `<id>_<version>_any.tar.gz` for a plugin without a binary. The platform archive wins.
* `<id>_<version>_checksums.txt` with a `<sha256>  <archive>` line per archive.
* The archive is a gzipped tar of the plugin directory, `plugin.json` at its top or in its single top
  directory. Only directories, files and relative links that stay inside, at most 5000 entries and 256 MiB.
  The manifest's `version` equals the tag.

The newest fitting release is downloaded, verified against the checksums, unpacked into staging and checked
like a load, then the page shows name, version, id, description and source and asks. A failing step refuses
it, nothing changes. Looking up an installed plugin's repository offers a newer release as an update and
refuses an older or equal one. The plugin is a link `<id> -> .versions/<id>/<release>`, a switch is a rename.

Updates are checked a minute after the start and every six hours, or on request. A row shows the step with the
notes of every release in between, one tap updates. A plugin on automatic updates updates on its own. The
previous release is kept: when the new one does not start or exits within 30 seconds, the cockpit rolls back,
starts the previous one and shows why, and never installs the failed release automatically again. A cockpit
restart during an install or update finishes or undoes it. Updates come only from the forge, host and path a
plugin was installed from, a plugin of the same id from another source asks first. The cockpit sends no token,
private repositories are not supported. Downloads use https only, follow at most five redirects and never reach
a link local address, hosts in private networks are fine.

`<state-dir>/external-plugins.json` records each plugin's origin. When it is lost, empty or broken, a start
deletes no release: every plugin keeps running the release its link points to, but shows as a folder plugin
without updates until it is installed from its repository again. The switches return to their defaults,
external plugins on and optional compiled ones off. A broken file is kept as `external-plugins.json.broken`.
