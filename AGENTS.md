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
- **The editor reads git, it never writes it.** `internal/git` is the only
  place that runs the binary, and every call goes through its one helper:
  `GIT_OPTIONAL_LOCKS=0` (a status read must never take the `index.lock` from a
  coder that is committing), `-c core.quotepath=false`, `-z` where git offers
  it, `--` before any path, no shell, `exec.CommandContext` with a timeout and
  a cap on the output. A directory without a repository answers "no repo" and
  never an error, and the editor then looks exactly like it did before it
  learned git. Staging, committing and discarding stay with a coder or the
  command line, so the whole surface has no destructive path. The editor's
  routes sit in the editor group (`/projects/:name/editor/git/...`); the
  cheap facts on the projects page keep coming from `internal/project`, which
  reads `.git` as files and starts no process. One route answers one round:
  the editor asks `.../git/changes` alone, which answers `repo` and the one
  list `worktree`, because a second status route beside it would run
  `git status` again at a second moment and the two answers could disagree
  about the same repository. A project below the repository root is the case
  to keep in mind on both sides of that answer: git reports every path
  relative to the repository root, so the status paths are cut back to the
  project (`withinPrefix`) **after** the line counts have been looked up, which
  are keyed the way git printed them. Cutting first and looking up after finds
  nothing, or the numbers of a same named file at the root. A rename whose
  source lies outside the project reports no source at all rather than one the
  tree cannot show. `POST .../git/watch` is what
  starts and stops the per-project poller: it runs only while a client says it
  is watching, compares a fingerprint and publishes a `git` event naming the
  project, which every open editor answers by pulling the status itself, like
  the terminals event. The fingerprint has two parts and the event says which
  moved: the base (the commit HEAD points at) and the working copy
  (a hash of the status output). Only a moved base can make an open comparison
  stale, so a save costs no revision request at all. A round that could not ask
  git at all is not a round that saw nothing: `Fingerprint` says so with a
  second return value, and the poller keeps the last answer instead of
  publishing a move that never happened. The poller reads the interval
  again before every round, so a changed setting reaches a running poller and
  zero stops it. A page that comes back to the front pulls everything itself:
  while it was away its watch lapsed, the poller ended and nothing was
  published.
- **Everything git cannot attribute is an answer, not a failure.** A repository
  without a first commit, a file git never heard of, a path that is not on the
  disk any more: `Blame` answers each of them empty with a 200, and the unborn
  case has to be asked first (`hasCommit`), because `ls-files` does list a
  staged file there and the other two checks would let the error through. The
  same for `FileAt`, which reads a file at HEAD with `cat-file blob` rather
  than `git show`: show prints a directory's listing as if it were content, so
  a path that is a directory in HEAD would come back as a file whose text is
  that listing, and the verification behind it peels to a blob (`^{blob}`) so
  such a path reads as "no file here" instead of a bad gateway. An empty
  `?path=` is refused by the handler with a 400: the project root resolves
  fine, so nothing further down would call it a missing parameter. What is left
  for a 502 is git actually failing.
- **A comparison of two files is a tab, not a file.** Both sides are real files,
  picked in two steps (`Select for compare`, then `Compare with`) in the context
  menu of a file, which the tree row and the tab both carry. `setCompare` builds
  a `MergeView` whose two editors are writable, and the bar above the surface
  names each side and carries its own Save; the ordinary save paths reach it
  too, Ctrl+S and Save all write whatever sides a comparison carries unsaved
  (`saveCompareTab`), because its tab path is synthetic and the file route could
  never write it. That path (`//compare/<enc left>/<enc right>`) starts with a
  double slash, which no project relative path does, and both halves are encoded
  so it stays usable in a selector. The tab persists as its two paths and is
  rebuilt from the disk on restore, carries no git mark, no preview and no git
  compare control, its menu keeps only the close entries, and a tab switch
  carries both documents on the tab and costs the two undo histories, the same
  limit `setDiff` runs into.
