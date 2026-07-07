# AGENTS.md

Read before changing anything. After every change: rebuild, restart both servers,
test. Update this file when a convention changes.

## Conventions

- **CLI flags:** never remove. Use `flags.MarkDeprecated` (or `MarkHidden`),
  ignore the value, keep parsing valid so crontab and start scripts still work.
- **No breaking changes** to behavior, URLs, cookies, config keys, start
  commands. If unavoidable, ask the user first, then build a move forward
  migration that keeps the old path working.
- **Hashed assets:** reference via the manifest, `{{ asset "/css/app.css" }}`,
  never the raw path. See `internal/web/static_assets.go`.
- **Forms:** POST action path must equal the GET path that renders it (pairs in
  `internal/web/router.go`, e.g. `/coders/new`). Backlinks, login redirect, and
  post then redirect depend on it. New form, add both routes on one path.
- **Coders:** one instance serves every coder whose CLI is installed
  (`--provider` is deprecated and ignored, kept parseable for existing start
  commands). Coder-scoped pages (agents, skills, instructions) select via
  `?coder=` query and a hidden `coder` form field, defaulting to the first
  active coder. UI stays adaptive: coder tabs, the new-coder coder select and
  the coder badges render only when more than one coder is active, so
  single-coder hosts look unchanged.
- **v2.0.0 markers:** legacy compatibility code that may be removed once
  breaking changes are allowed carries a `TODO(v2.0.0)` comment. Grep for it
  when preparing a 2.0.0 release.
- **Page headers:** one pattern everywhere: `page-header d-print-none mb-3`,
  inside it pretitle/breadcrumb plus `page-title`. Pages with a right side action
  wrap both in `d-flex align-items-center gap-2` with the title block as
  `flex-fill min-w-0` and the action as `flex-shrink-0`. No `row`/`col` in
  headers. Tabler's `.page-header` is a wrapping flex column, so style.css clamps
  every direct child (`min-width: 0; max-width: 100%`), otherwise long
  unbreakable names widen the layout. Page specific controls (for example the
  terminal font size and rows selects) belong to the content below, not into the
  header.

## Frontend

All browser behavior lives in custom elements and shared ES modules, no
free floating page scripts.

- **pe.js (progressive enhancement):** `internal/web/static/js/pe.js` boosts every
  link and form, swapping the `[data-page-content]` region, no full reloads, so the
  audio context survives and notification sounds stay consistent. It is a verbatim
  copy of https://github.com/marein/php-gaming-website; **do not edit it without
  asking.** `app.js` is the glue: loading bar, lazy custom element loader (by tag
  name via the import map, so pages carry no `<script>` tags), `pe:*` hooks,
  `data-confirm`, and a `dc-build` head check that forces one native reload after a
  redeploy. `data-no-pe` opts a link or form out into a native load (login, logout,
  downloads, JS owned forms). Framework scripts and toasts sit outside the swap and
  survive it.
- **Shared modules:** `internal/web/static/js/dc/` (toast, dialog, http, dom,
  store, repeater, fold, project-sort). Imported by bare specifier `@dc/<name>`.
- **Custom elements:** `internal/web/static/js/components/`, one element per
  file, registered with `customElements.define`. Each imports only from `@dc/*`,
  never from another component, so the import map stays flat.
- **Asset hashing for modules:** the import map in `layout.gohtml` head maps
  every `@dc/*` specifier, each custom element tag name, and the CodeMirror
  packages to their hashed URL via `{{asset}}`. Imports resolve through it, so
  module to module references stay hashed, and `app.js` lazy imports a custom
  element by its tag name. Never import a module by raw path, and add a tag to the
  import map when you add a component.
- **Element config:** pass data through attributes (e.g. `stream-url`,
  `input-url`), not window globals.
- **Lifecycle:** set up in connectedCallback behind a re-init guard, tear down
  everything in disconnectedCallback, nothing may outlive the element. Create one
  AbortController per element and pass its signal to every addEventListener, then
  abort it on disconnect. Also close any EventSource, disconnect observers, clear
  timers, and dispose xterm (`term.dispose`) and CodeMirror (`view.destroy`). The
  heavy islands (`terminal-attach`, `terminal-input`, `dc-editor`) run their setup
  in a function that returns a teardown the element stores and calls on disconnect.
