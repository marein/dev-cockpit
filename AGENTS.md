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

- **Shared modules:** `internal/web/static/js/dc/` (toast, dialog, http, dom,
  store, repeater, fold, project-sort). Imported by bare specifier `@dc/<name>`.
- **Custom elements:** `internal/web/static/js/components/`, one element per
  file, registered with `customElements.define`. Each imports only from `@dc/*`,
  never from another component, so the import map stays flat.
- **Asset hashing for modules:** the import map in `layout.gohtml` head maps
  every `@dc/*` specifier (and the CodeMirror packages) to its hashed URL via
  `{{asset}}`. Imports resolve through it, so module to module references stay
  hashed. Never import a module by raw path. Load an entry module with
  `<script type="module" src="{{asset "/js/components/x.js"}}">`.
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

## Build and run

dev-cockpit runs on the host, not in a container. Host-specific build, run, and
restart steps live in `AGENTS.local.md` (gitignored). If it is missing or a step
no longer matches, ask the user how they run the project and update it.

## Test

After a change, run the affected feature's runner and keep it in sync. The suite
is executable Playwright runners in `tests/e2e/`, run headless in Docker, not
curl (curl skips client JS, the SSE stream, and form flows). Setup, run commands,
the per-feature index, and conventions are in `tests/e2e/README.md`.
