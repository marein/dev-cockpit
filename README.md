# dev-cockpit

__Table of contents__

* [Overview](#overview): A brief introduction to the cockpit.
* [Features](#features): What the cockpit does, grouped by area.
* [Deployment Guide](#deployment-guide): Requirements, installation, running and updating.
* [Configuration](#configuration): Flags, settings and custom distributions.

## Overview

This is a web cockpit for a development machine, usable from a desktop browser and a phone.
It runs coders (Claude Code, GitHub Copilot CLI, OpenCode) and shells in tmux on the host,
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
  * **Code navigation**: Go to definition and find usages for Go, PHP, TypeScript and JavaScript, including
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
  <summary>Notifications</summary>

  ### Notifications

  * **Sources**: A coder finishes or asks, a long shell command ends, a compose run ends, git needs a passphrase,
    or an assistant answers or needs an approval.
  * **In the browser**: Bell, marks on terminals and projects, toasts and a jingle.
  * **Off the page**: Web Push to phones and desktops, and webhooks (Slack compatible).
</details>

<details>
  <summary>Costs</summary>

  ### Costs

  * **Scope**: Experimental, counts claude with Claude models only.
  * **Spend**: What coders and assistants spent, at API list price.
  * **Charts**: Stacked bar charts per project, assistant, coder and model over any period.
  * **Prices**: Price lists are refreshed daily from LiteLLM. Months to keep are set under Settings.
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
* At least one coder CLI installed and logged in: `claude`, `copilot` or `opencode`.
* Optional: `git` for the git features, Docker for compose stacks, code navigation and voice.

The server refuses to start without tmux or without any coder CLI. A coder whose CLI is missing is skipped.
The UI edits each coder's config under the home directory:

| Coder      | Instructions file                    | Agents directory           | Skills directory            |
|------------|--------------------------------------|----------------------------|-----------------------------|
| `claude`   | `~/.claude/CLAUDE.md`                | `~/.claude/agents`         | `~/.claude/skills`          |
| `copilot`  | `~/.copilot/copilot-instructions.md` | `~/.copilot/agents`        | `~/.copilot/skills`         |
| `opencode` | `~/.config/opencode/AGENTS.md`       | `~/.config/opencode/agent` | `~/.config/opencode/skills` |

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