- **Blame belongs to the file.** The gutter is a per-file switch that rides on
  the tab (`tab.blameOn`), persisted with the tab state like the diff switch,
  never a server setting, never a key in the shared store and never a global
  toggle. It is reachable only from the file's own context menu, on its tab and
  on its tree row (`blameMenuItem`); a tree row whose file is not open opens it
  with the gutter on. It renders through a compartment so turning it on and off
  never rebuilds the document. `.../git/blame` answers the
  commits once and one index per line, so a few thousand lines cost a handful of
  entries; the gutter shows what git has, so a dirty buffer drops it until the
  save catches up rather than attributing moved lines to the wrong commits, and
  a file git has never seen answers empty, which the status line says instead of
  an empty gutter. The editor's cross-device settings live in the shared settings store
  under `editor-*` keys (`internal/web/editorsettings.go`); every default lives
  there, so an install with an empty store behaves like one that saved the
  defaults. They are edited on `/settings/editor/git`, one form behind a
  tab built like a coder's sections (shared frame in `editor_nav.gohtml`), so
  the page can grow more tabs later; `/settings/editor` redirects there. What
  belongs to the screen in front of you goes the other way and never reaches
  the server: tab width, indentation, font size, line wrapping **and how a
  comparison looks, the view and the folding of unchanged parts**, are one
  localStorage entry (`dc-editor-settings`), edited in the editor's own
  settings and applied live, `reapplyComparison` rebuilding what is open from
  the revision text or the two sides it already holds, so neither costs a
  request. The view reaches a diff alone, a comparison of two files is always
  side by side; the folding reaches both. What is left on the server is what
  describes the install: the poll interval and the two size limits, a house
  rule against a slow device. The rule for a new one is the question, not the
  mechanism: does it describe this repository and everybody looking at it, or
  this screen?
