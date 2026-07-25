---
name: release
description: Create a dev-cockpit GitHub release. Verify the tag, draft curated user centric notes, publish after the user's go, check the assets. Trigger = the user announces a new tag.
---

# Release for dev-cockpit (repo marein/dev-cockpit)

A recurring ritual with the maintainer. They announce a new tag, I verify, draft,
show the draft for review, they adjust or give their go, then I create the release.

The user's writing style for the notes: NO hyphens, only commas. Precise simple
English. Keep this prose hyphen free too.

## 1. Verify first, always

```
git fetch --tags && git tag --sort=-creatordate | head -6
git fetch origin master && git rev-parse origin/master
```

For the new tag:
```
git cat-file -t <tag>            # lightweight = "commit", not "tag"
git rev-parse <tag>              # must == origin/master HEAD
git log --format='===== %h %s =====%n%b' <prev-tag>..<tag>
```

Check the tag points at master HEAD. If it does not, tell the user, do not guess.
Get the full SHAs for the links: `git rev-parse <short-sha>`.
For commits without a body, read the diff (`git show <sha> --stat`, then the
relevant files) so the notes stay user centric and correct, do not infer from the
subject alone.

## 2. Calibrate on the existing notes

Before drafting, read the last few published releases to match tone, structure and
reuse established wording:
```
gh release list --limit 5
gh release view <prev-tag> --json body --jq '.body'
```
The published history is the source of truth for how these notes read. New notes
should feel like a continuation, same voice, same shape, same phrasing for things
that recur (see the wording precedents below).

## 3. Notes format

Title = the bare version number, no prefix (e.g. `1.33.0`).

One top bullet per commit, with optional sub bullets as full sentences ending in a
period:

```
* <Commit subject> by [@<author>](https://github.com/<author>) in [<short-sha>](https://github.com/marein/dev-cockpit/commit/<full-sha>).
  * A user centric sentence about what changes for the user, ends with a period.
  * Another sentence.
```

The top bullet text = the commit subject verbatim. `<author>` is the commit's own
author, not a fixed handle, resolve it per commit. It is usually marein because they
author master, but do not hardcode it, a commit can come from someone else. Get the
GitHub login per commit:
```
gh api repos/marein/dev-cockpit/commits/<full-sha> --jq '.author.login'
```
Link text = the 7 character short SHA, URL = the full SHA. NO `## What's Changed`, NO
`**Full Changelog**`.

## 4. Curation rules (the quality lever)

- **User centric, short and concise.** Strip technical detail from the commit
  message (tmux options, z index, internals, file or function names, snapshot
  mechanics).
- **Drop internal, test only, no impact commits** and flag them to the user.
  Example: a commit that only touches `tests/e2e/README.md` does not go in the
  notes. Always decide by the diff, not the subject.
- **Trivial fixes without a sub bullet** when the subject already says it all.
- **Order:** a sensible default is the most important or most visible feature
  first. When the user gives an importance order ("X at the top, then Y"), follow
  it exactly.
- **Avoid duplication:** when two commits produce the same visible effect, state it
  once (e.g. "tab strip refreshes on mobile" already lives in the live update
  entry).
- **Only verified claims**, no invented reasons. Every statement must hold against
  the commit body or the diff.

## 5. Wording precedents (keep consistent)

- Running coders after an update: "A new or resumed coder picks this up, a coder
  already running when you update needs a resume first." (or, when it is claude
  specific, "Claude coders already running when you update need a resume to pick
  this up.")
- Status is shown through the **icon color** (green active, blue rings on news, grey
  idle), NOT through a separate dot. Never write "dot".
- Use "coder", not "session", in user text.

## 6. Draft ritual

Show the notes in chat (title + a markdown block), with remarks about any dropped
commits or uncertain phrasing. THEN wait for the user's go. Fold in adjustments and
show again until they approve. Do not remove parts of the diff on your own as scope
creep, clarify intent first.

## 7. Publish, only after the go

Write the notes to a scratchpad file, then:
```
gh release create <tag> --title "<tag>" --notes-file <scratchpad>/release-<tag>.md
```
NEVER `--generate-notes` (it fills only from merged PRs, here work lands directly on
master, so it comes out empty or useless).

To correct notes afterwards: `gh release edit <tag> --notes-file <f>`. That triggers
only `release: edited`, NO rebuild, the assets stay.

## 8. Workflow and assets

`.github/workflows/release.yml` triggers on `release: published`, NOT on the tag
push. Only the publish builds the 4 archives (linux/darwin x amd64/arm64) plus
`*_checksums.txt` and uploads them via `gh release upload --clobber`. The GitHub
dispatch can take a few seconds, the run does not show in `gh run list` right away.

Confirm the run started:
```
gh run list --limit 3 --json databaseId,status,event,name
```

The updater offers a release only once its assets finished uploading (the
assetsReady gate). "Are the assets there?" = poll for 5 assets (4 archives +
checksums):
```
gh release view <tag> --json assets --jq '.assets[] | "\(.name)\t\(.size)"'
```

## Guards

- `gh release create` is outward facing, only after the user's explicit go.
- NEVER `git add` / `commit` / `push` or tag changes without an explicit instruction
  in the prompt. Deleting a tag (when the user asks): first check whether a release
  is already attached, then `git push origin --delete <tag>` + `git tag -d <tag>`,
  then verify it is gone.