- **CSRF:** the per session token is rendered once into `<meta name="csrf-token">`;
  `@dc/http` reads it and attaches the `X-CSRF-Token` header to every POST, so
  components never read or thread the token. Server rendered forms keep their
  hidden `csrf_token` field for plain and ajax form posts.

## Notifications

A notification means one thing: the coder or shell has news (turn finished,
question asked, permission wanted, shell command done). Events are
deliberately not classified further, a target holds at most one unread entry,
and follow-up signals within 30s of a fresh unread entry are swallowed.
Signals are coder-native, no pane-content parsing: claude sessions get
Stop/Notification hooks injected via `--settings`, copilot sessions ring BEL
through the CLI's global `beep` setting (enabled at startup when copilot is
active), which a read-only control-mode bell watcher per running session
picks up (`internal/coder/bellwatch.go`; it never resizes panes). Shells get
OSC 133 prompt marks injected via `PS0`/`PROMPT_COMMAND`
(`internal/shell/shellwatch.go`): every foreground command counts as news
when the prompt returns (bare prompt redraws stay silent), and a BEL in a
shell counts too (an rc file overwriting those variables silently turns the
marks off). The serve process also polls one inbox per coder
(`<state-dir>/notification-inbox/<coder>`), the generic ingestion seam: claude
hooks drop their JSON there, and the e2e suite injects events through it.
State persists to `<state-dir>/notifications.json` (one list like the recent
projects store) and fans out over SSE at `/notifications/stream`; a state dir
belongs to one serve process, a second process on the same dir would miss
live pushes. The `dc-notifications` element owns bell, badge, center, toasts,
and the title counter; the SSE channel is shared module state because the
element mounts once per header breakpoint. Opening an attach page marks that
target read. Entries always start unread; the dc-notifications client
reconciles on every SSE event (including the initial one after a reconnect)
and on visibilitychange: when the target's own page is open in a visible tab
(Page Visibility API) and that target is unread, it posts a target-level
read. The whole notification (badge, title counter, list dots, toast, jingle)
waits out a short grace period (750ms), held per target in the client, so a
read racing across tabs surfaces nothing at all; the read drops the held
target before it ever shows, and a hidden tab then lets it through so sound
reaches the user from background tabs. The
projects list and the quick nav mark coders and shells with unread news (blue
animated status dot on the row and on the project; blue is the notification
color everywhere, red stays reserved for errors); the marks render
server-side and stay fresh because the projects page renders per navigation
and the quick nav refetches on every open. On top of that, dc-notifications
updates opted-in DOM live over its SSE channel: `[data-notify-count]` badges
(the quick nav toggle), `[data-notify-target]` dots and
`[data-notify-project-dot]` (the projects page). A toast also plays a jingle
from `@dc/jingle` (composed for `@marein/js-scriptune`, loaded via the import
map from jsDelivr). Volume lives in scriptune's own localStorage
key (`scriptune-master-volume`, default 100%, 0 = off, per device); the
jingle selection is cross-device state in `<state-dir>/settings.json`
(`internal/settings`), rendered into the `dc-jingle` meta tag on every page
and edited on the settings page (`/settings/notifications`:
`dc-notify-volume`, `dc-jingle-picker`). Jingle ids in `handlers_settings.go`
and `@dc/jingle` must stay in sync.

## Build and run

dev-cockpit runs on the host, not in a container. Host-specific build, run, and
restart steps live in `AGENTS.local.md` (gitignored). If it is missing or a step
no longer matches, ask the user how they run the project and update it.

## Test

After a change, run the affected feature's runner and keep it in sync. The suite
is executable Playwright runners in `tests/e2e/`, run headless in Docker, not
curl (curl skips client JS, the SSE stream, and form flows). Setup, run commands,
the per-feature index, and conventions are in `tests/e2e/README.md`.
