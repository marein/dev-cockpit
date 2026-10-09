# AGENTS.md

Read this file before changing anything. After every change, rebuild,
restart and test. Update this file when a convention changes. It holds
general rules only. A decision about a single function that the code
cannot show stays at that function, within the comment rule below.

## Hard Rules

- **No breaking changes** to behavior, URLs, cookies, config keys, form
  fields, state files or start commands. If one is unavoidable, ask the
  user first, then build a forward migration that keeps the old path
  working. Mark such compatibility code with `TODO(v2.0.0)` and grep for
  it when preparing 2.0.0.
- **Migrations only for released states.** Compatibility code exists only
  for a shape that a released build wrote or handed out. Unreleased shapes
  change freely.
- **Moved addresses redirect.** A URL a released build handed out keeps
  answering after a surface moves, with a 308 to the new place, marked
  `TODO(v2.0.0)`. Entry points that resolve to a changing target (an area
  entry like `/editor`) answer 303 with `Cache-Control: no-store`.
- **CLI flags are never removed.** Use `flags.MarkDeprecated` or
  `MarkHidden`, ignore the value and keep parsing valid, so crontabs and
  start scripts still work. The internal `dev-cockpit assistant ...` group
  is exempt, its names, flags and output may change with any release. Its
  commands are named object first and verb last (`job-list`).
- **The update surface never expires.** The fields of the `/update/check`
  response only ever grow. The release conventions stay fixed: the feed
  shape, the asset `dev-cockpit_<version>_<os>_<arch>.tar.gz` holding a
  file named `dev-cockpit`, and `dev-cockpit_<version>_checksums.txt` with
  `<sha256>  <asset>` lines. The `TODO(v2.0.0)` marker never applies here.
- **Backup archives are a compat surface.** `internal/backup` maps
  `data/<section>/<source>` onto host paths. Never rename or reuse a
  section id or source name, only add new ones. A feature that adds
  persistent state asks the user whether and where it belongs in the
  backup. Never leave state out silently.
- **All JSON state goes through `internal/statefile`.** It reads through
  on every call, writes atomically via a tmp file plus rename, and
  quarantines a corrupt file as `<path>.broken`. Never hand roll load and
  save. Ids come from `statefile.NewID`. Anything that deletes based on a
  read must treat an empty read as "unknown", never as "nothing there".
- **One writer per state directory.** All state lives under one
  `--state-dir`, which belongs to one serve process. Other processes,
  including the assistant, never write state files, they call the server
  over the local API.
- **User visible behavior is documented.** A changed route, control, key,
  setting or workflow updates `/docs` (`render/docs.go`) in one change.
- **No source file grows beyond 800 lines.** Split it by responsibility
  before new code goes in. Some files are already above, they do not grow
  further and get split when touched.
- **No comments by default.** A comment is only allowed when it is
  important and explains why, never what the code does.
- **No dead code.** After a change, check what it left unused (functions,
  types, constants, CSS, templates, routes, tests of removed behavior)
  and delete it in the same change.

## Security

- The cockpit is single user, the session is the whole authorization. Every
  POST carries a CSRF token. `@dc/http` sends the `X-CSRF-Token` header
  from `<meta name="csrf-token">`, server forms carry a hidden `csrf_token`.
- The local unix socket (`internal/localapi`) is the credential for local
  processes. It lives in a directory only the owner can enter, there are
  no tokens. `--as <id>` names the calling assistant, it identifies but
  does not authorize. Routes that change security or host settings
  (approvals, compose actions, backup restore) refuse the local socket.
- Actions an assistant may only take with the user's approval go through
  `internal/approval`. An approved run resolves its target again by name,
  nothing captured at the time of the request is trusted.
- Never run a user string through a shell. Split arguments, pass argv,
  put `--` before paths, keep option shaped input out of option position.
- Every short lived external process runs with a timeout and an output
  cap (`exec.CommandContext`). Long running ones (tmux control mode,
  `detach`, coder servers) belong to the package that starts them.
  Background git never prompts.
