# dev-cockpit

**Disclaimer**

This is a personal, internal productivity tool, and it is **100% vibe coded**. The
agent writes the code, tests every feature directly in the browser, and runs
integration tests. From time to time I do an architecture and security review,
but not on every change. **Run it only on machines and networks you trust.**

## What it is

Manage your projects from the browser, including your phone: run CLI coding
agents (GitHub Copilot CLI, Claude Code, or both), open shells, and edit files
in a small built-in editor. Everything runs in tmux on the host, the browser
attaches over a live stream, so sessions survive dropped connections and you
can start on your phone and continue on your laptop. It is the persistence
tmux already gives you over SSH, with a web UI in front of it.

- Create projects from the UI; git repos show their branch and remote.
- Start coder sessions, attach in the browser, resume earlier ones.
- Open shell sessions, rename them, run several at once.
- Edit files (browse, create, rename, delete), upload and download.
- Edit each coder's global config: instructions, custom agents, skills.

One server instance serves every coder whose CLI is installed on the host.

## Requirements

- Linux or macOS.
- `tmux` on the host.
- At least one coder CLI installed and logged in: `copilot` or `claude`.

The server refuses to start without tmux or without any coder CLI; a coder
whose CLI is missing is skipped. The UI edits each coder's config under your
home directory:

| Coder     | Instructions file                    | Agents dir          | Skills dir          |
|-----------|--------------------------------------|---------------------|---------------------|
| `copilot` | `~/.copilot/copilot-instructions.md` | `~/.copilot/agents` | `~/.copilot/skills` |
| `claude`  | `~/.claude/CLAUDE.md`                | `~/.claude/agents`  | `~/.claude/skills`  |

## Install

### Quick install (curl)

This resolves the latest release, downloads the archive for your platform,
extracts the `dev-cockpit` binary into `~/.local/bin`, and makes it executable.
To pin a version, replace the first line with `VERSION=1.6.0`.

`~/.local/bin` is user-writable, so the in-app self-update can replace the binary
in place without `sudo`. A root-owned path like `/usr/local/bin` works for
self-update only if dev-cockpit runs as root.

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/marein/dev-cockpit/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac

mkdir -p ~/.local/bin
curl -fsSL "https://github.com/marein/dev-cockpit/releases/download/${VERSION}/dev-cockpit_${VERSION}_${os}_${arch}.tar.gz" \
  | tar -xzf - -C ~/.local/bin dev-cockpit
chmod +x ~/.local/bin/dev-cockpit
```

Make sure `~/.local/bin` is on your `PATH` so you can run it from anywhere; add
this to your shell's rc file (`~/.bashrc`, `~/.zshrc`, …) if needed:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

On macOS, the binary is unsigned. If Gatekeeper blocks it, clear the quarantine
flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/dev-cockpit
```

### Manual

Download the archive for your platform from the
[releases](https://github.com/marein/dev-cockpit/releases) and extract it:

```bash
tar -xzf dev-cockpit_*.tar.gz
```

Move the `dev-cockpit` binary into a directory on your `PATH` so you can run it
from anywhere. Use a user-writable path like `~/.local/bin` if you want the
in-app self-update to work; `/usr/local/bin` needs `sudo` to install, and
self-update there only works when dev-cockpit runs as root.

```bash
mkdir -p ~/.local/bin && mv dev-cockpit ~/.local/bin/
```

## Run

See all options with `./dev-cockpit serve --help`. The main ones:

| Flag             | Default      | Meaning                         |
|------------------|--------------|---------------------------------|
| `--addr`         | `0.0.0.0:80` | listen address                  |
| `--projects-dir` | `~/projects` | root directory of your projects |

```bash
./dev-cockpit serve --addr 0.0.0.0:3000 --projects-dir ~/projects
```

The default `--addr` uses port 80, which needs root; the examples use 3000.
Then open the server address in your browser and log in.

### Login

The default login is `admin` / `password`. Change it before exposing the
server. Generate a bcrypt hash with `./dev-cockpit hash-password`, then pass
it along with a random cookie key:

```bash
./dev-cockpit serve --addr 0.0.0.0:3000 \
  --auth-user admin \
  --auth-password-hash '<hash>' \
  --session-cookie-key '<random-secret>'
```

### HTTPS

Serve TLS directly, or terminate it in a reverse proxy: drop the TLS flags,
bind locally (e.g. `--addr 127.0.0.1:3000`), and set `--trusted-proxies` to
your proxy's address.

<details>
<summary>Self-signed certificate and TLS flags</summary>

Adjust `CN`/`subjectAltName` for a real domain or IP:

```bash
mkdir -p ~/.config/dev-cockpit/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout ~/.config/dev-cockpit/tls/dev-cockpit.key \
  -out ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

./dev-cockpit serve --addr 0.0.0.0:3000 \
  --tls-cert-file ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  --tls-key-file ~/.config/dev-cockpit/tls/dev-cockpit.key
```

</details>

## Custom distributions

A distribution ships its own version, source link, and update feed. It is a
module of your own (`go mod init`, `go get github.com/marein/dev-cockpit`)
with a `main.go` (example below). An empty field keeps the default of a plain build.

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

`UpdateFeedFormat` is `github` or `gitlab`. The feed must follow this
repository's release conventions: semver tags,
`dev-cockpit_<version>_<os>_<arch>.tar.gz` containing `dev-cockpit`, plus
`dev-cockpit_<version>_checksums.txt`. `dev-cockpit --version` prints what a
binary was built with.

### Plugins

Plugins are highly experimental and not yet part of the stable contract,
examples will follow. A distribution adds them through the `ServePlugins`
field on `distro.Build`. See the
[plugin package](https://github.com/marein/dev-cockpit/tree/master/plugin)
for what a plugin can contribute.
