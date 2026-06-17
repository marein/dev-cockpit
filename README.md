# dev-cockpit

**Still work in progress.**

## Install

Download the archive for your platform from the [releases](https://github.com/marein/dev-cockpit/releases), then extract it:

```bash
tar -xzf dev-cockpit_*.tar.gz
```

On macOS the binary is unsigned. Clear the quarantine flag:

```bash
xattr -d com.apple.quarantine dev-cockpit
```

## Startup

All options can be seen with:

```bash
./dev-cockpit --help
```

A password hash for the option `--auth-password-hash` can be generated with `./dev-cockpit hash-password`.

dev-cockpit can be served the following:

### HTTP

```bash
./dev-cockpit serve --provider copilot --addr 0.0.0.0:3000
```

### Direct HTTPS

Create a certificate first. Update `localhost` and `127.0.0.1` in `CN` and `subjectAltName` when serving a different domain or IP address.

```bash
mkdir -p ~/.config/dev-cockpit/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout ~/.config/dev-cockpit/tls/dev-cockpit.key \
  -out ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

<details>
<summary>Start dev-cockpit with HTTPS</summary>

```bash
./dev-cockpit serve --provider copilot --addr 0.0.0.0:3000 \
  --tls-cert-file ~/.config/dev-cockpit/tls/dev-cockpit.crt \
  --tls-key-file ~/.config/dev-cockpit/tls/dev-cockpit.key
```

</details>

### HTTP via Load Balancer

Run dev-cockpit with HTTP and terminate TLS in a load balancer or reverse proxy.

```bash
./dev-cockpit serve --provider copilot --addr 127.0.0.1:3000
```
