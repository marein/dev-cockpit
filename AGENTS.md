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
  The create dialog changes nothing about it, see below.
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
  badge), it doubles as the status light. A coder's name is optional: without
  one the CLI is started with no name flag, and what the session is called is
  read back from the CLI's own record, and all three write one that reads like
  a title. claude puts an `ai-title` entry into its transcript a moment after
  the first prompt, a short summary of it from the small model, the same title
  its own session picker shows; copilot and opencode write theirs into their
  own record. None of them is touched here. Only claude's gap is bridged: until
  the `ai-title` lands, the transcript reader falls back to the first prompt
  itself (`promptTitle`, slash commands and injected reminders left out), cut
  by `coder.ShortTitle` to one line of `coder.TitleRunes`. A name a person gave
  always stands above a generated one, and with no title at all
  `coder.DisplayName`'s fallback stands in. A runtime therefore never passes an
  empty name on, and the promote step matches an unnamed session on its
  working directory, over `coder.SessionCandidates` where a coder offers it:
  a record the lists hide is exactly what a promote is after, copilot's fresh
  session carries neither a name nor events until somebody types, and without
  that view the session keeps running under the key the cockpit minted while
  copilot holds its own id and its own title.
- **Claude session settings:** every claude session starts with one injected
  `--settings` blob (`internal/coder/claude/runtime.go`): theme auto, the
  notification hooks, and `disableAgentView`. The cockpit forwards keys via
  send-keys, tmux never swallows Ctrl+B as prefix, so without the flag an
  accidental Ctrl+B or a left arrow into the agent view turns the session
  into a background agent the cockpit can no longer resume.
- **v2.0.0 markers:** legacy compatibility code that may be removed once
  breaking changes are allowed carries a `TODO(v2.0.0)` comment. Grep for it
  when preparing a 2.0.0 release.
- **Retired addresses redirect.** What a released build handed out keeps
  answering after a surface moves: `/quicknav` to `/ctx/projects`,
  `/assistant/panel` and `/assistant/history` to `/assistants` (`retiredPath`
  in `router.go`), and `/projects?assistant=<id|open|memory>`, the shape the
  notification entries carry from when the assistant was an overlay, to the
  assistant's own page (`handleProjectsList`). All 308, all TODO(v2.0.0). The
  body a redirect lands on is shaped for the new surface, so this saves the
  address, not an old page's reading of it.
- **The assistant acts through the local API, never around it.** A state
  directory belongs to one serve process, so a turn writes nothing itself: it
  runs the cockpit's own commands, they reach the server over the unix socket in
  its state directory (`internal/localapi`), and the request lands in the same
  handler a browser hits. The socket is the whole credential, there is no token
  anywhere: it sits in a directory only the owner may enter, and `LocalHandler`
  marks what arrives on it, which is what the session check and the CSRF check
  read. Never add a second writer of the state files, and never let the assistant
  drive tmux directly.
- **`dev-cockpit assistant …` is internal surface.** Everything an assistant
  runs sits under that one command group, which shares the `--state-dir`,
  `--projects-dir` and `--as` flags, the last one naming the assistant that is
  calling, see the identity rule below. Unlike the rest of the CLI it is exempt from the never
  remove rule above: its names, flags and output are tuned for the model and may
  change with any release, and its help text says so. The generated instructions
  and the check prompt spell every call through the workspace's `cockpit`
  wrapper (`Workspace.Wrapper`, see the wrapper rule below), so a rename lands
  in one place. Names are object first and verb last (`coder-send-prompt`, `job-list`,
  `project-delete`), so the flat help list groups itself by object; `status` is
  the one exception, it is about the whole cockpit.
  What is not exempt is the group itself, `serve` and `hash-password`:
  those are what a person and a start script use.
- **Every reading is capped, and `--contains` is the one way past a cap.** The
  lists and the threads an assistant reads are bounded on purpose, and what a
  bound left out is counted, never dropped in silence. The way back to it is one
  flag with one behaviour wherever it stands (`job-list`, `assistant-list`,
  `assistant-show`, `line-comment-list`, `trigger-list`): a word compared case
  insensitively, narrowing **before** the cap, so it reaches what the plain
  reading never
  shows, composing with the flags that widen a reading (`--entries`, `--full`,
  `--all`) and replacing none of them. What it searches is what the surface
  shows a reader and nothing behind it: a job's name, task, criterion and last
  report, a trigger's name, event, task and last fire, and in a thread the body
  of every message plus the headline a note of
  the cockpit and an answer a trigger pushed stand under. That thread rule is
  one function, `Message.carries`, read by `Service.Search` for the list and by
  `Service.Transcript` for the reading, so the list that says an assistant
  carries a word and the reading that shows where cannot disagree; a headline
  it searched therefore travels in the JSON and stands over its message in the
  output, a hit nobody can see is no hit. The task a trigger gave is
  deliberately out of it, it is the same words on every fire and would make one
  trigger a hit on every answer it ever pushed. A filtered output says in its
  first line that it is one, with the hits and the whole count, or three
  matches are read as the whole history. Two searches in one tool that behave
  differently are worse than none, so a new one reuses these.
  **`--since` is the second way past a cap, the one a moment answers.** It
  stands where a reading is about when something happened, `job-list` and
  `trigger-list`, and it behaves like the word: a span back from now (`24h`)
  or a date (`2026-03-04`, with a time of day where somebody writes one),
  narrowing **before** the cap and composing with everything else, so "what
  happened yesterday" reaches what the plain reading never shows. What counts
  as having happened is the entry's own `UpdatedAt`, which a job writes on
  every move and a trigger on every fire, every end and every edit. There is
  one parser and it stands where the flag is typed (`parseSince` in
  `internal/cli`), because a refusal belongs to whoever wrote the word; the
  resolved moment is what travels to the server (`querySince`), which refuses
  a stamp it cannot read rather than answering a narrowed call with
  everything.
  **What is over is history and stands as one line.** A closed job and a spent
  trigger cannot move again, so the two lists print them the way `status`
  prints its inactive sessions: the tail capped at the recent ones with the
  rest counted (`maxClosedJobsShown`, `spentTriggersShown`, the same five the
  page's aside caps at in `closedJobsShown`), and the two long lines left off,
  a closed job's criterion and last report and a spent trigger's task and
  bounds, because its state already says what became of it. `job-show
  <terminal>` is the whole single view, and `--full` gives either of them
  those lines back, so a `--contains` hit that fell in one of them can be read
  where it was found. The trigger reading is capped on
  the route both surfaces take (`narrowTriggers`), the aside and the JSON
  `trigger-list` reads, and only that command's `--all` lifts it: a page that
  caps and a command that does not is the same list disagreeing with itself.
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
- **Several assistants live side by side, and a job belongs to one of them.**
  An assistant is one instance: a name, a thread, the coders it steers, and it
  lives until somebody deletes it. Nothing is archived, nothing is "the live
  one", and nothing creates one by itself: `Service.Create` is the only way in
  and the surfaces call it when a person asks. Which one the area's own
  address opens is decided by `handleAssistantsEntry` alone, see the page
  rule below: the one looked at last, else the first row.
  On disk each one owns a directory, `assistant/instances/<id>/` with its
  `transcript.json`, its `jobs.json`, its `draft.json` and its `workspace/`,
  so what belongs to one conversation is deleted and backed up as one thing;
  `assistant.json` stays the index. The workspace is where that assistant's
  turns run, with `assistant-files/` for what it writes, `user-upload/` for
  what a message carried, and its own generated `CLAUDE.md`, `AGENTS.md` and
  `cockpit` wrapper (`generatedFiles`, noise in a backup, dropped by the move);
  an assistant comes to it the way every directory here comes to be, on first
  use through `Workspace.Workdir`, no migration makes one. Before every turn
  the coder's CLI is told to trust it (`assistant.WorkdirTruster`, claude and
  copilot answer it), because a non interactive turn cannot answer a trust
  dialog. What is shared is the memory, `assistant/memory/`, because what the
  user told one assistant holds for all of them; everything of one
  conversation is separated by its directory, and that separation is **order,
  not protection**: an assistant may read another's transcript and another's
  workspace, it writes only its own. There is no way to send another assistant
  anything, work is handed to coders, and no assistant may delete itself.
  **A job carries its owner**, and the owner is the directory it was read from
  (`Job.Owner`, `json:"-"`, filled by `Jobs.Of`). A check wakes that assistant
  and its report is written into that thread, never into whoever is on screen.
  One terminal carries at most one job: a second assistant steering a coder
  somebody already steers is refused **by name**, releasing somebody else's job
  is refused the same way, and only the user may take a coder off an assistant.
  Sending a prompt to any terminal stays allowed and makes nothing yours, which
  is why `NoteAssistantInput` counts only the steering assistant's own send.
  Deleting an assistant takes its jobs with it: the open ones are read before
  the delete, the entries go with the directory, and only after the delete
  went through does `Watcher.Dropped` kill their running checks and announce
  their projects, so a delete that fails keeps the assistant with its jobs
  steering; the answer names the coders that came back. The jobs live on one
  path, `/assistants/jobs`, which serves
  the list (`?assistant=<id>` narrows it to one) and takes both actions on it.
  Two routes make a job, `/assistants/jobs` and the `done_when` of
  `/coders/new`, and both read the owner through `steerOwner`: a job built
  without one is refused by the watcher. The create already started the coder,
  so it reports that refusal as `steerError` in its answer instead of ending
  the request.
  A check runs in a provider session of its own, which is also why only a chat
  turn (`RunChat`) may write what a turn reports about the context window onto
  the assistant: a check's consumption is not the thread's. On the page a
  person reads coders, not jobs (the aside's first tab, its rows and its
  empty state say steered coders); code, routes, state values, the
  `dev-cockpit assistant` commands and the notification titles keep job. A
  trigger is not split that way, it is called trigger everywhere, on the page
  and below it alike, which is the word the notification titles always used.
  What holds both is the aside, see the page rule below.
- **One chat turn per assistant, and the queue is that assistant's.** A second
  prompt into an assistant that is answering waits: it goes into the transcript
  as `StateQueued`, the page shows it as Waiting and offers to take it back
  (`Service.Discard`), and the end of the turn sends everything waiting as
  exactly one new turn (`Service.flushReady`, called from the settle and once
  per assistant after `Recover`, so a queue survives a restart). The queue
  belongs to the one assistant, so a thread that is thinking holds up nothing
  but itself and every other assistant answers at the same time; the decision
  falls under the service lock, the same one the turn's end takes, so a send
  racing that end either queues or starts. `MaxQueuedMessages` bounds what may
  wait. Across assistants there is no
  cap at all, one that answers "busy" because another is thinking is one nobody
  can rely on. What **is** capped globally is the checks, the turns nobody asked
  for interactively: `assistant-max-checks`, a setting on
  `/settings/assistant/jobs`, default `assistant.DefaultConcurrentChecks`, read
  again before every check so a change applies without a restart, and a check
  that has to wait still happens.