- Paths from a request resolve under their root via `internal/filesystem`
  (`ResolveUnder`), including symlinks. Nothing reads outside an
  allowlisted root.
- Secrets (webhook URLs, keys, passphrases) are stored 0600 outside the
  shared settings store, or never stored and never logged.
- HTTP to a user supplied URL (webhooks, Web Push, the Ollama host) never follows
  redirects and refuses link local targets (`internal/netguard`).
- Containers the cockpit runs are built locally from embedded build
  files and never pulled prebuilt. They run as the cockpit's own uid and
  carry a label with the owning instance. Sweeps only touch resources
  that carry this instance's label.

## Code Layout

The cockpit is a single Go binary. `cmd/dev-cockpit` is a thin main,
`distro` is the importable facade (a custom distribution calls
`distro.Main`), `plugin` is the experimental plugin surface and
`internal/pluginhost` is its host side, used by `cli` and `web`.
Everything else is in `internal/`.
The layers are listed top down, a layer imports only the layers below it.

1. **`internal/cli`**: The cobra commands and the wiring. `runServe`
   builds every service and store, connects callbacks between them and
   hands them to the web server. Only `distro` imports it.
2. **`internal/web`**: HTTP routes, handlers, SSE, templates and static
   assets. Only `cli` imports it. Handlers parse the request, call a
   service and render, with no business rules and no file IO of state.
   `internal/web/render` holds the view models and templates. Every
   route is registered in `router.go`. A few small stores still live in
   `web` (commit drafts, line comments, search drafts), new state goes
   into a service.
3. **Domain Services**: One package per subject, `assistant`, `approval`,
   `coder` (one subpackage per coder CLI: `claude`, `copilot`,
   `opencode`), `shell`, `terminal`, `project`, `docker`, `git`,
   `editorintelligence`, `voice`, `backup`, `notify`, `push`, `restore`,
   `update`, `askpass`, `activity`, `hostinfo`, `terminalstate` and `cost`
   (one ledger for every coder CLI, one source per CLI under it, `claude`,
   and the list prices by provider and model in `cost/price`).
4. **Infrastructure**: `statefile`, `settings`, `recent`, `eventbus`,
   `localapi`, `detach`, `tmux`, `proctree`, `clirun`, `filesystem`, `netguard`,
   `gitfacts`, `markdown`, `keys` and `config`. It imports no service.

### Boundaries

- A service owns its state files and its locks. Nobody else reads them.
- A service knows no HTTP and no templates. It returns data and typed
  errors, the web layer words them.
- Services do not import each other sideways without need. Cross service
  reactions are wired in `runServe` as callbacks or interfaces.
- A generic package never names a caller's subject. `detach`, `approval`,
  `eventbus` and `statefile` know nothing about assistants or docker.
- Coder specifics live in the coder's own package, behind interfaces in
  `internal/coder`. Optional capabilities are separate interfaces,
  checked by type assertion. Do not add a `coder == "claude"` check
  outside it.
- Every question has one place that answers it. Before writing a helper,
  search for the existing one (paths, names, search matching, model
  names, time parsing). Two implementations of one rule drift apart.
- `internal/git` is the only package that runs the git binary, and all
  calls go through its single helper. `gitfacts` reads `.git` files and
  starts no process. The same applies to tmux (`internal/tmux`).
- `internal/docker` holds the one daemon connection and an event driven
  cache that every docker surface reads. `editorintelligence` and
  `voice` build and run their own containers through the docker CLI.
- A plugin declares, the cockpit applies. What a plugin contributes to the
  state directory or the settings goes through an Add on its `plugin.Serve`
  and is written by `internal/pluginhost` at the moment the target is ready,
  never by the plugin into a path it would have to know. Only its own
  `PluginStateDir` belongs to the plugin, it writes there itself.