- **No route ever answers a diff.** `@codemirror/merge` computes it in the
  browser; the server only serves the file at HEAD
  (`.../editor/git/file?path=`), with the same binary and too large markers the
  plain read route uses. The diff is a mode of a normal file tab and never a
  tab of its own, because the working copy side **is** that file's buffer
  (`workView()` answers the merge view's right editor while one is up): save,
  the dirty marker, undo, search, go to line and the blame gutter all address
  it without knowing a diff exists, and the comparison therefore shows what you
  are typing, not what lies on the disk. A tab type would hold a second copy of
  the same file, and two writable copies of one file is a save clobbering the
  other. The tab carries the revision it is compared against (`tab.diffRev`,
  persisted as `diff: "<rev>"`), which is `DIFF_REV` and nothing else today: a
  picker later fills that same field and teaches the route a `rev`, so neither
  the tab model nor the stored shape changes for it. Where the working copy is
  on neither side, two revisions against each other, the compare tab's shape
  fits and this one does not.
  `filesystem.ResolveUnder` is what the git routes resolve a path with, and it
  answers about paths that are not on the disk at all: the symlink check walks
  up to the first existing ancestor (a file inside a deleted folder is a path
  the repository still has and the disk does not), while a path that walks out
  of the project is refused in its own step, which that walk-up used to do by
  accident. `repoPath` is not that guard, it clamps an upward path instead of
  refusing it; a caller that skips `ResolveUnder` would quietly ask about
  another file. Side by side is `MergeView`, inline is
  `unifiedMergeView`, which is why a switch between them rebuilds the view.
  **The buffer belongs to the person in front of it**: no switch, no revision
  change and no server event ever writes into it. A `git` event whose base
  moved makes the revision side follow on its own (`refreshDiffHead`), nothing
  is asked and the buffer is not touched. Building a comparison waits for two
  dynamic imports, and a tab switch inside that window would mount it over
  whatever is open now, which is why `setDiff`, `setOriginal` and `setCompare`
  all take a `valid` predicate and check it after the last await, before the
  first write to the surface; for the same reason the side by side view reads
  the document after those loads and not before, so what was typed while they
  ran is in it. The one thing a switch costs is the undo
  history of the side by side view, see the comment on `setDiff`. Hiding the
  plain editor while the side by side view is up must go through `visibility`:
  CodeMirror's base theme carries `display: flex !important` on `.cm-editor`,
  and an important declaration in a stylesheet beats a plain inline style, so
  `style.display = "none"` on an editor does nothing at all. The merge view
  also owns the scrolling of its two editors, so the host styles
  `.cm-mergeView` and never the editors inside it.
- **One editor on every width: the strip stays, the options fold into one
  menu.** A strip and seven icons do not share 390px, and two different
  headers are two things to learn, so the icons went into the kebab instead of
  the strip going away. Outside the menu the header carries only the folder
  toggle, the strip, `[data-editor-save]` (`hidden` unless the active file is
  dirty) and the menu itself; every other control is an entry of `[data-
  editor-menu-list]`, and the entries are the same at 390 and at 1440. The
  menu carries no git entry at all: the diff and blame switches are entries of
  the file's context menu (`diffMenuItem`/`blameMenuItem`, tab and tree row),
  because both are statements about one file. The folder toggle shows on both
  widths with the effect the width
  allows: below `md` it opens the drawer, above it folds the tree column and
  its splitter away (`.editor-tree-folded`, per device in `dc-editor-tree-
  folded`, the rule scoped to the widths that have a column so the class is
  inert on a phone). The bottom sheet `[data-editor-sheet]` serves the menus
  that need more than a dropdown on a phone: the editor settings live in the
  hidden store `[data-editor-panels]` and the sheet **borrows the very
  nodes** and puts them back on close, so there is one set of controls with
  one wiring and every
  `root.querySelectorAll` sync keeps working while they are adopted. The same
  sheet lists the open files (`Open files`): tap switches, the cross closes,
  the grip handle drags, which on touch is the only way to reorder them, and
  the order is the tab order through `persistTabs`, no route and no server
  state. A horizontal swipe on the surface steps through the open files
  (threshold, damping and abort from the terminal swipe), wrapping around at
  both ends like `stepTab` and the terminal swipe do, and only while
  `line_wrap` is on: with wrapping off the surface scrolls sideways and the
  gesture is the code's. Touch only, never with a selection, never in a
  comparison. It does what the terminal's `terminal-scroll-zone` does rather
  than listening harder: every pan is taken from the browser (`touch-action:
  pinch-zoom` while wrapping is on, set through `.editor-swipe-zone`, and it
  has to sit on `.cm-scroller` as well, because a pan reads the value from the
  hit element up to the element that scrolls), the axis is decided here, and
  the pointer is captured the moment it is. Leaving the vertical axis with the
  browser (`pan-y`) looks like less to build and is worse: the browser decides
  the axis at the first pixels and never revisits it, so a swipe with any
  downward drift became a page scroll and answered ours with `pointercancel`.
  The price is that scrolling the text is ours too, finger 1:1 plus a fling
  that decays; what a scroller cannot take chains on to the page. The zone
  class is therefore off wherever we do not want that job: with wrapping off,
  in a comparison, and while a selection stands (`syncSwipeZone`, called from
  `afterActiveChanged` and `onCursor`). The pill naming the target is one thing app wide,
  `.dc-swipe-pill`, shared with the terminal swipe and fixed near the top of
  the viewport; only the terminal adds the pulsing pending state, because only
  it waits for a navigation. A tree row is `draggable` on a fine pointer only:
  a row that carries it hands the long press to the browser's own drag lift,
  and iOS then never lets that press become the row's context menu, which is
  the one way to reach a file's actions with a finger.
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
  redeploy. **The head is never swapped, so anything the head carries goes stale
  in an open tab.** What has to survive that reads the answer instead: both the
  `dc-build` check and `syncJingle` take it out of the parsed document in the
  `parsed` hook, and the jingle one writes the fresh value onto the live
  `meta[name="dc-jingle"]`, so a jingle picked in the settings plays on the next
  notification without a reload. A value only qualifies when nothing caches it:
  `@dc/jingle` reads that meta on every play, while `@dc/http` caches the CSRF
  token in module scope, so copying that one would be a lie. A response without
  a head (a fragment) is left alone. It also fires a global `dc:navigated` event
  after every boosted navigation (in the `pe:*` succeed hook, so `location.hash`
  is already pushed); elements that must react to the final URL listen for it. `data-no-pe` opts a link or form out
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
- **`hidden` and a `d-*` display utility on one element:** style.css carries
  `[hidden] { display: none !important; }` so the attribute always wins. Tabler
  ships that same declaration from Bootstrap's reboot, but near the top of its
  file, while `.d-flex` and its siblings sit near the bottom; both are important
  and weigh the same, so source order decided and the utility won. An element
  that carried both was visible no matter what JavaScript set, which is how the
  editor's comparison bar stood on an empty editor with two nameless save
  buttons. style.css is loaded after tabler.min.css in `layout.gohtml`, that
  order is what makes the rule work. Two consequences for tests: an e2e check
  must read real visibility (`state: "hidden"`, a computed `display`, a zero
  box), never the attribute, and a check that something appears is only half of
  it, the half that disappears is where this hid.
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
