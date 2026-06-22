# dev-cockpit

**Disclaimer**

This is a personal, internal productivity tool, and it is **100% vibe coded**. The
agent writes the code, tests every feature directly in the browser, and runs
integration tests. From time to time I do an architecture and security review,
but not on every change. **Run it only on machines and networks you trust.**

## What it is

Manage your projects from the browser, including your phone: run coding agents,
open shells, and edit files. Each project lives under one directory; for each one
you can start a CLI coding agent (GitHub Copilot CLI or Claude Code), open shell
sessions, and edit files in a small built-in editor. Agents and shells run in
tmux on the host, and the browser attaches to their terminals over a live stream.
The terminal UIs are colorful and fully usable from the browser.

You can reach the same sessions and shells from any device, so you can start on
your phone and continue on your laptop. Connections drop at times, on mobile and
over some VPNs in particular. When that happens the tmux session keeps running on
the host, and you continue by reopening the page.

This is the persistence tmux already gives you over SSH. dev-cockpit only puts a
web UI in front of it, so you can use it from any device with a browser, without
the need to install a terminal or an SSH client.

## What you can do

Create projects from the UI; git repos show their branch and remote.

In a project you can:

- Start agent sessions and attach to them in the browser.
- Resume earlier agent sessions from the provider's saved state.
- Open shell sessions, rename them, run several at once.
- Edit files in a minimal code editor (browse, create, rename, delete).
- Upload and download files.

Across the server you can also edit the provider's global config from the UI:
custom agents, skills, and the global instructions file.

One server instance serves one provider (`copilot` or `claude`). To use both, run
two instances on different ports.

## Requirements

- Linux or macOS.
- `tmux` on the host.
- The provider's CLI installed and logged in: `copilot` or `claude`.

The server checks for these on startup and refuses to start if they are missing.

The UI edits the provider's config under your home directory:

| Provider  | Instructions file                    | Agents dir          | Skills dir          |
|-----------|--------------------------------------|---------------------|---------------------|
| `copilot` | `~/.copilot/copilot-instructions.md` | `~/.copilot/agents` | `~/.copilot/skills` |
| `claude`  | `~/.claude/CLAUDE.md`                | `~/.claude/agents`  | `~/.claude/skills`  |

Only these two providers exist for now, but others can be added when needed.

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
| `--provider`     | `copilot`    | `copilot` or `claude`           |
| `--addr`         | `0.0.0.0:80` | listen address                  |
| `--projects-dir` | `~/projects` | root directory of your projects |

```bash
./dev-cockpit serve --provider copilot --addr 0.0.0.0:3000 --projects-dir ~/projects
```

The default `--addr` uses port 80, which needs root; the examples use 3000. Then
open the server address in your browser and log in.

### Login

The default login is `admin` / `password`. Change it before exposing the server.
Generate a bcrypt hash with `./dev-cockpit hash-password`, then pass it along with
a random cookie key:

```bash
./dev-cockpit serve --provider copilot --addr 0.0.0.0:3000 \
  --auth-user admin \
  --auth-password-hash '<hash>' \
  --session-cookie-key '<random-secret>'
```

### HTTPS

Serve TLS directly, or terminate it in a reverse proxy and serve plain HTTP.

Create a certificate (adjust `CN`/`subjectAltName` for a real domain or IP):

```bash
mkdir -p ~/.config/dev-cockpit/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout ~/.config/dev-cockpit/tls/dev-cockpit.key \
  -out ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

./dev-cockpit serve --provider copilot --addr 0.0.0.0:3000 \
  --tls-cert-file ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  --tls-key-file ~/.config/dev-cockpit/tls/dev-cockpit.key
```

Behind a reverse proxy that terminates TLS, drop the TLS flags, bind locally
(e.g. `--addr 127.0.0.1:3000`), and set `--trusted-proxies` to your proxy's
address.