- Work that must outlive the server runs through `internal/detach`.
- Texts written for a model are `text/template` files in
  `internal/assistant/templates`, fed with typed data.

## Frontend

There is no build step and no bundler. Pages are server rendered Go
templates plus ES modules.

- **Page Shell**: Every app page stands in one grid (`.dc-app` in
  `layout.gohtml`). It consists of the rail of areas, an optional list
  column (`ctx_start`/`ctx_body`/`ctx_end`), the work surface
  (`work_start`, `work_body` or `work_body_fill`) and the status line
  (`app_start`, `app_end`). Below `lg`, the rail becomes a bottom tab bar
  and the list column becomes a sheet loaded from `/ctx/<area>`. The work
  body scrolls, the page never does.
- **Templates**: They live in `internal/web/render/templates/*.gohtml`,
  view models in `internal/web/render`. One template serves every
  place a thing is shown (page, dialog, fragment, sheet). Do not render
  the same markup twice in JS.
- **Navigation**: `static/js/pe.js` boosts links and forms and swaps
  `[data-page-content]` without full reloads. It is copied from
  php-gaming-website, change it only with the user's permission, keep
  edits minimal and in its style. `components/app.js` is the glue. It
  holds the loading bar, the lazy element loader, `data-confirm`, a
  build check that reloads after a deploy and the global `dc:navigated`
  event. The head is never swapped, so head
  values go stale in an open tab. `data-no-pe` opts out.
- **Forms**: The POST path equals the GET path that renders the form
  (pairs in `router.go`). Create forms open in `dc-form-modal` from the
  same GET with `modal=1` and stay reachable as pages.
- **Custom Elements**: One per file in `static/js/components/`. Shared
  code lives in `static/js/dc/` and is imported as `@dc/<name>` (toast,
  dialog, contextmenu, http, dom, events, ctx, store, filter, rowdrag,
  swipe, theme, etc.). A component imports `@dc/*`, from other
  components only the exported helpers of `dc-form-modal` and `dc-time`.
  Behavior lives in elements and modules. There are no free page scripts
  and no window globals besides `window.pe` and `window.app`, config
  travels in attributes.
- **Shared Modules**: There is one `escapeHtml` (`@dc/dom`), one context
  menu with row menus and long press (`@dc/contextmenu`), one row drag
  (`@dc/rowdrag`), one swipe (`@dc/swipe`), one token match (`@dc/filter`),
  one popup door (`@dc/dialog`, SweetAlert for dialogs only) and toasts via
  `@dc/toast` (Bootstrap toasts). New surfaces reuse these, never copy them.
- **Lifecycle**: Set up in `connectedCallback` behind a re-init guard,
  tear down everything in `disconnectedCallback`. Use one `AbortController`
  per element for all listeners, close streams, disconnect observers,
  clear timers, dispose xterm and CodeMirror. Nothing outlives the element.
- **Assets**: Reference static files through the manifest,
  `{{ asset "/css/style.css" }}`, never by raw path
  (`internal/web/static_assets.go`). The import map in `layout.gohtml`
  maps every `@dc/*` module, every component tag and the CodeMirror
  packages to hashed URLs. The element loader imports an undefined tag
  by its name, so add the tag there when adding a component. Never
  import by raw path. Third party code (Tabler 1.5.1, Bootstrap JS,
  SweetAlert, CodeMirror, xterm) comes from jsDelivr, nothing is vendored.
- **Styling**: Tabler and Bootstrap utilities first, custom CSS only where
  they cannot do it. Colors go through `--tblr-*` variables, work in both
  themes and never use hardcoded palette values. `[hidden]` always wins
  over a `d-*` utility. `@dc/theme` owns the scheme, listen to `dc:theme`
  and ask `isDark()`, never read `prefers-color-scheme` directly.