- **A turn's model is a choice per purpose, and one function resolves it.**
  Nothing picks a model by itself, the assistant inherits from the coder or
  overrides it, and a check and a reaction inherit from the chat: a chat
  turn takes the ring's chat pick, else the coder's own start default on its
  settings page, the one level that knows the CLI, else empty, the CLI's own
  default, which is what every turn ran on before; a check takes the ring's
  check pick, else that whole chat chain, ring pick included, evaluated when
  the check starts; a reaction the trigger's own model, else the ring's
  Triggers pick, else the chat chain the same way, evaluated when the
  reaction starts. That is what Same as chat means on the ring's Checks and
  Triggers picks, and it is stored as the empty string: an assistant whose
  chat is picked at the ring and nothing else set runs its checks and its
  reactions on that pick, and moving the chat at the ring moves every later
  check and every later reaction of a trigger without a model of its own,
  with nothing stored anywhere. A trigger's own Model pick reads Assistant
  default (<resolved>) in its empty entry (`assistantDefaultLabel`), the
  owner's Triggers pick where one stands, else its resolved chat, and a
  trigger carries a model only where a person set one on the form or with
  `--model`. **Everything on the Models tab of the assistant settings is a
  creation default** (the first tab, where the bare `/settings/assistant`
  lands), like the coder's start default is for a session, and nothing on
  it is read at run time: `Service.create` copies the tab's Chat default
  onto a new assistant's `Model`, the Checks default onto its `CheckModel`
  and the Trigger default onto its `TriggerModel`, each only where the
  default is set and else left empty, so an assistant made before a default
  was set keeps what it had, its chat on the coder's start default and its
  checks and reactions following the chat. Nothing is copied onto a
  trigger, `Reactor.Add` leaves a model nobody set empty (the form's
  Assistant default entry, `trigger-new` without `--model` and the sequel of
  `coder-new --then` alike). The tab says so in its one line, Applies to new
  assistants, its Chat entry reads Coder default (<start default>) or Coder
  default (CLI), what a new one runs on where the default is empty, and its
  Checks and Triggers entries read Same as chat the same way. The tab's chat
  default was the one value on it read at run time and became a creation
  default like the other two on 2026-09-24, one rule for the whole tab:
  `DefaultModelOrigin(RunChat)` reads `defaults.Start` and nothing else of
  the tab, `defaults.Chat` is read by `Service.create` alone, and
  `ModelSource` has no tab level, a copied default reads as a pick. The
  defaults are a setting
  per coder (`assistant.ModelDefaults`, the four keys `ModelDefaultKey`
  names, chat, check, trigger and start), read fresh by `ModelDefaultsFor`
  where a turn or a session starts and where an assistant is made, so a
  save applies to the next one. An
  assistant carries three, `Summary.Model` for its chat turns,
  `Summary.CheckModel` for the checks of its steered jobs and
  `Summary.TriggerModel` for the reactions of its triggers, a trigger carries
  `Trigger.Model` for its reactions, all four `omitempty`, so a stored
  instance and a stored trigger load unchanged. `ModelOrigin`
  (`internal/assistant/model.go`) is the one reading and answers a
  `ModelReading`, `ModelFor` its model alone: the pick, else
  `DefaultModelOrigin`, the chain below a pick, which is also what a pick's
  empty entry names and which for a check is the chat's own reading and
  nothing else, for a reaction the ring's Triggers pick, else that same
  reading, `defaults.Check` and `defaults.Trigger` stand in no run time
  path. `ModelSource` travels with the answer and names the level the model
  really stands on, whatever purpose asked, and `SameAsChat` beside it says
  that a check or a reaction took it as the chat, so a check that landed on
  the ring's chat pick reports the ring as the chat's (`assistant-models-get`
  prints `Checks: fable (same as chat, ring)` for an assistant with only
  that pick, its Checks line always prints the resolved chat with that
  source form, and its Triggers line the ring's Triggers pick as `(ring)`
  where one stands, else the resolved chat the same way). A trigger's model
  and an assistant's check and trigger models are read when the turn
  starts, and
  `RunRecord.Model` says which model a check or a reaction really ran on,
  pinned from `startOwnSession` into `TurnRequest.Model` and the three
  command lines. The defaults reach
  the service through `CoderInfo.Defaults` and a coder session's start
  through `Manager.SetModelDefaults`, both wired in `runServe` over the
  settings store, so `internal/assistant` still reads no store of its own for
  a turn. It travels as `TurnRequest.Model`, each runner appends its
  own flag (`claude --model`, `opencode -m provider/model`, `copilot --model`)
  and nothing else about the command moves, and `RunRecord.Model` says which
  model a run was started with. `CleanModel` is the one validation: trimmed,
  at most `MaxModelRunes`, letters, digits and `. _ / : - [ ]`, never a first
  rune of `-` (an argv reads that as an option, which is the one shape the
  alphabet alone lets through), refused with a sentence otherwise, and an
  empty value is a value, the choice cleared. What a
  coder offers is its `coder.ModelRepository` (`internal/coder/models.go`),
  the optional capability `coder.ModelKeeper` on the coder itself beside
  `AssistantCapable`, never on its conversation runner: the same list serves
  a coder session's start and an assistant's turn, and a coder without the
  conversation capability still starts sessions. `List` answers the CLI's
  own names and the added ones, each marked with its source, `Note` the one
  line saying where the CLI's names come from, which the selects show under
  themselves, `Add` remembers a name (checked by `CleanModel`, idempotent,
  stored per coder under `ModelAddedKey` in the settings store as one JSON
  array, in memory without a store), `Delete` forgets an added one and
  refuses a CLI name, `Exists` reads the list. Every coder builds one with
  `coder.NewModelRepository` in its constructor over the settings store it is
  handed, and `ModelRepositoryFor` answers the empty one for a coder without,
  which lists nothing and remembers nothing without refusing. The web layer
  reads it in one place, `coderModelRepository` (`assistantmodels.go`, over
  the coder managers), for the ring, the trigger form, the New coder dialog,
  the two settings pages and the `model-list` read alike, and
  `internal/assistant` knows nothing of lists. claude lists the four aliases (`fable`,
  `opus`, `sonnet`, `haiku`, always the newest of their family, there is no
  list command); opencode lists what `opencode models --verbose` prints,
  the one run answering the names and the context windows alike (the prompt
  bound of each model's metadata, `limit.input` where it names one and else
  `limit.context`, the bound opencode's own compaction check reads and for
  the github-copilot provider copilot's own `max_prompt_tokens`, is what an
  opencode turn's ring is measured against, `windowOf` in `models.go`, read
  by `reportUsage` in its `assistant.go` under the provider/model name the
  message record names, which is why the `contextWindows` table carries no
  opencode rows), fetched in the
  background and cached in the process for ten minutes, the next read past
  that starting a refresh, never run on the request path (`modelList` in
  `models.go`), every refresh under a deadline and an output cap
  (`modelListTimeout`, `modelListMaxOutput`, `exec.CommandContext`), so a
  hung `opencode models` is a failed refresh that keeps what stood and
  retries after the ten minutes, never a flag that stays set for the life of
  the process; the first fetch starts where the coders are registered for
  serving (`coder.WarmModels` in `runServe`), on the list capability alone
  and never on the conversation probe, because the list serves the New coder
  dialog too, which a coder without the conversation capability still gets;
  copilot lists `auto` plus the `recentModelIds` of
  `~/.copilot/config.json`, the comment lines before its JSON skipped, a
  missing file an empty list. Every select ends in Other…, which reveals a
  text field (`dc-model-pick`, the one element every form uses; the Other…
  entry carries the stored value so a page without JS posts what stands), a
  name typed there goes through `Add` on the save (`rememberModel`, on the
  ring, the trigger form, the New coder dialog and the settings pages) so it
  stands in every later list of that coder, and a stored value the
  repository does not hold (`Exists`) renders as the selected entry, never
  dropped. The empty entry is worded per level and always names what it
  resolves to (`modelDefaultLabel`, `coderDefaultLabel`): the coder page's
  Start pick reads Default (CLI), the Models tab's Chat pick
  Coder default (<start default>) or Coder default (CLI) and its Checks and
  Triggers picks Same as chat, the ring's Chat pick that same Coder default
  (<start default>) or Coder default (CLI), the chain below a chat pick
  being the start default and the CLI and nothing else, its
  Checks and Triggers picks Same as chat unconditionally, because that is
  exactly what an empty pick there does, a trigger's Assistant default
  (<resolved>) or Assistant default (CLI), and the New coder
  dialog's Default (<start default>) or Default (CLI). No help line stands
  under a pick; the one line a list gets is the repository's note, one short
  line per coder, and the Models tab's one line of its own stands over the
  coders. The assistant reads the
  list with `model-list [coder]` (`GET /assistants/models`, one row per name
  with its source, the defaults set for the coder, the note last, every
  installed coder without a name), which the generated instructions name
  next to `--model` on `coder-new` and on triggers instead of listing any
  name: a trigger passes it unasked, the cheapest the list offers, only for a
  task that prints a fixed sentence or answers NOTHING most of the time,
  every other trigger only on the user's own word. They say that the model
  is named to the user only when the
  started or added line names it, quoted from that line, which those lines
  do when `--model` set one the session or the reaction would not have run
  on without the flag (`startedLine`, `addedLine`, comparing the answered
  `model` with the answered `modelDefault`), and that otherwise nothing is
  said about models. What an assistant's own turns run on is read with
  `assistant-models-get` (`GET /assistants/models/resolved`, the caller's
  own three resolutions with their origin out of `ModelOrigin`, ring, Models
  tab, coder default or the CLI, and `sameAsChat` on the checks and the
  triggers), named under the reads in the instructions, and moved with
  `assistant-models-set --chat/--checks/--triggers` (`modelsSetForm`: the
  ring's own `form=model` post to `/assistants/<own id>`, the path built from
  `--as` and nothing else, only the named flags posted, `default` the empty
  field that clears a pick, the answer the ring's own sentence), named under
  the actions with the one rule a turn carries: set only when the user says
  so, or to clear a pick the coder rejected, and name the change quoted from
  what the command printed. No model
  ever stands in the instruction file itself: a changed file costs every
  assistant its cache. The user sets the
  ring's own three at the button beside the composer, which wears the icon of
  the coder it runs on and is a dropdown always
  (`assistant_new_button.gohtml` with `Models`; `assistant_context_ring_icon.gohtml`
  reuses `coder_icon.gohtml`'s own glyph, an svg kept as the ring icon itself
  where the coder's glyph is an svg (claude, opencode, its path data shared
  between the two templates), never a bare webfont glyph, which a font puts
  flush left in its advance with the baseline rounded a different way on
  every platform, so its ink stands up to half a pixel off the center of its
  own box, which the ring around it shows; a font icon (copilot) is wrapped
  in a span carrying the ring icon's own class and centering the same way;
  the ring svg is sized
  explicitly, `width` and `height` as `calc(100% - 2px)` at `top` and `left`
  1px, never by `inset` alone: a WebKit without the inset aware replaced width
  (an iPhone today) sizes an auto width svg from its containing block's whole
  width and drops the over-constrained right and bottom, so the ring stood
  38px wide one pixel right and down while a check of the centers within one
  pixel passed, which is why the e2e reads the box and its four gaps at half a
  pixel; its
  selects stand inside a `.dropdown-menu`, where Bootstrap's dropdown data api
  takes ArrowUp and ArrowDown on the document in the capture phase and puts
  the focus on the first entry, so `dc-model-pick` takes the two keys for its
  own select on the window in the capture phase, the editor sheet's way, and
  the browser's own stepping is the default action left alone): the
  Chat, the Checks and the Triggers select, and the coder's note under them.
  It holds no
  New with entries and no divider, so its label and its
  `data-assistant-new-label` read only This assistant's models; a new
  assistant is made from the list column's own button instead, which keeps
  the message-plus icon and the old label, New assistant, the memory comes
  along. A change posts
  `form=model` (`model`, `check_model`, `trigger_model`) to the assistant's own path,
  `Service.SetModels`, JSON with a toast for the page, a flash otherwise; the
  posting form stands beside the composer's own form and the selects reach it
  through the `form` attribute, because a form inside the composer's own form
  is dropped by the parser, and the Save button in the menu is the way
  without JS, hidden once the element runs. **Every open page of the assistant
  follows a pick without a reload**: `Service.SetModels` publishes the fresh
  picks as one `models` frame on that assistant's own stream and on no other
  (`FrameModels`, its load `ModelPicks`: the assistant's id, the three picks
  as stored and the stamp they were written at), the stream handler writes
  the same frame on every connect as the stream's own snapshot, so a pick
  that moved while a socket was down lands with the reconnect, and the
  save's JSON answer carries the same reading under `models`. `applyModels`
  in `assistant.js` moves the selects, for the assistant the surface shows
  and never for another (the id is checked on top of the channel), never
  from a reading older than the one applied (the stamp says which, so the
  page's own answer never puts an older choice back behind a newer frame),
  and never over a pick that holds the focus, which keeps its value and
  takes the fresh one when the focus leaves it; the page's own answer is
  applied over the focus, it is what was just picked, and a save going out
  drops what waited, because its answer is the newer reading. A trigger's form has the Model
  select after the task with the empty entry reading Assistant default
  (<resolved>), `trigger-new` and `trigger-edit` take `--model` (`--model
  default` clears it on an edit, the way `--until never` does, `modelField`;
  both helps say the assistant default, evaluated when it fires),
  `trigger-new` names the model in its added line only where `--model` set
  one the reaction would not have run on without it, the answered
  `modelDefault` being the owner's Triggers pick where one stands and else
  the chat, `trigger-list`
  prints `model <name>` on a row that sets one and the aside's fold shows it,
  and the instance reads (`/assistants/instances`, the header of
  `assistant-show`) carry `model`, `checkModel` and `triggerModel`. New
  assistants start with an empty chat model and the copied Checks and
  Trigger defaults, the ring is where all three are set afterwards.
- **A CLI that refuses to start a turn is a named refusal, and the cockpit
  owns the sentence.** `assistant.Refusal` (`process.go`, beside
  `ErrNotLoggedIn`, which is its login kind, so `errors.Is` against it keeps
  holding) carries a kind and what the CLI named, nothing else: not logged in
  and unknown model today. A parser answers whether it happened and never how
  it reads, each recogniser pinned on captured output: claude's assistant
  record with `error: model_not_found` decides in `Line`, and the stderr line
  `[claude-code:unrecognized_model] {"model":…}` names the model in
  `Diagnose` (`unrecognizedModel`); copilot's stderr line `Error: Model "x"
  from --model flag is not available.` (`modelRefusal`); opencode has none,
  it answers the same opaque `UnknownError` record for a bogus provider and a
  bogus model alike, so it takes the quote path below. `read` places the
  refusal on its run (`Refusal.placed`, through `failureOf`), which is what
  the sentence needs to say where to fix it: the ring button for a chat turn
  and a check, the trigger for a reaction on the trigger's own model
  (`RunRecord.TriggerModel`, written by `startReaction`), the ring again
  where the trigger followed the assistant's; the model the cockpit passed
  stands above the one the CLI echoed. **A refused check is no silence**:
  `Watcher.conclude` closes the job as BLOCKED at once through
  `refused_report.md.tmpl`, because the CLI will refuse again and a second
  silent check would only put the message off; every other failure keeps its
  one silent retry (`maxSilentChecks`, the job's own line after the first,
  `silent_report.md.tmpl` on the second). A refused reaction and a refused
  chat turn read the same sentence, under the pushed message and on the
  trigger's line, under the failed message. **What no recogniser names quotes
  the CLI once.** The frame stays the cockpit's sentence and `assistant.Quote`
  puts one line behind it, `The coder said: <line>`: the last non empty line
  of the stderr tail, or of the error record on standard output where a CLI
  reports there (claude's API error text or its failed result's own,
  opencode's `errorWording`), one line, redacted (`redact`: keys by their
  issuer's prefix or by their name, bearer tokens, a run of token characters
  longer than `MaxModelRunes`, the one bound both rules read, because the one
  long run a CLI line quotes legitimately is a model name, the directories of
  an absolute path) and at most `quoteRunes`; nothing said leaves the frame
  alone, a quote is never doubled, and an end the cockpit decided itself, the
  deadline and the size cap, quotes nothing. `sanitizeError` keeps the
  sentence and the quote as two bounded parts, and the redaction reaches the
  quoted line alone: a curated sentence is one line and the cut, never
  redacted, so the name in the refusal sentence stands whole. The one string
  a CLI supplies that lands inside a sentence, the model it echoed as unknown,
  goes through `assistant.UnknownModel`, cut to `MaxModelRunes` and dropped
  where `CleanModel` refuses it, and no parser builds the refusal itself. The
  rule at `ErrNotLoggedIn` is therefore read as: the cockpit owns the
  sentence, the CLI supplies at most a quoted detail behind it.
  **A coder session starts on a model too, and a resume never passes one.** A
  session burns context for hours, so the start is where the choice saves the
  most. `SessionStart.Model` carries it into the runtime, `StartOptions.Model`
  into `Manager.Start`, which cleans it with the same `CleanModel` before
  anything exists and refuses with that rule's sentence, takes the coder's
  start default behind an empty pick (`Manager.SetModelDefaults`, read on
  every start), and `StartResult.Model` says what the session came up on,
  the pick or the default, beside `AgentID`, which the create's answer
  carries as `model`; nothing else stores it, the agent is stored nowhere
  either. Each
  `StartCommand` appends its own flag when one is set, quoted like every
  value: claude `--model <m>`, copilot `--model <m>`, opencode `--model=<m>`
  in the equals form `--prompt=` takes, in front of either start shape, and an
  empty model is no flag at all, the CLI's own default, exactly what every
  session did before. `ResumeCommand` takes no model and never will: a resumed
  session keeps the model it has, and what `/model` set inside it must not be
  overridden by a flag the cockpit puts back. The New coder dialog carries a
  Model select per coder right after the Coder select and before the Agent
  block, the same shape
  (`data-coder-models`, hidden and disabled for every coder but the picked one,
  `dc-coder-select` switching both blocks together; the pick's `Disabled` renders
  the select disabled so a page without JS posts one `model`), built by the same
  `modelPick` over `coderModels` with Default as the empty entry, the list, then
  Other… (`dc-model-pick`) and the source line under it; one render,
  `handleCoderNew`, serves the page, the dialog, the projects board, the
  terminals area and the editor's terminal panel. It posts `model` to
  `/coders/new`, and `coder-new --model <name>` posts the same field, while
  `coder-resume` has no such flag.
- **The unsent message is a file of its own.** `instances/<id>/draft.json`
  through `assistant.DraftStore`, reached by the `Drafts` registry the way the
  jobs are: a draft is saved every time the typing pauses (200ms in
  `assistant.js`), and writing it into the transcript meant rewriting a thread
  that grows without bound, plus its index entry, for a keystroke. A draft save
  touches that one file and announces the `draft` event, nothing else. A draft
  written before the move is carried over on the first read, once, marked
  TODO(v2.0.0). Two saves can be in the air at once, so the answer of an
  overtaken one is dropped rather than moving this device's watermark backwards.
- **The list of assistants is sorted by hand.** `POST /assistants/order` takes
  the ids top first and `Store.Reorder` writes the index in that order: the
  index array **is** the order, so nothing carries a position and nothing can
  disagree with anything. A posted order is read as a permutation of the seats
  those assistants already hold, the way `applyTabOrder` reads the tab strip's,
  so an assistant the post never saw keeps its exact seat. A new one goes to the
  top, and an answer arriving in one moves nobody. The gesture is not a second
  one beside the strip's: both lists run `@dc/rowdrag`, whose defaults *are*
  the strip's behaviour, so a mouse drags a row from anywhere and a finger from
  the grip at the end of the row, behind the three dots, where the strip's
  grip stands. **Ctrl+Tab steps through the assistants** and wraps at both
  ends, the strip's own gesture (`stepAssistant`, the `pendingIndex` of
  `switchTo`, so mashing the key walks the list instead of bouncing between two
  rows while a page loads). It hangs on `dc-assistant` in the document's
  capture phase, so it is caught with the cursor in the composer, and it reads
  the rows of the page's own column (`.dc-app > .dc-ctx[data-assistant-rows]`),
  never the phone's sheet, which holds the same rows a second time. A row says
  the name, the coder, how many messages and when, and its badges keep the
  right edge whatever the name is: a title long enough to truncate must not
  carry them out of line with the rows above. The preview of the last message
  is not on the row, `Summary.Preview` and the `preview` field stay for what
  `dev-cockpit assistant assistant-list` prints. The column passes three things of its own: its grip
  (`[data-assistant-grip]`), its classes (`.dc-rows-dragging`,
  `.dc-row-dragging`) and `capture: "drag"`. That last one is not a taste:
  the row is a container with the link inside it, and a capture taken on the
  press retargets the click that follows to the row, so every click that opens
  an assistant would be swallowed. Taking it when the drag begins releases the
  grip's **implicit touch capture** and fires `lostpointercapture` before the
  row has moved a pixel, so only the capture the drag itself holds may end a
  drag (`event.target === drag.row`), or no finger ever sorts anything.
- **Nobody sweeps the assistant's disk but the startup sweep.**
  `Store.SweepOrphans` removes an instance directory the index does not list,
  workspace included, invisible from every surface and collected by nothing
  else. It reads the index itself instead of through the store, and it sweeps
  only when that read produced entries: a corrupt state file is quarantined as
  `<path>.broken` and reads as absent afterwards, so a sweep trusting an empty
  read would delete every assistant on disk the one time the index cannot be
  parsed. The check session sweep at startup asks `Workspace.IsWorkdir` whether
  a session ran in some assistant's workspace, whoever that assistant was.
- **An assistant knows who it is from its own instruction file.** The
  generated `CLAUDE.md`/`AGENTS.md` in an instance's workspace are that
  assistant's, rebuilt from the memory right before every turn of its
  (`Workspace.Prepare`, through `preparingRunner`) and by the memory page for
  every assistant that has a workspace: they carry its id, its workspace
  path, the memory, and every cockpit command through its wrapper. The name
  it is called is deliberately not in them: the id is what every path into
  `assistant-files/`, every reaction's prompt and every `assistant-show` of
  its own is built on, while `assistant-list` marks its own row with a star,
  so a turn recognises itself without a name and never needed one to act
  (dropped 2026-09-21). Nothing is said in a prompt, so the transcript keeps
  showing what the user typed, and a check reads the same file because it
  runs in the same workspace.
- **A cockpit command is `./cockpit`, the wrapper.** Every workspace carries a
  generated executable, `<workspace>/cockpit` (`Workspace.Wrapper`, written by
  `Workspace.write` with the instruction files, so it is rewritten before every
  turn): `exec <binary> assistant --state-dir … --projects-dir … --as <id> "$@"`.
  The instructions and the check prompt (`Watcher.cockpit`, through the
  `cockpitNamer` the workspace implements) spell every example as `./cockpit …`
  (`shortCockpit`), the script in the directory the turn starts in, and name
  the absolute path exactly once, with the sentence that it is the spelling for
  a turn standing in another directory, which happens all the time and often in
  the same line as `cd <project> && …`. They name nothing else, so a check
  reads one spelling of a command and a turn cannot drop or misspell a flag. A
  caller without a workspace to ask (the plain `wakePrompt`) falls back to
  `dev-cockpit assistant` and names no path.
- **A check runs in its assistant's workspace and reads the assistant's own
  instructions.** No directory and no shorter file of its own: every ability a
  conversation has, the files it can hand over, what it knows about the user,
  is one a check may need for its report, which goes to the user like an
  answer does, and every ability struck from a check would have to be written
  back in one by one, the next gap noticed only when a job dies of it
  (decided 2026-09-16 after both had been built and taken out again).
- **A flag the prose requires stands in the example block too.** The
  instruction file is read by a model that copies an example and fills in its
  task, not by one that reads a paragraph and composes a call from it. So a
  flag the prose requires, `--name` on every trigger and `--once` on a
  schedule meant to fire once, carries its own example line; one the user's
  own words decide, `--until` and `--tz`, does not. A flag whose **value**
  carries a requirement the call cannot check is written out whatever decides
  the flag itself: `coder-new --then`'s sequel task wakes hours later in a
  session of its own, so it names the project, what to start and where the
  report goes, and one that does not is taken and breaks then, while the
  `--done-when` that same flag needs is refused on the spot and corrects
  itself. The example carries the value, not the placeholder.
  Moving a detail into a `--help` is only a saving where the detail is one
  nobody needs before the call: what is needed every time and shown nowhere is
  paid for twice, once as a failed call and once as the `--help` that follows.
- **The texts the cockpit writes for an assistant are templates.**
  `internal/assistant/templates/*.tmpl`, text/template files embedded by
  `templates.go` and filled through `render`: the instruction files, the
  wrapper, the check prompt (`wake_prompt.md.tmpl`,
  it also sends a check to the project's own instruction files when a
  criterion touches conventions or gates, naming no file list because the
  names differ per project and coder, and it draws the line on long runs: a
  suite, an e2e pass, a build are the coder's work, the check judges the
  report and verifies cheaply, a criterion that needs a long run goes to the
  coder as WORKING with what was sent, and a verdict comes in time whatever is
  still open; the assistant's own instructions say the same from the writing
  side, long runs into the coder's task with the proof in the criterion) and
  the three reports a check ends in. **What a check does with what it found
  is that prompt's alone**: the verdict forms, which of them reach the user,
  what a check may start and how it moves a coder stood in the instruction
  file as well, which every turn and every reaction pays for while only a
  check ever acts on them (dropped 2026-09-21). What stayed there is what a
  chat turn decides on, that steering buys a check which gets the coder going
  again and answers with a verdict, and that a steered coder is the
  assistant's to write into until its job closes. They read like text and are edited as
  text; the Go side hands over typed data (`instructionsData`, `wrapperData`,
  `wakeData`, `reportData`) and nothing else. A paragraph stays on one line in
  a template, so a pinned sentence in a test matches the file. The
  instructions ride along in every turn and every check, so they carry the
  rules and not the reference: what a command's flags do is in that command's
  `--help`, the instructions point there, and `TestTheHelpCarriesWhatTheInstructionsDelegate`
  in the cli package pins that the help really says it, so the pointer never
  leads nowhere.
- **Who is calling on the local socket is a different question from whether
  they may.** Reaching the socket is the whole credential and `localCall`
  answers that and nothing else. Which assistant is acting is the `--as <id>`
  flag on the assistant command group, handed to `localapi.Dial` and sent on
  `localapi.AssistantHeader` by every local API request, so a command run by a
  turn says who it is and one typed by a person says nothing. It is a flag and
  not the environment because the workspace's wrapper carries it into every
  call, so it is never remembered by a model, it stands on exactly the call it
  belongs to instead of on every shell below a turn, and it reads in every log
  line; it is `--as` because `job-list --assistant` already means whose jobs. `Server.callingAssistant` checks the id against the assistants
  that exist, so a stale id reaches nothing, and everything that has to be
  charged to somebody refuses on an empty answer rather than picking one, with
  a sentence that names the flag (`assistantCallerRefusal`).
- **The one conversation layout migrates once.** A state directory holding the
  shape of the last release (`assistant/conversations/`, `jobs.json` and one
  shared `workspace/` with the memory in it) is moved at startup by
  `internal/assistant/migrate.go`, and that is the only shape it reads: the
  conversation that was live becomes the first assistant with the jobs and
  with the whole shared workspace as its own (the files every conversation
  wrote, its own uploads flattened into `user-upload/`, the generated files
  dropped), the memory moves up to `assistant/memory/`, and every other
  conversation is deleted with the directory that held them and its uploads:
  each had lost its provider session when it was archived, so nobody could
  ever answer in one again, and a hundred threads nobody can answer in would
  make the one list that matters unreadable. A link an old answer carries
  (`assistant-files/x`) lands on the moved file because a link is read in the
  workspace of the assistant whose answer it is; an attachment is found by its
  name in that assistant's upload folder (`attachmentPath`), never by the
  absolute path the transcript stored. The backup keeps the three source names
  of the release (`conversations`, `jobs.json`, `workspace`, TODO(v2.0.0)) so
  an old archive still lands in the layout this move reads.
- **The area is `/assistants`, and the old subtree keeps landing.** Every
  address under `/assistant` answers 308 to its plural twin
  (`movedAssistantPath`, TODO(v2.0.0)), because a stored push message and a
  notification entry point at `/assistant/<id>`; `/ctx/assistant` does the same
  for the phone's sheet. Nothing in the old subtree answers in place, not even
  `/assistant/jobs` and `/assistant/conversations`, the paths the released CLI
  called over the socket (removed 2026-09-17): no released CLI can reach this
  server. A turn runs the binary on disk, through the workspace's wrapper or
  by the absolute path the released instructions spelled, and a self update
  replaces that file in place, so after the restart every command a turn runs
  is the new binary calling the new paths; the old image lives on only in the
  server process that is being replaced, and it calls nothing. What that
  window costs is a check instructed by the old binary: its commands run the
  new one without `--as`, are refused where an owner is needed, and the
  standstill rule ends the job as BLOCKED once, at the update. The area is
  plural everywhere it is named, like Projects and Terminals,
  because several things live behind it; the page title of one open assistant
  ("Name - Assistant") and the `dev-cockpit assistant` command group stay
  singular, they name one.
- **A note is the third message kind: the cockpit speaks.** Next to the
  user's and the assistant's messages a thread holds notes, `RoleCockpit`
  with a `Note` (source, headline, verdict where there is one, the body in
  `Content`). A check's report is the one source (`NoteCheck`, written by
  `recordWake`). The old `wake` key on a report is read once on load and
  becomes a note (`Store.load`, TODO(v2.0.0)). **The stripe says who spoke
  and whether anybody asked**, and it means one thing each: blue the user,
  purple an answer to what the user asked, grey the cockpit unasked. So the
  grey covers a note and the answer a reaction pushed alike, which is the one
  condition `assistant_message.gohtml` reads, `.Note` or `.Auto`, the pair
  `replaceMessage` in `assistant.js` already holds back as one kind. Purple on
  both would cost the stripe the difference between an answer and a message
  nobody ordered, which is the only thing it still says on its own. **A note
  and a pushed answer read the same way**, one shape in
  `assistant_message.gohtml` for both: a header that says who spoke and
  carries no switch (the badge, the headline, the time), under it one control
  that is the chevron and a preview of the text together, and the message
  folded below it, closed until somebody opens it. The switch used to stand
  on the right of the one header and under the other, and one of the two
  arrived open, which is two things to learn for one kind of message. **The
  speaker is one button in one place**, the `assistant_speaker` template both
  headers call, and it hangs on the audio URL alone. What can be read aloud
  is `assistant.Message.Speakable`, not the user's, words in it, finished:
  the view sets `AudioURL` from it and the audio route asks the same question
  again for a page from before a settings change, so the button that stands
  and the route that answers cannot disagree. An origin decides what the
  header says, never whether a message can be spoken, and a check's report
  therefore speaks like an answer. **The preview is as long as the push
  carries** (`assistant.PreviewRunes`, 140,
  cut by `markdown.Excerpt`, the very line `answerExcerpt` builds for the
  notification), so what brought a reader here is what stands in front of
  them when they arrive; it folds the body's own line breaks into one run of
  words, because one line of a report is half a sentence as often as not
  while three are usually the result and its reason. A message the preview
  holds whole folds nothing, there is nothing behind it, and a pushed answer
  is no exception: **under the header stands the result and nothing else.**
  That nobody asked for it is what the bolt and the name over it say, the
  occasion is that very headline, and the task is the user's own words on the
  trigger, where they are read; one of those lines said it twice and the
  other was a wall of the user's own text on every unfold, thousands of
  characters on a chain of jobs. So `Note.Task` stays in the record, it says
  which task this reaction really ran with and an edit may have moved the
  trigger's since, and nothing renders it. The check's report always read
  this way, and the reaction was pulled onto it (2026-09-21).
  While an answer streams the page holds every arriving note, and every
  answer a reaction pushed, in a bar above the composer (`holdNote` in
  `assistant.js`, decided at the frame, two headlines, from three on the
  count with an unfold) and lands them under the finished answer in arrival
  order without scrolling (`landHeld`, the pin masked for the frame the
  landing lays out, the reader unpinned and pinned again by the next answer
  within `repinIfNear`'s distance). A row of that bar is a fold and paints
  nothing under a pointer: the one rule it carries puts Tabler's hover and
  active button variables back onto the resting ones, the pressed shadow
  included, because a headline that greys out under the finger reads as a
  place the reader is being sent to while it only unfolds in place. What it
  keeps is the focus ring, `:focus-visible` alone, which is the keyboard
  saying where it stands and no pointer's doing. The transcript keeps
  chronological order, a reload mid stream shows it. The composer is the user's alone: Send and
  Stop are what they are on master. A badge in the thread that shows an
  icon alone names itself with `aria-label`, never with a visually hidden
  span: that span is absolutely positioned while the thread's scroller is
  not, so it stands outside the scroller, the app grid grows to the
  transcript's height, and the hash scroll of the show earlier link then
  moves the whole page by the head's height under an unchanged scroller. What the cockpit wrote since the
  assistant's last chat answer, notes and pushed answers alike, rides into the
  next chat prompt as a short summary (`notesSince`, `withNotes`): the count,
  a pointer at `job-list` and `assistant-show`, and one headline per line,
  newest first, never the text of anything. The headline is the one a note or
  a pushed answer's origin already carries, nothing is formulated a second
  time, and a reaction whose turn broke off says so on its line, because that
  is the one an assistant must not read past. Equal headlines fold into one
  line with a count, which is what keeps a schedule firing every half hour out
  of the prompt, and `maxNoteLines` bounds the list at fifty with the rest
  summarized the way `status` summarizes its older coders. A bare count was
  what stood here, and it carried nothing an assistant could decide on: it
  either read every job to find out or guessed. The walk stops only at a chat
  answer, never at a pushed one.
- **An assistant reacts to events in a session of its own, and every bound
  is enforced.** `CockpitEvent` is the one small type a source publishes
  (source, kind, target, time, headline, body, plus the owner for a job's
  event); the sources are a job's end (published by `recordWake`), a coder's
  signal out of the notify inbox (`notify.SetEvent` hands the raw hook name
  on, `Reactor.Coder` reads Notification as asks and everything else as
  ended, a bell included) and a cron tick (`cron.go`, five crontab fields
  parsed here, no dependency).
  **A schedule is a wall clock somewhere, so it carries the zone it is read
  in.** An IANA name on the trigger (`Timezone`, `Trigger.Zone`), never an
  offset, which carries no changeover rules and is wrong for half the year,
  and never `time.Local`, which means a different schedule on every host. It
  is resolved when the trigger is written and stored on it, always, so moving
  the default later moves nothing that already stands; the zone database
  travels in the binary (the blank `time/tzdata` import in `cron.go`, pinned
  by a test), because a slim image carries no `/usr/share/zoneinfo` and every
  name but UTC would be refused there. Three sources in order, and the surface
  resolves them because the settings store is its: what the caller named
  (`--tz`, the form's field), the stored default (`assistant-timezone`,
  `AssistantTimezone`), the zone this server runs in (`ServerZone`: `$TZ`,
  then what `/etc/localtime` points at, then UTC, because `time.Local` answers
  "Local" and a name nothing loads is no name to store). **Only an explicit
  choice moves the stored one**, the form's select and `timezone-set`, never
  `--tz`: in the browser a person picks a zone and sees the field, while a
  turn picks one out of a sentence on the user's behalf, and a choice somebody
  derived must not become everybody's default. Drawing that line with its own
  verb puts it in the tool instead of in a rule somebody has to keep.
  **Daylight saving is taken as the wall clock hands it over, never worked
  around**, which is what a crontab does and what `Next` does: a local time
  the spring changeover skips exists on no clock that day, matches no minute,
  and the schedule falls out once; an hour the autumn changeover repeats
  exists twice, matches twice, and it fires twice. Both are pinned by a test.
  What a turn reads about it is `trigger-new --help`'s: the instructions carry
  the rules alone, which zone each verb is for, never to invent one and to
  quote the zone and the next tick from the command's own answer, and that
  last one forbids working a time out, so a changeover is a lookup for a
  question and never a step in one.
  That holds only because `skipTo` collapses the minutes a month, a day or an
  hour cannot match **while the zone stands still** and takes its ordinary
  minute where the offset at the target differs from the offset here: a wall
  clock built with `time.Date` picks one side of a changeover without saying
  which, and landing on the later side of a repeated hour steps over an hour
  of minutes the schedule may match. **The zone is always shown**, never only
  where it differs from the reader's own: it stands on the line with the
  crontab fields (`Where`, the row and `trigger-list` alike) and the next tick
  is written out by the server in that zone with the zone named
  (`triggerNextText`, `zonedStamp`), never handed to `dc-time`, which answers
  in the browser's zone. A zone that is sometimes there carries meaning by
  being absent, and that is read wrong. `trigger-new` and `trigger-edit`
  answer the zone that was applied and the next tick in it, and `trigger-list`
  says which zone is in force and whether anybody stored it, because a
  crontab expression plus a zone name is arithmetic a person and a model get
  wrong around a changeover, with conviction.
  **Two kinds are umbrellas**, and
  `umbrellaKind` is the one place that says which: `job-closed` takes every
  way a job ends, `coder-news` every signal a coder sends, whichever of the
  two that classifier read it as, and `Trigger.matches` asks for the
  exact kind or that one. **A coder has that one event and no narrower one.**
  The classification reads the hook name and does not hold everywhere: claude
  and opencode carry one, copilot has no hook path at all, only the terminal
  bell, and a bell is read as ended whatever it rang for. A trigger on ended
  alone would therefore be a promise the cockpit cannot keep for every coder,
  so `EventOptions` offers the umbrella and nothing under it, and the two
  narrow kinds stay what they always were, the reading an event carries. The
  way back to a choice is a bell classified from the coder's own record
  instead of a hook name it never had. Nothing of the difference is lost to a
  reaction meanwhile: a published event always carries the kind it was
  classified as, never the umbrella, so the headline still says which of the
  two it was. It is called news and not signal because news is the word the
  cockpit already uses for this event where the user reads it, "Coder has
  news", while signal is what the mechanism below passes around. A `Trigger` belongs to one assistant like
  a job, stored in `instances/<id>/triggers.json` through
  `internal/statefile` (so it rides in the backup's instances source and goes
  with the delete), and carries its filter, its task and its bounds: once or
  standing, an expiry nobody has to give (none by default, so a trigger stands
  until it is removed: eight hours were the default once and they took an
  alarm set for the next morning away before it could ring), a batch window
  (30s, none for cron, which `applyTrigger` clears on every write because a
  tick is never seconds from the next one, and the surfaces say where somebody
  named one anyway, the CLI in its answer and the form by taking the field
  away). **A name is optional and it is the row's heading.** `Name` is the
  user's own word for a trigger, at most `MaxTriggerNameRunes` (32, the same
  32 a coder's label is cut to; the head line leaves the name 330px in the
  420px aside, 290px in the 380px one a 1280 window gives it and 275px in the
  phone's sheet at 390px, over 40 runes of prose at the row's 14px even there,
  so the cap is the one number and not the width), refused rather than cut because a name a
  person typed is theirs, and collapsed to one line because a heading is one. With one the row reads by it and the event, the
  target or the schedule moves to the line under it, where a steered coder's
  row carries its project, so a schedule stops reading by `*/30 9-17 * * 1-5`,
  which says when it fires and never what for; without one that line is the
  heading and everything is exactly as it was before names existed, which is
  why there is no migration. It is one element either way, marked
  `data-assistant-trigger-event` wherever it stands, and one reading decides
  what the row, the remove confirm and every answer call it
  (`AssistantTriggerView.Heading`). The cockpit itself derives a name from
  nothing but the coder a sequel waits for (`FitTriggerName`):
  a trigger nobody named has none, the form's first field says optional, and
  the empty value is a value, so the page posting an emptied field clears the
  name while a request without the field leaves what stands (`Name` with its
  `NameSet`, the way `Once` and `All` carry theirs, and `Model` with its
  `ModelSet` the same way, see the model rule above). **The assistant is told
  to write one anyway**, two or three words for what the trigger is for, in
  the generated instructions and nowhere else, `trigger-new --help` carrying
  the cap alone: that is a decision for whoever writes the trigger and a rule
  rides in every turn, while a help is read when somebody asks for it. The
  rule stands under the example block and the first example carries `--name`,
  because a block without it writes a nameless trigger at the one place a
  turn copies from. It is
  never a field the code fills in, and it is there because the name is the one
  line the row, the notification and the line in front of the next chat prompt
  all read a trigger by, while
  `*/30 9-17 * * 1-5` says when it fires and never what for.
  **Below the page the name travels in the note, and one line decides it.**
  Everything a fire leaves behind is read off the origin `Note` the fire
  builds, so `Reactor.fire` puts the name into `Note.Headline`, the field
  whose whole job is to be the one line a note is read by, and moves what
  fired it into `Note.Event`. The notification and its push
  (`assistantNews`), the header over the pushed answer and the line the next
  chat prompt is preceded by (`notesSince`) therefore take the name without
  any of them knowing that names exist, and the `--contains` search keeps
  searching what stands on the screen. Where there is room for the name and
  the occasion both, the reader asks for it: `Note.Occasion` (the event, or
  the headline where no name pushed it out) is what the reaction's own prompt
  is built from, because a turn has to be told what happened and not what the
  trigger is called, and what the trigger's own note line says, because that
  line stands under the row's heading, which is the name already, which is
  why `trigger-list` renders both. The thread renders neither, see the note
  rule above. Without a name `Event` is empty, `Occasion` is the headline,
  and every surface reads exactly what it read before names existed. **A trigger waits for terminals, not for one terminal**:
  `Targets` is the list (`TriggerTarget`, the id plus the name it had when
  the trigger was made, because a deleted terminal has none left to look
  up), none of them is any of them, and `All` turns several into a barrier, one
  turn once every one of them arrived, with the batch window folding their
  events into it (`Waiting`, the per target `Met` taken back with the events it
  spends, so a standing barrier waits for all of them again). A barrier belongs
  on `job-closed`, which the instructions say and nothing enforces: on
  `job-done` a job that closes blocked never arrives. What it would otherwise
  lose is the race at its own creation, three coders started one after another
  and the first finished before the third exists, so `seedArrived` takes the
  jobs of its targets that are closed already, through the same `jobEvent` the
  recovery publishes, report included. `Target` and `TargetName` are read once
  out of a file written before the list and dropped by the next write
  (TODO(v2.0.0)). The `Reactor` (`events.go`, one per service) matches, collects
  inside the window, and fires a reaction: a run of `RunReaction` through
  `startOwnSession`, the same path a check takes (`startWake`): a fresh
  provider session reserved and dropped with the turn, the owner's workspace
  and instruction file, the wake slot (`Service.slots`, shared with the
  watcher), the run register with the origin on the entry, so
  `Service.Recover` follows it on and the reactor concludes it. Nothing is
  written into the owner's chat session for an event, no user turn, no
  envelope, no queue. The prompt is the event plus the task, the cockpit
  speaking (`reactionPrompt`), and it carries the rule a check's prompt
  carries about its first line, for the opposite reason: a check may think out
  loud because `parseVerdict` throws away everything before the verdict, while
  a reaction is pushed as it stands and only a sentence or two of its first
  line reaches a phone, so the result comes first and an announcement in front
  of it eats the message. It says too that `DONE`, `BLOCKED` and `WORKING`
  belong to a check, because the reaction reads the assistant's own
  instruction file, which says a check answers with a verdict. It names its deadline the way a check's prompt names the same
  one, and for a sharper reason: a check that runs into it has a next check
  to carry on, a reaction has nothing and reaches the thread as one that
  broke off, so a task it cannot finish is answered with what it has and what
  is still open. Its answer is pushed into the owner's thread
  (`pushReaction`) as an assistant message with `Auto` and `Origin` (the
  event's headline, the task), rendered in the note's own shape, see the note
  rule above, announced on a frame of its own so a streaming page holds it, and it rings
  as what it is: the title says that a trigger fired, the line below it names
  what fired it and carries the reaction's own answer
  (`assistantNews`), because nobody asked for this answer. **A turn that broke
  off goes that very way**, the same message, the same notification, the same
  push, marked as the turn it was (`activeRun.turnState`, the reading a chat
  turn gets: `StateInterrupted` where a restart took it, `StateFailed`
  otherwise), carrying what it had written before it stopped, which
  `reactionOutcome` keeps for it, and the sentence that says why under it. A
  failure on the trigger's line alone is a failure nobody sees, the next event
  overwrites that line, and "Trigger broke off" in `assistantNews` is the title
  it arrives under. An answer of NOTHING pushes nothing and
  notifies nobody, and what finds that word is the check's own `parseVerdict`
  and never a second reading (`quietAnswer`): a reaction that thinks out loud
  and writes its NOTHING behind the preamble decided what a bare one decided,
  and two readings of that one habit drift apart, which is how a reaction that
  had decided right rang the user anyway. What keeps a real answer out of it is
  that reading's own guards plus what is left over, because text behind the
  word is an answer to push, and a turn that wrote nothing at all wrote no
  contract. The writing side of it stands in the generated instructions, that a
  task wanting a trigger to speak only in the exceptional case has to ask for
  NOTHING by name, because the contract stood in the reaction's prompt alone
  and whoever writes the task never reads that. A turn that broke off is never
  read as that contract; a
  an expiry shows on the
  trigger's line and state alone, never in the thread; the trigger
  counts every fire, whatever came back, and says `reacting` while the run
  is on. **One trigger reacts once at a time.** While its reaction runs
  the events stay in its window, whatever the window says, and the end of
  that reaction spends them as one turn: two reactions of one trigger
  would answer one thread about the same thing twice and out of order. What holds the
  window open is `ReactingSince`, cleared where a reaction ends (`conclude`,
  `adopt`), so a held window always has an end that reaches it, and the tick
  is its backstop; the window is on disk with everything else, so a process
  that dies mid reaction leaves it for the next one. The page and the CLI
  (`trigger-new`, `trigger-list`, `trigger-edit`,
  `trigger-delete`, `timezone-get`, `timezone-set`) share
  `/assistants/triggers` and `assistant.EventOptions`, so nothing is
  page only; which zone is in force is its own reading (`?timezone=1`),
  because a caller that only wants to know which clock a schedule would be
  read on must not pay for the whole list to find out; the generated instructions list every kind with an example and
  say that the task must be self contained or point to a file in
  `assistant-files/`.
  **A standing trigger is changed, not made again, and a field nobody
  names does not move.** One reading of the form decides that for both ways
  in (`assistantTriggerSpec`): a field the request does not carry is one
  nobody named, which on a create takes the default and on an edit leaves
  what stands, so the page posts every field and `trigger-edit` posts
  the flags `cmd.Flags().Changed` says were typed. Two of them have no zero
  that could say "no": a multiple select with nothing picked and an unchecked
  box post nothing at all, so the form carries a hidden empty `terminal` and
  `once` in front of them and clearing is a value, not an absence. One
  function then writes a trigger whichever way in the caller took
  (`applyTrigger`, which `newTrigger` runs over an entry seeded
  with the defaults and `editTrigger` over the one that stands), so
  there is one validation and one meaning of every bound. `Reactor.Edit`
  holds the reactor's lock, the one a fire and the tick take, and answers the
  line naming what moved (`triggerChanges`), so the CLI's output and the
  page's toast are not written twice. What never moves is the event, another
  event is another trigger; what an edit never touches is the count, the
  moment it was made, the events waiting in the window and a running
  reaction, which keeps the task it was handed. The targets are replaced as a
  list and each one keeps what it reached (`mergedTargets`), so a barrier
  goes on waiting for the ones that have not arrived, a target named for the
  first time is caught up by `seedArrivedLocked` where its job is closed
  already (which skips one that arrived, or an end would land in the window
  twice) and checked by `nameJobTargets` like a fresh one, while a target
  that stood is left alone, its job may well have closed since. A schedule
  that moved works its next tick out again, one nobody touched keeps it. A
  trigger that is done or expired is spent and is refused with a
  sentence. On the page it is the create dialog's form rendered on the stand
  it opens, see the form rule below: Change stands on a row only while it
  still fires, the form posts to the same path with `form=edit`, the event
  select is locked and therefore posts nothing, and the expiry opens on what
  the trigger has left, because what is stored is a moment while the field
  asks for a span. **The sequel of a job is wired in the call that starts
  it**, `coder-new --then "<task>"` (the `then` field of `/coders/new`, beside
  `done_when`): one job-done trigger with `--once` on the new terminal,
  made in that same request right after the steer, and the answer names it. It
  expires with that job (`Until: time.Until(job.ExpiresAt)`, the expiry the
  steer one line above just wrote), never on the trigger's own default: the
  two defaults sit in two files knowing nothing of each other, and the day one
  of them moves the chain would die in silence, an expiry showing on the
  trigger's line and never in the thread.
  Nobody types a name on that way in, so it takes the name of the coder it
  waits for (`assistant.FitTriggerName`), and a session name longer than a
  trigger's name may be leaves it without one rather than refusing the sequel:
  no name is the fallback every trigger has anyway.
  Without a `done_when` it is refused, a sequel hangs on a steered job.
  Deferring the arrangement to a later call loses the race, a job can close
  before the second call goes out, which is what a handover must not do. A
  check is no handover: it is one narrow turn with a verdict and carries
  nothing on, and the instructions say so.
  **A deleted terminal is the last thing it ever does, and one path clears up
  after it**: `Watcher.TerminalDeleted` (what every surface reaches through
  `Server.jobDeleted`, the page's button, the chip, the pane, the editor's
  panel, `coder-delete` and a project delete's purge one terminal at a time).
  An open job is closed first, the way the heartbeat closes one whose terminal
  vanished, with the reason that the coder was deleted, so `job-closed` and
  `job-expired` fire and `job-done` does not; then the entry goes, and
  `Reactor.TerminalGone` marks the target gone in every trigger that names
  it, fires a coder trigger once for the deletion (`Kind` is the
  trigger's own, so a `coder-news` trigger hears it exactly once and no
  phantom event is published beside it), and removes
  the ones with no terminal left (`Vanished`), spending their window on the
  way out because
  nothing can come from a gone terminal. A barrier counts a gone target as
  arrived and the event says it was deleted, not finished. Triggers
  without targets and cron are untouched, and `coder-stop` clears up nothing at
  all: the session keeps its identifier and comes back under it, so its
  arrangements stand, and the heartbeat's own vanish path is unchanged. What
  fell is one sentence, `assistant.DroppedNote`, and every surface says that
  one: the flash, the `dropped` field of the delete's JSON answer (appended to
  the toast by `alsoDropped` in `@dc/steer`, so no surface writes a second
  wording) and what `coder-delete` prints. A project delete goes the same path
  and reports nothing, its answer may leave before the purge runs and it
  cascades into worktree projects, so one number in it would be short as often
  as right.
- **A turn's answer is blocks, and the seam between two of them is read, never
  guessed.** An answer that works with tools arrives in several text blocks, and
  every runner hands them over as one stream of deltas the turn appends as it
  goes, so a runner writes `assistant.BlockSeparator` between two of them or the
  last word of one is welded onto the first of the next. Where that boundary is
  comes out of the provider's own output and out of nothing else: claude has the
  content block records (`content_block_start`, and the assembled message's
  content is a block per entry), copilot moves the `messageId` on every record
  of a new message. Nothing looks at the text that is already there, no last
  character, no punctuation, no suffix check. It is written only between two
  blocks, never in front of a turn's first and never behind its last, so the
  stored answer keeps its own ends, and a provider version that stops naming its
  boundaries falls back to the plain appended answer.
- **The streamed tail stands where the renderer put the mark.** A streaming
  answer is the prefix the server rendered plus the raw text that arrived
  since, and only a Markdown parser knows whether that text continues the open
  paragraph, list item or code block. So `publishRender` renders the prefix
  with `assistant.RenderMark` behind it (a word joiner), and `assistant.js`
  puts the tail span where that character came out, cutting it away. A mark the
  render swallowed (a table row, dropped raw HTML) leaves the tail behind the
  prefix, where it stood before. The page parses no model output for this, and
  hanging the tail behind the markup is what made a sentence still being typed
  read as two paragraphs.
- **A picture in the transcript brings its own ratio, and the three places that
  write one write it the same way.** `filesystem.ImageSize` answers with the
  size the browser draws the file at, which for a photo out of a phone is not
  the size in the frame header: the Exif orientation may put it on its side,
  and a browser turns it before it draws it. That size goes into the markup
  twice, as the `width` and `height` attributes and as `--dc-media-ratio`, and
  never one without the other, in all three writers (`internal/markdown`'s
  transformer and its own renderer, `assistant_message.gohtml`, and
  `renderAttachment` in `assistant.js`). The attributes hold the box open
  before the file arrives, the property is what lets the stylesheet write the
  height cap as the width that cap allows: the attribute width is a width the
  layout may no longer choose, so a plain `max-height` would clamp the height
  alone and draw the picture out of shape. A picture nobody could measure
  carries neither, and both sides stay auto, which is the one case a browser
  keeps the ratio by itself. Everything the thread shows wears
  `dc-assistant-media`, goldmark's own image included, because that class is
  the only thing that keeps a picture inside the conversation. The thread has
  no width of its own, it reads across the whole work column, so the two caps
  on that class are what keeps a picture an attachment inside a message rather
  than the message: `--dc-media-width` and `--dc-media-cap`, width and height,
  and both travel through the same `min()` as the ratio, never as a bare
  `max-height`.
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
  cheap facts on the projects page keep coming from `internal/project` over
  `internal/gitfacts`, the passive half of the split: gitfacts reads `.git` as
  files and starts no process, `internal/git` runs the binary, and the two do
  not meet. The create form is the one git write outside the editor: a project
  made as a worktree of another one (`projectworktree.go`) takes its directory
  from the same `project.Repository.Create` every project is made with and
  fills it with `git.AddWorktree`, holding the editor's own write lock
  (`gitWriteKeys`) around both steps, so it never runs beside a checkout or a
  commit in that repository, and a git that refuses takes the empty directory
  with it. Its resync is the second one
  (`POST /projects/:name/fetch`, `handleProjectFetch`): the editor's explicit
  fetch itself (`fetchRemotes`, `git.Repo.Fetch` behind the write lock and the
  askpass bridge) on a path of its own, because a branch nobody fetched is a
  branch the pickers cannot offer, and a remote that asks for a passphrase has
  to reach the one app-wide dialog. Both project scoped routes of that form,
  the fetch and the branch read, sit **off** the editor group: the create form
  is no editor and its rounds must not count as somebody working in that
  project. One route answers one round:
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
  token behind. What a save writes is the buffer as it was **read**
  (`editor.snapshot`), handed back to `markSaved` afterwards, and dirtiness is
  asked again: typing on while the write is in flight would otherwise mark the
  newer text as saved and lose it.
- **Autosave is one trigger and decides nothing.** The setting is the
  install's (`editor-autosave`, Settings, Editor, Files, on unless switched
  off) and rides into the page like the diff limits, so an open editor takes
  it on its next load. The trigger is the pause after the last change
  (`AUTOSAVE_DEBOUNCE_MS`), nothing else: a save on a lost focus or on a page
  going away is a path a phone reaches unreliably and a test cannot hold
  still. It runs the same `saveTab` a person does, with `ask: false`: a
  refused write asks nothing, writes nothing and marks the tab the way the
  disk watch marks one (`stale`, `missing`). That mark is also the brake, a
  marked tab is skipped, because its version stays stale until somebody saves
  or reloads it by hand. It leaves alone what the save button leaves alone, a
  comparison and a file from outside the project, and writes one file at a
  time.
- **The editor follows the disk, and the scope of that is what is on the
  screen.** The `git` event is not enough and cannot be made enough:
  `Fingerprint.Worktree` is a hash over `git status`, which moves when a file
  is created, deleted or modified for the **first** time and stands still when
  an already modified file is written again, which is the everyday case here
  because a coder writes into the same open file for an hour. Ignored files
  and projects without a repository never move it at all. So a second watch
  asks the disk, and it asks only about what somebody can see: the open tabs
  and the unfolded folders, sent by the client (`POST .../editor/watch`, JSON,
  `client` plus `files` and `dirs` as `{path, token}`, the project root as the
  empty path), because the client is the only party that knows. The server
  holds the **union** over every client of a project (`fileWatchers`),
  refcounted like `gitWatchers`: two browsers on one project are one tick over
  both their scopes, a renewal replaces that one client's scope whole so a
  closed tab leaves the union, and the round after the last window lapsed is
  the round that ends the tick. Every client needs a name of its own or two of
  them overwrite each other.
  **The tokens are what make it a comparison instead of a baseline**, and that
  is not a detail: a path that joins the watch and is written a moment later is
  written into the tick's very first reading of it, and a difference nobody
  ever recorded is a difference nobody will ever report. So a client hands back
  the server's own answers, a file's `version` from `/editor/file` and a
  folder's `sig` from `/editor/list` (`filesystem.DirSignature`, which is why
  the listing route answers one), the watch seeds the stamp of every path it
  holds none for (`filesystem.SeedStamp`, a token with a deliberately empty
  stat so the next round cannot take the prefilter's word for it), and the
  first round then answers "the disk is not what you are showing". Every read
  of a listing renews the watch for that reason (`listDir`), not only a changed
  scope. The interval is its own setting (`editor-file-poll-seconds`, Settings,
  Editor, Files) and deliberately not `GitPollSeconds`: one `git status` walks
  the working copy and may take the index lock from a coder, fifty `stat` calls
  cost nothing, and one number would force one of them into the wrong
  frequency. Both are read again before every round, and both watch renewals
  are marked `editorPoll` so they leave the quick open index alone; a poll every
  few seconds would otherwise rebuild it around the clock. One round is one
  `stat` per path (`filesystem.Stamp`): a file is **read** and a folder is
  **listed** only where its stat moved, and the token decides whether anything
  happened, the same token the save stands on, so a `git checkout` of identical
  bytes wakes nobody. A directory's mtime is the sharper prefilter, the kernel
  moves it on a create, a delete and a rename inside it and on nothing else,
  which is exactly what a lazily loaded tree needs and why a folder is no
  special case. What goes out is the bare `files` event, project plus the moved
  paths, files and directories apart, no content: the client pulls
  `/editor/file` or `/editor/list` itself. Only a moved **directory** drops the
  quick open index (`quickOpen.Invalidate`), and that is not a detail either:
  the index knows paths and nothing about contents, and more hangs on it by now
  than the file palette. In the browser the three answers are not one answer: a
  clean buffer takes the disk silently, with the cursor and the scroll position
  carried across the swap (`captureView`/`restoreView`, as a **line and a
  column** and never an offset, because a coder writing three words into line
  twelve moves every offset after it and a cursor put back by offset slides
  backwards for no reason anybody could see), because a file nobody typed in
  poses no question with two sides and must not jump away under whoever is
  reading it; a buffer with unsaved work is never touched but marked stale on
  its tab, JetBrains style, and the existing save dialog stays the one place
  the two versions are told apart; a file that is gone marks its tab and keeps
  it open, because the next save is then the create path that already exists,
  and a rename is a delete plus a create from outside, which a tree can follow
  and a tab cannot, so it is marked rather than guessed after. **A refresh
  nobody asked for reconciles, it never rebuilds** (`reconcileEntries`): a row
  that is still there stays, with its subtree, its open folders and whatever a
  pointer is doing to it, and a drag holds the tree off until it ends. Only a
  person asking rebuilds (`loadTree`). The bare `files` signal in the connect
  snapshot is the catch-up after a gap, where the watch had lapsed and nothing
  was published to anybody.
- **A commit takes the checked paths and nothing else.** `git.Commit` is the
  one call in `internal/git` that records a commit, one write out of the short
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
  **The panel is built for a working copy with tens of thousands of changes**,
  which a vendored tree or a generated folder makes an ordinary case. What a
  folder row says about its subtree, how many changes sit under it and how
  many of them are picked, comes out of one index built per status and moved
  by the clicks from there on; asking that by filtering the change list costs
  one pass per folder row and another per click, which is what made ten
  thousand changes a panel that took seconds to open and a folder checkbox
  that locked the tab for the better part of a minute. Only the first
  `COMMIT_ROWS_STEP` **changes** have rows, their folder rows ride along
  uncounted, and the rest waits behind a button whose numbers count the same
  way: built rows plus the footer's rest is the whole count over the list, the
  total without a filter and the hit count under one, and its words say
  changes or matches to match. The footer once counted tree rows instead,
  which made three thousand hits read as five thousand missing. Rows nobody
  reads are the other half of the cost; the counts, the all box, the folder
  checkboxes and the commit always speak for every change, never for the rows
  that happen to exist. **The panel carries a
  filter field over every list**, short or long and with no threshold, because
  a field that is only sometimes there is one nobody learns. It is one bar the
  width of the panel, flush with the rows under it, and the hit count and the
  clear button float over the field's own right padding rather than standing
  beside it: they appear with the first keystroke and change width with every
  one after it, and a field that resized under that would be a field nobody
  can type in. Nothing under it moves when the list narrows either, which is
  what the list's zero flex basis buys: how long the list is must never decide
  how much room the composer below it gets, or a filter that finds one file
  reshapes the whole panel. Where the panel runs out of height, a phone with
  its keyboard up, the list keeps a floor of a few rows and the panel scrolls
  rather than crushing the list to nothing. On a pointer device the field
  takes the focus when the view opens, the way every other filter in this app
  does, and on touch it must not: that is `pointerMedia`, never the width, or
  a phone answers the commit view with its keyboard. **A keystroke is budgeted
  at 100 milliseconds** from the key going down to the new rows standing in
  the DOM, measured on a working copy of thirty thousand changes and pinned by
  the runner on ten thousand: the sort belongs to the status and not to the
  keystroke, and the keystroke rebuilds the folder tree over what matches plus
  one batch of rows, which is what keeps the widest query, the one every path
  answers, inside the budget. The field is also where
  the keyboard takes the list over,
  and it does so with the real focus, because that is the only way the keys
  can be what they look like: arrow down walks out of the field onto the rows
  (`.editor-commit-row.active` is the focused row), and the walk reaches
  **every row that carries a checkbox, folder rows and file rows alike**, in
  the order they stand on the screen. Enter shows the focused file's diff and
  hands the focus back to it (from the field it takes the first hit); on a
  folder row Enter does nothing at all, no error and no state, because only a
  file has a diff. **Space is the click on the focused row's checkbox**, a
  file's pick or a folder's whole subtree with the half checked state
  included, and like every checkbox it stays in place, stepping on is the
  arrows' business. Arrow up over the first row and Escape in the list step
  back out into the field, and Escape in the field clears a standing query
  before the panel's own Escape closes the view. The walk survives a held key, thirty presses a
  second, because nothing on that path costs what the list is long: the
  marked row is remembered instead of swept for, the index is a map lookup,
  the scroller follows once per frame in a rAF rather than once per press (a
  geometry read per press is a forced layout of the whole list, which was a
  hundred milliseconds per repeat at thirty thousand changes), a walk past
  the built edge appends the next batch instead of rebuilding everything
  before it, and the walk clamps at the bottom and steps out at the top,
  never a wrap, which would build every row in between in one keystroke.
  **There is deliberately no key legend anywhere in the panel**: a line under
  the field lied about the keys as soon as the focus moved, and its focus
  following replacement was more furniture than help, so the keys simply
  follow the conventions the focus already carries and nothing is rendered
  for them. **The filter is a
  view over the picks and never a change to them**: it narrows what is shown
  and what the boxes over it act on, the all box picks the matches, everything
  picked outside stands, the summary switches to the words of what it acts on
  and names the rest ("3 of 7 matches · 40 picked in all"), and the commit
  takes every pick there is. A commit whose
  pathspec passes `pathspecArgvLimit` travels through a file
  (`--pathspec-from-file`, NUL separated) instead of the argument list, which
  the kernel refuses with E2BIG past about two megabytes: without it, picking
  a change of that size builds a commit no exec can carry.
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
- **What wants the tree brings the tree back.** The commit view and the
  revision comparison stand where the tree stands, so anything that shows
  something *in* the tree closes them first: `revealInTree` (the tab menu's
  Reveal in tree, Ctrl+Alt+R) calls `closeCommit` and `closeRevdiff` before it
  expands and selects, then opens the drawer on a phone and unfolds a folded
  tree column on the desktop, the same pair the commit view uses to reveal
  itself. Without it the row was marked under a panel nobody could see
  through, which on a phone is the whole surface.
- **Two revisions against each other are one more face of the tree column,
  and the list under it is the commit view's list.** `Compare revisions` in
  the git sheet opens `[data-editor-revdiff]` where the commit view opens,
  closing the other one, and the rows come out of `changeList`, the one
  implementation both panels are built on: the index per list, the folder
  tree over the hits, the batches of `COMMIT_ROWS_STEP` rows behind the
  button, the filter with `matchesTokens`, the keyboard walk and the focus
  handoff. What differs is handed in as options, whether the rows carry
  checkboxes (`picks`), what a row's letter is (`kindOf`), what Enter opens
  (`open`) and what an empty list says; a second copy of any of that is the
  thing this rule forbids. The server side is `git.Compare`
  (`GET .../git/compare?from=&to=&mode=`): both names go through
  `git.Resolve` first, each on its own, so a refusal names its side
  (`RevisionError`, a 400 carrying `side`); `mode=since` (the default) is
  git's three dots, the merge base as the left side, and two revisions
  without shared history answer `ErrNoSplit` as a 409, `mode=direct` is
  git's two dots and needs no shared history. The list is one
  `diff --name-status -z -M --relative` plus one `--numstat` over the same
  pair, `--relative` because a project below the repository root sees its
  own paths and nothing outside them, a rename from outside then reading as
  an addition; an answer that reaches `git.MaxOutput` is marked `truncated`
  and its cut record dropped. Empty names take `git.DefaultCompare`: the
  branch by name (HEAD detached) against the main branch when it is not
  the main branch (the remote's HEAD, else a local main or master), else
  the newest tag before HEAD, else the commit before it, so the panel opens
  on "what does this branch bring" or "what changed since the last
  release". The client keeps the pair and the question per device in
  `dc-editor-revdiff:<project>` and writes back what the server resolved,
  so a suggestion becomes the next open's start. The select's two entries
  are git's own syntax and nothing else, `a...b` and `a..b` with the two
  names, three dots first like GitHub's default: a sentence beside it was
  tried and a phone cut it to "What master changed since it s". A hash is
  shortened to seven characters the way git does, a name past 28 characters
  keeps its head and its tail (`shortRev`), in the select and on the two
  buttons alike, the full name staying in the tooltip. Enter
  opens the file at the two **hashes** the answer carries, never at the
  names, through `fetchRev` on both sides (a side the revision does not
  hold reads as empty, which is what an addition and a deletion look like;
  a rename's left side is its old path): the tab is a compare tab with
  `readOnly` set, the bar naming revision and path per side with its Save
  buttons hidden, persisted as a `revdiff` entry with both hashes, paths and
  labels and rebuilt from git on restore, and every path that acts on a
  compare tab's files (rename, delete, revert, save) steps around a read
  only one. It follows `diff_view` like every diff (`showRevdiff`): side by
  side is `setCompare` with both sides through `readOnlyExtensions`, inline
  is the plain editor holding the right revision as a read only document
  (the tab carries a `handle` made with `createDoc(..., {readOnly: true})`)
  under `setDiff`'s own inline path, `unifiedMergeView` against the left
  revision, so there is one inline mechanism and not two; automatic means
  side by side from `lg` up and inline below, which is what a phone gets,
  and `reapplyComparison` rebuilds an open revision diff on a changed
  setting or a crossed width the way it does for a file's diff. The
  two-file comparison stays side by side by design, both of its sides are
  writable. A moved base reloads
  an open comparison, because a branch name may have moved with it, and a
  reload that answers the same two commits renders in place: the batches
  somebody walked into and the marked row stand, only a changed pair starts
  the list over at its first batch.
- **A picker asks git, it never filters a list it happens to hold.**
  `git/refs` (`?q=`, `?kinds=`) answers only the hits, capped per kind
  (`editorRefsCap`), the name match in Go because `for-each-ref`'s wildmatch
  does not cross slashes, commits through `log --all --regexp-ignore-case
  --fixed-strings --grep=…` plus a hex gated `rev-parse` for hashes. **The
  typed text travels into git arguments**: it rides in the attached
  `--grep=` form, never as an option and never as a pattern. The name match
  is **the token search the whole app shares** (`namespaceRefs`, beside the
  browser's `matchesTokens` and the file index's `scoreQuickOpen`):
  lowercased, split on whitespace, every token contained somewhere, because a
  ref name is a path and somebody types the pieces of it they remember rather
  than one contiguous run. In the client `openRefPicker` debounces, numbers
  its rounds so a slow answer never paints over a newer one, and lets a raw
  name typed past the list through on Enter. **The create form's two branch
  fields are the same picker on another page** (`project-new.js`,
  `GET /projects/:name/branches?q=`, capped by `worktreeBranchPage`): the
  hidden field beside each one is what the form posts, so `branch` and
  `start` arrive as they always did, the visible field carries the choice and
  is put back to it whenever the list is left without one, the query being
  tracked apart from it.
  The checkout field marks a branch another working copy holds and refuses
  it, the starting point does not, a new branch may start where somebody else
  stands. Whether a remote
  branch is offered at all is decided against the **whole** local namespace
  (`git.BranchNames`), never against the hits: under a search the local
  branch a remote row would collide with need not have matched, and offering
  both is offering a create git refuses. That rule is off for the starting
  point (`?pick=start`), the one list where a local branch and the remote
  branch it follows are two different places to begin, and where nothing is
  created under either name. A picked name is resolved for the
  create by searching for it (`worktreeRef`) and not by walking a capped
  listing, or a branch found by typing it could not be created. **The resync
  is a row of each menu**, its head, above the hits and outside the box that
  scrolls, so it keeps its place however far somebody scrolled and is there
  when nothing matched, which is the moment it is looked for; it says when
  the repository last heard from a remote (`fetchedAt`, `git.LastFetch`,
  FETCH_HEAD's mtime, one `rev-parse` for the git directory and one stat on
  it), fetches in place and answers the list again without ever closing the
  menu. A running fetch turns the row's own refresh icon (`dc-spin`) and
  swaps in no spinner: a spinner's box is taller than the icon's and the row
  would grow around it, moving every field below. **The arrows walk that row
  and the branches as one list**, the resync first because it is the head of
  the menu: Enter on it fetches instead of picking, and a freshly opened menu
  marks the first branch and never the row, or Enter would fetch for somebody
  who aimed at a branch (`RESYNC` and `NONE` are apart for the same reason, a
  list that matched nothing has no entry to mark). The marked row is scrolled
  into sight (`block: "nearest"`, so a list that already fits stays put), and
  every row carries a scroll margin the height of the head above it, measured
  from the two boxes rather than computed: the resync sits outside the
  scrolling box and the browser knows nothing about it. A rebuild that only
  moved the mark keeps the scroll position, every other render is a new answer
  and belongs at the top. A click on a field that already holds the focus
  opens the list again, because Escape leaves the focus where it is and no
  focus event follows it. **A local branch row carries
  its distance to its upstream** ("3 behind origin/master"), out of
  `%(upstream:short)` and `%(upstream:track)` in the same `for-each-ref`
  (`git.Ref.Upstream`, `Ahead`, `Behind`, no second call), because a name says
  nothing about how old the place behind it is. That is also what moves the
  default start (`DefaultStart`): a head that is **purely** behind starts at
  its upstream, the same history further along, while an ahead or diverged
  head keeps the local branch, because those commits exist nowhere else and
  branching off the upstream would drop them without a word. **The form never
  fetches by itself**, a remote with a passphrase would open a dialog nobody
  asked for, so the checkout of a branch that is purely behind offers to catch
  it up instead (`fast_forward`): a checkbox that appears only there, and a
  `merge --ff-only` in the **new** working copy against a ref that is already
  local, so it reaches no network and can be asked nothing. The box is a wish
  and never an instruction, `catchUpWorktree` reads the distance again in the
  copy that now exists and moves nothing that is ahead or diverged; a catch up
  that fails is reported in the flash and takes no project with it, nobody
  removes a finished working copy because a follow up did not run.
- **The create form states, it does not explain.** No prose in it: what
  somebody needs stands in the label, the placeholder, a row of a list, or
  nowhere (`projects_new.gohtml` carries no `form-hint` at all). A row says
  what picking it does, which local branch a remote one creates and which
  project holds a taken one; the passphrase shows itself in the dialog when it
  happens. The name field is the name and nothing else, no paragraph under it
  and no path in front of it: where a project lands is the same place for every
  project and belongs on none of them. What stays is what is not a leaflet:
  the warning that a source has no branch yet. Nothing is checked in the
  browser before the send either: the create answers what it refuses in its
  own words and the form comes back carrying what was typed
  (`newProjectPath`), which is the one place that judgement lives. The picked
  value stays readable: focus **selects** rather than
  empties, the checkout field shows the branch that will exist here with its
  remote named beside the label while the starting point shows the ref it
  begins at, the hidden field keeps the ref either way, and the query is
  tracked apart from the text because the two stopped being the same thing.
- **The projects page's git menu leads somewhere, and acts once.** Every row
  that is a repository carries a git button (`[data-git-project-menu]`, built
  like the compose button left of it (compose before git): the menu is
  `@dc/contextmenu`, and every
  destination is rendered onto the button by the server as `data-git-worktree`,
  `data-git-commit` and `data-git-compare`, so the client knows no route). The
  worktree entry opens the create form with this project as the source
  (`/projects/new?create=worktree:<name>`, in the create dialog) and stands only on a main
  repository, because the form offers nothing else as a source; the two editor
  entries open the editor on a view (`?view=commit`, `?view=compare`, rendered
  as `data-editor-view` and read once after the first status answer and the
  tab restore, never out of the URL, for the reason the terminal id is not),
  and an unknown value opens the plain editor. The one action is the fetch:
  the same `POST /projects/:name/fetch` the create form's resync uses, so the
  passphrase question reaches the app-wide dialog, and its answer is a toast
  (`@dc/toast`, like the editor's own fetch), never a flash and never a
  re-render: the list stands where it was, and while the fetch runs the
  button is disabled and its icon pulses (`.dc-git-working`, the upload
  button's pulse over opacity and scale behind the same reduced motion
  guard the docker wave has, so the row never moves). The answer carries `message`
  beside `fetched`, worded on the server (`fetchedMessage`): what was fetched
  and where the checked out branch now stands against its upstream, one
  `for-each-ref` after an action a person started; a refusal is git's words
  through the ordinary 409. Deliberately not in the menu: switching the branch, push and pull
  (they move a working copy coders are working in and stay in the editor's git
  sheet, which shows the working copy around them) and removing a worktree
  (that is deleting the project, which the row already has). None of it costs
  the list a git process: the button and its entries come out of the
  `gitfacts` facts the row already renders from, and the distance to the
  upstream is deliberately not on the row, it would be a process per row per
  render, or a cache with no honest invalidation.
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
- **A line comment is one note on one line, and the notes are project
  state.** One note on one line of one file, kept per project in
  `<state-dir>/line-comments/<project>.json` through `internal/statefile`,
  the notification inbox layout: deleting a project is `lineComments.Clear`,
  which removes that project's file, an emptied list takes its file with it,
  and the backup carries the whole directory as a `line-comments` source of
  the `projects` section, no section of its own (the earlier own section
  never shipped in a release, so no archive holds its id). The pre-release
  single `editor-line-comments.json`
  was never migrated, nothing reads or writes it any more. The routes sit in
  the editor group: `GET/POST .../editor/comments` (the list, and one
  upsert; the quote in a save is the sender's buffer line, stored untouched
  even when empty — a note born in a dirty buffer deliberately reads
  outdated to other readers until the save catches the disk up — while a
  request without the field, the CLI's add, gets the code line read from
  the disk),
  `.../comments/delete` (ids, whole files through `paths`, the orphans
  through `outdated` — narrowed by `paths` when both are given — or `all`;
  the answer counts what fell) and `.../comments/move`, which is
  how an edited buffer's anchors land — and only with the save: while a
  buffer is dirty the mapping is a local overlay (`onDocChanged`, line and
  quoted code line follow the buffer), no move request leaves the client, so
  the server only ever knows saved states. Another reader therefore sees no
  phantom outdated from a buffer the disk never saw, and a discard only
  drops the overlay while the stored anchors still match the disk (the
  reload from disk and a discarded close pull the list fresh). A successful
  save posts the file's lines and quotes in one move (outdated comments send
  nothing) and then pulls the reconciled answer, which is also where the
  server's own rebind repairs what a failed post left behind.
  **The quote is the anchor, and every read judges it**
  (`reconcileLineComments`, behind every answer of the list): the stored
  quote is held against the file's current line, reading only the commented
  files. A quote that stands where its line says is fine; one that moved and
  stands in the file exactly once is rebound in the same read, persisted
  through `Move` and published, so the answer never lags its own repair; the
  rest is honestly `outdated` in the view — quote gone or ambiguous, file
  missing or unreadable as text, and an empty quote never rebinds, every
  empty line would match it. Outdated is judged fresh per read and never
  stored, so a file that comes back heals its comments; deliberately no
  fuzzy matching, no git mapping and no watcher behind it. An open dirty
  buffer keeps its live mapping, the server judges the saved state, and the
  client's local array is the one source its three consumers read — the
  gutter repaint, the sheet and the jump. A pull therefore never overwrites
  the line, quote or outdated mark of a comment whose tab is dirty
  (`loadComments` keeps the held values per id): the server's answer is the
  disk's view, and taking that in mid-edit is how the sheet once jumped to
  the wrong line while the gutter stood right. A comment that arrives new
  for a dirty tab — another device pinned it against the saved state — is
  neither painted at its raw disk line nor held back: the tab accumulates
  its changes since the last save (`tab.commentChanges`, a composed
  `ChangeDesc`, reset by the save and the disk reload),
  `editor.mapSavedLine` maps the disk anchor through that overlay onto the
  shifted line, a line the overlay deleted makes it locally outdated, and
  the next save persists the mapping for everyone, the new comment
  included. The save reconciles both sides on the next read. The
  live mapping carries the same anchor rule as the server, the quote is the
  truth and the position only a hint: a change that covers the whole
  commented line (`iterChangedRanges`, a real deletion spanning both of the
  line's ends) never remaps the anchor onto the neighbour line — that would
  rewrite the quote with foreign text and make the orphan look legitimate
  forever. The comment freezes with its old quote and reads as outdated at
  once, in gutter and sheet, and the move sync skips outdated comments
  entirely, so no orphan ever writes a new quote. It heals in place the
  moment its line carries the exact quote again, which is what an undo does;
  past that, the server's rebind after the save is the way back. Typing
  inside the commented line stays what it was: anchor follows, quote
  follows. Every
  movement publishes the `linecomments` event (project named, bare in the
  connect snapshot), and every open editor pulls the list itself. The surface
  is the gutter itself, deliberately no column of its own: a commented line
  highlights its whole gutter row (`cm-comment-line` through
  `gutterLineClass`, a StateField in a compartment beside the blame gutter,
  its RangeSet mapped through every change so the mark follows the buffer
  between repaints; `lineNumberMarkers` colored only the number cell and
  left the fold gutter white beside it). The line's menu opens on a plain
  click anywhere in the gutter (`gutterClick`, skipping only a fold gutter
  cell that carries a marker so folding keeps working, and asking
  `menuJustClosed` so a second click closes; the gutter wears the pointer
  cursor on a commentable tab, part of `commentsTheme`), on a right click and on a
  touch long press (`wireRowMenus` from `@dc/contextmenu` over the CM gutter
  cells, delegated from the editor root because the cells are rebuilt while
  scrolling); the line comes from `editor.lineAtGutter` (workView only, so a
  diff's revision side answers nothing), and the menu holds, in this order:
  add or edit the comment, on a commented line the danger `Delete comment`,
  `Copy path:line`, and the blame toggle (`blameMenuItem`); deleting lives
  only in the menus, the dialog itself only creates and edits.
  The dialog is a Bootstrap modal, deliberately not SweetAlert
  (`data-editor-comment-modal`, the one exception the feature carries):
  path:line and the code line above a textarea, a dimmed `form-hint` under
  the field saying the comment follows the line and the assistant can read
  and manage the notes, Cancel and Save or Add,
  an empty save marks the field `is-invalid`
  instead of writing, Ctrl/Cmd+Enter saves it the way the commit message
  commits, and its host div moves to `document.body` like the terminal
  panel's modals. The delete confirms (the gutter menu's `Delete
  comment`, a cell's `Delete`, both through `deleteCommentDialog`, and
  `Delete all`) stay SweetAlert like every other confirm.
  Ctrl+Alt+C comments the cursor line,
  opening an existing note; Ctrl+Shift+C opens and closes the comments
  sheet; both stand in the editor's shortcuts modal and the docs row, and
  there is deliberately no `Line comment` entry in the editor menu any more.
  The kebab's `Line comments` entry stands directly above `Git`, and the
  two menu badges (`Line comments`, `Git`) hide at zero instead of standing
  as an empty pill. An outdated note shows itself everywhere the note shows: the
  gutter mark turns orange with the number struck through
  (`cm-comment-line-outdated`), the sheet cell carries the orange
  `Outdated · was:` line with the old quote, the Markdown export appends
  `(outdated)` to the `path:line` head, and the CLI list marks the entry.
  A last known line past the file's end paints no gutter mark at all (the
  extension drops out-of-range lines instead of clamping, the stored note
  stays untouched), so the sheet is the only place then, the same as a
  deleted file.
  The assistant reaches the same routes over the local API
  (`internal/cli/linecomments.go`): `line-comment-list` (capped like the
  other list commands, filters before the cap) and `line-comment-remove`
  take `--outdated` beside `--path`, ids stay their own case, and
  `line-comment-add`'s quote is the server's disk read; announced in the
  generated instructions (`internal/assistant/memory.go`). `--path` is the same filter on list and
  remove, repeatable and ored, each value an exact file, a folder prefix, or
  a glob where `*` stays inside a segment and `**` crosses them; the one
  matcher is the server's (`matchCommentPath`, the list route takes it as
  `?path=`, the delete route as `paths`), so the CLI never grows a second
  spelling of it. Because everything lands in the editor's own handlers,
  every open editor follows an add or a remove live, badge and sheet
  without a reload. `Line comments` in the menu opens a
  sheet built like the git sheet, its notes in the history's own shape: one
  cell per note in a `row row-deck` grid (`commentCell`, `col-12 col-lg-6`,
  two per line from `lg` up), the whole cell is the control, and a click on
  it opens the app's menu over it, anchored at the click or at the cell when
  the keyboard got there — `Go to line`, `Edit` and the
  danger `Delete`, which asks first: the confirm stacks path:line and the
  note's text dimmed on lines of their own under the title, no separator;
  the sheet stands behind the menu, so
  it is in the keep-open list of the row-click auto-close. Above the grid the two
  actions, `Copy as Markdown` (heading `Line comments in <project>:`, then
  path:line, the comment, the code line as quote; the comments reach the
  assistant through the CLI or pasted as a message, the sheet sends nothing
  itself) and `Delete all`, both closing the sheet themselves the way the
  auto-close used to. A file deleted in the editor takes its notes with
  it, and a rename or a tree move carries them along on the server, the one
  moment it knows both names (`lineComments.Rename` in the rename and move
  handlers, exact file or folder prefix, publishing the event); the client
  only remaps its local list, it re-posts nothing. One project's list is
  capped (`maxLineCommentsPerProject`) so the state file stays a state file.
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
- **Editor layout is per project** (`editor.js`, `readLayout`/`writeLayout`):
  tree width, fold and scroll, the tree column's view (`dc-editor-view:<project>`,
  restored without the reveal `?view=` does), the terminal panel's height, and
  the commit and compare lists' filter and scroll while the view stands
  (`dc-editor-commit-list:<project>`, `-revdiff-list:`, kept by `changeList`
  behind `stateKey`, restored on the page's first open, dropped on close),
  every tab's cursor and scroll (`view` on its `dc-editor-tabs` entry, line and
  column, both axes; a diff's or comparison's scroll rides the same entry and is
  put back after the merge view is built, `scrollTo` in the facade, because the
  outer `.cm-mergeView` is the vertical scroller and the sides only scroll
  sideways). The unchanged blocks a person opened in a collapsed diff ride
  along as `expanded`, the start lines in the working copy: `@codemirror/merge`
  exports the `uncollapseUnchanged` effect but not its field, so `expandedField`
  records the effects and `expandBlocks` replays them after the build, onto
  the revision side through the chunk mapping. Width, fold and height also write the bare key, the start for a
  project this device never opened, copied onto the project on its first open.
  Saves debounce 300ms and flush in the teardown; the tree scroll is read only
  while the tree has a box and put back when the box appears (`wireTreeScroll`).
- **One editor on every width: the strip stays, the options fold into one
  menu.** A strip and seven icons do not share 390px, and two different
  headers are two things to learn, so the icons went into the kebab instead of
  the strip going away. Outside the menu the header carries only the folder
  toggle, the strip, `[data-editor-save]` (`hidden` unless the active file is
  dirty) and the menu itself; every other control is an entry of `[data-
  editor-menu-list]`, and the entries are the same at 390 and at 1440. The
  two editor heads are the shell's heads: the tree head is a
  `dc-ctx-head` (project switcher as the title, commit and refresh in
  `dc-ctx-tools`), the strip row a `dc-work-head` (42px, 28px icon buttons,
  the phone's `shell_head_tools` at the end), so the editor has no head row
  of its own above them. The
  menu carries one git entry, `Git`, which opens the git sheet; the per-file
  switches stay entries of the file's context menu
  (`diffMenuItem`/`blameMenuItem`, the revision diff and the file history,
  tab and tree row), because those are statements about one file. The folder
  toggle shows on both
  widths with the effect the width
  allows: below `md` it opens the drawer, above it folds the tree column and
  its splitter away (`.editor-tree-folded`, per project in `dc-editor-tree-
  folded:<project>`, the rule scoped to the widths that have a column so the class is
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
  (`SHEET_ROW`, the action rows plus the list rows); a sheet opens on the
  filter in its head and a drilled level starts over on it, both through
  `focusSheetTop` and only on a fine pointer, because a marked row is noise
  on a touch screen and a raised keyboard would cover the list; a sheet
  without a filter (the settings, the pickers with their own server search,
  the usages with their own field) opens on its first row as before.
  **Every sheet with text rows filters through that field**, built once in
  the hull (`[data-editor-sheet-filter]`, `applySheetFilter`) with
  `@dc/filter`'s `matchesTokens`, the same match the commit view and the
  project switcher use, so the three feel alike: a row hides with its unit
  (`sheetUnit`: the `.editor-sheet-row` line around a list row, the grid
  column around a cell, else the row itself), a node marked
  `data-editor-sheet-head` (the docker stack's status line, the git
  history's `History` heading) follows its group, the siblings after it up
  to the next divider with the containers of the body read as one list, and
  stands only while its own text or a row of its group is a hit,
  a row marked `data-editor-sheet-pin` (the history's `Older commits`) is
  never filtered, and a `.dropdown-divider` stands only between two groups
  that both kept a hit. `rowsOf` leaves rows inside a hidden node out, so
  the arrows walk the hits alone; while the field holds the focus the first
  hit carries the `selected` surface as what Enter would run
  (`paintSheetMark`), so ArrowDown in the field steps onto the second hit,
  Enter in the field runs the first, Escape in the field clears a
  standing query before the sheet's own Escape chain (one level back, then
  close) takes over, and `openSheet` and a level change (`drill`) empty the
  field. Every repaint applies the filter again (`repaintSheet`, and
  `appendGitLog` for the pages it adds), so the focus position it keeps is
  a position among the hits. **A row is reached only through `focusRow`**, which
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
- **Replace means replace, for a folder as for a file.** A paste or a drop onto
  a taken name answers `ErrExists` as a 409, the browser asks once
  (`confirmReplace`, one question for both ways in), and the repeat carries
  `overwrite=1`. What comes back then is a replacement and not a merge: the old
  folder goes with everything below it, which is why the dialog says so for a
  folder and not for a file. `CopyEntry` builds the new one beside the old one
  and swaps it in (`replaceWithCopy`, `swapIn`), `MoveEntry` swaps the moved
  entry in the same way, so a replacement that fails leaves the old one standing
  rather than nothing at all, and neither ever touches the source. The one case
  refused is a source that sits inside what it would overwrite (`canReplace`):
  clearing the target would take the source with it.
- **The project switcher is a palette, and the palette owns no data.** The
  project name above the tree (`[data-editor-project-switch]`), the menu's
  first entry `Switch project` and Ctrl/Cmd+Shift+P open
  `[data-editor-palette]`, an
  overlay over the editor body built like the file palette: one field, one
  list, and a window level capture keydown for the arrows, Enter, Escape and
  Tab while it stands, so Tab cannot walk out behind it and Escape closes it
  before anything under it hears; a click on the backdrop closes it, opening
  it closes the drawer, the sheet and the quick open, the quick open closes it
  in turn, and the swipe and the double Shift stay off while it is up. The
  rows are the server's fragment (`editor_projects.gohtml`, held out of sight
  in `[data-editor-project-list]` and pulled again on a `projects` event),
  sorted there by `@dc/project-sort` like every other project list, a
  worktree behind its main; the palette clones them into groups
  (`renderPalette`) and never builds that markup a second time. Every fact on
  a row comes from the file reads the project list already did
  (`render.EditorProject`: repository, branch, worktree and its main, worded
  by `switcherRepo` and `switcherSearch` in `handlers_editor.go`), no git
  process runs for the palette. Without a query two groups stand: `Recent`,
  the last used projects with the current one on top carrying its check
  (`PALETTE_RECENT`), and `All projects` in the shared order with a worktree
  drawn as its main's member
  (`projectSort.mainOf`, the same grouping the projects page folds); a query
  collapses both into one ranked list: every token has to hit the row's
  search line (`data-project-search`: name, repository, branch, and the
  main's path for a worktree whose main is no project here) through
  `@dc/filter`'s `matchesTokens`, and the first token ranks the hits
  (`rankProject`: a name or repository it starts, then one it sits inside,
  then the branch or the path alone); a query nothing matches says so in the
  list right under the field, where the quick open puts its note. A fresh
  palette marks its best row and focuses the field on a fine pointer alone
  (`markPaletteBest`, `openPalette`), and the best row is the first one that
  is a switch (`bestIndex`: under no query the row under the current project,
  under a query the first hit), which is what makes Ctrl+Shift+P, Enter the
  way back to the previous project and what keeps a phone's keyboard down; `closePalette` blurs the
  field before it hides the overlay, because a focus left inside a hidden
  element is one nobody can see, and an empty editor cannot take it. **The
  palette looks like the file palette, on every width**: the same overlay
  and panel measures (`36rem` by `26rem`, capped by the editor), the same
  plain field, rows at the file rows' font and padding with the floor the
  chooser rows keep (`2.25rem`), a worktree's directory as a second line
  under the name the way a match's text stands under its file, and no phone
  block of its own; the field on top and the list under it, groups top
  down, the best row on top, a fresh list at its top, and Enter, the
  keyboard's Go, takes the first row with nothing marked (`commitPalette`).
  Two phone shapes were tried and taken out on 2026-09-05, a bottom sheet
  with the field as its foot and the rows turned around, then a taller
  panel with bigger rows and a bigger field: the palettes of the editor
  are one thing, and looking alike counts more than the thumb's way.
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
  directories an older release wrote). **The server runs as the cockpit's
  own user, never as the image's root** (`--user` with this process's uid
  and gid, a root cockpit reads 0:0 and nothing changes): what it writes
  into the cache bind stays the cockpit's to remove. HOME points into the
  cache mount (`-e HOME=<cache>/home`, the directory stands before the bind
  via `ensureCacheDir`), because the uid has no passwd entry in the image
  and `-e` wins over the home such an entry would name; the entrypoint's
  restart flag lives under `/tmp`, `/run` inside the images is root's; and
  the default configuration profile gets a tmpfs on its workspace
  directory, docker mounts the shorter path first, so the project bind
  below stays what it is. What an older release's root server wrote,
  chmod cannot repair on a host where the cockpit is not root, so
  `removeCacheDir` falls back to a throwaway container of a cockpit built
  image, picked with preference (`removalImage`): the profile's current
  tag, then any tag of its repository, then another profile's, because
  find is in every image and the hash tags a release moved must not park
  an orphan forever (`--pull=never`, no network, the directory as its only
  mount, `find -mindepth 1 -delete` inside, `os.Remove` for the then empty
  top level); only a host without any cockpit built image logs calmly and
  leaves the directory for a later sweep, and without a docker client the
  local error stands. Before a
  start the launcher reads the ownership one level below the cache
  directory's top, the top is `ensureCacheDir`'s and always the cockpit's
  own; a foreign owned cache is removed the same way and the server starts
  cold once. **The projects root label is the
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
- **The assistant's voice runs in engine containers the cockpit owns.**
  `internal/voice` follows the editor intelligence Docker pattern with two
  fixed profiles, whisper (speech to text, faster-whisper) and piper (text to
  speech): the recipe is compiled in, the image is built locally from the
  embedded build file under a content hash tag and never pulled prebuilt, the
  container runs as the cockpit's own user with HOME inside its cache bind
  (`<state-dir>/voice/dev-cockpit-<engine>`, a host bind at its own path, so
  the model downloads survive the container and land owned by the cockpit;
  deliberately not in the backup), and the boot sweep removes leftovers
  behind the state root label (`dev-cockpit.voice-state-root`), which is the
  ownership boundary here: an engine is per instance, not per project, so the
  container name carries a short hash of the state root and two serve
  processes on one daemon never fight over one name. An engine warms on first
  use behind a small HTTP API inside the container, published on loopback
  under an ephemeral host port (one profile, one fixed inner port), and stops
  after an idle timeout; a transport error drops the container and retries
  once, an answer the engine gave is never retried. The very first warm also
  pays the image build and the model download, minutes rather than seconds,
  so a start that still owes either (`firstUse`: image tag missing, or the
  model cache empty) is announced over the event bus (`voice-warming`, the
  profile id as `engine`) and the assistant surface answers it with a toast
  saying the first use downloads the model. Container writes end at
  the cache bind: clip and text travel over the API, and the cockpit process
  itself writes transcripts and rendered audio, so no container ever touches
  the conversation directories. The settings live on the Assistant settings
  page's Voice tab (`/settings/assistant/voice`, the bare `/settings/assistant`
  redirects there; the same tabbed shape the editor page uses, so the page can
  grow more tabs), one key per engine (`voice-stt`, `voice-tts`) with the LSP
  value scheme (`auto`, `<engine>-docker`, `off`; absent and unknown read as
  auto). The short-lived top level `/settings/voice` never shipped in a
  release and went away without a redirect.
  What a profile runs with is a second key each, `voice-stt-model` (`tiny`,
  `base`, `small` (the default), `medium`, `large`) and `voice-tts-voice`
  (`male`, the default, or `female`), one speaker for every language on
  purpose, so an answer never changes gender when it changes language. Both
  store a generic id, a size and a gender, never a model or voice file name:
  which files a size or a gender means is decided in the build file
  (`large` is faster-whisper's large-v3-turbo, `male` speaks thorsten and
  ryan, `female` kerstin and amy), so a better model of the same size never
  touches a stored setting. small is the default because it is the size that
  still answers a push to talk press quickly on a CPU only host. The recipe
  still is not configurable: the value is an id out of the profile's own
  `Options` and travels into the container in its `EnvVar`
  (`DC_WHISPER_MODEL`, `DC_PIPER_VOICE`), `Profile.Normalize` reads absent and
  unknown as the profile's `Default`, the build file's own mapping is the same
  allowlist and falls back the same way (an unknown whisper model would
  otherwise read as a repository to go and download), and only the picked
  piper pair downloads. A model loads before the port binds, so a changed option cannot
  reach a warm engine: `ensureRunning` compares the running container's option
  against the current one and starts over when they differ.
  Speech to text is `POST /assistants/:id/stt`: the clip is whatever
  MediaRecorder produced, webm/opus mostly and mp4/aac on Safari, the engine
  decodes either, and whisper detects the language per utterance, so German,
  English and mixed input work without a language setting. **The button
  beside the message box wears two faces, and one function paints it**
  (`syncSendFace`, the only writer of that button): the microphone while the
  composer holds neither words nor a finished attachment and speech to text
  is available (`talkFace`), the send as soon as anything stands in it, an
  attachment alone included, it is a message on its own. The face follows
  every change of the box, the typing, the send that empties it, an
  attachment coming or going and a draft the page loads, because `onInput`
  and `renderAttachments` both end in that one paint. A host without the
  engine (no `stt-url`) or without a microphone (`talkReady` false) never
  paints, so the server rendered send stands and nothing there ever records.
  **Holding the microphone records, and the release decides.** The press is
  one pointer capture on the button (`beginPress`, `movePress`, `endPress`):
  the recorder starts on the way down, the button goes solid red and grows
  under the finger, the hint over the message box carries the red dot with
  the clock and Slide to cancel, and a pill with an open lock stands over the
  button. Sliding left past a third of the message box's width
  (`press.cancelDx`, measured at the press, never a fixed number of pixels)
  throws the clip away, the hint slides along and fades so the way out reads
  before it is taken; sliding up as far as the lock pill stands
  (`press.lockDy`, measured from the pill's own place) locks the recording:
  the finger may leave, the button turns into the send, the hint shows the
  clock, a live level trace (`startWave`, an AnalyserNode on the same
  stream, one sample every `WAVE_SAMPLE_MS`, bars of `WAVE_BAR_PX` at
  `WAVE_STEP_PX`, drawn in the hint's own text colour) and the trash
  (`[data-assistant-talk-discard]`), and then the send button sends and the
  trash discards. A release with at least `MIN_CLIP_MS` of recording sends,
  a shorter one is a tap: on touch it throws the clip away and the hint says
  Hold to record, release to send, with a mouse it locks, a click is how the
  hands free recording is started there. A press whose pointer the browser
  takes away (`pointercancel`, a lost capture) locks too, nothing is sent and
  nothing is lost. The click that still follows every release is spent
  (`swallowSubmitUntil`), or the composer would be sent beside the clip; a
  submit that reaches `onSubmit` with the microphone face is therefore the
  keyboard's, Space or Enter on the button, and starts the hands free
  recording the way Alt Alt does, while one that reaches it during a
  recording sends it. The recorder runs on a timeslice (`TALK_SLICE_MS`) and
  stops itself and sends where the next slice would pass the upload limit
  the route refuses at (`max-file-bytes`, the same `maxUploadBytes` the STT
  route reads), so a recording nobody ends still lands. Escape throws a
  recording away whatever started it, held or locked: the listener sits on
  the document in the capture phase, is armed with the recording state and
  taken off with it, so no other Escape in the app ever sees a press that
  meant cancel and every press outside a recording still reaches the dialog,
  the editor or the terminal that owns it. Nothing about the gesture is a
  stylesheet rule: the growth, the slide and the pill's travel are
  transforms the element writes, the pill and the hint are Bootstrap
  position utilities, the dot is Tabler's animated status dot. The keyboard
  way is Alt Alt through the `@dc/doubletap` machine, wired by the surface on
  the document's capture phase so it works from anywhere on the assistant
  page (`toggleTalk`): the first double tap starts a locked recording, the
  second stops and sends, during a held press it locks, and it records only
  where the button would, so with words or a file in the box Alt Alt starts
  nothing and sends nothing. Only a bare
  Alt counts, so Alt+<key> combos never half-arm it
  and nothing leaks into a terminal or an editable field, where a bare Alt
  types nothing; the default is taken off every clean tap's keyup, because
  Firefox on Windows otherwise hands a bare Alt keyup to its menu bar and
  parks the focus outside the page. Text to speech is
  `GET /assistants/:id/messages/:messageId/audio`, synthesized per request and
  never stored: a spoken answer is a couple of seconds of engine time and
  megabytes of uncompressed audio, so saying it again is cheaper than keeping
  it, and nothing of a conversation then lies outside its transcript, with no
  stale copy to invalidate when the voice changes. Concurrent asks for the same
  answer in the same voice share one synthesis (`spokenAnswer` behind
  `audioBusy`), and the wav goes out through `http.ServeContent` off a byte
  reader, so a range request is still answered. The text comes from
  `markdown.Speech` (code blocks and bare addresses dropped, inline code kept)
  and the language `voice.DetectLanguage` reads off that spoken text
  (whatlanggo restricted to German against English, English the fallback;
  deliberately not lingua-go, whose embedded models more than tripled the
  binary the self-update ships). The speaker button on
  a finished answer replays it; it keeps the small button's visible height
  so the answer layout never moves, and its touch target is an absolutely
  positioned overlay reaching 8px past the box on every side (a `btn-lg` was
  tried and pushed the message rows apart). While a spoken answer renders, the
  speaker and the send button wear `.dc-icon-spinner`, which is added to the
  icon rather than replacing it: the glyph stays and only turns invisible, so
  the element keeps its box and its text baseline and the baseline aligned
  header does not move, and the ring is drawn from a border because a rotating
  icon font glyph shimmers.
  The speaker in the assistant's head opens the voice menu, it never toggles
  on its own and wears no colour, the icon swap (`ti-volume` against
  `ti-volume-off`) is the whole state display. The menu holds the two per
  device choices, both in localStorage like the terminal's settings: autoplay
  (`dc-assistant-voice-mode`) reads a finished answer aloud by itself,
  standing on the muted play the microphone tap spends on the audio
  element, because autoplay needs a user gesture once, and switching it on is
  itself that gesture; and the volume (`dc-assistant-voice-volume`, whole
  percent, unreadable or out of range reads as full). The volume rides on the
  audio element's own `volume`, and deliberately not on a gain node the way
  the notification jingles do: an element routed through Web Audio only
  sounds through its graph, and iOS suspends an audio context when the page
  goes to the background, so a spoken answer would stop the moment the app is
  put away, while a plain element keeps playing; a graph also runs a beat
  behind the element's clock and clips the first and last words. The price is
  that iOS ignores a volume set from script, the hardware buttons own it
  there, and it cannot be probed: the property takes the value and reads it
  back while playback ignores it, so `dc-assistant-volume` runs a platform
  check instead (user agent plus the Mac-with-touch shape iPadOS reports) and
  puts a quiet line under its row saying the loudness follows the hardware
  buttons, the slider itself stays. Should the slider ever have to work there,
  the level belongs in the wav the server renders, not in a graph in front of
  the speaker. Both volume rows are one control, `@dc/volume-slider`: the shared
  element carries the markup, the icon steps and the reading, a subclass says
  only where the value lives and what a move does (`dc-notify-volume` onto
  scriptune's master volume with a jingle preview, `dc-assistant-volume` into
  localStorage plus a bubbling `dc-volume-change` the surface listens for).
  The module also owns `GAIN_BASE`, the one number both features attenuate by:
  scriptune keeps its master gain at a tenth of the stored value, and the
  speech follows it through `gainFor`, or the same percentage would mean two
  different loudnesses in one product.
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
  for the log follower), so it lives in the tab strip and the
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
  **Logs are a terminal, never a dialog**, and two entries open one: `Logs`
  with the logs icon, opening what `Log terminal` used to
  (`POST /docker/:id/logs-shell`), and `Filter logs…`, the same terminal after
  asking for a pattern. A whole stack has the same pair, project scoped
  (`POST /projects/:name/docker/logs`, `docker.ComposeLogsCommand`), so nobody
  has to find the container that is talking first. Every log shell pipes
  through this binary's own formatter (`dev-cockpit docker log-formatter`,
  under a hidden `docker` group like the assistant's, engine in
  `internal/docker/logformat.go`), with stderr merged into the pipe by 2>&1:
  a severity gutter block per line (red for errors, yellow for warnings, read
  case insensitively from tokens, logfmt `level=` and a JSON level field,
  behind the compose prefix when one stands), a stable tint per compose
  service hashed from its name, and with `--grep` only the matching lines
  pass plus `--context` lines around them (default 2), matches inverted and
  groups separated the way grep does it. The filter travels as the `filter`
  form field, a pattern that does not compile is refused where it was typed
  (`docker.CompileLogPattern`, one compile rule for the handlers and the
  formatter), and the shell name carries target plus filter (`app-1 logs:
  foo`, `docker logs: foo`). A stack's logs terminal is
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
- **The shell:** every app page stands in one grid, `.dc-app` in
  `layout.gohtml`: the rail of areas on the left (`shell_rail.gohtml`, the
  Projects, Terminals, Editor and Assistants entries, Settings and Docs at the
  foot with the theme button, the update button, the bell and the logout, all
  32px with 20px glyphs), an
  optional list column (`.dc-ctx`), the work surface (`.dc-work`) and one
  status line (`shell_status.gohtml`: running coders and shells, steered
  coders, the server status as a dropup, the version with the update check).
  Below lg the rail and the status line go, a tab bar with the same five areas
  stands at the bottom (`shell_tabbar.gohtml`), the list column and the work
  surface share the screen one at a time (`data-focus` on `.dc-app`, switched
  by `[data-dc-focus]` from `app.js`), and the controls from the rail's foot
  stand at the end of every work head (`shell_head_tools.gohtml`, placed by
  `work_body.gohtml`). `app_start.gohtml` opens the grid and paints the rail,
  the page renders its list column through `ctx_start`/`ctx_body`/`ctx_end`
  (a dict with `Title` and `Count`) and its work surface through `work_start`
  (opens the head with the phone's list button, the page fills the head with
  a `dc-work-title` or a `dc-work-tabs` nav and its actions) and `work_body`
  (padded, `dc-narrow` for forms) or `work_body_fill` (the terminal and the
  editor size themselves), and `app_end.gohtml` closes with the status line,
  the tab bar and the overlays. The projects page is the one exception, its
  `dc-project-list` element is the work surface itself and closes with
  `app_close.gohtml`. The grid reads the list column's presence from the DOM
  (`:has`), so a page without one needs no class. The settings pages share
  `settings_ctx.gohtml` (the sidebar rows of `settings_nav`), the terminal
  pages `terminals_ctx.gohtml` (the strip's entries as rows, the group's
  members nested), the docs and the projects page build their own. The two
  work areas are reached through one fixed address each, `/editor` and
  `/terminals`, and the server resolves it (`handleEditorEntry`,
  `handleTerminalsEntry`): they answer a See Other with `Cache-Control:
  no-store`, never a permanent redirect, or the browser would keep reopening
  what the first click resolved to. No page therefore carries a link that
  ages, and nothing follows a moving context client side. **Where somebody
  was is server state**, two capped `internal/recent` stores beside the
  unbounded `recent-projects.json` (`recentEntries`, five names each):
  `recent-editor-projects.json` keyed by project name, written where the
  editor page renders, and `recent-terminals.json` keyed by session id,
  written in `terminalFocused`, so a pane made active inside a split counts
  too. `recent-projects.json` stays unbounded, it sorts the whole projects
  list and a dropped entry there is a project that loses its place. No cookie, no session, no browser storage: a phone picked up in the
  evening opens what the desktop was on. The editor entry walks the
  remembered projects newest first and takes the first that still exists,
  else the project used last anywhere (`recent-projects.json`), else the
  first of the list, else the projects page with the info notice that one has
  to be created. The terminals entry walks the remembered ids the same way and
  takes the first that is still running, a grouped one on its split page with
  the pane focused, else the first entry of the strip, else the area's own
  empty page (`terminals_empty.gohtml`, a 200 and the one answer of the two
  entries that is not a redirect): the column, its plus menu and a New coder
  and a New shell action, so the first terminal is started where it will run.
  Neither rail entry is ever disabled, and the one thing neither area can
  answer for itself is a missing project: then both hand over to the projects
  list with an info notice saying one has to be created. A dead row explains
  nothing, and a page that can do the thing beats a page that names it. The editor remembers the project
  and nothing else, a file or a scroll position is the page's state, not the
  area's. `ActiveTab` marks the area (`projects`, `terminals`,
  `editor`, `settings`, `docs`). Below lg the list columns give way to the
  sheet (below), a wide screen has the rail and the list columns for everything it
  lists. On the terminal pages the list column is the tab strip itself
  (`terminals_ctx.gohtml`: `terminal-tabs` with `data-tabs-vertical`, rows
  instead of tabs, the plus menu in its head, the terminal settings behind
  the gear in the work head); no stop or delete stands in the work head, the
  row's close control and menu on a wide screen and the sheet's row menu on
  a phone are the way, for a split's members too. A close leaves the browser
  on the right neighbour, and with no neighbour left on `/terminals`, not on
  the projects list: closing the last terminal is the moment the area's empty
  page is for, and the entry sorts out what to show, another client's session
  included. The landing the server offers a form post (`projectLanding`)
  stays the projects list, that one answers a stop from there. The work body is the column that scrolls, never the page:
  `body.dc-body` has no overflow, `overflow.js` measures every page against
  that.
- **The list column stays live:** `@dc/ctx` listens for the `projects` and
  `terminals` events (only inside the signed-in shell, the events module is
  imported lazily so the login page opens no stream), refetches the current
  page and swaps only `.dc-ctx-body` and the title, scroll position kept.
  **That pull belongs to the page it was asked for.** A boosted navigation can
  leave that page while it is in flight, and its answer describes the page
  that was left: `refreshCtx` remembers the address it asked for and drops an
  answer the location has moved on from, and `swapCtx` refuses a document
  whose `.dc-app` names another area, so no caller can paint a foreign column.
  Without it an action that starts a terminal and then navigates (the docker
  menus' Logs and Shell, which do both in one go) landed on the terminal with
  the project index still standing in its column. A full load is not exposed
  to this, it throws the pending answer away with the document; only the
  boosted path is.
  A list that wants this carries `data-ctx-list="projects"` or `"terminals"`;
  the project index also follows `terminals` for its running counts. The
  index sorts like every other project list, through `@dc/project-sort` with
  its `INDEX` field set (`data-index-*`, never `data-project-name`, which the
  runners and the notifications reserve for the cards): `dc-project-list`
  applies the chosen mode to the index along with the board and again on
  every `dc:rendered` swap of the column.
  The index is `dc-project-index` (`components/project-index.js`), the list
  element itself, so every swap wires its own rows; an open menu stands
  through a swap and acts on the facts its row carried. The three dots
  (`[data-index-menu]`), a right click and a touch long press open: open
  project, open editor, new coder (the create dialog), new shell (`POST
  /shells/new` with `data-index-path`, lands on the shell), the board's git
  entries, the compose entries where the project has stacks, Delete project
  with the board's confirm and `ProjectRow.DeleteNote`. The row carries what
  the board's buttons carry, `@dc/project-actions` builds the entries for
  both (`gitMenuItems`, `composeMenuItems`, `deleteProject`), and the
  projects event takes a deleted row off every surface.
- **The list column remembers its width and its place per area:** the
  `[data-ctx-resize]` handle at the column's right edge (desktop only) drags
  `--dc-ctx-w` on `.dc-app`, 200px up to half the window, a double click puts
  the default back; `initCtxLayout` (`@dc/ctx`, run on load and on every
  `dc:navigated`) stores the width under `dc-ctx-width:<area>` (`data-area`
  of `.dc-app`) and restores it, the way the editor keeps its layout per
  project. The `.dc-ctx-body` scroll position goes under
  `dc-ctx-scroll:<area>` through `keepCtxScroll`, but only for a column that
  asks with `data-ctx-keep-scroll` (`KeepScroll` in `ctx_start.gohtml`, the
  projects column and nothing else): the terminals strip and the assistants
  list center their active row on connect (`revealActive(true)`), a put back
  position would undo it. The phone's
  sheet reads the same attribute off the column it just built, so the
  projects sheet comes back where it was left and the others open at their
  top (`ctx-sheet.js`, after `decorate()` so the sort and the filter have
  run, in the same task as the insert so nothing paints at the top first).
- **Below lg the list column is a sheet.** The page's own `.dc-ctx` is
  hidden there; `[data-ctx-area="<area>"]` (the tab bar's Projects, Terminals
  and Settings buttons, nothing else) opens
  `dc-ctx-sheet` (`ctx_sheet.gohtml` next to the swapped region,
  `components/ctx-sheet.js`), which pulls `GET /ctx/<area>?path=<current>`
  (`ctx.go`: the very partial the page renders, `projects_ctx`,
  `terminals_ctx`, `settings_ctx`, `docs_ctx`, with the page's QuickNav so
  the current terminal is marked and the create links carry its project) and
  shows the column from the bottom, a fixed 52vh so a filter never resizes
  it, standing on the tab bar (`bottom: var(--dc-tabbar-h)`, the bar stays
  usable, another area's button swaps the content, the same button again
  closes), the column head as the sheet head with its back button turned
  into the close, the list ending where the sheet does. A row navigates and
  the sheet closes on the click; Escape, the backdrop, `dc:navigated` and
  `show.bs.modal` close it too. A fresh open shows a spinner placeholder
  after 150ms and, when the fragment fails, a message with a Try again
  button (`placeholder()` in `ctx-sheet.js`, marked
  `data-ctx-sheet-placeholder` so a live refresh treats it as no column;
  every load carries a token, a stale answer never paints) (the create dialog stops a link's click in
  the document's capture phase, before the sheet sees it). The sheet's own
  click listener stays in the bubble phase and skips a `defaultPrevented`
  click: the row menus and grips in the strip prevent theirs, and a capture
  listener closed the sheet before the menu opened. Phone only extras carry
  `d-lg-none`: the filter row
  (`ctx_filter.gohtml`, `[data-ctx-filter]`, a row between head and body so
  the list scrolls under it; the projects one matches the name plus its
  main's, the terminals one the row text. Each area remembers its query
  under `areaKey("filter", area)` from `@dc/ctx`, `dc-ctx-filter:<area>`
  like the column's width and scroll, and a stored one hides its rows the
  moment the sheet opens. The projects board keeps its own field under
  `dc-project-filter`: two lists over the same projects stand on one phone
  screen, and neither writes the other's memory), the
  projects sort menu (`[data-ctx-sort-option]`, the shared `dc-project-sort`
  key), and on every strip
  row the three dots (`[data-tab-menu]`, opens the row's context menu at the
  button) and the grip (`[data-tab-grip]`): on touch a drag starts only from
  the grip, the strip itself is `touch-action: pan-y`, so a finger on the
  row scrolls. The terminals sheet and the assistants sheet open with the current row
  centered (`revealActive(true)` on both elements, the focused member's row
  when the page is a split; refreshes keep `nearest`, so a live update never
  moves the list). A drag
  moves units: a split row travels with its member rows (`unitRows`, one
  transform for all of them, the others shift by the unit's height), the
  edge zone is measured on the scroller (`.dc-ctx-body` in the column and
  the sheet), and the click suppression after a drag lasts one task, a
  touch drag has no click to swallow and the next tap must land. The gesture
  itself is `@dc/rowdrag`, whose defaults are this strip's behaviour to the
  pixel, and the assistants' column is the other caller: it passes what differs
  there and changes nothing here. The strip captures the pointer on the press
  and measures from where the threshold was crossed. The carried
  rows are the topmost thing on the page (`.terminal-tab-dragging`, opaque,
  a z-index above every layer) and the row under the pointer wears the 2px
  frame of the drop target (`.terminal-tab-group-target`), the active row
  included: both rules name the row class as well, because Tabler's
  `.list-group-item-action:not(.active):hover` and the column's
  `.dc-rows .list-group-item.active` outrank a plain class and once put the
  carried row under its neighbours and the active row's bar over the frame.
  A tap on a split row carries the pane this device was on last in its
  address (`aimSplit`, from `dc-split-active-<gid>`): a plain open lands on
  the first pane, and on a phone the hidden panes never boot, so the
  remembered pane could not restore itself there. A split row is followed by one `.terminal-tab-member` row per
  member (phone only, `data-tab-group` names the split, no `terminal-tab`
  class so the strip order never sees them), whose menu adds *Remove from
  split view* (`POST /terminal-tabs/ungroup` with that one id) and whose
  grip reorders the members among themselves (`memberGroup`, no group
  target, `POST /terminal-tabs/group` with the member ids in the new order,
  which is what sets `@dc_tab_gpos` and so the pane order everywhere). The projects sheet refetches on `projects` and `terminals`
  events, the terminals sheet is a `terminal-tabs` instance and refetches on
  its own. The quick nav (FAB, palette, `/quicknav`) is gone.
- **Third-party assets come from jsDelivr:** Tabler 1.5.1, the icon webfont,
  Bootstrap's JS, SweetAlert, CodeMirror, xterm and the jingle player are
  loaded from the CDN, nothing is vendored. The shell's CSS is written
  against Tabler 1.5.1 (its `a:hover:has(.icon)` rule, the alert variables).
- **The palette:** style.css sets the cockpit's colors on Tabler's variable
  names at the root for both schemes (`--tblr-primary`, the surfaces, the
  border color), so every Tabler component and every `--tblr-*` reference in
  custom CSS follows. The session icon (`dc-term-icon`) is a tinted tile:
  grey idle, green running, purple steered or assistant, a pulsing ring while
  working (green, and purple on a steered coder and on the assistant,
  `.steered.working` and `.assistant.working`, dot, glow and ring together), and the
  news dot below at its top right corner. **Both marks draw outside the tile**,
  the working dot rides a path a pixel past its edge and the news dot pulses to
  three times its size, so a container that carries a session icon must not clip
  it. That is what took `overflow: hidden` off `.project-chip`: it was there to
  keep the two parts' hover backgrounds inside the rounded pill, which the parts
  now do themselves (`.project-chip-main` rounds the start corners,
  `.project-chip-x` the end ones, and a chip without an X rounds all four on the
  main part), and the pulse is whole again, and it is why the phone's tab bar
  carries a `z-index` above everything the work surface furnishes itself with
  (its sticky footer at 10, the editor's panes and splitters up to 18) and far
  below what opens over the whole app (the sheets at 1045): the mark of the tab
  it carries bleeds upward out of the bar. What a scrollport cuts stays cut, a
  list has to clip what scrolls under its head, and so does the editor's
  terminal strip, which scrolls sideways. Steered is the open job of `Watcher.Marks()`, the same source
  the assistant's steered list reads, rendered as the `steered` class
  wherever a coder icon shows, the hidden member spans of a split row
  included.

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
  signal close it; programmatic scrolls must never close it). A row that leads
  to a page carries `href` and renders as an anchor (`target: "_blank"` for an
  address outside the app), so it opens in a new tab, copies and middle clicks
  like any link: a plain click runs its `action` where one is given, else
  pe.js takes it, a modifier click stays with the browser. Only what acts
  (a POST, a dialog) is a button with an `action`. The editor's docker sheet
  renders the same `@dc/docker` items itself (`sheetActionRow`), so it takes
  `href` and `target` too; a new renderer of menu items has to. The arrow key
  movement over its rows is exported (`rowsOf`, `focusRow`, `stepRowFocus`)
  and takes a row selector, so a second list of rows walks the same way
  instead of growing its own: the editor's sheets are that second one.
  `focusRow` is the one that reaches a row, and it scrolls the container it
  was given, never the page. A menu opened from the keyboard opens on its
  first row, a menu opened with a pointer marks nothing: what decides is the
  last input before the open (`openedByKeyboard`, a keydown sets it, a
  pointerdown clears it). `openMenu` focuses that row, one `shown.bs.dropdown`
  listener in app.js does the same for every Bootstrap dropdown unless the
  menu already holds the focus or walks its rows itself (`data-own-selection`,
  the plus menus, which read the same flag for their own mark). A pointer
  moving over a row marks it, the same mark the keys move on, and leaving
  the menu clears it (`followPointer`, `focusFollowsPointer` for the menus
  that mark by focus; a touch pointer does not hover and is left out), so
  the arrows continue from where the mouse stands. The menu's
  keys are caught on the window in the capture phase: Bootstrap's dropdown
  data api listens on the document in the capture phase and answers the
  arrows inside any `.dropdown-menu` by opening the nearest toggle, which for
  a body mounted menu is the first toggle in the body, the bell. Row menus
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
  Every row with the gesture sets `-webkit-touch-callout: none` and
  `user-select: none` in style.css, or iOS answers the hold with its link
  preview.
  iOS also ignores `draggable="false"` on links and its drag lift ends the
  touch stream too, so the handler prevents `dragstart` on rows and
  `touchcancel` does not kill an armed press (a real scroll delivers
  touchmove past the movement threshold first).
  `preventDefault` on `touchend` is what stops the lift from following the
  link; when a cancelled stream delivers no touchend, the suppressed click
  does.
  A menu opened by a resting finger ignores that finger's wobble for a moment
  (`noteTouchOpen`), otherwise its own `touchmove` closes it at once. The editor
  tabs, the file tree, the chips, the project index, the assistants list and
  the tab strip use it.
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
  without an origin island (footer controls, paste, direction
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
  303-redirect to `/splits/<gid>?focus=<id>`. **A terminal that takes the
  focus moves its project up the recent list**, and one place does it,
  `terminalFocused` (touch the project, mark the target read): the attach
  pages call it while they render, the split page for the pane it renders
  focused (that pane's project, not the group's shared one, which a mixed
  split has none of), and an open split posts
  `POST /splits/:id/focus` with the member id whenever the focus moves to
  another pane, because activating a pane there changes no address. The
  client sends it from `terminal-split`, starting from the rendered focus,
  so a plain open says nothing and a pane that is already active never fires,
  by mouse and by keyboard alike (both end in the island's `activate`). The `terminal-split` element
  owns the pane headers (context menu, drag reorder via CSS `order` +
  re-POSTing `/terminal-tabs/group`). The group tab's close control closes
  every member (confirmed); ungrouping is the non-destructive context menu /
  header / pane-remove path. Decisions and endpoints: `docs/split-view.md`.
  **A split arranges its panes in columns, and a layout change is a style
  change.** One more member option says which of them share a column,
  `@dc_tab_gcol`; a member without one renders as a column of its own, which
  is what every group looked like before columns existed, so there is nothing
  to migrate. `@dc_tab_gpos` stays the group's one global order: it drives the
  strip label, the sheet, the mobile swipe and the stacking inside a
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
  on a phone from the row's grip) says nothing about columns and what it says nothing
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
  paints the page you just left as the active tab. **Every terminal fits its
  box**: the split fills the work body, the panes divide it, and each island
  takes its rows from the box it is given (the `fitAddon` path), so grouping
  or stacking never changes the page height. Nothing is ever drawn outside
  that box and nothing inside it scrolls. The inset between the pane edge
  and the first cell (`--dc-terminal-inset`, 3px) is padding on `.xterm`,
  never on the host: the fit addon reads the host's border box and subtracts
  only the padding of `.xterm`, so padding on the host would fit a row and a
  column that the clip cuts off. The host is `overflow: clip`: a
  canvas past the box, a subpixel or a server size beyond it, is cut
  silently and never draws a scrollbar (with `overflow: auto` every pixel
  of overhang drew bars in both directions on a 2x2 split).
  There was a setting that gave the pane rows past the fit
  (`dc-terminal-extra-rows`) and let the host scroll for them. It is gone, and
  what it was for is gone with it: it existed so a person could reach text that
  had left the screen, and it paid for that by laying every full screen program
  out for a height nobody can see, and by giving the view a second owner, the
  browser, which moves a scroll position of its own accord. Reaching that text
  is the history sheet's job now. Do not bring rows past the fit back to solve
  a reading problem.
  **The history sheet is where text is copied from** (`terminal-copy.js`, the
  copy button in the control row, `GET /coders/:id/copy` and
  `/shells/:id/copy`). A terminal draws to a canvas and holds no text anybody
  can select, so the sheet puts what the terminal has said into the page as
  ordinary text: selecting, scrolling and copying are then the browser's job
  and, on a phone, the system's own handles. It is a snapshot from the moment
  it opened and never follows the live stream, because a target that keeps
  moving is the one thing nobody can copy from. `white-space: pre-wrap` and not
  `pre`: a soft wrap is drawn only, the text keeps its own line breaks, so a
  table copies exactly as it stood even where the screen is too narrow to show
  it in one piece.
  Where the text comes from is the same order the activity reading uses, for
  the same reason: the coder's own record first, the screen only when there is
  none. `coder.TranscriptReader` is that capability, optional beside
  `ActivityReporter`, and all three coders implement it, claude from its
  transcript, copilot from its event log, opencode from its rows. It asks for
  what was said and may not flatten or cut inside a message, which is exactly
  what `Activity` may do; that is why it is a second method on the same record
  and not a bigger budget on the first. It also takes less out of the record
  than `Activity` does: a tool call leaves nothing behind here, no `coder ran`
  line, because this is text somebody copies and that a tool ran is the coder's
  bookkeeping. The activity reading keeps naming the tools, it answers what the
  session last did. Everything else answers with
  `capture-pane`, which never attaches, so the pane keeps the size the client
  that owns it gave it.
  How much is shown is the reader's choice and the unit follows the source,
  messages for a record, lines for a screen, kept per unit in localStorage
  (`dc-copy-messages`, `dc-copy-lines`). A coder answering with its screen
  offers no choice at all: it runs on the alternate screen, which keeps no
  scrollback, so every amount would answer with the same picture. Behind the
  choice sits one cap that is not a product decision, `coder.TranscriptCap`,
  so that a single pasted file cannot become the whole answer.
  The size in tmux is one per session while every open view
  has a box of its own, and a `terminal-size` from the server past the box
  (another device attached or resized, a stacked pane is the smallest box
  there is) would leave the canvas cut: `keepRows` answers it with the rows
  the box holds and, on a fine pointer, its own columns (a resize capped to
  the box, deduped against the last request so the server's row floor cannot
  loop), so the smallest open box decides and the taller view keeps a gap.
  A phone keeps the server's columns, a mirror always followed the desktop's
  width. The phone's cursor input, placed two rows under the cursor as the
  keyboard anchor, is clamped to the last row, so nothing inside the box
  reaches past it. The wheel goes to the program, like over SSH; Ctrl with the
  wheel stays with the browser (zoom, pinch).
  **The view is brought to the cursor when a person acts**, never because the
  cursor moved: on the first snapshot of a page, on focus, when a pane is come
  to, and while typing (`followCursor`). A cursor move is not a signal that can
  say who caused it, and a full screen program walks its cursor over its whole
  screen with every redraw, so following it pulled the view back and forth
  under whoever was reading. What a person did is known on this side, and that
  is what it hangs on.
  **A terminal can be created straight into a split**, from the
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
  one a view on it (the strip shows everything, the editor's
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
  islands carry `embedded`: rows fit the pane like everywhere else
  (`MinTerminalRows`, 5, is the server's floor), the size observer watches
  height too, a hidden pane does not connect. Open state,
  active tab and height are
  per project (`dc-editor-term-open:<project>`, `-active:`, `-height:`).
  Inside the panel the terminal keys mirror the attach pages; the panel
  owns them as long as the last click landed inside it, a focus-owner flag,
  because a click on the bare strip focuses nothing. The editor's own
  shortcuts skip events from inside the panel. A coder created through the +
  menu comes back: the create form's action carries the return target **and
  the `panel=1` marker** through the POST, that pair redirects to
  `.../editor?terminal=<id>`, and the panel activates that tab **after** the
  tab restore, whose own `editor.focus()` lands later. The marker is what
  earns the comeback, the return alone cannot: the strip's create links
  on an editor page carry the same editor return for their Cancel, and
  without the marker a create lands on the coder's own page like a created
  shell does, which is the correct place wherever the panel does not exist.
  The id reaches the client as `data-editor-terminal` on the page, never out
  of the URL: a boosted navigation swaps the body before it pushes the
  address, so the editor's init still reads the previous one. A coder pane gets the attach page's files
  modal, a `[data-terminal-footer]` button block the island's activation
  unhides, and `coder-file-upload` is re-inserted after the mount so its drop
  zone finds the terminal. The modals host moves to `document.body` like the terminal
  panel's, and `dc-host-float` keeps ducking to 5 for as long as a popup
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
  quick-access palette: active terminals, an Assistants row, inactive coders,
  an Editors section (one row per project, `ProjectNav.EditorURL`) and a New
  section (New coder / New shell rows reusing the plus menu links, so the
  current project is preselected on the create form), all filterable. The
  assistant link and the `[data-tabs-editors]` list are hidden data inside
  the plus menu, never menu entries: the menu itself offers only what creates
  a terminal.
- **The assistants are a page.** `/assistants` is the area's entry and decides
  in `handleAssistantsEntry`, never in a rendered link, which one opens: the
  assistant last looked at (`assistantRecent`), else the first row of the hand
  sorted list, and the empty state only when there is no assistant at all. Like
  the terminals entry it answers with a **See Other and `Cache-Control:
  no-store`**, never a permanent redirect, or the browser would keep reopening
  the assistant of the first click. The list is not the other half of that
  choice, it stands in the column beside the thread and marks the open row,
  here and in the phone's sheet, which render the same column.
  `/assistants/:id` opens one, and
  `assistant_page.gohtml` renders both: the list column
  (`assistant_ctx.gohtml`, a `dc-assistant-list` with the `history` attribute
  as the `.dc-ctx` itself, so it refreshes its `[data-assistant-body]` from
  `/ctx/assistants?path=` on the assistant event and keeps the marked row in
  view over the swap (`revealActive()`, `nearest`) after centering it on
  connect (`revealActive(true)`, the strip's way, called once more by the
  sheet after its filter ran), its rows carry the assistant
  menu and are dragged into order, the new assistant control sits in the head
  with its own form id prefix, and the phone's sheet adopts the same column
  through `/ctx/assistants`, with the phone's `ctx_filter.gohtml` row over the
  rows like every other list column), then `dc-assistant` as the work column itself
  (`class="dc-work"`, so the head's voice menu and the composer are its
  children), and an aside (`offcanvas-xl offcanvas-end`,
  never with the plain `offcanvas` class, which would keep it fixed) that
  stands inline from xl up and below xl is the sheet the head's eye
  (`d-xl-none`) opens.
  **The aside is one thing with two halves, and it is called Watching**: the
  coders this assistant steers and the triggers it waits for, which to a reader
  are one thing, work the assistant carries on by itself without being asked
  again. They are **two tabs** and not two sections under each other
  (`assistant_watching_tabs.gohtml`, Bootstrap's own `data-bs-toggle="tab"` in
  a `nav nav-bordered`, one `tab-pane` per list): under each other the second
  one starts below the fold on every window that matters, and a half nobody
  scrolls to is a half nobody uses. Each tab carries an icon, a name and a
  count, each pane one self refreshing list with a one line empty state, and
  the strip stands outside the body a list swaps, so a refresh leaves the
  chosen tab alone. **The two tabs share the row in equal halves**, Bootstrap's
  `nav-justified` (`flex-basis: 0` plus `flex-grow: 1` on every link) and never
  `nav-fill`, which divides by the text and hands the longer name the larger
  share; they carry `justify-content-center` because Tabler makes a `.nav-link`
  a flex box, so `nav-justified`'s own `text-align` reaches nothing, and there
  is no gap between them, `nav-bordered` takes a link's horizontal padding off
  so the active half's border runs to the middle of the row and says where the
  half ends. Equal halves hold only while the longer name fits into one, which
  is measured on the phone, the narrowest the aside ever is; if it ever stops
  fitting the name is shortened, never the type. **What makes a trigger stands
  in the triggers**, at the head of that pane: it is the pane's own child, so
  the tab carries it and no visibility is switched by hand, and it stands
  outside the swapped body like the strip does. Beside the two tabs it read as
  a control for both while it only ever made a trigger. It runs the full width
  of the list and carries no colour, at `btn btn-sm`, the weight a trigger
  row's own Open coder and Change wear: a coloured block beside nothing reads
  as dropped in, the full width closes the strip off above the list, and colour
  in this aside is left to the one action that destroys something. The empty
  state stays one line and offers nothing, because that button is already the
  line above it. **A row is folded and the fold holds the text**: the head
  says who it is and where it stands (a coder's name and project, a trigger's
  event and where it listens), and the fold under it holds the named parts
  (`datagrid-title`, prompt, criterion and last report on a coder; task and
  last fire on a trigger) and the counters, because three grey lines under
  each other were one text nobody read. **The actions stand outside that
  fold**, under the row and always: opening the coder, its editor, taking it
  back, and a trigger's Open coder, Change and Remove are one press from a
  shut row. Reading what a coder was sent is a question somebody
  asks now and then, acting on it is the everyday case, and a shut row that
  answers neither has to be opened before it is of any use. A shut row is
  taller for it and several of them are a column of buttons: that is the price
  and it was weighed and taken (2026-09-20), so do not fold them again or make
  them a size of their own. A steered coder's row offers both ways to look at that coder, its
  screen and the editor on the project it works in, the second one only where
  there is a project, and the two names on the row are those ways too: the
  coder's name opens the coder, the project's name the project. Nothing on that
  row makes a trigger: the one way to a new one is the button over the trigger
  list (removed 2026-09-20, the row's own Add trigger).
  **The row's own icon is the state, and it is the only thing that says it.**
  One fact is drawn once: the steering wheel of a job and the bolt or the
  clock of a trigger carry the colour, and the word that stood in a badge
  beside them is now the icon's `aria-label` and `title`, the way a note's
  icon badge in the thread names itself. Two renderings of one fact drift
  apart, and did: an open trigger's icon read `running`, which is green, while
  the badge next to it read blue for the same state. The colours mean the same
  on both kinds. Purple (`steered`) is alive, a job being steered and a
  trigger that still stands. Purple with the dot running along the icon's edge
  (`working`, the same pair every session icon uses) is a turn of this
  assistant on it right now, a check on the job or the trigger's reaction, and
  the marker `data-assistant-working` says which. Red (`text-danger`) is over
  without arriving: a job closed blocked or expired, a trigger that expired
  having never fired, and a trigger whose last reaction broke off
  (`Trigger.Broke`, written where a reaction concludes and cleared by the next
  one that comes back whole). Grey, the icon's resting tint, is done and
  nothing to do: a job done or released, a trigger done, and one that expired
  after it had fired at least once, which arrived. Green is deliberately
  not used here, it says "running" on every terminal icon in the cockpit and a
  second meaning in one aside is no meaning. The state itself travels as the
  icon's `data-assistant-job-state` / `data-assistant-trigger-state`, which is
  what a test reads. The events waiting in a trigger's batch window ride the
  corner of that icon as `.dc-steer-badge`, the count the tabs above wear, at
  the offset the news dot hangs at, and they stand in the icon's label too.
  None of it is painted over by the row: an aside row is a plain
  `list-group-item` and no `list-group-item-action`, so hover and active leave
  it and its icon exactly as they are.
  **The trigger form is the create dialog, and the stand is rendered into it.**
  `GET /assistants/triggers?form=new|edit` answers the form, alone with
  `modal=1` and inside its page without, the way the create forms do (see that
  rule below); the POST is the same path with the same `form` field it always
  had, so the page and `trigger-new`/`trigger-edit` still post the
  same fields to the same route. What fills it is the server
  (`assistantTriggerForm`): a new one on the defaults, one begun on a
  coder's row on `?job=<terminal>`, a change on `?id=<id>` with every field on
  what stands, the event locked, the expiry on what the trigger has left,
  and a picked terminal the selects no longer offer appended so a save cannot
  drop it. Nothing fills a form in the browser any more, which is what took the
  form out of the aside, `triggerEditJSON` and `jobTriggerJSON` out of the
  server and `fill` out of `dc-assistant-trigger`; what is left there is the
  switching of the fields, one rule for all of them (`showBox`): a field that
  is hidden is disabled with it, so it posts nothing, and a field nobody named
  is one the server leaves standing. The target fields show for their own
  event, the mode and the batch window for every event but a schedule, which
  has no window to fold anything into. Nothing writes a value into a field any
  more; the form once put a zero into the window for a schedule, which a
  stored value had to be defended from.
  **The expiry is a number with a unit beside it**, minutes, hours or days,
  and **No expiry is the fourth entry of that select**. Five guessed spans
  stood here and the page could say less than `--until` could; every value
  these two fields produce is a span the command takes and nothing beyond it,
  so nobody has to know a notation and a phone answers with a number and one
  tap. The unit is where the reading starts and the number carries it where
  there is one (`untilUnit` onto `until`, `triggerNoExpiry` the word both the
  select and `--until` spell `never`), so one reading and one parser serve the
  form and the command alike. No expiry belongs in that select and not in a
  box beside it because it answers the same question, how long, and it is the
  answer that leaves the number nothing to count: the element greys the number
  out while it stands (`data-trigger-unit` over `data-trigger-span`, the one
  switching beside the target fields it still does), which is a disabled field
  whose reason stands right next to it, where a box left it a field that was
  dead for no visible reason. The markup disables nothing, the select wins over
  the number on the server anyway, or a page whose JS never ran could pick a
  unit and still not type. The field takes the whole row (`col-12`, with the
  batch window and the one shot box sharing the line below it, so the form is
  no taller than it was): beside the window the number was too narrow to read
  its own placeholder in. A change opens on what the trigger has left in the
  largest unit that keeps that number whole (`triggerSpan`, so 90 minutes reads
  as 90 minutes and never as 1.5 hours), because an empty number puts the
  select on No expiry: a form that opened empty on a trigger that expires would
  take its expiry away with the next save of the task. What is stored stays a
  moment while the field asks for a span, so saving re-anchors the expiry to
  now, which is what the field says it does. **The task
  field follows what is typed, and there is one computation of that in the
  app**: `growTextarea` in `@dc/dom`, the assistant composer's growth, the
  height reset to the content's and capped, the box scrolling past the cap.
  The share is the helper's (`GROW_SHARE`) and a caller hands over only the
  surface its field sits in, asked for after the reset: the composer's
  transcript scroller, the form's window. A measured limit was built first and
  taken out again: the trigger form is the taller kind and already fills a
  laptop window on its own, so the room left around it is nothing and the field
  stayed at its two rows, which is the peephole this fixes. What the growth
  needs instead is a dialog that stays put while its content grows, so **a form
  whose field grows says so** (`data-form-scrollable`, read by
  `dc-form-modal`, which puts `modal-dialog-scrollable` on the dialog for that
  one form): the header and Cancel and Save stand still and the body scrolls.
  It is per form and not the dialog's default, because a body that scrolls
  clips what stands inside it, and the project form's branch picker is a
  `dropdown-menu` that has to reach past the body it is in. The growth is asked
  on every keystroke, on the event switch that reshapes the form, and on
  `shown.bs.modal`, which is what makes a stored task stand grown before
  anything is typed: the dialog is `display: none` while the form is put into
  it, and a field measured there reads zero.
  The aside's own head (`dc-ctx-head` with the title and the close, the
  sheet's way out) wears `d-xl-none` too: inline it stands under the page's
  head, nothing is there to close, and the list starts at once. The width
  decides, never the offcanvas state, it is one element in both sizes; Tabler
  hid the old `offcanvas-header` the same way. **One number outside, the split one touch later.** The button
  carries the open jobs plus the triggers that still fire, because it is all a
  phone sees of the aside and a half it cannot count is a half nobody finds;
  which of them it was stands on the two tabs inside, one count each, and the
  two add up to the button by construction (`WatchingOpen`). Every count hides
  at zero instead of standing as an empty pill. No badge fetches:
  `dc-steer-badge` reads the number off the list bodies that already stand on
  the page, which carry it as `data-assistant-jobs-open` and
  `data-assistant-triggers-open`, and every list says
  `dc:assistant-counts` when it swapped one in, so a badge costs no request of
  its own. All three are the one badge, `.dc-steer-badge`, out of the flow and
  `var(--dc-steer)` on white, because a number that comes and goes must move
  nothing around it. Only where it hangs differs, and it follows what it
  belongs to: on the button the corner of the glyph, on a tab the end of the
  label (`data-assistant-tab-label`, the anchor, with the badge growing to the
  right of it at the gap the icon keeps to the word). **On a tab it stands
  raised**, its bottom edge on the label's middle line (`bottom: 50%`, no
  transform), so it covers the upper half of the word's ink, the share a corner
  badge covers of its glyph, and reads as a note at the word instead of a
  second word beside it. Raised it still ends inside the tab's own padding, so
  it reaches neither the row above nor the scroller's edge and the row keeps
  its height. The tab is no anchor: it
  is half the aside wide with its label centered, so every corner of it stands
  a different distance from each of the two words, 16px from Steered coders and
  38px from Triggers, and the first tab's corner lands on the seam between the
  halves. **What the badge must survive is the surface under it**, and the row
  paints two: `nav-bordered` takes Tabler's 4% wash off `:hover` but not off
  `:focus`, which keeps it (`.nav-link:focus, .nav-link:hover` sets
  `--tblr-nav-link-hover-bg`), so a focused tab is washed while a hovered one
  is not, and pressed and active paint nothing, the active half being the 2px
  border and the primary colour alone. The badge is opaque and reads the same
  over either, which is the whole reason it is this badge and not a translucent
  `bg-*-lt` pill, the one that went muddy over that wash. The focus ring is a
  box shadow outside the button's border box and never reaches it. **A live update leaves what the reader put where**: a list swap
  carries the ids of the rows that stand unfolded onto the rows that arrive
  (`keepUnfolded`, the `show` class and the control's `aria-expanded`, which is
  what Bootstrap reads before it instantiates a collapse), the strip and the
  scroller are outside what is swapped, and the transcript's own swap
  (`syncFromServer`) moves the standing aside into the fresh surface instead of
  rebuilding it, because its two lists keep themselves up to date anyway. An
  event of the assistant arrives every few seconds while coders work, and a
  fold that closes under the hand is worse than a row that is a moment old.
  Its size is set against the head button, not copied from the rail's
  count: the head's `btn-icon` is 28px where the rail's is 40px, so a 16px mark
  would sit on the wheel instead of beside it. 14px in the corner leaves the
  same third of the glyph covered that the pre-redesign badge left (18.4px at
  2.6px in a 40px button, 33 percent of a 14px glyph), which is why the wheel
  stays readable. The sheet and the inline aside start their
  list at the same distance from every edge, which is the offcanvas body's own
  padding: the aside's body spaces its children by hand and not by a flex gap,
  so a head sits on its own list and the second section keeps its distance from
  the first.
  The memory is a sheet of the layout (`assistant_memory_sheet.gohtml`,
  `#assistant-memory`, a plain `offcanvas` next to the ctx sheet on every
  page), opened by the brain in the list's head, from the assistant page's
  column and from the phone's sheet on any page alike (the ctx sheet closes on
  `show.bs.offcanvas`); it renders empty and its `dc-assistant-list` pulls
  `/assistants/memory` on every `show.bs.offcanvas` and counts the entries into
  the header badge, so no page pays for the list. The add form ids carry
  `AssistantMemoryData.Prefix` (`/assistants/memory?prefix=`).
  The steered coder rows' actions are `btn-sm`. Deleting an assistant sends
  the browser to `/assistants`; a notification names `/assistants/<id>#message-<id>`
  and the surface lands on the message itself (`landOnHash`, pulling
  `?all=1` once when the window held it back) since pe.js scrolls nothing. The
  surface pulls its own address on the assistant event (`syncFromServer`) and
  swaps itself only when the transcript moved or the composer went read-only,
  and never over unsaved words or a running answer. The tab bar's sparkle is a
  `[data-ctx-area="assistant"]` button, the rail's a link with the active
  state, both keep `[data-assistant-link]` for the news mark. A message wears
  a stripe on its left: blue for the user, the assistant's purple for its
  answers, grey (`dc-msg-info`) for a check's report. The column's Earlier
  list shows its newest five and folds the rest behind one row
  (`applyFold` in `dc-assistant-list`, hidden class `dc-folded`, the choice
  survives the list's refreshes); the column takes no filter row
  (`ctx_filter.gohtml` stays out, as it does in `docs_ctx.gohtml`), so the
  phone's sheet shows the whole list; a steered coder's task and criterion
  fold under its row (Bootstrap collapse per terminal id, closed by default,
  the e2e reads them through `textContent`); a running check wears a purple
  badge with a spinner. A conversation row tells working from broken: while
  its turn runs the round icon carries the working ring
  (`.dc-term-icon.assistant.working`, the coders' run on a circular path, in
  the assistant's own purple), and the orange Unfinished badge stands only for
  a turn that stopped before it was done. The two come apart at the source (`Summary.Running`,
  `Summary.Unfinished` in `internal/assistant`), no surface reads one out of
  the other, and the coarse assistant event swaps the list at both ends of a
  turn. A user message shares the answer's typography
  (`1rem`, the markdown line height). The work head carries the
  conversation title alone, the sparkle icon only below lg and in every state
  (`d-lg-none` never comes off: a running turn adds the working ring to the
  phone's head icon and nothing else, `setRunning` toggling `working` off the
  stream frames the stop button already follows, so the desktop head stays
  bare and reads a running turn off the list column beside it), and the aside
  starts with the first coder, its heading is the sheet's below xl.
- **A picture in the transcript carries its pixel size.** Every image the
  assistant renders, an attachment and one an answer points at alike, gets
  `width` and `height` from the file itself (`filesystem.ImageSize`, the
  header alone, png, jpeg and gif; anything else says nothing and takes the
  `aspect-ratio: auto 4 / 3` placeholder). Without them a transcript full of
  screenshots rebuilds itself under the reader while they arrive, and the
  browser's scroll anchoring cannot save it: it may pick the empty picture
  itself as the anchor. The upload and draft answers carry the size too, so
  the bubble the composer paints stands where the server's does.
- **The create forms open in a dialog, and stay pages.** `/coders/new`,
  `/shells/new`, `/projects/new` and the trigger form
  (`/assistants/triggers?form=new|edit`) open in `dc-form-modal` (layout, next to the
  swapped region, Bootstrap modal like the editor's comment dialog). It fetches
  the same GET with `modal=1`, the server answers the form alone
  (`*_new_form.gohtml`, one template for page and dialog, body/footer classes and
  Cancel differ), and the marker rides the action back out through the POST, so
  the POST path is still the GET path (`/projects/new` posts to `/projects`,
  unchanged). Two seams: a capture click listener for every link to those paths
  (capture, a cancelled `pe:click` would mean a full load) and `openFormModal`
  for the JS ways (`@dc/split`, `@dc/docker`, the switcher's `navigate`,
  the project form's choice select, which swaps the reshaped form into the
  standing dialog). Both fall back to the page. The marker changes the answer,
  not the destination (`formmodal.go`): a refusal comes back as the message
  (`formRefused`) so the dialog keeps the typed values, a create answers the
  location the redirect would have taken (`createLanded`, flash in the session).
  A form whose result is on the page it already stands on answers a `message`
  instead (`formStayed`, the trigger form): the dialog closes, says it in a
  toast and navigates nowhere, because the lists behind it update themselves on
  the event the action published, and a navigation would rebuild the very
  thread the person is reading. The field a form marks `autofocus` is the one
  the dialog focuses, on a fine pointer as always.
  The chips that create without a form stay one click. The first field takes the
  focus on opening, fine pointer only, no scroll. While a create runs the form
  hides behind a spinner and one line (`data-form-wait`), a refusal brings it
  back untouched.
- **A popup opens inside the modal that stands.** Bootstrap traps the focus in
  an open modal, so a popup elsewhere in the document cannot be typed into (the
  git passphrase question from a resync inside the create dialog). `@dc/dialog`'s
  `fire` targets the topmost `.modal.show` unless the caller names a target
  (`heightAuto` off with it); every popup goes through that door,
  `@dc/gitprompt` included.
- **A wait shows on its surface.** `.dc-loading-bar` is a zero height sticky
  line prepended to what is loading (the tab strip fragment). The
  project list shows no line, its refreshes swap rows in place. pe.js's button
  spinner is for `.btn` only: it needs the element's own box, and on a chip it
  leaves an empty pill with a spinner somewhere else. Other buttons only go dead.
- **Lifecycle:** set up in connectedCallback behind a re-init guard, tear down
  everything in disconnectedCallback, nothing may outlive the element. Create one
  AbortController per element and pass its signal to every addEventListener, then
  abort it on disconnect. Also close any EventSource, disconnect observers, clear
  timers, and dispose xterm (`term.dispose`) and CodeMirror (`view.destroy`). The
  heavy islands (`terminal-attach`, `terminal-input`, `dc-editor`) run their setup
  in a function that returns a teardown the element stores and calls on disconnect.
- **Theming:** the color theme follows the OS by default, and the theme
  button at the foot of the rail (`dc-theme-cycle` from `theme_cycle.gohtml`,
  on a phone a row of the menu at the end of the work head, on the login page
  next to the version) shows the mode in force and moves to the next one per
  click along a fixed ring, light, dark, follow the OS, the same order whatever
  the OS says, so no mode drops out of the round. The choice is per device
  (`dc-theme` in localStorage, absent means auto), without a toast. `@dc/theme` is the one source of the
  effective scheme: it sets `data-bs-theme`, keeps the `theme-color` metas in
  step, follows the OS while the preference is auto, follows a change made in
  another tab over the storage event, and publishes every move as a `dc:theme`
  event on `document`. Everything scheme dependent listens to that event and
  asks `isDark()`, never `prefers-color-scheme` directly, or a forced theme
  would pass it by. A user toggle rides a short `dc-theme-flip` transition
  class on the root element; an OS flip in auto mode deliberately does not,
  the e2e checks measure colors right after `emulateMedia`.
  `layout.gohtml`'s inline head script reads the stored choice and sets
  `data-bs-theme` before first paint, so a forced theme never flashes.
  Custom CSS must work in both themes: use `--tblr-*`
  variables (`rgba(var(--tblr-emphasis-color-rgb), …)` for hover/overlay tints),
  never hardcode palette colors. **Marked text is one rule for the whole app**,
  set once in `style.css` on `::selection` and `::-moz-selection` from
  `--dc-selection-bg` / `--dc-selection-fg` (the light scheme's primary and
  white, pinned as literals because the dark scheme's lighter primary would
  drop the pair under 4.5:1). The
  fill is opaque and the mark names its own foreground on purpose: a translucent
  one would hand the contrast to whatever sits underneath, and what sits
  underneath is a green or red diff row, a code block, a field. That pair is
  #ffffff on #146ecd, 5.06:1, so no surface has to be measured on its own.
  Two surfaces bring a mark of their own and are named where the rule is:
  CodeMirror hides the native mark inside a `.cm-line` and paints
  `.cm-selectionBackground` instead (and oneDark's own selection leaves a comment
  at 2.7:1), so the rule is repeated for the layer and for the text inside
  `.cm-content`, `!important` because the library's hiding rule is. The terminal
  brings nothing of its own here: its mirror layer is never selectable, and
  copying happens in the history sheet, which is ordinary text.
  **Every floating menu wears one look**, set once on `.dropdown-menu` in
  style.css out of three variables per scheme (`--dc-float-bg`,
  `--dc-float-border`, `--dc-float-shadow`): a real border, the palettes'
  shadow, and in the dark scheme a surface a step above the cards
  (`--tblr-bg-surface-secondary`) with a border a step above
  `--tblr-border-color`, because Tabler 1.5.1 draws no border on a dropdown,
  only a 1px ring inside `--tblr-shadow-dropdown`, which is black on black
  in the dark scheme. `.dc-context-menu` is a `.dropdown-menu` and takes it
  with no rule of its own; never put the `shadow` utility on a dropdown, it
  is `!important` and drops the ring with the shadow; the one menu that stays
  bare is `.editor-sheet-body .dropdown-menu`, embedded in the sheet. The
  bottom sheets (`.dc-sheet-panel`, `.editor-sheet-panel`) share
  `--dc-sheet-radius`. Palettes, CodeMirror tooltips, SweetAlert and toasts
  are untouched by it.
  The terminal screen has its own palette, picked
  in the settings menu (`dc-terminal-theme` in localStorage, every scheme
  follows the effective theme between a light and dark variant), defined in
  `terminal-attach.js`. The host element carries that palette's background as
  well, inline from `applyTheme` and as the pre-script paint in `style.css`:
  the canvas covers whole cells, so a partial row below the last line and a
  partial column right of the last one leave the host showing through, and a
  second colour there draws an edge across the pane. The tab strip follows the page theme, only the active
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
  and sends the subscribed TUIs (claude and opencode, `schemeReportCoder`) the
  mode 2031 color scheme report so a running one switches live: claude reads
  the reported value, opencode answers a report by re-querying OSC 10/11 and
  reads the fresh pane style back. The report only reaches interactive panes
  of those coders (their foreground command on the alternate screen; other
  programs would read it as keystrokes). New sessions get the pane style on
  create/resume so a fresh coder detects at startup, and claude sessions get
  `"theme": "auto"` pinned via the injected `--settings`
  (`internal/coder/claude/runtime.go`) so detection works despite a fixed theme
  in the user's global config. opencode's TUI paints its own theme colors over
  the pane, so the cockpit pins its sessions to a generated theme instead
  (`internal/coder/opencode/theme.go`): a static `dev-cockpit` theme of ANSI
  slot references and `"none"` backgrounds under opencode's themes folder,
  selected by a pin config that reaches only cockpit sessions through
  `OPENCODE_CONFIG` in the tmux environment. The TUI then renders the
  terminal's own palette, follows a palette change live like copilot and the
  mode report picks the dark or light diff tints; both files are generated
  like the notify plugin (rewritten unconditionally at every session start,
  instance free, no removal at stop), and the theme stays a compiled constant
  because an unresolved color reference crashes opencode's TUI at start.
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
**Every notification is written the same way round, and one builder writes
them all**: `Notification.Title` is what happened and nothing else, one fixed
sentence per kind, and `Notification.Detail` is what it happened to. A coder,
a shell, a backup, a git question, a compose run and an assistant all read
alike, so nobody has to work out which pattern a line follows: "Coder has
news.", "Command finished.", "Backup ready.", "Git asks a question.",
"Compose finished.", and for an assistant `Job done.`, `Job blocked.`, `Job
expired.`, `Trigger fired.`, `Trigger broke off.`, `Answer ready.` or `Answer
broke off.`. Nothing is composed out of user text up there and nothing is
ever cut, so `newsTitleRunes` is no budget the code spends, it is the bound
the wording is written to and the test pins: 32 runes, the narrowest of the
three surfaces, a phone's push, where iOS gives the title one line and writes
the app's name on the second (a lock screen of this cockpit's own pushes ran
out at exactly 32 with the system's mark among them). The bell's list and a
toast hold 40 to 46, so a sentence that fits the push stands whole in all
three. The wider two decided that line while it still ended in an identifier
that could be shortened; a sentence cannot be shortened, so the narrowest one
decides it now.
**The identifier opens the line below, and it is the most precise one there
is** (`newsDetail`): a job's name for a report, a trigger's headline, which
is its name where the user gave it one, the coder, the shell, the archive,
the action, the command for the kinds that write no text, and for an answer
somebody asked for, where nothing narrower exists, the assistant it came
from. That last one is the same rule and no exception, and it lands the name
in exactly the case where several assistants could be confused; a report from
before the note carried a name falls back the same way. Behind the identifier
stands a colon and then the text that was written, an assistant's answer, a
check's report, a reaction's answer, and the kinds that write none stop after
the identifier. It stands there **whole**: a phone gives the title one line
and the body three or four, so this is the one place a name is read to its
end, and what a push clamps off this line is the tail of an excerpt, by
construction the cheapest part of it. Nothing cuts it, every identifier is
already a label where it is written (`coder.ShortTitle` for a session title
and for the assistant's own name through `assistantNewsName`, `MaxTriggerNameRunes`
where a trigger's name is typed). `answerExcerptRunes` stays at 140: it is
written for the bell and the toast, which show the whole line, while the push
clamps at the reader's own screen, so raising it would only add runes past a
clamp and lowering it would take text off the two surfaces that have the
room. **The project no longer surfaces for the textless kinds**: their line
is the identifier, so `Notification.Project` falls back only for an entry
nobody could resolve, and in the CLI's list, which keeps a column of its own
for it. A report's excerpt never repeats the verdict its title already says,
`parseVerdict` takes that word off the check's answer before the report is
written down. No title classifies a coder's signal. The wording of
every case lives together next to `notifyResolver` in `internal/cli`
(`coderNews`, `shellNews`, `backupNews`, `gitPromptNews`, `composeNews`,
`assistantNews`), never in notify,
which classifies nothing; a job report takes its name from the message's own
`Note`, never from a lookup. An entry without a title (a
target the resolver could not resolve, an entry an older build stored) falls
back to `Something new in "..."` in the list, the toast, the push and
`dev-cockpit assistant notification-list`.
Signals are coder-native, no pane-content parsing: claude sessions get
Stop/Notification hooks injected via `--settings`, copilot sessions ring BEL
through the CLI's global `beep` setting (enabled at startup when copilot is
active), which a read-only control-mode bell watcher per running session
picks up (`internal/coder/bellwatch.go`; it never resizes panes). opencode
sessions get a plugin (`internal/coder/opencode/notify.go`): the runtime
writes it into opencode's config plugin directory at every session start and
carries the notify inbox in the tmux session environment, the plugin hears
the server's event bus and drops claude-shaped event files into that inbox,
a finished turn (`session.idle`, only after a busy report, so a TUI opening
an idle session stays silent) and an asked permission. A subagent's child
session is skipped, a handed-over conversation reports under the cockpit id
its metadata carries, and without the environment variable the plugin writes
nothing at all, which is what keeps the assistant's own opencode runs and
sessions started outside the cockpit silent. The file is instance-free, its
path carries the cockpit's own name and is rewritten unconditionally, and
nothing removes it at stop. Shells get
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
dead socket does not reliably fire an error. The conversation's own stream
(`/assistants/:id/stream`) carries the same ping on the same beat and is judged
by the same 45s, and it needs it more than any other surface: an answer is
silent while the model thinks, so without a life sign the page would have to
read that silence as a dead socket and rebuild the stream over and over, each
time paying for the whole rendered prefix the hub sends on subscribe. Silence
costs the page nothing else either: the running message is pulled after a break
alone, when a connection was established again or the page came back in front,
never on a timer, and such a pull is only ever allowed to finish a message,
because the store holds an answer once the turn settled and a fragment that
still says streaming would wipe the streamed words off the screen. `Server.publishTerminals(project)`
emits a `terminals` event on every live coder/shell change (create, stop,
resume, delete, rename, reorder, project delete, out-of-band end); an empty
project means "refresh everything". A coder renames its own sessions, so that
one change reaches no handler: the turn watch (`RunTurnWatch`, which reads the
snapshot every second anyway) compares the display names between two ticks and
reports a moved one through the exported `Server.PublishTerminals`, wired in
`runServe`. It is the same event, so no surface knows that renames exist, and
a session seen for the first time is only taken in, or every start would
announce a rename. Surfaces react by pulling their own
fragment (per client, so path, CSRF and element state like unfold or filter
stay correct) and coalesce bursts behind one in-flight fetch; the tab strip
shows a `.dc-loading-bar` while it pulls (zero-height sticky first child: no
layout shift, the line stays pinned to the visible top), the project list shows
nothing. The tab strip skips the pull while hidden
(coarse pointer, the phone navigates through the sheet) or during a close/drag and
flushes after; its refresh keeps the + menu and switcher current.
`dc-project-list` swaps only the named project's
`[data-sessions-body]` chip lists and re-folds them; the unfold flags live on
that container, which stays in the DOM across swaps, one per row. Its rows are
two: the terminals with the two new chips, and below them the containers, each
its own `[data-chip-fold]` with its own "+N", because a project with a dozen
containers must not push its coders behind a fold and the other way round. The
shell attach header (`dc-inline-rename`) re-pulls
`GET /shells/:id/name` into heading and page title; the coder attach header
does the same read-only, `dc-live-name` on `GET /coders/:id/name`, because a
coder is never renamed from here. The split page pulls no second name for
either: its pane labels, its desktop title and the coarse header of the
focused member (`[data-pane-title]`) are mirrored out of the strip fragment
the page already refreshes. A state dir belongs to one
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
projects list and the terminal lists mark coders and shells with unread news;
blue is the notification color everywhere, red stays reserved for errors. The
mark is one dot for the whole app, `news_dot.gohtml`: Tabler's animated
`status-dot` in blue, nothing of ours repaints it, and one rule in style.css
(`.dc-news-dot`) puts it on the top right corner of the icon it belongs to. A
carrier that is no session icon wraps its glyph in a `.dc-news-anchor` to
offer that same corner. What shows a dot is decided by the dot's own wiring,
not by its carrier: a dot that names one target is shown by that session
icon's `news` class, and a dot that brings its wiring along (`data-notify-any`
for a whole area, `data-notify-project-dot` for a scope) governs itself
through `d-none` wherever it hangs, session icon included
(`.dc-term-icon > .dc-news-dot:not([data-notify-any])` is what the icon
hides). That is why the assistants' entries keep the session icon look
(`.dc-term-icon.assistant`, the sparkles in its round badge) and still wear a
mark that stands for every assistant. Nothing but the bell counts: the tab bar's Terminals
button used to carry a number and wears the dot now, and the rail's Terminals
button, which carried nothing, wears it too. That button also means what it
says: a terminal is a coder or a shell, so a compose action, a backup job, a
standing git question and the assistant (which has its own button and its own
mark) leave it dark. The narrowing lives in `internal/web`
(`notifyterminals.go`), never in `internal/notify`, which carries targets and
classifies none of them: the test is positive and asked of the cached coder
snapshots and the shell list, so an unknown kind does not count until a coder
or a shell answers to it, and a session deleted while its news stood drops out
with it. The stream carries both lists, `targets` whole for the bell and
`terminals` narrowed for the dot. The marks render
server-side and stay fresh because the projects page renders per navigation
and the sheet refetches on every open. On top of that, dc-notifications
updates opted-in DOM live over its SSE channel: `[data-notify-target]` icons
(the `news` class shows their dot), `[data-notify-any]` (the Terminals
buttons, shown while a coder or a shell is unread) and
`[data-notify-project-dot]` (the projects page; a dot naming row ids in
`data-notify-projects`, the badge of a folded worktree group, collects those
rows' news instead of its own container's, and the open group hides it by
CSS because the rows then show theirs; the badge's fork icon is green the
same way while a folded worktree is at work, `data-worktrees-active`, which
is server rendered and comes live because a worktree's terminals event
refreshes its main's row along with it). A toast also plays a jingle
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
