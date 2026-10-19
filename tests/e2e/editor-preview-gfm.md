# dev-cockpit

- [Overview](#overview)
- [Getting started](#getting-started)
- [Security](#security)
- [Über uns](#über-uns)

## Overview

One Go binary that serves coder CLIs, shells and an editor in the browser.
Read [the guide](gfm-docs/guide%20one.md#install), browse [the docs folder](/gfm-docs) or try [a way out](../outside.md).

![a pixel](gfm-docs/pixel.png)

## Getting started

<details>
  <summary>Start the server</summary>

  | Flag             | Meaning                       |
  |------------------|-------------------------------|
  | `--state-dir`    | Where every state file lives  |
  | `--projects-dir` | The root holding the projects |

  <details>
    <summary>Build from source</summary>

  ```bash
  go build ./cmd/dev-cockpit
  ```
  </details>
</details>

## Security

> [!WARNING]
> The cockpit is single user, the session is the whole authorization.

## Über uns

Wir bauen das Cockpit.