- **Live Updates**: There is one SSE stream, `/events` (`internal/eventbus`,
  client `@dc/events`, `onServerEvent`). Events are small signals that
  name what moved (`terminals`, `projects`, `docker`, `git`, etc.), never
  content. Each client pulls its own fragment or JSON and swaps it. It
  coalesces bursts behind one request in flight and keeps scroll, focus
  and unfolded state. On every connect, the server sends a snapshot of
  all signals so a reconnect catches up. A ping arrives every 15s, the
  client reconnects after 45s of silence. A pull that answers after the
  page moved on is dropped.
- **Pointer and Touch**: Decide by input capability (`pointer: fine`,
  `any-pointer`), not by width. Autofocus only on a fine pointer. Keyboard
  opened menus mark their first row, pointer opened menus mark nothing.

## Domain Model

- **Coders and Shells**: They are tmux sessions (`internal/tmux`). One
  instance serves every installed coder CLI. A coder's settings pages live
  under `/settings/coders/<coder>/...`. Terminal pages attach via
  `terminal-attach`/`terminal-input` islands, several per page in a split.
  Session metadata (tab order, groups, columns) lives in tmux user options.
- **Editor**: One large component (`editor.js`) over the routes in the
  editor group (`/projects/:name/editor/...`). It reads git and writes it
  only through a short list of explicit actions (commit, push, pull,
  fetch, checkout, branch, tag, clone, revert). There is no staging,
  stash or merge UI. A save carries the version it was loaded from and
  never overwrites a file that changed on disk. Diffs are computed in the
  browser. Per screen settings live in localStorage, per install settings
  in the settings store (`editor-*` keys).
- **Assistants**: `internal/assistant` holds several independent
  conversations, each with its own directory under
  `assistant/instances/<id>/` and a shared memory. A turn runs the coder
  CLI in the assistant's workspace and acts only through the cockpit's
  own commands over the local API (the `./cockpit` wrapper). Jobs,
  triggers and approvals belong to one assistant.
- **Code Navigation and Voice**: `internal/editorintelligence` and
  `internal/voice` run fixed server profiles in Docker containers that
  the cockpit builds and owns, with idle timeouts and caches under the
  state directory.

## Notifications

- There is one meaning, a target (coder, shell, job, run) has news.
  `internal/notify` has no finer event kinds and classifies nothing. A
  target holds at most one unread entry, follow ups within 30s are
  swallowed unless the resolver marks them urgent.
- Signals are coder native, never parsed from pane content: claude hooks,
  copilot's bell, an opencode plugin, OSC 133 marks for shells. Everything
  enters through the notify inbox under the state directory.
- The title says what happened in one fixed sentence per kind, at most
  32 runes. The detail line names the target and carries any text. All
  wording lives next to `notifyResolver` in `internal/cli`.
- Opening a target's page marks it read. The client holds new news for a
  short grace period, so a read in another tab can cancel it.
- Blue marks news, red is for errors only (`news_dot.gohtml`).
- `internal/push` forwards unread news off the page (Web Push, webhooks)
  after a short re-check.

## Build/Run/Test

- The cockpit runs on the host, not in a container. Host specific build,
  run and restart steps are in `AGENTS.local.md` (gitignored). If it is
  missing or wrong, ask the user and update it.
- After a change, run `gofmt`, `go vet` and `go test` for the touched
  packages, then build, restart the instance and check its health, then
  run the e2e runner of the feature. Restart before any browser test.
- e2e tests are executable Playwright runners in `tests/e2e/`, one per
  feature, run headless in Docker (`dc-e2e` image, `--network host`).
  Setup, commands, the per feature index and conventions are in
  `tests/e2e/README.md`. Do not use curl, it skips client JS, SSE, forms.
- Runners drive throwaway instances with their own state directory,
  project and tmux server. Never attach to or act on a session a runner
  did not create, scope destructive selectors to the runner's own project.
- A check reads real visibility (computed style, box), never the
  `hidden` attribute alone. Check that things disappear, not only that
  they appear. Keep a runner in sync with the feature it covers.
