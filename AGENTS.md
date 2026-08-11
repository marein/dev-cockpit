# AGENTS.md

Read before changing anything. After every change: rebuild, restart both servers,
test. Update this file when a convention changes.

## Conventions

- **CLI flags:** never remove. Use `flags.MarkDeprecated` (or `MarkHidden`),
  ignore the value, keep parsing valid so crontab and start scripts still work.
- **No breaking changes** to behavior, URLs, cookies, config keys, start
  commands. If unavoidable, ask the user first, then build a move forward
  migration that keeps the old path working.
- **The update surface is the recovery path.** Two parts of it cross versions
  on every single update and therefore never expire, no removal markers of
  any kind, no matter which major release: the `/update/check` response
  fields (the post restart poll always hits the new server with the old
  page's JS, fields only ever grow), and the release artifact conventions an
  old binary needs to pull itself forward (feed shape, asset name
  `dev-cockpit_<version>_<os>_<arch>.tar.gz` containing a file named
  `dev-cockpit`, `dev-cockpit_<version>_checksums.txt` with
  `<sha256>  <asset>` lines). The empty `/update/apply` body (newest pending)
  only serves stale tabs from before the version pin and may be dropped at a
  major release, it carries a TODO(v2.0.0) marker.
- **Hashed assets:** reference via the manifest, `{{ asset "/css/app.css" }}`,
  never the raw path. See `internal/web/static_assets.go`. Static files that
  reference other assets by raw path (manifest.json, sw.js) get those
  references rewritten to the hashed URLs at build of the asset manifest.
- **State files:** every JSON state file goes through `internal/statefile`
  (read through on every call, atomic tmp+rename write, a corrupt file is
  quarantined as `<path>.broken` instead of being silently overwritten).
  Do not hand-roll load/save; entry ids come from `statefile.NewID`.
- **Passkeys:** `authenticated()` in `internal/web/auth.go` stays the one check,
  `session["user"] == cfg.AuthUsername`. A passkey only proves the identity and
  then writes that same line. Username and password come from the flags and stay
  reachable from the login page whatever is registered, so nothing can lock a
  host out of its own cockpit. The passkeys are one file, `<state-dir>/auth/`
  (`internal/auth`, mode 0600, read through like every state file), and the file
  is the whole switch, there is no enabled field and no unlocking step: a file
  placed by hand takes effect on the next request. It stays out of the backup on
  purpose, a passkey is bound to the host name it was registered under and is
  silent rubbish anywhere else. Registration sits behind `requireAuth`, so a new
  device always starts with username and password.
- **Forms:** POST action path must equal the GET path that renders it (pairs in
  `internal/web/router.go`, e.g. `/coders/new`). Backlinks, login redirect, and
  post then redirect depend on it. New form, add both routes on one path.
- **Coders:** one instance serves every coder whose CLI is installed
  (`--provider` is deprecated and ignored, kept parseable for existing start
  commands). A coder's pages are the settings of that coder, so they live
  under the settings at canonical URLs
  `/settings/coders/<coder>/{instructions,agents,skills}` (`coderBase`), their
  main nav tab is Settings, and the settings sidebar is where the coder is
  picked: `settings_nav.gohtml` replaces the single Coder row with a quiet
  Coder label plus one row per active coder, indented along a guide line
  (`border-start`) that ends where the group ends, so the entries below it do
  not read as part of it. It is fed by `render.SettingsNav` (built by
  `coderSettingsNav`, never by threading values through a template `dict`).
  The coder comes before the section on purpose, a coder may grow settings of
  its own: pick the coder in the sidebar, then its sections in the card
  header. Every coder row keeps the current section, so switching the coder
  stays on instructions, agents or skills. The pages share one layout
  (`coder_page_start`/`coder_page_end` in `coder_nav.gohtml`), the same shell
  the other settings pages use: page title Settings, sidebar in the left card
  column, section tabs in the right column's card header. Two older shapes
  308-redirect to the canonical URLs, both marked TODO(v2.0.0): the
  pre-settings `/coders/<coder>/...` (`redirectMovedCoderPath`) and the legacy
  top-level paths (`/instructions`, `/agents`, `/skills`, coder picked via
  `?coder=` or a hidden `coder` form field, `redirectLegacyCoderPath`). Both
  replay `coderPagePaths` in `router.go`, the one list of a coder's pages, so
  a page cannot move without its old links. Session identifiers are
  UUID-shaped, so the redirect subtrees cannot collide with the `/coders/:id`
  session routes. UI stays adaptive: the sidebar coder rows, the coder label
  in the browser title and the new-coder coder select render only when more
  than one coder is active, so single-coder hosts look unchanged. The coder
  icon badge on the attach and split pages always renders (like the shell
  badge), it doubles as the status light.
- **Claude session settings:** every claude session starts with one injected
  `--settings` blob (`internal/coder/claude/runtime.go`): theme auto, the
  notification hooks, and `disableAgentView`. The cockpit forwards keys via
  send-keys, tmux never swallows Ctrl+B as prefix, so without the flag an
  accidental Ctrl+B or a left arrow into the agent view turns the session
  into a background agent the cockpit can no longer resume.
- **v2.0.0 markers:** legacy compatibility code that may be removed once
  breaking changes are allowed carries a `TODO(v2.0.0)` comment. Grep for it
  when preparing a 2.0.0 release.
- **The assistant acts through the local API, never around it.** A state
  directory belongs to one serve process, so a turn writes nothing itself: it
  runs the cockpit's own commands, they reach the server over the unix socket in
  its state directory (`internal/localapi`), and the request lands in the same
  handler a browser hits. The socket is the whole credential, there is no token
  anywhere: it sits in a directory only the owner may enter, and `LocalHandler`
  marks what arrives on it, which is what the session check and the CSRF check
  read. Never add a second writer of the state files, and never let the assistant
  drive tmux directly.
- **`dev-cockpit assistant …` is internal surface.** Everything the assistant
  runs sits under that one command group, which shares the `--state-dir` and
  `--projects-dir` flags. Unlike the rest of the CLI it is exempt from the never
  remove rule above: its names, flags and output are tuned for the model and may
  change with any release, and its help text says so. The generated instructions
  build every call through `Workspace.CockpitCommand`, so a rename lands in one
  place. Names are object first and verb last (`coder-send-prompt`, `job-list`,
  `project-delete`), so the flat help list groups itself by object; `status` is
  the one exception, it is about the whole cockpit. `run-turn` is hidden: it is
  the mechanics a turn runs under, not something the assistant chooses to call,
  and it takes the provider's argv unparsed, so it has no usable help.
  What is not exempt is the group itself, `serve` and `hash-password`:
  those are what a person and a start script use.
- **One live conversation, and jobs belong to the assistant.** `Service.Current`
  is the live conversation and `Service.Open` starts one when there is none;
  nothing that acts carries a conversation id. A watched job outlives the
  conversation it was asked for and reports into whichever one is live when its
  check comes back, so a report is never written into a transcript the user has
  already left. The jobs live on one path, `/assistant/jobs`, which serves the
  list and takes both actions on it. A check runs in a provider session of its
  own, which is also why only a chat turn (`RunChat`) may write what a turn
  reports about the context window onto the conversation: a check's consumption
  is not the conversation's, and the ring on the new conversation button would
  otherwise show a stranger's number. In the panel a person reads coders, not
  jobs (the head, the button and the empty state say steered coders); code,
  routes, state values, the `dev-cockpit assistant` commands and the
  notification titles keep job.
- **Backup archives are a compat surface.** `internal/backup` maps archive
  paths `data/<section id>/<source name>` onto host paths through the current
  registry, and the manifest identifies the file (`app`, `format`). Old
  export files must keep importing: never rename or reuse existing section
  ids or source names, only add. Unknown sections render as unsupported on
  the import page, that is the forward path.
- **New features consider the backup.** Whenever a feature adds persistent
  state (a state file, a directory, host files the app manages), weigh it
  against the backup registry in `internal/backup` and ask the user whether
  it belongs into a backup, into which section, and with which dependencies.
  Never leave new state out silently.
- **New features consider documentation.** Whenever a feature adds, changes,
  or removes user-visible behavior (a route, control, gesture, keyboard
  shortcut, notification, setting, or workflow), weigh it against `/docs` and
  update the relevant documentation section as part of the feature. Never
  leave user-facing behavior undocumented silently.
- **Page headers:** one pattern everywhere: `page-header d-print-none mb-3`,
  inside it pretitle/breadcrumb plus `page-title`. Pages with a right side action
  wrap both in `d-flex align-items-center gap-2` with the title block as
  `flex-fill min-w-0` and the action as `flex-shrink-0`. No `row`/`col` in
  headers. Tabler's `.page-header` is a wrapping flex column, so style.css clamps
  every direct child (`min-width: 0; max-width: 100%`), otherwise long
  unbreakable names widen the layout. Page specific controls (for example the
  terminal font size and rows selects) belong to the content below, not into the
  header. On the terminal pages the header's destructive actions (stop a coder,
  delete a shell) render `dc-coarse-only`: on a desktop the tab strip owns them
  (close control and tab context menu), on touch the header is the direct way.
  The split page has no close-all in its header at all, that is the group tab's
  close control and the quick nav swipe.

