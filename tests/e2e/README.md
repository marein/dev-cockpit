# e2e runners

Executable Playwright runners, one file per feature. Each script carries its own
what and why in a header comment (purpose, routes, custom elements, gotchas) and its
checks as descriptive names, so this is the single source of truth (no separate
markdown spec). Shared helpers are in `lib.js`. Mobile and desktop divergence is
covered inside the feature that owns it, mainly `terminal.js` and `coders.js`.

The scripts drive throwaway instances only, create their own scratch project plus
sessions and shells, and delete everything they create. Never attach to a session
you did not create. Destructive selectors (resume, delete, stop) on shared pages
like /projects MUST be scoped to the runner's own project card
(`#project-<scratch>-coders`); the page lists the real projects' stored coders
too, and an unscoped `.first()` deletes someone's session store.

## Run

Build the runner image once (Playwright base plus a global `playwright-core`, see
`Dockerfile`), then a run needs no install and no `node_modules`:

```bash
docker build -t dc-e2e:1.60.0 - < tests/e2e/Dockerfile   # once
docker run --rm --network host [-e BASE_URL=...] [-e ENGINE=...] \
  -v "$PWD/tests/e2e":/work -w /work dc-e2e:1.60.0 node <feature>.js
```

`BASE_URL` defaults to `https://localhost:3010`. `ENGINE` defaults to `chromium`;
set `webkit` or `chromium,webkit` for the cross-browser (Chrome plus Safari) pass.
The only dependency is `playwright-core`, baked into the image (browser drivers need
it; there is no stdlib equivalent that also covers WebKit/Safari).

## Files

