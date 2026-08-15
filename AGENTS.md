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
  the one exception, it is about the whole cockpit.
  What is not exempt is the group itself, `serve` and `hash-password`:
  those are what a person and a start script use.
- **Work that has to outlive the cockpit goes through `internal/detach`.** One
  package, no caller's subject in it: it starts a program in a session of its
  own (`Setsid`), writes its output into files instead of pipes, and takes an
  exclusive flock before anything starts that travels into the child as an
  inherited descriptor. Whether that file can be locked is whether the run is
  still going, so nothing trusts a process number, and `Alive`/`Kill` are what
  a later process asks with. The program never runs directly: it runs under a
  hold process, `dev-cockpit run-detached [--result <file>] [--timeout <d>] --
  <program> ...`, a copy of this binary that holds the lock, enforces the
  timeout (the server that asked may be gone long before it passes; it ends
  the run's whole process group, itself included, with the result already on
  disk, because the program's helpers inherit the lock and a survivor would
  keep the run reading as alive) and writes the exit code down, because the
  exit code of a process this server did not start is lost to it. That command
  is hidden and nobody's interface, and it
  takes everything behind the separator unparsed, which is also what lets a
  test binary stand in for it (`detach.HoldArgs`). Two features hang on it: an
  assistant turn (no timeout, no result, its parser diagnoses) and a compose
  run (timeout and result, one output file for both streams). A run without a
  result did not finish by its own decision, and that is not the same as a
  zero.
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
- **The editor reads git, and writes it through a deliberately short list of
  actions.** `internal/git` is the only place that runs the binary, and every
  call goes through its one helper:
  `GIT_OPTIONAL_LOCKS=0` (a status read must never take the `index.lock` from a
  coder that is committing), `-c core.quotepath=false`, `-z` where git offers
  it, `--` before any path, no shell, `exec.CommandContext` with a timeout and
  a cap on the output, and `GIT_ALLOW_PROTOCOL` as an own whitelist, because
  `ext::` runs the command in the URL and is a scheme, not an option a `--`
  could disarm. A directory without a repository answers "no repo" and never
  an error; only `Changes` keeps "git could not be asked" apart from it, so
  one stalled git does not put the clone where the repository's actions
  were. The writes are `git.Commit`, `git.Push` (plain or force-with-lease,
  and `--set-upstream` where the branch has none, to the repository's single
  remote or to `origin` among several, because a branch created here would
  otherwise be refused with git's line about setting one; several remotes
  without an `origin` are a guess this does not make and stay git's refusal),
  `git.Fetch`, the fast forward `git.Pull`, `git.Checkout`,
  `git.CreateBranch`, `git.Tag` (a message makes it annotated, without one it
  stays lightweight, and a name that is taken is git's refusal: nothing here
  ever moves an existing tag) with the `git.PushTag` that sends **that one
  tag** to the same unambiguous remote, `git.DeleteTag` and the
  `git.DeleteRemoteTag` that is never implied by it, because what a remote
  holds is what everybody else sees, `git.Clone` and `git.Revert`, the one deliberate
  discard: one path back to HEAD, staged edits included, found by asking
  status itself (what HEAD knows goes through restore, what it does not is
  deleted through clean, a rename's source joins like it joins the commit),
  and a repository without a commit refuses instead of deleting somebody's
  only copy. Staging, stashing, merging
  and conflict resolution stay with a coder or the command line, and a
  refused write leaves the working copy as it was. Writes carry their own
  timeouts, minutes not seconds, and run on `gitWriteContext`
  (`context.WithoutCancel`): a closed tab or a dropped line must never
  SIGKILL a checkout mid working copy or leave half a clone git refuses to
  reuse, so the write's own deadline is the only thing that ends it. Ending
  means killing the whole process group, with `cmd.WaitDelay` bounding the
  wait for pipes a leftover ssh or pinentry still holds, and the error says
  which of deadline, cancellation or a process that never ran it was; those
  three carry `git.ErrNoAnswer`, an exit code never does, the distinction
  `Fingerprint` and `WorkingCopy` are built on. Every background call fails
  prompts in seconds instead: `GIT_TERMINAL_PROMPT=0` for git's own
  questions, `SSH_ASKPASS=/bin/false` plus `SSH_ASKPASS_REQUIRE=force` for
  ssh's — the askpass is pinned, never the ssh, so the host's wiring
  (`core.sshCommand`, `GIT_SSH_COMMAND`) and agent keys keep working. A
  user-triggered action instead carries the askpass bridge
  (`internal/askpass`), and a git question travels like this: the call's
  `SSH_ASKPASS` and `GIT_ASKPASS` point at a stub that execs this binary's
  hidden `askpass` command, which reports the prompt line over the broker's
  unix socket (the one-time token from its environment is the helper's
  whole credential) and blocks until the answer comes back. The parked
  question is server state, keyed by the project and carrying the action's
  name: it belongs to the cockpit and not to the page that started the
  action, which may be reloaded, updated away or lying on a desk while the
  phone answers, and it is bound to nothing with a lifecycle of its own,
  after two bindings (the CSRF token, then a lazily minted session value)
  each stood dialogs that never came while git waited blind. Every move of
  the standing questions publishes the bare `gitprompt` event
  (`Broker.OnChange`), the connect snapshot carries the same signal, and
  one app level pair serves every page alike: `GET /git/prompt` lists the
  standing questions oldest first, `POST /git/prompt` answers under project
  and question id; the session is the whole authorization, single user by
  design. The dialog is the global module `@dc/gitprompt` (imported by the
  notification bell: every app page, never login), a mirror of that server
  state: it shows the oldest question, never re-fires the one it already
  shows, so typing survives every signal, closes when the server no longer
  lists it, which is how an answer on one device takes it down on all, and
  replaces it when a new question follows, ssh asking again after a wrong
  passphrase. It names project and action as this server's truth above the
  escaped prompt line, which is ssh's, git's or a repository hook's and
  therefore capped (`maxPrompt`); the field is masked only when the line
  names a secret, because the same helper carries user names and host key
  confirmations, and masking those is answering blind. A rejected answer
  (answered elsewhere, action gone) is swallowed, the closing travels on
  the event; without SweetAlert the module shows nothing and denies
  nothing, an auto deny from one Swal-less page would cancel questions
  every other page could answer. The backstop is the breathing deadline
  (`git.Prompt`): a delivered question grants the person `promptWait`, an
  answer grants the action its full budget back, silence ends in the
  readable timeout sentence. Answers live in memory for one question, never
  logged, never stored; cancel ends the action in git's words plus `— the
  question was cancelled.` Which calls may ask is the route's decision
  alone, the request bodies carry nothing for it: push, pull, the explicit
  fetch, clone, checkout and the commit's ride-along push (opened before
  the commit, so a refusal refuses the whole request); a status poll or the
  quiet fetch never asks. `Begin` refusing a project that already runs an
  action is an invariant guard behind the write lock, not a surface. Two
  mechanics are not detail: the socket path comes from a **resolved** state
  directory (`filesystem.AbsDir`), because it travels into git processes
  whose working directory is the project, and the stub is rewritten at
  every start with the binary's path **shell quoted** (`helperScript`),
  because an update may move the binary onto a path a shell would take
  apart. Nothing of the bridge outlives the process, so `<state-dir>/ask/`
  is deliberately no backup section. The editor's
  routes sit in the editor group (`/projects/:name/editor/git/...`); the
  cheap facts on the projects page keep coming from `internal/project`, which
  reads `.git` as files and starts no process. One route answers one round:
  the editor asks `.../git/changes` alone, which answers `repo`, the branch
  (name, upstream, ahead and behind, out of the same status call's
  `--branch` headers, read from the leading block alone, `parseBranch`,
  because a rename's bare source record could fake them) and the one list
  `worktree`, because a second status route beside it would run `git
  status` again at a second moment and the two answers could disagree; the
  headers riding in the status output is also why a fetch from anywhere
  moves the fingerprint and reaches every open editor through the ordinary
  poll. A project below the repository root is the case
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
  published. A reconnected stream is the same case with the page in front:
  the snapshot carries a bare `git` signal, the editor answers it with the
  full catch-up, and a failed status round retries itself once
  (`gitRetryTimer`), because a move published into a gap or into a dead
  fetch is published never again.
- **A save writes onto the file it was loaded from, or it does not write.**
  The editor is one writer among several on the same working copy, a coder and
  git being the others, so the read route answers a version of the file
  (`filesystem.ReadFileText`) and the save carries it back. The comparison is
  the filesystem package's and never the handler's
  (`filesystem.WriteFileTextIfUnchanged`): a version that no longer describes
  the disk writes nothing at all. The token is a hash over the content and
  deliberately not mtime plus size, which would miss a same sized write inside
  one clock tick and invent a conflict for every `git checkout` that rewrites
  identical bytes; it is FNV-1a, fast and not cryptographic, because it says
  whether the file moved and authenticates nothing. The load path pays nothing
  for it, the save one read. A refused save answers a 409 whose `conflict`
  names which of the two happened, and the two are apart because their ways
  out are: `changed` offers to read the disk back over the buffer,
  `deleted` offers to write the buffer as a new file, since a deleted file has
  no state to reload and `WriteFileText` would silently put it back. There is
  no force save on any path and no flag that could be one: a save is written,
  or the buffer stands untouched, and `saveTab` answers `saved`, `reloaded` or
  `kept` so every caller can tell those apart, the commit's save-first
  included, which stops rather than commit a buffer that never landed. A save
  **without** a version is the create path and writes: a file created in the
  editor is saved before anything read it back. Every side of a comparison
  carries its own version, and every path that puts the disk into a tab goes
  through one place (`applyDiskContent`), so a reload can never leave the old
  token behind.
- **A commit takes the checked paths and nothing else.** `git.Commit` is the
  one call in `internal/git` that records a commit, one write out of the short
  list further up, and it has the editor's commit route pair to itself
  (`GET`/`POST /projects/:name/editor/git/commit`; the GET answers branch,
  hasCommit and the last message, which is what an amend starts from). It is a
  pathspec commit of exactly the picked paths: it records their working copy
  content and leaves what is staged for any other path staged and out of the
  commit, which is what lets it run beside a coder preparing a commit of its
  own. Two things have to travel along for the commit to mean what the panel
  showed, and the server finds both by asking status itself rather than
  trusting the client's list: an untracked path gets an intent-to-add entry
  first (taken back when the commit is refused), and the source of a rename
  joins the pathspec, or the commit would record a copy and keep the deletion
  pending. Every pathspec is built `:(top,literal)`: top so a rename source
  outside a subdirectory project stays addressable, literal so a name that
  looks like a glob stays a name. Amend rewrites the tip. What git refuses
  travels back in git's own words (a missing identity, a hook that said no,
  the partial-commit ban during a merge), because no wording of ours says it
  better; the write gets a longer timeout than a read, hooks and signers are
  programs of their own. A successful commit publishes the `git` event itself,
  base moved, so every open editor of the project follows at once instead of
  waiting for the poller's round. The panel is the tree column's second face
  (commit button above the tree, `Commit` in the editor menu, Ctrl+K, Escape
  from inside it goes back to the files): the same flat changes list with a
  checkbox per row, nothing starts picked, unchecked rows stay out,
  conflicted rows cannot be picked, a row click opens the file's diff, dirty
  picked buffers are saved before the commit like every save path, and an
  amend borrows the message field and gives the draft back on the way out.
  **The draft is server state, per project** (`editor-commit-drafts.json`,
  `GET`/`POST .../editor/git/commit-draft`, deliberately not in the backup:
  an unsent commit is typing, not configuration): message, picks and an
  amend in progress follow the assistant composer's pattern, one debounced
  save as the only write path, the `commitdraft` event only on movement, a
  pull never typing over unsaved local edits, a successful commit spending
  the draft on every device. The pre-1.43 localStorage draft is lifted onto
  an empty server draft once, TODO(v2.0.0). The status lists untracked files one by one
  (`--untracked-files=all`), never a collapsed folder line, so a single file
  of a new folder can be picked; the list opens grouped by
  folder (flat behind the device-local switch `dc-editor-commit-grouped`),
  folders first and files after them on every level: a folder becomes a row
  of its own as soon as it holds more than one thing, a chain of folders that
  only hands down to a single subfolder and has no files of its own merges
  into one row with the joined path as its label, a group's checkbox covers
  its whole subtree, and folders stay grouping and never the committed unit.
  An amend with nothing picked commits anyway and rewrites only the message
  (`--only --amend`, the everyday typo fix; `--only` is what keeps a coder's
  staged work out of it), while without the amend flag an empty pick stays
  refused. `Commit and push` behind the button's arrow is that same
  `git.Push` right after a successful commit (plain, no force; a longer
  timeout again because the network and a credential prompt with no terminal
  both end here), where it goes and whether it may is
  the repository's own configuration. A commit whose push is refused stands
  as a commit: the answer stays a 200 and carries `pushed` and `pushError`,
  the panel shows the refusal in git's words next to the success. The editor
  still never stages; what it discards is the explicit revert alone
  (`git/revert`, one path back to HEAD, no bridge because nothing on that
  path can ask): the entry sits in the file tree's context menu, on a file's
  tab and on the changes list's rows, only where the path carries a mark,
  and the confirmation is built from the status the page already holds, so
  the deletion of what has no state in HEAD, untracked files and staged
  additions, is said before anything runs, with the counts on a directory.
  After it the reverted tabs read the disk again, dirty buffers included
  because discarding them is what was asked, and a file the revert deleted
  closes its tab the way a delete does. The branch moves the editor makes
  are the next bullet's.
- **The branch lives in the statusbar, and the git sheet is where the
  repository acts** (the segment, `Git` in the editor menu, Ctrl+Shift+G).
  The sheet's actions are routes of their own: `git/push` (`force` is
  force-with-lease behind an explicit confirmation), `git/fetch`, `git/pull`
  (fast forward only), `git/checkout` and `git/branch` (name normalized
  client side, `normalizeBranchName`); a refusal travels as a 409 in git's
  words, and there is deliberately no stash, no merge and no conflict UI
  behind any of them. The file history and the revision picker fill
  `tab.diffRev`, the same field the HEAD switch fills, and `.../git/file`
  takes it as `?rev=` (verified server side, `git.ErrRevision` answers a
  400). Opening the sheet and listing branches go through `git.FetchIfStale`
  (`editorFetchMaxAge`; no remote means nothing to fetch, a state and not a
  failure). Checkout and pull publish the `git` event with the base moved,
  push, fetch and a created branch with it standing, **and that event is the
  only round those three cost**. After a checkout or pull the client reloads
  every clean tab (`reloadCleanTabs`; only the server's own 4xx closes a
  tab, a transport error must not take the open set away), and a dirty
  buffer is never touched by a branch move. `git/log` answers one page of
  history (`?skip=`, the file's with `?path=`), and every commit carries the
  tags pointing at it, read out of the same `log` call's ref names (`%D`,
  `parseTags`, branches and HEAD dropped: they say where the repository
  stands, a tag says what the commit is). **The history is built like the
  docker sheet's containers**: one cell per commit in a grid (`gitLogCell`,
  `row row-deck`, two per line from `lg` up and one below it, one grid for
  every page so a later one joins the lines that stand), the whole cell is
  the control, and a click on it opens **the app's menu** over it
  (`commitMenuItems` through `@dc/contextmenu`, the same menu a tab and a
  tree row open), anchored at the click or at the cell when the keyboard got
  there. No controls on the cell and no level of the sheet: controls left a
  phone's subject truncated with nowhere for a tag to go, and a drilled level
  costs a Back that re-renders the history and puts the reader at the top of
  it. The menu holds the diff, the hash and the three tag routes; a created
  or deleted tag repaints that cell's chips alone (`paintChips`). `git/tag` creates one, one write and one bridge like
  the commit's ride-along push: the tag is created, then pushed when the
  dialog's box is ticked (`git.PushTag`, that tag alone), and a tag whose
  push is refused stands as a tag, so the answer is a 200 carrying `pushed`
  and `pushError`. `git/tag/push` publishes one that already exists, which is
  the only way to reach a tag a coder made on the command line, and
  `git/tag/delete` takes one away, the remote half when the dialog's box says
  so, which it does by default because a tag deleted only here comes back with
  the next fetch, and reported beside a 200 the same way. Nothing
  there moves HEAD, so the event says the base stood. A project that is no
  repository yet gets the same segment saying so and a sheet whose one
  action is `git/clone`, straight into the project directory, which git
  itself refuses unless it holds nothing.
- **A picker asks git, it never filters a list it happens to hold.**
  `git/refs` (`?q=`, `?kinds=`) answers only the hits, capped per kind
  (`editorRefsCap`), the name match in Go because `for-each-ref`'s wildmatch
  does not cross slashes, commits through `log --all --regexp-ignore-case
  --fixed-strings --grep=…` plus a hex gated `rev-parse` for hashes. **The
  typed text travels into git arguments**: it rides in the attached
  `--grep=` form, never as an option and never as a pattern. In the client
  `openRefPicker` debounces, numbers its rounds so a slow answer never
  paints over a newer one, and lets a raw name typed past the list through
  on Enter.
- **One write runs at a time, and that is two locks for two questions.** The
  page's own (`gitBusy`) is what a person sees: spinner on the tapped row
  and in the statusbar, every other row and the commit panel disabled with
  it. The server holds the working copy for every write (`gitWrites`, taken
  in `takeGitWrite` before the bridge opens; a second write reads a 409
  `gitInUse`), and a write holds two names, taken together or not at all
  (`gitWriteKeys`): the absolute git directory (`git.WorkingCopy`; two
  projects in one checkout are one working copy, a linked worktree is its
  own) and the project path, which is what covers a clone, because the
  fresh `.git` resolves moments after it starts and the git directory alone
  would be walked past for the minutes it still runs. A git that could not
  be asked ends the write with a 502 `gitUnknownCopy` instead of guessing a
  name (`ErrNoAnswer` kept apart in `WorkingCopy`): two names for one
  working copy are no lock at all. The lock is a try and never a wait, and
  a commit and its ride-along push are one write. The quiet fetch holds
  marked names of its own (`quietFetchKeys`) and a short budget of its own
  (`quietFetchTimeout`): it meets itself, never a commit or a push, sets no
  `gitBusy` and fails without a word. The five sheet writes share their
  whole shape in `gitWrite`; the commit, the created branch and the quiet
  fetch are deliberately not on it. None of this reaches a coder on the
  command line: git's `index.lock` is the only thing between the two, and
  that is on purpose.
- **`dev-cockpit git` is a proxy and decides nothing.** A coder in a terminal
  cannot answer an ssh passphrase, and the passphrase has no business in a
  coder session either, so the command hands the whole line to the running
  cockpit (`POST /projects/:name/git`), which runs it in the project's
  working copy with the askpass bridge attached. It is deliberately generic:
  `git.Exec` takes the arguments **unchanged**, injects not even
  `core.quotepath` (the one `-c` every other call carries, which is why
  `run` builds its argv on top of `exec` and `Exec` goes to `exec`
  directly), and answers both streams plus the exit code, which travel back
  base64 in a 200 and out of the CLI onto its own streams and its own exit
  status. **The one thing read out of the arguments before they travel** is
  their shape (`git.CheckProxyArgs`): the git subcommand comes first and its
  own options behind it, and the options of git itself, everything that would
  stand in front of a subcommand, are not proxied. That is where the whole
  danger sits and none of it is needed here, `-c core.sshCommand=…` and
  `-c credential.helper=…` point git at a program of the caller's choosing
  which then inherits the bridge environment, `--exec-path` moves where git
  finds its own subcommands, `-C` and `--git-dir` move the call out of the
  working copy the dialog names, and the first word is what the dialog shows as
  this server's own truth (`git.Subcommand`, which therefore reads the first
  argument and nothing behind it: walking past an option cannot tell an option
  from its value). Refusing the *position* is what makes that complete, every
  one of them is only valid in front of a subcommand. Behind it only
  `--upload-pack` and `--receive-pack` are named, the transport's own program,
  which reaches a local process through a `file:` remote. **That is the honest
  bound**: it is what the cockpit accepts, and no wall against a coder that
  means harm. A coder runs under the same user account as the server, so it can
  read the bridge token out of the git child's environment in `/proc` and ask
  the browser whatever it likes, and a repository it can write carries hooks
  that run on push. The account is the trust boundary; what this path is for is
  that the passphrase never travels into a coder session.
  A non-zero exit is git deciding something and therefore a result;
  the error case is the runner's own `ErrNoAnswer` alone, which is what a
  refused or timed out question ends as, and it reaches the caller as a
  failing command with the cockpit's sentence rather than a hang. Cancel is
  the case that needs help: it denies the helper, so git fails in *its* words
  about a key it could not use, and `cancelNote` appends the honest half to
  stderr the way `promptRefusal` appends it to the editor's errors. It takes
  no `gitWrites` lock on purpose, a proxied call is a coder's git and the
  `index.lock` rule above is the whole arrangement between them; what it does
  take is the bridge, so two dialogs of one scope can never interleave, and
  that refusal reads like the editor's. The route stays off the editor group,
  it is no editor action and must not count as one for the language server
  lifetime.
  **It is a proxy and no project surface.** One path, `POST /git`, with the
  caller's own working directory in the body and no project anywhere: naming
  a project would be a second way to say where the call runs, and a
  `--projects-dir` on the caller would be a second copy of a value only the
  server is authoritative for, which is exactly where the two disagreed the
  moment one of them spelled it `~/projects`. git runs in that directory,
  whether or not it lies under the projects root: a checkout in `/tmp` is an
  ordinary thing to have, and refusing it would only send somebody back to the
  plain git that cannot ask for the passphrase. That this is safe rests on
  `CheckProxyArgs` alone: with `-C`, `-c` and `--git-dir` refused, the working
  directory is the only thing that decides where git runs, so the dialog can
  never name one place while the call runs in another. What was once a project
  is now a **scope** (`gitProxyScope`), and it is what the dialog label, the
  one-question-at-a-time bridge and the notification target hang on: the
  project name inside a project, because that is what a person reads, and the
  absolute path outside one. The two cannot collide, a project name is a
  single segment and a path scope starts with a separator. The editor's git
  surface is untouched by all of this and stays project bound. **The question is dropped when its caller is**
  (`endWhenCallerGone`): a coder that pressed Ctrl-C leaves a question nobody
  can answer for, so the request going away ends the action, which denies the
  helper and frees the project's bridge instead of holding it for the two
  minutes a person would have had. Only the question, not the operation, git
  runs on its own context to its end like every write. The editor's routes do
  the opposite on purpose, their dialog is app-wide and another device may
  still answer it.
  **A proxied question and an editor question are two different questions**,
  and `askpass.Question.External` is the one fact that tells them apart, set
  only by the proxy (`BeginCommand`, `promptActionCommand`). It is its own
  field and not read off `Command` below, or the policy would ride on a
  rendering detail: the day a second surface in the app wants to show what it
  is about to run, setting `Command` would turn the push channels on for it.
  Two things hang on it, both deliberately absent from the editor's own git
  surface. The dialog **shows** `Command` and `Dir`, as the plain monospace
  block the compose run output uses (`cwd:` then the command), because whoever
  answers a passphrase here is answering for a caller they cannot see, a
  terminal or a coding agent, and has to be able to read the whole picture: the
  directory is half of it, the same `git push` means different things in two
  checkouts of one repository, and the caller picked its project through a
  working directory nobody in the browser can see. An argument that is not a
  plain word is quoted (`commandLine`, `readableArg`), which is what keeps a
  line break inside an argument from writing its own `cwd:` and `$ git …` lines
  into a block that is rendered line by line; a runaway line is cut
  (`maxCommandLine`) and never hides the subcommand, which stands first. It is
  text to read and is never parsed back. And the question **leaves the app**:
  it becomes the `gitprompt:<project>` notification and therefore rides the
  push channels, which is the only way a question reaches somebody when the
  call came from a terminal and no page is open. An editor action is the
  opposite case, somebody started it on a page and that page is showing the
  dialog, so it gets neither: news would ring for what is already on screen.
  Which question holds an entry is decided **once, here**: the server hands the
  target out with the question (`gitPromptView`), the client only reads it.
  Deciding it again in the browser would be this rule written a second time in
  another language, next to a prefix that only exists in Go.
  `reconcileGitPromptNews` does the reading and the writing under one lock:
  outside it, two hooks firing together (one for a parked question, one for the
  answer taking it away) can land the entry after the clear, and the bell would
  claim a question that no longer stands, forever.
- **The cockpit writes one skill, keeps it current, and takes it away again.**
  The coder side of the
  proxy is not documentation somebody has to copy into an AGENTS.md: every
  installed coder gets `dev-cockpit-git` written into its global skill
  directory at start (`coder.EnsureManagedSkills`), **rendered from the
  running configuration** the way the assistant's instructions are, so the
  text carries this instance's own binary path, `--state-dir` and
  `--projects-dir`, and changed start flags reach every coder with the next
  start. An unchanged skill writes nothing, a tampered one is rewritten. The
  stop removes it again (`RemoveManagedSkills`, from the signal handler before
  the language servers close), because the skill points a coder at the local
  API socket of a running instance: one left behind would send every coder
  down a path that cannot answer. That is safe precisely because the skill is
  rendered state and nobody's configuration, and it is not the only thing
  keeping the disk clean, a SIGKILL and the self-update's exec both walk past
  it and are covered by the start rewriting it. Removing what is not there is
  no error.
  **What it may write over is written in the file, never derived from the
  name.** The text carries `managedSkillMark` and an owner line naming the
  instance's state directory, and both are read back before anything is
  touched. Somebody's own skill under that name has neither and is left exactly
  as it is, with a log line saying so: taking it over would rename its
  directory, replace its text, and the stop would then `RemoveAll` the folder
  with everything else in it. A copy somebody edited still carries the mark and
  is rewritten, which is what "kept current" means. The owner line is the
  second slot problem: **a coder home is shared by every cockpit on the
  machine** while the skill directory is one slot, so a throwaway started
  beside the real instance would otherwise point every coder at its own socket
  and delete the skill when it stops. It writes only when the owner is not
  answering any more (`CockpitInstance.Running`, one connect on the owner's
  local API socket), which is also what tells "another cockpit is running right
  now" from "this same cockpit restarted with a different `--state-dir`", and
  it removes only its own. Its
  description names the operations and the condition, a passphrased key,
  because that is what the coder matches a task against. `coder.IsManagedSkill`
  is the marker: the skills list renders it locked with the note that the
  cockpit manages it, and edit, save and delete refuse it (`managedSkillNote`),
  including under the name another skill tries to take. A coder whose home
  refuses the write keeps running, the skill is help and no requirement.
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
  an empty gutter. The porcelain format costs a multiple of the file it
  describes, so a file the editor still opens can fill git's output cap: that
  answer carries `large` and no lines at all, because half a blame would
  attribute the head of the file and leave the rest looking untouched.
  The editor's cross-device settings live in the shared settings store
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
  plain read route uses. **git's own output cap (`git.MaxOutput`) counts as
  too large there, and everywhere else a whole answer is the point**: the cap
  truncates silently, so an answer that reaches it is the head of a larger
  one, and a head diffed against the whole of it claims everything past the
  cut was deleted. The cap sits below the edit limit, so a revision between
  the two ends here, and the blame above says `large` for the same reason.
  The diff is a mode of a normal file tab and never a
  tab of its own, because the working copy side **is** that file's buffer
  (`workView()` answers the merge view's right editor while one is up): save,
  the dirty marker, undo, search, go to line and the blame gutter all address
  it without knowing a diff exists, and the comparison therefore shows what you
  are typing, not what lies on the disk. A tab type would hold a second copy of
  the same file, and two writable copies of one file is a save clobbering the
  other. The tab carries the revision it is compared against (`tab.diffRev`,
  persisted as `diff: "<rev>"`): HEAD from the diff switch, or whatever the
  file history and the revision picker put there, which the route takes as
  `?rev=`. `refreshDiffHead` refetches the revision side on a moved base
  whatever the revision is; an immutable one answers the same text and the
  replace is a no-op. Where the working copy is
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
  **An inline diff is taken down before another one goes up**
  (`buildUnified`, `dropUnified`): `unifiedMergeView` keeps the revision in
  a StateField, and reconfiguring a compartment keeps existing field
  values, so a rebuilt extension keeps the old revision and the new one
  never arrives. `showDoc` empties the compartment **after** the state
  swap, and a moved base goes through `originalDocChangeEffect`, which is
  what recomputes the chunks.
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
  `.cm-mergeView` and never the editors inside it. That ownership covers one
  axis: the outer `.cm-mergeView` is the vertical scroller and the editors
  grow to their full height inside it, while sideways every editor keeps its
  own `.cm-scroller`, so with wrapping off the two sides drift apart and the
  halves of a line stop standing next to each other. `syncMergeScroll` ties
  them together on that axis alone, for both merge views (`setDiff` and
  `setCompare`), and `dropMergeView` aborts it with the view. Writing
  `scrollLeft` raises a scroll event of its own, so a write that really moved
  the other side marks it and that one event is spent instead of answered:
  guarding by comparing the two values would let a side whose longest line is
  shorter clamp what it is given and pull its neighbour back to its own end,
  which is a comparison that cannot be scrolled past the shorter file's width.
- **One editor on every width: the strip stays, the options fold into one
  menu.** A strip and seven icons do not share 390px, and two different
  headers are two things to learn, so the icons went into the kebab instead of
  the strip going away. Outside the menu the header carries only the folder
  toggle, the strip, `[data-editor-save]` (`hidden` unless the active file is
  dirty) and the menu itself; every other control is an entry of `[data-
  editor-menu-list]`, and the entries are the same at 390 and at 1440. The
  menu carries one git entry, `Git`, which opens the git sheet; the per-file
  switches stay entries of the file's context menu
  (`diffMenuItem`/`blameMenuItem`, the revision diff and the file history,
  tab and tree row), because those are statements about one file. The folder
  toggle shows on both
  widths with the effect the width
  allows: below `md` it opens the drawer, above it folds the tree column and
  its splitter away (`.editor-tree-folded`, per device in `dc-editor-tree-
  folded`, the rule scoped to the widths that have a column so the class is
  inert on a phone). The sheet `[data-editor-sheet]` serves the menus
  that need more than a dropdown on a phone: the editor settings live in the
  hidden store `[data-editor-panels]` and the sheet **borrows the very
  nodes** and puts them back on close, so there is one set of controls with
  one wiring and every
  `root.querySelectorAll` sync keeps working while they are adopted. It is a
  full width bottom sheet on a phone and docks to the right edge of the editor
  from `md` up, three quarters of the width, still sitting on the bottom edge
  and still capped at 85% of the height, so the rounding, the border and the
  shadow are on its left side there. It stays `position: absolute` inside the
  editor and never `fixed` in the window: the backdrop a click closes it on is
  the sheet element itself (`event.target === sheetEl`). The same
  sheet lists the open files (`Open files`): tap switches, the cross closes,
  the grip handle drags, which on touch is the only way to reorder them, and
  the order is the tab order through `persistTabs`, no route and no server
  state.
  **Whatever the sheet shows, it is a list of rows, and the keyboard walks
  it.** The movement is `@dc/contextmenu`'s (`rowsOf`, `focusRow`,
  `stepRowFocus`), the
  same one the context menu's arrows use, with the sheet's own row selector
  (`SHEET_ROW`, the action rows plus the list rows); a sheet opens on its
  first row and a drilled level starts over on its own first row, both
  through `focusSheetTop` and only on a fine pointer, because a marked row is
  noise on a touch screen. **A row is reached only through `focusRow`**, which
  focuses with `preventScroll` and then moves the container's own `scrollTop`
  by exactly what brings the row back inside it: the page behind a sheet or a
  menu must not move, and a row below the fold must not stay there. **What
  marks that row is a surface and never a ring**: the sheet paints
  `--tblr-active-bg`, the same variable the terminal view's new menu paints on
  the row its arrows stand on, for `:hover` and `:focus` alike, so the mouse
  and the keyboard mark a row the same way and there is one state to
  recognise; the browser's focus ring is taken off with it (`outline: 0`), and
  a list row carries the surface on `.editor-sheet-row` so it marks over the
  full width. **Tabler gives every `.dropdown-item` a `min-width` of 11rem**,
  which is what shapes a floating menu and what makes a container cell grow
  out of its grid column, so the sheet resets it: without that the cell stands
  over the next one, its surface runs past the row and the whole sheet scrolls
  sideways. Two things are not detail. **A repaint replaces
  the rows and the focus falls to the body with them**, so every repaint runs
  through `repaintSheet(host, paint)`, which takes the position inside the
  very list it repaints and gives it back afterwards: per list and not per
  sheet, because a position in the git actions means nothing in the history
  below them, and a repaint that leaves no row to focus at all (a git write
  disables them while it runs) keeps the position for the repaint that ends
  the write. **And bootstrap's dropdown answers those very keys**: its data
  api listens for ArrowUp, ArrowDown and Escape from inside a
  `.dropdown-menu`, which the borrowed menus are, and looks for the toggle
  that opened them, which they have none of, so it swallows the arrows off a
  focused select and then throws. It listens on the document in the capture
  phase, so the one place ahead of it is the **window**, where the editor
  takes those three keys for an open sheet. A horizontal swipe on the surface steps through the open files
  (threshold, damping and abort from the terminal swipe), wrapping around at
  both ends like `stepTab` and the terminal swipe do, and only while
  `line_wrap` is on: with wrapping off the surface scrolls sideways and the
  gesture is the code's. Touch only, never with a selection, never in a
  comparison, never while the text has the focus: dragging the cursor along a
  line is a sideways drag too, and it has to keep working while someone types.
  It does what the terminal's `terminal-scroll-zone` does rather
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
  in a comparison, while a selection stands and while the editor holds the
  focus (`syncSwipeZone`, called from `afterActiveChanged`, `onCursor` and
  `onFocusChange`). That last hook is an `updateListener` on `focusChanged` in
  the shared extensions, so both editors of a side by side view report it, not
  a listener on the document. The pill naming the target is one thing app wide,
  `.dc-swipe-pill`, shared with the terminal swipe and fixed near the top of
  the viewport; only the terminal adds the pulsing pending state, because only
  it waits for a navigation. A tree row is `draggable` on a fine pointer only:
  a row that carries it hands the long press to the browser's own drag lift,
  and iOS then never lets that press become the row's context menu, which is
  the one way to reach a file's actions with a finger.
  **A name that does not fit is cut inside the row it belongs to, never over
  its neighbour.** Every one of those rows is a flex line, and a flex child
  refuses to go under its own text unless it is told to (`min-width: 0`), so
  each of them says which part yields first: in a tab the parent directory
  hint takes the whole shrink (`flex: 1 1 0`, so it lives in the room the name
  leaves and never steals a sub pixel of it), in a quick open row the
  directory does, and the file name follows only once the other side is spent.
  Both ends in an ellipsis, the tab keeps its `max-width` and its close
  control its full hit area, and the strip stays the one thing that scrolls
  sideways. Whatever a row cuts it carries whole as its `title`: a tab the
  file's path, a quick open row its path, a find in files row its path and
  line, because that is the only place a cut name can be read out in full.
- **Code navigation asks a language server, and the server processes belong
  to the cockpit.** `internal/editorintelligence` keeps a fixed profile
  registry, only the languages the navigation is verified against (gopls,
  intelephense, tsgo, which owns TypeScript and JavaScript alike and tells
  them apart by the language id of the `didOpen`);
  the commands and the container recipe are compiled in, so
  no setting can become a command execution surface. A way to run a server
  is a `Launcher`, Docker the only one today: it owns detection, preparation,
  argv and what a death means, so the service carries no flavor branches and
  another runtime is one more implementation. The one setting is per language
  (`/settings/editor/lsp`, stored install wide as `editor-lsp-<profile>`:
  `auto`, `<server>-docker` or `off`, absent and unknown read as `auto`, so a
  later option never hides the feature). Automatic runs Docker while the
  daemon answers and is off otherwise without a word, while an explicitly
  picked Docker that cannot run keeps saying so; the select shows the stored
  choice, never what Automatic resolved to. A language on Off is not rendered
  into the client's surface (`data-editor-lsp`), and the routes refuse a stale
  page's request for it with `disabled`.
  **The Docker option spawns the server itself**: `docker run --rm -i --init`
  named `dev-cockpit-<server>-<project>` (the project part sanitized to
  docker's charset, the whole name capped at 63, a short hash of the raw name
  joining once anything was rewritten), the projects directory mounted at its
  own path so file URIs match inside and outside (a profile with a default
  configuration mounts its project alone instead, see below), and one cache
  directory per project and server wearing the container's name, which makes
  the directory the project boundary. **The server in those names is the
  profile's own short `Server` field and never the command's leading token**:
  a program name may be long enough to eat the room the cap leaves for the
  project, so the TypeScript server is `tsgo` everywhere the cockpit names
  something itself, the image, the container, the cache directory and the
  stored setting value, while the command it runs stays what it is. **That
  cache is a host bind and no named volume, and it is mounted at the very
  path it has outside**, under `<state-dir>/editor-lsp/` (`CacheRoot`),
  because a module cache is not only a cache: it is where the sources of
  every dependency lie, and a definition in one of them comes back as the
  path the server sees. Only an equal path
  on both sides lets the cockpit read that file back, which is the same trick
  the workspace mount uses; a bind wants a daemon on this machine, which the
  workspace mount wants anyway. The servers are pointed into that directory
  explicitly (intelephense's storagePath through the launcher's InitOptions,
  gopls' GOMODCACHE and XDG_CACHE_HOME and tsgo's XDG_CACHE_HOME through
  the container env), or the index, and with it what a plain JavaScript
  project's automatic type acquisition downloaded, would die with the
  container, and gopls also gets `-modcacherw`,
  because a module cache is written read only and the cockpit has to be able
  to delete it again (`removeCacheDir` hands the modes back for the
  directories an older release wrote). **The projects root label is the
  ownership boundary**: the boot sweep and the orphan sweeps only ever touch
  containers and image tags carrying this serve process's own root, because
  the throwaway test instance shares the daemon and must not lose the live
  one's servers; the cache directories are swept by name under this
  instance's own state directory. A name that outlived an unclean death is
  removed right before the next start. The image is built on this host from
  the shipped build file, on first use, and never pulled prebuilt: whoever
  builds holds the licenses. Deleting a project closes its servers and
  removes its caches. The named cache volumes of the release before the bind
  are removed by the boot sweep and by a project delete, both marked
  TODO(v2.0.0): nothing creates one anymore, so a volume of the scheme is a
  warm index nobody can reach.
  **A target outside the project is opened, not counted, while it lies in a
  source root** (`SourceRoot`, `internal/editorintelligence/sources.go`).
  Those roots are the whole allowlist and they are the launcher's answer,
  never a setting and never a client's word: the readable parts of the
  project's own cache directory (the module downloads and the typings the
  server fetched itself, deliberately not the file cache or the npm cache
  beside them, which hold no source) and the trees that live in the image,
  the Go standard library, intelephense's stubs and the `lib.*.d.ts` of the
  typescript the image carries. Which languages need which is what the
  profile says, and it is the language that decides, not the pattern: a PHP
  dependency lies in `vendor` and a node one in `node_modules`, both inside
  the project, so for those two only the image side is ever needed. `Holds`
  takes a path that is absolute and already clean and refuses everything else
  rather than repairing it, because a repaired path is a second spelling of
  a file and the check would then be about a path nobody asked for.
  `mapLocations` marks such a target `external` with its absolute path, and
  `GET .../editor/lsp/source` reads it: a root on this host through
  `filesystem.ReadFileText`, whose root is the boundary a symlink cannot
  walk out of, and a root inside the image through a throwaway container of
  the same pinned image with `cat` as its whole command, no mount, no
  network and no name.
  **The lookups go on inside such a file**, so a jump chains: on into the
  next dependency, or back into the project. The navigation request
  therefore carries either kind of path, project relative or the absolute
  one of a source outside, and `validLSPTarget` is where that is decided
  for every navigation route at once: a relative path resolves under the
  project, an absolute one has to be in a source root, and it is the same
  `lspSourceRoot` call the read route serves from, so what may be opened
  and what may be looked a symbol up in are one set by construction.
  Below that there is one notion of a path and not two: `documentPath`
  turns it into the file, and `docURI` into the URI the server sees.
  **The document is opened on the server like any other, and that is not a
  formality**: asked of the real servers, gopls without a `didOpen` drops
  the usages that sit in the asked file itself, and intelephense answers
  nothing at all for a stub it was not handed. The same `ensureDocument`
  path therefore carries these files, holders and `didClose` included, and
  the text it sends is what the read route answered. Deliberately not an exec into the running server: a
  read has to work when the server has idled out, must not depend on which
  container happens to be up, and must reach nothing a workspace mount
  would carry. The route answers text and never bytes, capped and binary
  checked like every editor buffer, and it carries no version, because
  nothing read there can be written back. A usages preview of such a
  location is read from the disk where that is possible and left empty
  where the file lives in an image: a preview line is not worth a container
  start. Client side the answer becomes a read only tab (`tab.external`,
  the CodeMirror state carries `EditorState.readOnly`, the surface stays
  fully readable): the tab wears a lock and the folder it came from, the
  statusbar says Read only with the whole path as its tooltip, its menu
  keeps the close entries plus Copy path, and every path that acts on a
  file of the project (save, git, diff, blame, preview, revert, rename, the
  tree selection, the lookup itself) steps around it. A save is the one
  that must never find a way in: the write route would take the absolute
  path for a relative one and create it inside the project.
  **The container watches its workspace**: the entrypoint, one block appended
  in Go to both build files so the exit code contract cannot drift apart, runs
  the server next to a recursive inotify watcher (.git excluded, settle window,
  a refused watch loud on stderr) and ends the container with exit code 64 on
  a relevant change. `WantsRestart` reads exactly that code: while the project
  still sees editor action the slot restarts right away, no backoff and no
  toast, and the fresh slot keeps the old idle clock, so background churn alone
  never holds a server past the timeout; an idle project's wish waits for the
  next editor open. Such a death stays out of the error backoff, which is keyed
  per project and profile.
  **One process per project and profile, shared by every editor of the
  project**, so a reload reconnects to the warm index instead of building a new
  one, which is what made usages complete. It starts with the editor page
  (`warmLSPServers`: a marker file at the root first, the bounded walk cached
  per project as the fallback), speaks stdio JSON-RPC with `processId: null`
  (a containerized server lives in another PID namespace and would exit
  believing its parent dead), and lives until the project saw no editor action
  for ten minutes: every route under the editor group counts through the
  middleware's `Touch`, the indexing status pull and the project switcher's
  row fragment deliberately do not, both being pulls nobody working in this
  project started. A full table evicts the least recently used idle
  connection, busy is the answer only when every slot works. Because the
  connection is shared, a document carries the server's own version counter
  and its set of holders (didClose on the last one, cancellation per client
  and document), and a lookup re-syncs and sends under one lock, so no other
  instance's didChange slips between the text and
  the position describing it.
  **A request waits out the announced workspace indexing**, bounded: answers
  during it are real but partial, references most of all, and a partial answer
  is not empty, so no empty-answer retry ever catches it, that was the missed
  usages bug. The announcement itself arrives seconds after the handshake, so a
  connection that announced nothing counts as warming for `warmupWindow` after
  start. **A server that announces no startup work is exempt from all of
  that** and carries `Profile.SilentStart`: it has the workspace ready before
  it answers the first request, so there is nothing to wait out and the
  waiting was pure delay. It is the profile that says so and never the
  timing, and the zero value is the careful behaviour, so a profile that says
  nothing keeps waiting. **No poll stands behind the indicator**: the service
  tells one listener about every move of the picture, the web layer publishes
  it as the `lsp` event
  naming the project, and every open editor pulls `.../editor/lsp/status`
  itself; the connect snapshot carries a bare signal for a page that opened
  mid-indexing. The status marks the stretch before the server answers as
  preparing, which is where the first-use image build lives.
  **Because no poll stands behind it, every reason the indicator is up needs
  an event that takes it down again.** There are three, and each has one: the
  announced work ends with the server's own `end`; the preparing stretch ends
  with the handshake; and the warming window, the stretch where a silent
  connection is counted as indexing because its announcement may still be on
  the way, publishes its own expiry (`endSilentWindow`, one timer per
  connection). That third one is the trap, a clock nobody would look at
  twice: without the timer a server that announces late, or never announces,
  leaves a bar standing that only an unrelated move of the picture would ever
  take down, which on an idle project is never.
  **A `SilentStart` server has no such window at all** and is never counted
  as indexing while it is quiet: it announces no startup work because it has
  none, it is ready by the time it answers, and claiming otherwise would put
  a bar on the screen waiting for an end that is not coming. That one fact
  is also what keeps its lookups from waiting: counted as warming, every
  lookup in a project the server had nothing to announce for sat the whole
  window out, 45 seconds of spinner for an answer that stood at once, and an
  empty answer was retried on top. What such a server does announce later,
  fetching types for an untyped dependency, is waited out and shown like
  anybody else's work. Reindex in the
  editor's menu stops the project's servers and warms them again over a
  fresh scan, the project is the unit.
  **A project without a configuration of its own gets one from the image**
  (`container.DefaultConfig`): without it the server builds a project out of
  the opened file and what it imports, and a usages list answers a fraction
  that reads like the whole. Nothing may land in the working copy, so such a
  profile mounts its project alone and the file goes into the directory above
  it, which is then the container's own; `workspaceDir` is that directory,
  and it travels both as the `DC_WORKSPACE` the image writes into and as the
  workspace the handshake announces (`lspConn.workspaceURI`, while `rootPath`
  and `rootURI` stay the project), so the two cannot drift. Which file, what
  is in it and whether the project already brought one is the build file's,
  which puts the check where it can only run per container start.
  **The lookup routes stay off the editor group**
  (`.../editor/lsp/{definition,references,close,source}`): its middleware
  drops the quick open index after a write, and a lookup writes nothing. The
  answer is editor coordinates plus a preview cut by the search snippet rule
  (`filesystem.SnippetAround`) around the usage's own column, so both lists
  share one cutting rule; a target under no source root is counted, never
  opened. A definition whose range covers the asked position carries
  `declaration` and the client shows the usages instead, and the check is range
  containment, never the start line, because one server answers the whole
  declaration body, docblock included.
  **Client side** the gesture is Ctrl/Cmd+click (the word underlines through a
  CodeMirror theme, no stylesheet rule), Ctrl/Cmd+B looks up the symbol under
  the cursor with the same declaration rule (bound as Mod and as plain Ctrl,
  always claimed, or the editable surface takes it as a formatting command),
  Shift+F12 lists the usages. No context menu is claimed anywhere: the right
  click stays the browser's, and touch gets the cursor pill
  (`data-editor-lsp-pill`) with one Look up action running that very command,
  so the label never pretends to know which of the two cases it lands in. A
  pointerdown resets the bare modifier double tap machines, or two modifier
  clicks in a row read as a double tap. The usages list is the quick open panel
  from `md` up and the editor's bottom sheet below. There is deliberately no
  jump history. What persists is the settings key, which lives in the shared
  settings store the backup already carries, and the cache directories, which
  are deliberately not in the backup: a server rebuilds an index and downloads
  a dependency again, and no answer of the cockpit is lost with them.
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
- **Docker is one connection and a cache.** `internal/docker` keeps the single
  daemon connection of the whole cockpit: one container list, then the event
  stream refreshes the cache (debounced, exec events filtered out, they are
  healthcheck noise), and every surface reads the cache, nothing asks the
  daemon per request or per project. Host resolution order: the `docker-host`
  setting (empty means not set), then `DOCKER_HOST`, then the current docker
  context, then the known socket paths. Containers join a project through the
  compose label `com.docker.compose.project.working_dir`, never through the
  compose project name, which is a normalised folder name and collides for
  same named directories. No reachable daemon is a normal state: the cache
  answers empty and every docker surface stays away, without errors. A moved
  cache publishes the `docker` SSE event (no payload, every client pulls its
  own state). Container actions are JSON routes on `/docker/:id/...`, the id
  is the daemon's and no project owns it; compose runs are project scoped
  (`POST /projects/:name/docker/compose`, stack picked by its project
  relative label), run in the stack's directory in the background like a backup
  job, and report through a notification target per project
  (`notify.DockerTarget(project)`, wording in `composeNews`, main.go, URL to
  the run's output page). Per project and not one for all of docker, because a
  target holds at most one unread entry: two projects brought down at the same
  moment are two pieces of news and read as two, while one project's down and
  up seconds apart still collapse into one. The resolver asks
  `Service.LastComposeRun(project)` for the run the entry is about, never a
  global "the newest run", which with two projects finishing together would
  name the wrong one. A failed run resolves as urgent news
  (`TargetInfo.Urgent`), which the notify dedupe window never swallows as a
  follow-up of a fresh success, and opening a run's output page marks the
  project's docker target read (`handleDockerRun`), the way an attach page
  reads a terminal's news.
  **What those runs are is configuration, not code.** The compose buttons are a
  list in the settings store (`docker-compose-actions`, one JSON value,
  `internal/docker/actions.go`): icon, label, command line, timeout, and
  whether it asks first, in the order the buttons stand. On the settings page
  that order is the row order: each row carries a grip handle
  (`data-action-grip`) that drags it, touch included, the rows are full of
  inputs so the grip is the one drag surface, and the save persists the order
  because the handler reads the rows in form order. That key has three
  states and only `Lookup` can tell them apart, which is why nothing reads it
  with `Get`: not set means `DefaultActions` (never written at first start, so
  a later version may improve the list instead of finding a copy of today's in
  everybody's settings), set means what is stored, and an empty list means
  somebody took every button away, which stays that way and is what the menu's
  restore entry is the way back from. That way back is one route,
  `POST /docker/actions/restore`, taken by the docker menu and the settings
  page alike, and it **removes** the key (`settings.Store.Delete`) rather than
  writing the defaults into it; saving a form whose rows are exactly the
  defaults does the same (`docker.IsDefault`, `storeComposeActions`). A stored
  copy would read as answered and freeze the install on the list of the version
  that wrote it, which is the one thing the absent state exists to prevent. A
  submit button could not carry that anyway, pe.js builds a form's body from
  the form alone and drops what the submitter carries.
  **Where a browser link comes from is two sources, and the second one is
  configuration too.** A container is reachable in two ways: through a
  published port, which is docker's own truth and always offered, and through
  a reverse proxy in front of it, which publishes nothing and routes by host
  name. That host stands in a label of the container, because that is how the
  proxy learned it, and the container list already carries the labels
  (`Container.Labels`), so reading them costs no call. Which label and how to
  read it is a list in the settings store, `docker-link-rules`
  (`internal/docker/links.go`), with the same three states as the compose
  actions and the same way back (`POST /docker/link-rules/restore`,
  `IsDefaultLinkRules`, `storeLinkRules`): a rule is a label with `*` as the
  wildcard, a regular expression over its value with named captures (`host`,
  optionally `path` and `port`; every match in the value counts, and a host
  capture may name several separated by commas), an optional scheme, and an
  optional `label=value` that switches the rule off for a container. No type,
  function, field or file in the engine is named after a proxy:
  `DefaultLinkRules` carries the traefik router labels as data, because that
  is the one convention wide enough to default to, and a second default
  belongs there only if it is as safe. A convention carried in an environment
  variable (nginx-proxy's `VIRTUAL_HOST`) is deliberately out of reach: it
  would cost an inspect per container, and the whole integration is one list
  call plus the event stream. `LinkMatcher` compiles the rules once per read
  and is asked per container, `Links` answers the routes before the ports,
  deduplicated and stably ordered, and a rule that does not validate is
  skipped there and reported where it is edited.
  **The scheme of a route is the browser's answer, never the server's.** A
  rule that pins none yields a link without one, which the client opens
  protocol relative, because what terminates TLS may sit above the proxy where
  no label of the routed container can see it, and this server's
  `X-Forwarded-Proto` is the proxy's own entrypoint, which says http for a
  page the browser loaded over https. A published port keeps exactly what it
  had: the scheme of the container port (443 is https) and
  `window.location.hostname`. The two labels of the proxy's inner side,
  `traefik.http.services.*.loadbalancer.server.port` and `.scheme`, are how
  the proxy reaches the container inside its own network and must never reach
  a browser link. One shape carries both kinds, `docker.Link` (empty host is
  this page's host, empty scheme is this page's scheme, a route carries no
  port), one client function turns it into menu entries (`linkItems` in
  `@dc/docker`: "Open :18088" for a port, "Open host/path" for a route), and
  every surface carries both, the chip's hidden `[data-docker-link]` spans,
  the project menu, the editor's docker JSON and its sheet. The disabled ports
  line in the container menu stays the published mappings alone. The rules are
  edited under the commands on `/settings/docker`
  (`#settings-docker-links`, `dc-docker-link-rules`), and a regex field needs
  the two things that row gives it: what makes the pattern unusable, and what
  the rule finds in the containers running right now, both out of the same
  matcher and the same cache the pages read, so the preview cannot drift. An
  entry's icon is one word out of `docker.IconNames` (`start`, `purge`, ...),
  our own vocabulary for what a command does, and which picture a word gets is
  one table in the render layer (`render.DockerIconClass`): the stored setting
  never names a glyph, no client carries a second copy of the table (the
  editor's JSON is resolved server side too), and the icon set can be swapped
  without touching anybody's settings.
  Docker has a settings section of its own, `/settings/docker`, which carries
  the host, the command list and the link rules, and it is the only place any
  of them is edited: the host field left `/settings/general` without a redirect and
  without a compat branch, a move made before any of this had shipped. The
  docker integration has shipped since, so its routes, form fields and config
  keys are under the no breaking changes rule like everything else.
  `Action.Resolve` is the one place an entry becomes a run, argv and timeout
  together, and `startCompose` knows nothing else: the line is split by
  `SplitCommand` (quotes group, a backslash escapes, nothing is expanded and
  nothing is globbed, because no shell is ever involved) and a program written
  as `./deploy.sh` is searched from the stack directory up to the project root
  and handed on absolute, because `detach.Start` resolves the program before it
  sets the working directory. Every run, whichever entry it came from, lands on
  the same output page (`GET /projects/:name/docker/runs/:id`, JSON at
  `/output`, `dc-docker-run` repaints it while it goes), reachable from the
  stack's own menu entry and from the notification; cancelling goes at the hold
  process (`POST .../stop` to `Service.CancelCompose`), never at the server that
  asked, which may be long gone. A container shell is a normal cockpit shell
  started with a
  first command (`Shells.StartCommand`, `docker exec … ; exec bash -il`, same
  for the log follower), so it lives in the tab strip, the quick nav and the
  editor's terminal panel like any shell, falls back to a plain shell in the
  compose directory when the container ends, and restore brings it back
  commandless. The client menus are shared through `@dc/docker` (projects
  page chips and the editor); deleting a project brings its stacks down
  before the directory goes (`composeStacksToStop`, only the ones the daemon
  shows containers for), with the one command no setting reaches: a fixed
  `docker compose -p <compose project> down -v`, volumes included, and the
  project name comes from the daemon's label, because compose otherwise derives
  it from the directory name and clears nothing while reporting success. That is
  also why that delete runs off the request: the
  handler answers `deleting` at once, `projectDeletes` holds what is under way
  and what a finished one failed with, the row renders as working out of that
  state and disappears on the `projects` event. No daemon and no CLI means no
  stacks, so such a host deletes exactly as before. **Both of those outlive a
  restart, by different means, because they cost different things.** A compose
  run is a detached process (`internal/detach`, timeout in the hold process,
  combined output in one file) registered in `<state-dir>/docker/runs.json`
  with its files under `docker/runs/<id>.{out,lock,result}`; `Service.Recover`
  reads it at start, claims the directory of every run whose lock still holds
  and waits it out, reports the ones that finished while nobody was listening
  (their notification is the one a restart would otherwise have swallowed) and
  writes down how it ended either way. Every finished run, adopted or not,
  reports through the one `OnComposeDone` callback, never a per-call closure:
  the closure of the process that asked is gone by then. A `Quiet` run only
  speaks when it failed, which is what the deletion's own down is. A down that
  could not run or that failed ends the deletion instead of being worked past:
  its reason stands on the row, the directory stays, and the wait on a busy
  directory is bounded by the running entry's own timeout
  (`docker.ComposeDeadline`), never by a flat number. A finished
  entry stays in the register with its outcome and its output file, the newest
  `keptRuns` of them, which is what the output page still reads; only the lock
  and the result, what the run needed while it ran, go with the end.
  A project deletion instead remembers nothing but the intent: one flat file
  `<state-dir>/project-deletes.json`, name to path, written by `start` and
  removed by `finish` however it ended, so the file carries what is going on and
  never history. The failure text stays in this process's map, so a name cannot
  carry an old error into a project created under it later. `newProjectDeletes`
  reads the file before the server answers anything, which is what keeps the row
  from rendering naked, and `ResumeProjectDeletes` then runs every entry through
  `deleteProjectWithCompose` again from the top: every step of it is idempotent,
  which is why it needs no lock and no held process. It waits for the docker
  connection first, because a cache that has not answered yet looks exactly like
  a host without docker. The compose actions hang
  on a compose button
  next to the project row's actions (`[data-docker-project-menu]`, its stacks
  ride as hidden child spans so the live row swap keeps them fresh, the
  projects page swap replaces the button alongside the chip list), never on
  a chip of their own. The editor reads
  `GET /projects/:name/editor/docker` (JSON from the cache) for its statusbar
  segment and its docker sheet (also on Ctrl+Shift+D), and on a desktop opens
  container shells in its own terminal panel instead of navigating away. The
  container chips carry a direct logs icon (`[data-docker-logs]`), and a plain
  click or tap on a running container's chip opens a shell in it, the common
  reason to reach for one; the menu stays where every row's menu is, on the
  right click and the long press. That chip and the project's compose button
  both ask `menuJustClosed()` before they open anything, the shared window that
  makes a second click on a toggle close its menu instead of reopening it.
  **Logs are a terminal, never a dialog**, and there is one entry for them:
  `Logs` with the logs icon, opening what `Log terminal` used to
  (`POST /docker/:id/logs-shell`). A whole stack has the same, project scoped
  (`POST /projects/:name/docker/logs`, `docker.ComposeLogsCommand`), so nobody
  has to find the container that is talking first. A stack's logs terminal is
  called `docker logs` (`dockerLogsName`) and not after the project: it is every
  service of one compose directory, and its first line says which. A
  container's keeps its own name. Both menus, the projects
  page's and the editor's, are built by one function in `@dc/docker`
  (`projectMenuItems`): which container to reach first, because reaching
  something is the usual reason to open it, then per stack its logs and the two
  compose actions. **The project menu answers which container, a container's
  own menu answers which address.** One entry per container, in the order the
  containers already stand in: with exactly one address that entry is the
  address, with several it names the container and how many, and opening it
  opens the same menu again with that container's addresses and a Back entry
  (`onDrill`, `openMenu` is reentrant and the editor's sheet repaints its
  list). Everything in one flat list was a wall in front of the one entry
  somebody wants, and choosing the first of a dozen host names for them is
  exactly what the cockpit cannot know. The chip's own menu keeps every
  address of that container directly, whoever long presses a container asked
  about that container.
  **A label never loses its tail**: an address is told apart by its end, so
  `@dc/contextmenu` takes a `{head, tail}` label (plus a `title`) and renders
  two spans, the head shrinking and ellipsizing and the tail never, which puts
  the ellipsis in the middle without measuring text (`.dc-menu-label-head`,
  `.dc-menu-label-tail`, and `.dc-context-menu` carries the `max-width` that
  keeps the whole menu inside the viewport). **No surface renders the
  daemon's status string**: it is a snapshot of the last cache refresh, which
  only happens on connect and on container events, so an idle daemon serves an
  uptime that is hours old. What a container is doing is the icon color, what
  it offers is its addresses. In the editor the containers stand in a plain
  bootstrap row
  (`row-deck`, `col-12 col-sm-6 col-lg-4`, one, two or three per line by the
  width, equal heights per line, no stylesheet of our own for it) and carry no
  buttons of their own, everything is in the menu.
  **Every list of containers stands in one order, and `State.ForDir` decides
  it**: unwell first, then running, then the rest, and stable inside a group so
  the cache's own order (compose project, service, name) stands and a container
  that neither started nor stopped never moves. Every surface reads the per
  project list through that one call, the chips, the editor's grid and both
  menus, so an order cannot be copied three times and drift apart. A list is
  read from the top and the chip row folds after eight, which is the whole
  point: the one thing that is wrong must not sit behind the fold.
  **While a compose command runs the docker icon rides a wave**
  (`.dc-docker-working`, translate plus rotate, a motion and not a blink),
  fed on the project row by `ProjectDocker.Working()` and in the editor's
  statusbar by the stacks' `busy`/`run.running`, and the run's own menu entry
  carries the turning loader (`.dc-spin`). What kept that loader standing
  still was simply that nothing ever animated it, `ti-loader-2` is a picture
  of a spinner and no more. Both classes carry a `display` of their own
  anyway: `.ti` sets none, so outside a flex row the icon is an inline box,
  and an inline box ignores every transform, which makes a rotation on it
  silently nothing.
  **A swapped node brings a new animation with it**, starting at zero, so a
  spinner jumps back and the wave restarts on every refresh of the projects
  page. Every place that replaces server rendered markup pins what came in to
  the document timeline afterwards (`syncAnimations` in `@dc/dom`,
  `animation.startTime = 0`, which is where the document has been running
  since): the chip swap, the row's actions, the whole row of an ajax submit
  and the rebuilt list. The fresh animation then stands where the one it
  replaced stood and they all run in step. That is a synchronisation and not
  a morph, nothing is diffed and nothing is kept alive.
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
  style; **do not restructure it without asking.** `app.js` is the glue:
  loading bar, lazy custom element loader (by tag
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
  is already pushed); elements that must react to the final URL listen for it.
  `data-no-pe` opts a link or form out
  into a native load (login, logout, downloads, JS owned forms). Framework scripts
  and toasts sit outside the swap and survive it.
- **Shared modules:** `internal/web/static/js/dc/` (toast, dialog, contextmenu,
  http, dom, store, repeater, fold, project-sort). Imported by bare specifier
  `@dc/<name>`. There is exactly one `escapeHtml` and it lives in `@dc/dom`,
  imported by everything that builds markup out of a value: a second copy is
  how a smaller escape set ends up around a value inside an attribute one day.
  `@dc/contextmenu` renders a body-mounted `.dc-context-menu`
  dropdown at a point, one open menu at a time (Escape/arrow keys, outside
  pointerdown, outside wheel/touchmove, `dc:navigated` and the caller's abort
  signal close it; programmatic scrolls must never close it). The arrow key
  movement over its rows is exported (`rowsOf`, `focusRow`, `stepRowFocus`)
  and takes a row selector, so a second list of rows walks the same way
  instead of growing its own: the editor's sheets are that second one.
  `focusRow` is the one that reaches a row, and it scrolls the container it
  was given, never the page. Row menus
  (right click plus touch
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
  **A split arranges its panes in columns, and a layout change is a style
  change.** One more member option says which of them share a column,
  `@dc_tab_gcol`; a member without one renders as a column of its own, which
  is what every group looked like before columns existed, so there is nothing
  to migrate. `@dc_tab_gpos` stays the group's one global order: it drives the
  strip label, the quick nav, the mobile swipe and the stacking inside a
  column, and a column stands where its first member stands in that order,
  never by the raw option value. The panes stay flat siblings of one CSS grid
  (`splitLayout` in Go, mirrored by `terminal-split`; every column divides the
  same row tracks, `--dc-split-rows` is the least common multiple of the
  column depths), because moving a pane between column containers would take
  its terminal island with it and reconnect the stream. The pane head drag is
  therefore two-dimensional on that page, and it reads the geometry once at
  the start so the preview cannot move the ground it measures against: it
  posts `/terminal-tabs/group` with the flat order plus a `cols` array, which
  is optional on purpose, every other caller of that route (the strip drag,
  the quick nav drag) says nothing about columns and what it says nothing
  about keeps the columns it has. The mobile page is untouched, one pane per
  page and a flat swipe order. The desktop pane stepping (Ctrl+Shift+arrows)
  walks the visual order, columns left to right and each column top to
  bottom: the server emits it as every pane's `order` style
  (`splitCell.Order`) and `applyColumns` rewrites it, so a pane created into
  a mid page column steps where it stands while the flat order still lists
  it last. The strip's + menu follows the active pane: activating one fires
  `dc:terminal-activated`, which triggers the strip fragment refresh (the
  pull reports the active island as `?focus`, so the create links and the
  editor entry carry that pane's project); a guard compares the rendered
  links' focus first, so only a real context change costs a fetch. On a
  split page that refresh builds its path from the active island's
  attributes (`/splits/<split-group>?focus=<id>`), never from
  window.location: the remembered pane activation fires on the boosted DOM
  swap before pushState runs, and a fragment pulled with the old location
  paints the page you just left as the active tab. **The rows setting is the height of the
  vertical axis from here on**, not of every pane: the container carries a
  height of `rows × cell + one pane head` and the panes fit their rows into
  the box they are given (the fullscreen mechanism plus the `fitAddon` path,
  the editor panel's), so a column shows about `rows` lines in total and
  grouping or stacking never changes the page height. One terminal line in
  pixels is only known to a rendered terminal, so the islands report it
  (`dc:terminal-metrics` plus a `data-cell-height` attribute, because every
  custom element upgrades lazily and either of the two may come first) and it
  is remembered per font size, so the next split page is sized before its
  first paint. **A terminal can be created straight into a split**, from the
  pane head's menu into that pane's column and from the group tab's menu into
  a column of its own at the right edge (`@dc/split`); both entries open the
  session's create form prefilled. It rides the existing
  create routes, `group` and `column` travelling through the query and the
  form the way `return` does, so one request creates the terminal and puts it
  in; nothing ever renders a half done split. A column has no id of its own,
  so `column` names a member of it. Three rules hold: a split that vanished
  between the form and the POST still creates the terminal and lands on its
  own page, a layout wish must never fail a create; a failed group write
  reports and leaves the session running and ungrouped; and the new member is
  written alone, `@dc_tab_gpos` the group's highest plus one, so nobody is
  renumbered (the one exception writes the source pane's column when that
  column was never written down, which moves nobody either).
  **One order, and a partial post is a permutation.** The strip position lives
  in tmux as `@dc_tab_pos` and is the single order every surface renders, each
  one a view on it (the strip and the quick nav show everything, the editor's
  terminal panel one project). A surface that shows part of it also posts part
  of it, so `POST /terminal-tabs/order` never takes the posted ids as the whole
  strip: `applyTabOrder` folds them into the current order as a permutation of
  the places those sessions already hold, the slots stay and only who sits in
  which changes, and the write then covers every live session so no two share a
  position. A full post is that same operation with every slot in it. Never add
  a second order field for a new surface, and never let a surface renumber from
  one what it cannot see.
  The editor's terminal panel embeds the same islands, desktop only: the
  fragment `/projects/:name/editor/terminals` renders the project's sessions
  as tabs plus empty pane divs, and `editor.js` mounts an island pair into a
  pane on its first activation, so a never shown pane holds no stream. Those
  islands carry `embedded`: rows fit the pane the way fullscreen fits the
  viewport (`MinTerminalRows` is 5, else the server clamps a low panel back
  up to 30), the size observer watches height too, a hidden pane does not
  connect, and the terminal fullscreen keys stay off the page. Open state and
  active tab are
  per project (`dc-editor-term-open:<project>`, `-active:`), the height per
  device. Inside the panel the terminal keys mirror the attach pages and
  Ctrl/Cmd+Shift+Enter passes through to the editor fullscreen; the panel
  owns them as long as the last click landed inside it, a focus-owner flag,
  because a click on the bare strip focuses nothing. The editor's own
  shortcuts skip events from inside the panel. A coder created through the +
  menu comes back: the create form's action carries the return target
  through the POST, an editor return redirects to `.../editor?terminal=<id>`,
  and the panel activates that tab **after** the tab restore, whose own
  `editor.focus()` lands later. A coder pane gets the attach page's files
  modal, a `[data-terminal-footer]` button block the island's activation
  unhides, and `coder-file-upload` is re-inserted after the mount so its drop
  zone finds the terminal. The modals host and `dc-host-float` both meet the
  fullscreen editor's fixed context at 1030, with opposite answers: the
  modals move to `document.body`, else a modal falls under its own backdrop,
  while the float keeps ducking to 5 and is gone there for as long as a popup
  stands. Lifting that duck has been built and dropped twice, a float above
  the surface can never be covered by a dropdown inside it. The tab context
  menu mirrors the strip menu minus Open editor, plus Open terminal page.
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
  serves dialogs only, and is themed in `style.css` by setting its
  `--swal2-background`/`--swal2-color` custom properties on `body` to Tabler
  variables, so open dialogs follow a live theme flip (never pass colors to
  `Swal.fire`). Toasts deliberately do not ride the Swal singleton, a toast
  firing while a dialog stands must never close it: `@dc/toast` builds
  Bootstrap toasts (`showToast`, plus the notify helpers) into the layout's
  fixed `[data-dc-toasts]` container, which sits outside the swapped region
  and follows the theme through Tabler's own toast styles. CodeMirror
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
News the resolver marked urgent (`TargetInfo.Urgent`, today a failed compose
run) passes that window and replaces the stale unread entry: right after a
success it says the opposite of the fresh entry, so it is no follow-up. The
decision stays with the resolver, notify itself classifies nothing.
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
every connect, and that means every reconnect, the server sends a snapshot of
everything a page reads from this stream (unread state plus the bare
`terminals`, `projects` and `docker` signals, the draft and assistant ones and
the host reading), because a surface that asks once and then follows an event
stands on what it saw before the socket went down until something happens to
move: the editor's docker segment is exactly that. Then a `ping` frame every
15s; the client forces a reconnect when the
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
`[data-sessions-body]` chip lists and re-folds them; the unfold flags live on
that container, which stays in the DOM across swaps, one per row. Its rows are
two: the terminals with the two new chips, and below them the containers, each
its own `[data-chip-fold]` with its own "+N", because a project with a dozen
containers must not push its coders behind a fold and the other way round. The
shell attach header (`dc-inline-rename`) re-pulls
`GET /shells/:id/name` into heading and page title. A state dir belongs to one
serve process, a second process on the same dir would miss live pushes. The
`dc-notifications` element owns bell, badge, center, toasts, and the title
counter; unread state is module scope because the element mounts once per
header breakpoint, while `@dc/events` owns the one connection. Opening an
attach page marks that
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