## Frontend

All browser behavior lives in custom elements and shared ES modules, no
free floating page scripts.

- **pe.js (progressive enhancement):** `internal/web/static/js/pe.js` boosts every
  link and form, swapping the `[data-page-content]` region, no full reloads, so the
  audio context survives and notification sounds stay consistent. Based on
  https://github.com/marein/php-gaming-website with one local change: it applies a
  `Pe-Location` fragment to `scroll` and `pushState` (server sends `200` +
  `Pe-Location` with the anchor on a boosted redirect). Keep edits minimal and in its
  style; **do not restructure it without asking.** `app.js` is the glue: loading bar, lazy custom element loader (by tag
  name via the import map, so pages carry no `<script>` tags), `pe:*` hooks,
  `data-confirm`, and a `dc-build` head check that forces one native reload after a
  redeploy. It also fires a global `dc:navigated` event after every boosted
  navigation (in the `pe:*` succeed hook, so `location.hash` is already pushed);
  elements that must react to the final URL listen for it. `data-no-pe` opts a link or form out
  into a native load (login, logout, downloads, JS owned forms). Framework scripts
  and toasts sit outside the swap and survive it.
- **Shared modules:** `internal/web/static/js/dc/` (toast, dialog, contextmenu,
  http, dom, store, repeater, fold, project-sort). Imported by bare specifier
  `@dc/<name>`. `@dc/contextmenu` renders a body-mounted `.dc-context-menu`
  dropdown at a point, one open menu at a time (Escape/arrow keys, outside
  pointerdown, outside wheel/touchmove, `dc:navigated` and the caller's abort
  signal close it; programmatic scrolls must never close it). Row menus (right click plus touch
  long press) go through its `wireRowMenus(container, rowSelector, openFor)`,
  never a hand-rolled press timer. It runs three paths because no single one
  covers every device: `contextmenu` (the mouse, and browsers raising it on a
  long press; iOS Safari's carries no coordinates, so a row's rect is the anchor
  whenever `clientX`/`clientY` are 0, else the menu sits in the screen corner and
  reads as "not opening"), touch events, and pointer events. A press is
  cancelled only by the event family that OWNS it, and ownership needs one
  subtlety: `pointerdown` fires before `touchstart`, so the pointer arms the
  press first and the following `touchstart` claims it for the touch family
  (the timer reads the owning family at fire time). Over a row holding a link
  iOS hands the long press to its own gesture recognizer, which ends the
  pointer stream early and, with the callout suppressed, raises no
  `contextmenu`, so only a touch-owned press survives to open the menu.
  iOS also ignores `draggable="false"` on links and its drag lift ends the
  touch stream too, so the handler prevents `dragstart` on rows and
  `touchcancel` does not kill an armed press (a real scroll delivers
  touchmove past the movement threshold first).
  `preventDefault` on `touchend` is what stops the lift from following the
  link; when a cancelled stream delivers no touchend, the suppressed click
  does.
  A menu opened by a resting finger ignores that finger's wobble for a moment
  (`noteTouchOpen`), otherwise its own `touchmove` closes it at once. The editor
  tabs, the editor file tree and the projects page chips use it; the terminal
  tab strip has its own strip gesture and calls `openMenu` directly.
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
- **Terminal islands and split view:** `terminal-attach`/`terminal-input` are
  real multi-instance islands, paired per session via the `terminal-id`
  attribute. Islands dispatch their input events (`terminal-input`,
  `terminal-control`, `terminal-scroll`) on themselves with `bubbles: true`,
  never on `document`; a transport accepts an event when the origin island
  (`event.target.closest("terminal-attach")`) matches its id. The island
  touched last carries the `active` attribute (exactly one per page); events
  without an origin island (footer controls, prompt dialog, paste, direction
  pads) go to the active island's transport only. The split view page
  (`/splits/:id`) renders one island pair per group member; group membership
  lives in tmux user options (`@dc_tab_group`, `@dc_tab_gpos`,
  `@dc_tab_gname`), the strip folds members into one group tab, and the
  restore snapshot carries the group fields additively. The control footer is
  kind-specific and lives in shared partials (`terminal_footer_coder` /
  `terminal_footer_shell` in `terminal_footer.gohtml`), used by the single
  pages and rendered once per member on the split page
  (`[data-terminal-footer=<id>]`, only the active pane's footer shows).
  Grouped sessions live on the split page: their solo attach URLs
  303-redirect to `/splits/<gid>?focus=<id>`. The `terminal-split` element
  owns the pane headers (context menu, drag reorder via CSS `order` +
  re-POSTing `/terminal-tabs/group`). The group tab's close control closes
  every member (confirmed); ungrouping is the non-destructive context menu /
  header / pane-remove path. Decisions and endpoints: `docs/split-view.md`.
- **Terminal switcher app wide:** the attach pages render the tab strip inline
  and mark it via `Page.HasTabStrip`; every other authed page gets a hidden
  switcher-only `terminal-tabs` instance from the layout
  (`terminal_tabs_switcher.gohtml`, strip and plus menu only, data from
  `QuickNav.Strip`), so the double Ctrl/Meta switcher opens on any page. The
  hidden instance leaves direct Ctrl+Tab to the page (the editor binds it for
  its own tabs) and pulls the `/terminal-tabs` fragment lazily when the
  switcher opens instead of on every `terminals` event. The switcher is a
  quick-access palette: active terminals, inactive coders, an Editors section
  (one row per project, `ProjectNav.EditorURL`, fed by the hidden
  `[data-tabs-editors]` list in the plus menu) and a New section (New coder /
  New shell rows reusing the plus menu links, so the current project is
  preselected on the create form), all filterable.
- **Lifecycle:** set up in connectedCallback behind a re-init guard, tear down
  everything in disconnectedCallback, nothing may outlive the element. Create one
  AbortController per element and pass its signal to every addEventListener, then
  abort it on disconnect. Also close any EventSource, disconnect observers, clear
  timers, and dispose xterm (`term.dispose`) and CodeMirror (`view.destroy`). The
  heavy islands (`terminal-attach`, `terminal-input`, `dc-editor`) run their setup
  in a function that returns a teardown the element stores and calls on disconnect.
- **Theming:** the color theme follows the OS, no manual toggle.
  `layout.gohtml`'s inline head script sets `data-bs-theme` before first paint,
  `app.js` updates it live. Custom CSS must work in both themes: use `--tblr-*`
  variables (`rgba(var(--tblr-emphasis-color-rgb), …)` for hover/overlay tints),
  never hardcode palette colors. The terminal screen has its own palette, picked
  in the settings menu (`dc-terminal-theme` in localStorage, every scheme
  follows the OS between a light and dark variant), defined in
  `terminal-attach.js`. The tab strip follows the page theme, only the active
  tab keeps the dark frame via a `[data-bs-theme="dark"]` override. SweetAlert
  is themed in `style.css` by setting its `--swal2-background`/`--swal2-color`
  custom properties on `body` to Tabler variables, so open dialogs and toasts
  follow a live theme flip (never pass colors to `Swal.fire`). CodeMirror
  oneDark applies only while dark is active.
  The terminal colors ride every server contact — the `POST /terminal-theme`,
  the resize POST (`bg`/`fg` fields) and the stream connect (`bg`/`fg` query) all
  feed `updateTerminalTheme` (`internal/web/terminaltheme.go`) — so a reconnect
  or a resize on a differently themed device recovers on its own. The server
  mirrors the colors onto every session as the tmux pane style (tmux answers a
  program's OSC 11 background query from it; the control mode client never does)
  and sends claude the mode 2031 color scheme report so it switches live. The
  report only reaches interactive claude panes (foreground `claude` on the
  alternate screen; other programs would read it as keystrokes). New sessions
  get the pane style on create/resume so a fresh claude detects at startup, and
  claude sessions get `"theme": "auto"` pinned via the injected `--settings`
  (`internal/coder/claude/runtime.go`) so detection works despite a fixed theme
  in the user's global config.
- **CSRF:** the per session token is rendered once into `<meta name="csrf-token">`;
  `@dc/http` reads it and attaches the `X-CSRF-Token` header to every POST, so
  components never read or thread the token. Server rendered forms keep their
  hidden `csrf_token` field for plain and ajax form posts.

## Notifications

A notification means one thing: the coder or shell has news (turn finished,
question asked, permission wanted, shell command done). Events are
deliberately not classified further, a target holds at most one unread entry,
and follow-up signals within 30s of a fresh unread entry are swallowed.
News from a target somebody else is already looking at is written read from
the start (`Service.SetSilent`, set from the job store in `main.go` like
`SetSignal`, because notify classifies nothing): while the assistant steers a
job on a coder, its report is the message that reaches the user, so the raw
signal counts as no unread, marks nothing, carries no `Added` (no toast, no
jingle, no push) and only keeps the history complete. Such an entry replaces
the target's previous silent one and never touches an unread entry.
Every notification is written the same way, two lines: `Notification.Title`
says what happened ("Coder has news.", "Command finished.", "Job done." /
"blocked." / "expired.", "Assistant answered.", "Assistant could not
finish.", "Backup ready." / "failed."), `Notification.Detail` is the line
below it and names what it happened in, the name in quotes plus the project
(`"git" - dev-cockpit`), for the assistant the first words of the answer. It
is shown where the project stands (list, toast, push body). No name ever
stands in a title, and no title classifies a coder's signal. The wording of
every case lives together next to `notifyResolver` in `main.go`
(`coderNews`, `shellNews`, `backupNews`, `assistantNews`), never in notify,
which classifies nothing; a job report takes its name and project from the
message's own `WakeNote`, never from a lookup. An entry without a title (a
target the resolver could not resolve, an entry an older build stored) falls
back to `Something new in "..."` in the list, the toast, the push and
`dev-cockpit assistant notification-list`.
Signals are coder-native, no pane-content parsing: claude sessions get
Stop/Notification hooks injected via `--settings`, copilot sessions ring BEL
through the CLI's global `beep` setting (enabled at startup when copilot is
active), which a read-only control-mode bell watcher per running session
picks up (`internal/coder/bellwatch.go`; it never resizes panes). Shells get
OSC 133 prompt marks injected via `PS0`/`PROMPT_COMMAND`
(`internal/shell/shellwatch.go`): a foreground command counts as news when
the prompt returns and the command ran at least `minCommandDuration` (2s),
so quick commands and bare prompt redraws stay silent, and a BEL in a shell
counts regardless of duration (an rc file overwriting those variables
silently turns the marks off). The serve process also polls one inbox per coder
(`<state-dir>/notification-inbox/<coder>`), the generic ingestion seam: claude
hooks drop their JSON there, and the e2e suite injects events through it.
State persists to `<state-dir>/notifications.json` (one list like the recent
projects store) and fans out over SSE at `/events`, the app-wide server to
client event bus (`internal/eventbus`, client module `@dc/events`). Every frame
is a `{type,data}` envelope under the SSE event name `dc`, re-dispatched on
`document` as a `dc:<type>` CustomEvent (subscribe via `onServerEvent`). On
every connect the server sends a snapshot (unread state plus a bare `terminals`
signal), then a `ping` frame every 15s; the client forces a reconnect when the
stream stays silent past 45s (interval timer plus visibilitychange), because a
dead socket does not reliably fire an error. `Server.publishTerminals(project)`
emits a `terminals` event on every live coder/shell change (create, stop,
resume, delete, rename, reorder, project delete, out-of-band end); an empty
project means "refresh everything". Surfaces react by pulling their own
fragment (per client, so path, CSRF and element state like unfold or filter
stay correct), coalesce bursts behind one in-flight fetch, and show a
`.dc-loading-bar` (zero-height sticky first child: no layout shift, the line
stays pinned to the visible top). The tab strip skips the pull while hidden
(coarse pointer, mobile navigates via the quick nav) or during a close/drag and
flushes after; its refresh keeps the + menu and switcher current.
`dc-project-list` swaps only the named project's
`[data-sessions-body]` chip list and re-folds it; the unfold flag lives on
that container, which stays in the DOM across swaps. The shell attach header (`dc-inline-rename`) re-pulls
`GET /shells/:id/name` into heading and page title. A state dir belongs to one
serve process, a second process on the same dir would miss live pushes. The
`dc-notifications` element owns bell, badge, center, toasts, and the title
counter; unread state is module scope because the element mounts once per
header breakpoint, while `@dc/events` owns the one connection. Opening an attach page marks that
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

Push channels forward the same news off the page. `internal/push` subscribes
to the notifier fan-out, waits 2s, and re-checks that the target is still
unread before sending, so news auto-read on a visibly open page never rings a
phone. Channels: Web Push to registered devices (VAPID keys in
`<state-dir>/push-vapid.json`, generated once, rotating them invalidates every
subscription; devices in `<state-dir>/push-subscriptions.json`; subscriptions
the push service reports gone prune themselves) and registered webhooks
(several, each notification POSTs one JSON payload with text, title, body,
and url; the text field makes Slack incoming webhooks work as is).
Per-channel configuration lives in `<state-dir>/push-channels.json`, one
key per channel (the webhooks list, the web push subscriber contact for the
VAPID sub claim, empty means the built-in default), so a new channel adds a
key instead of scattering flat settings. A top-level `baseUrl` (settings
page form) holds the public address of the cockpit; channels that leave the
app use it to absolutize the notification link (the webhook payload url and
a trailing link line in its text), empty keeps app relative paths, and web
push always stays relative because the service worker resolves against its
own origin. Webhook URLs are bearer
credentials, so every push state file is written 0600 and channel config
stays out of the world-readable settings store; `settings.json` keeps only
real preferences like the jingle. All outbound push traffic (web push and
webhooks) shares one HTTP client with a 10s timeout that never follows
redirects and refuses link local destinations at dial time; loopback and
LAN targets stay allowed on purpose, local webhook receivers are a normal
setup. Every subscription records the VAPID
public key it was created with: after a key change (the key file was lost
or damaged and got regenerated, which is logged; a transient read error
refuses startup instead of rotating the identity) the dead devices render
with an "Old keys" badge plus a warning alert on the settings page and are
skipped on delivery, the device cap counts live devices only, and the
enable flow replaces a stale browser subscription on its own (unsubscribe,
then retry), so a device recovers with one click. The service worker
`static/sw.js` renders the payload and must stay registered from the stable
un-hashed `/sw.js` path; it has no fetch handler on purpose, pe.js owns
navigation. The `dc-push-settings` element on `/settings/notifications` does
the browser side (permission, registration, PushManager subscribe via the JS
routes `/push/subscribe`, `/push/unsubscribe`, `/push/test`) and only marks
the server rendered device rows; on iPhone and iPad web push requires the app
installed to the home screen, per origin, so a test instance needs its own
install. The settings page now hosts several forms; they all POST to
`/settings/notifications` and dispatch on a hidden `form` field, keeping the
form path pairing rule intact.

## Build and run

dev-cockpit runs on the host, not in a container. Host-specific build, run, and
restart steps live in `AGENTS.local.md` (gitignored). If it is missing or a step
no longer matches, ask the user how they run the project and update it.

## Test

After a change, run the affected feature's runner and keep it in sync. The suite
is executable Playwright runners in `tests/e2e/`, run headless in Docker, not
curl (curl skips client JS, the SSE stream, and form flows). Setup, run commands,
the per-feature index, and conventions are in `tests/e2e/README.md`.