| Script | Covers |
| --- | --- |
| `auth.js` | login, logout, requireAuth, rate limit, session cookie attrs, open-redirect matrix |
| `projects.js` | list, card, filter, sort, create, delete, collapse (>5) |
| `editor.js` | tree, tabs (switch keeps dirty buffers, close confirm, reload restore), save, Ctrl+S, save all, new file/folder, rename, recursive delete (closes tabs), quick open palette (Ctrl+O or a double tap on bare Shift), find in files (content search + jump to line), statusbar position, markdown preview, svg preview, image viewer + raw download, find panel (Ctrl+F also on mac, where CodeMirror binds only Cmd+F), upload (dialog + progress), settings, highlighting, mobile drawer, lifecycle, header back button (?return target like the create forms' Cancel, /projects fallback, scrolls to the project card fragment), CodeMirror theme follows the OS scheme on every open tab, tab context menu (right click on desktop, long-press or active-tab tap on touch: close/close others/close right/close all with dirty confirm, copy path, download, reveal in tree, rename, delete), tree context menu (rows: new file/folder + upload targeting the row's dir, copy path, download on files, rename, delete; empty area: root-targeted create/upload + refresh; replaces the per-row hover pencil/trash buttons and the header create/upload buttons, the tree header keeps only refresh), Ctrl+Tab / Ctrl+Shift+Tab step through the open tabs in strip order (wrap at both ends), mouse drag reorders the tabs with the terminal strip's gesture (per device, persists with the open tabs, touch keeps long press and scroll), desktop fullscreen (toolbar button + Ctrl/Cmd+Shift+Enter + double-click on empty tab strip space, `dc-editor-fullscreen` class on the root element, persists per device, button hidden on coarse pointers, a back link to the ?return target shows next to the fullscreen toggle while the page header is covered), project switcher dropdown in the tree header (current project marked active, ordered by the projects-page sort setting via @dc/project-sort, switching navigates to the other project's editor and carries the ?return target), landscape phone: the closed tree drawer stays fully offscreen regardless of the card's left offset |
| `terminal.js` | shared attach: desktop canvas typing + mobile mirror/cursor input, controls, ctrl modifier, swipe scroll (drag + fling), horizontal swipe rotates through the open terminals in tab order, wrapping at both ends (terminal-swipe-nav: damped frame follow, target pill that stays as the pending indicator on slow connections, chaining from the pending target), copy, paste, refresh, setting select (strip settings menu on desktop, legacy key migration), terminal theme select (palette applies to the frame, persists, posts to /terminal-theme, auto follows the OS scheme), desktop fullscreen mode (strip button + Ctrl/Cmd+Shift+F + the app-wide Ctrl/Cmd+Shift+Enter alias + double-click on empty strip space; the attach page takes the viewport, tabs stay on top, the control footer stays at the bottom, rows follow the viewport height instead of the rows setting, persists per device, dialogs and toasts stack above) |
| `terminal-tabs.js` | desktop tab strip on the attach pages (sticky at the viewport top): render + active mark, newest right, click switch, pointer drag reorder with cross-device order (tmux @dc_tab_pos via POST /terminal-tabs/order, second-context check), Ctrl+Tab / Ctrl+Shift+Tab step straight to the next/previous active tab (rotate over actives only, wrap, mashing chains via pendingIndex and aborts intermediates before render; the tab being switched to carries a pending highlight in the switcher's blue tint while its nav is in flight, cleared when it loads), switcher via double tap Ctrl/Meta only (open anchors one step forward within the active tabs and wraps there, then cycling rotates the full list; cycle via Tab/arrows, type-to-filter, empty state, Enter, Esc, click, chord cancels tap), close control on tabs (confirm; closing the current tab switches to the right neighbor without an ended or error toast; switcher rows deliberately carry no close control or badge, current row = accent bar), inline rename reflects in the tab (dc-renamed event), + menu with project preselect plus resume section (all inactive coders grouped by project, projects-page sort order via @dc/project-sort, folded to 3 per project), switcher lists inactive coders in a dimmed section grouped per project (fold to 3 with a Tab-selectable Show-N-more row, Enter expands and selects the first revealed row, filter suspends folding, Enter/click resumes), tabs hide the idle dot (news dot only), help button in the desktop attach footer opens the keyboard shortcuts modal (kbd rows for tab switch, switcher, fullscreen), + menu editor link, live refresh of strip + menu + switcher over the `terminals` event (GET /terminal-tabs fragment; loading bar in the open + menu/switcher, none over the tabs; coalesced past a close/drag), so the + menu and switcher are current when opened without an open-time refetch, attach-to-attach navigation keeps the scroll position (no top jump), strip settings menu (gear right of +, font-size + rows as native selects, settings row hidden on desktop; mobile keeps the row but hides both selects behind the same gear menu), per-tab context menu via right click (Rename for shells with live tab update, Mark read only with news, Open project, Open editor with return target, coder Stop / shell Delete with the close-control confirm), hidden on coarse pointer, app-wide switcher (pages without the strip mount a hidden switcher-only instance from the layout: double Ctrl/Meta opens it on /projects and the editor page, direct Ctrl+Tab stays with the page, deferred fragment refresh flushes when the switcher opens), switcher as quick-access palette (Editors section with one row per project opening that project's editor with the ?return target, New section with New coder / New shell rows carrying the current project so the create form is preselected, everything reachable through the filter) |
| `shells.js` | create, attach, rename (CSRF header), scroll-history, delete (suppresses the ended toast), typing exit ends with an info toast instead of an error |
| `split.js` | split view (grouped terminal tabs): drag a tab onto the middle of another tab (220ms dwell, target highlight) creates the group and navigates to /splits/:id, one terminal island pair per member, typing stays pane-scoped (input isolation, `active` island routing), contextual per-member footer (`data-terminal-footer`, shell footer without prompt/files but with the scroll pad, coder footer with prompt + per-member files modal that posts to the right coder, footer flips with the active pane in a mixed coder+shell split, prompt POST goes to the coder's /input only), member links redirect to the split with that pane focused (?focus, server-rendered active island + visible footer, group tab active), the last active pane is remembered per device (localStorage dc-split-active-<gid>) and restored on plain opens including boosted strip navigations (split-group/split-focused attributes, never window.location), on mobile only the active pane renders (hidden islands do not boot, so no bogus tmux resizes) with the active member's header (inline shell rename + stop/delete) and no group UI on the page, the swipe rotation expands the group tab's hidden data-member-url spans so swiping pages through the members before moving on, the quick nav renders the group as a block (head row with aggregated news + indented member rows; head drags the block through the top-level order, a member row drags inside the block and rewrites gpos via POST /terminal-tabs/group, head swipe reveals rename + ungroup + close-all, member rows show the project and their swipe reveals remove-from-split next to stop/delete, shell members also a rename, member taps switch panes) and creates groups with the same dwell gesture as the strip (drag a row onto the middle of another row/block, 220ms highlight, works with the touch grip handle so mobile can group), a visibly rendered pane counts as read (auto-read, no toast/badge/push for on-screen output), news in an inactive pane keeps its pane head icon blue via the device-local changed class until activation (dc:terminal-activated clears it), the strip folds members into one group tab (single ti-layout icon with aggregated data-notify-targets news, data-tab-members), pane head drag reorders the panes (CSS order preview, persists via POST /terminal-tabs/group -> @dc_tab_gpos), the pane head close control stops/deletes that terminal after a confirm, pane head context menu (shell rename in place, remove from split without killing, stop/delete), tab context menu rename/ungroup/close-all (renaming to empty restores the derived label; grouping is drag or the group route, not a menu item), the page header close control closes the whole split after one confirm, Ctrl+Shift+Arrow switches the active pane, the open split follows group changes live (member add/remove re-renders, reorder re-applies CSS order in place, renames update the pane heads; strip-mirror MutationObserver + deferred refresh so self-triggered navigations win), adding a terminal from the split page renders the new pane in place, the pane context menu remove dissolves a two pane split onto the survivor, close-all flows keep going when single members fail |
| `coders.js` | new form + agent select, create/attach, prompt (desktop + mobile), files (multi upload, reference, download, delete), stop, resumable |
| `agents.js` | list, create, edit (id move), delete, validation |
| `skills.js` | list, create, edit (id move), delete |
| `instructions.js` | textarea, csrf, save, empty allowed |
| `quicknav.js` | toggle, tabs, drill/back, context bar, fold (>5), active pane as one flat list (no per-kind grouping) in the tab strip's `@dc_tab_pos` order with New coder/New shell last, mouse drag reorder (desktop) and grip-handle touch drag reorder (mobile) both persisting through POST /terminal-tabs/order and agreeing with the tab strip, swipe left on touch reveals a delete that mirrors the desktop tab close (confirm, stop/delete POST, neighbor navigation when deleting the current terminal), shell rows also reveal a rename (prompt, POST /shells/:id/rename), the projects tab detail rows swipe too (no reorder there): active coders reveal stop, inactive coders reveal delete (POST /coders/:id/delete), shells reveal rename plus delete |
| `security.js` | CSRF header path, form path, negative (wrong + empty token) |
| `frontend.js` | custom elements upgraded, teardown, re-init guard |
| `overflow.js` | no horizontal overflow at 320/375/768/1366 with long unbreakable names (chromium) |
| `notifications.js` | bell + center (dc-notifications, desktop + mobile), SSE badge/toast/title counter, blue-dot markers on projects list + quick nav, settings volume bar + jingle picker, dedupe window, visibility auto-read, live toast dismissal, shell command completion (real `sleep`, OSC 133 marks, /shells link), mark read/all, push channels (dc-push-settings render state, webhook add + duplicate reject + per-row test button against a local stub server, unread news delivered to the webhook after the 2s re-check while auto-read news stays silent); event injection needs the instance's notify dir mounted (`-v <state-dir>/notification-inbox:/inbox -e NOTIFY_DIR=/inbox`), otherwise those checks soft-skip; the runner needs `--network host` for the webhook stub |
| `live-updates.js` | the shared server event stream (`@dc/events` over `GET /events`): two desktop clients on one instance, a coder/shell started, renamed, reordered or stopped in one client updates the other's tab strip live, and an open quick nav refreshes; the "terminals" event carries no data, clients pull the fresh fragment |
| `update.js` | complete self update: check shape, daily auto modal (once per day per version via localStorage, new version prompts again), badge + link, changelog dialog, real non-destructive apply (`MODE=available`); no-update (`MODE=uptodate`) |
| `coder-claude.js` | coder create/attach/prompt with the claude coder picked in the form (needs the claude CLI on the host) |
| `multi-coder.js` | coder select on new session, coder sidebar + section tabs on agents/skills/instructions, coder badges, quicknav labels; `MODE=single` asserts the adaptive parts stay off (only applies on hosts with a single coder CLI) |

## Instances a full run needs

Throwaway instances per the shared setup, each with its own `--state-dir` and cookie
name. `--provider` is deprecated and ignored, every instance serves all coders whose
CLI is installed on the host. Most scripts run against the instance on `:3010`
(cookie `tc_session`), including `coder-claude.js` and `multi-coder.js`.
Extra instances:

- one on `:3012` started with `DEV_COCKPIT_UPDATE_API_URL` pointing at a stub
  returning a `v999.0.0` release whose asset is this tree built with
  `-ldflags "-X main.version=999.0.0"`, packaged as
  `dev-cockpit_v999.0.0_linux_amd64.tar.gz` (tar.gz with a file named `dev-cockpit`)
  plus `dev-cockpit_v999.0.0_checksums.txt` (`<sha256>  <asset name>`), for
  `update.js MODE=available`. The apply test asserts that the instance re-execs
  as `999.0.0`, so the version must really be baked in. Apply swaps the binary
  in place, the instance must run its own copy, never the repo build.
- one on `:3013` with the stub URL returning `[]`, for `update.js MODE=uptodate`.

Never save `/instructions` outside the runners' own flows, the instance writes the
real per-coder files in `$HOME` (the default coder is the first installed one).

Stop each instance by PID when done, never `pkill`.

## Notes

- Runs report a non-gating `cdn noise` count (the editor's CodeMirror packs
  throttled by their third-party CDN, plus WebKit's "due to access control
  checks" page error for the /events and /update/check background requests a
  navigation aborts mid-flight); only app-origin console and page errors
  gate. Rationale and filters live in `lib.js` (`isCdnNoise`,
  `isWebkitAbortNoise`).
