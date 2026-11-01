const fs = require("fs");
const http = require("http");
const https = require("https");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");

// The fake forge forge.js, forge-status.js, forge-poll.js, forge-editor.js,
// forge-events.js, forge-news.js, forge-comments.js, forge-retry.js,
// forge-change.js, forge-publish.js and forge-thread.js point their host at, so no check ever reaches a real
// forge. One handler answers over http and, with a certificate openssl makes
// for 127.0.0.1 and nobody else trusts, over https:
// like GitLab 17.5 below /gl and GitLab 14.10 below /old, like a GitHub
// Enterprise Server 3.14 at the root, where a GitLab finds no API of its own,
// and with a redirect below /moved whose target only the log ever sees. Each
// answer takes the one token the runner hands it and refuses every other, in
// the words of the forge it plays. The log keeps path, query and
// Authorization of every request.
//
// Below /gl the project team/sub/app has a merge request of its branch
// feature beside a fork's of the same name, and the pipeline 12 for the
// commit state.head names, standing where state.status says, failed unless
// the runner sets another: two jobs, the one that stands with the pipeline
// with a coloured trace in sections, state.traceFill lines longer, a failed
// trigger job and a failed
// external status. The merge request's discussions hold a thread on a line
// of the latest diff with a reply and a note GitLab writes itself about a
// push, one on a line an earlier version of the diff had, a resolved one on
// the old side, a general thread with a reply, a comment and a system note,
// unless the runner sets state.discussions, which answers them instead from
// state, built with gitlabNote, gitlabDiscussion and gitlabDiscussions. On the
// Enterprise Server octo/app has a pull request of feature beside one of a
// fork of the same owner, and for that commit two check runs of GitHub
// Actions, one failed, a failed one of CodeQL and a commit status. The failed
// Actions job's log answers with a redirect to a download below /blob, the
// way GitHub hands out blob storage, which refuses a request that carries an
// Authorization. The pull request's review threads answer over GraphQL at
// /api/graphql in two pages, an outdated one, a resolved one on the old side
// and one with a comment of a pending review among them, each with the diff
// hunk of its first comment, and its
// conversation holds a comment and the reviews, one pending and one without
// a body. The runner sets state.head. A POST to /hook is a webhook
// the cockpit delivers news to, its body kept in hooks. The log keeps a
// query's variables too, and every request's method.
//
// The writes of P14: GitLab's retry of the pipeline 12 sets state.status to
// running, its retry of a job (unit 31, the trigger job child 32) answers
// the new job; on the Enterprise Server the workflow run 501 of state.head
// ends as state.ghRun says, failure unless the runner sets another, its
// failed jobs re-run and the job test (6) re-runs on its own, both setting
// state.ghTest, the Actions check run test's conclusion, to running. The
// commit status ci/jenkins stands at state.ghStatus, pending unless the
// runner sets another. state.readOnly answers every write the way a token
// without the right to write is refused, state.refusal, where set, has
// GitLab refuse every retry with 409 and that message. Each write that went
// through is kept in state.retried.
//
// The writes of P15: GitLab answers the project with its default branch
// state.defaultBranch and opens a merge request of a branch other than
// feature at state.head, a draft by the mark its title opens with, kept in
// state.changes, which the list of the branch's merge requests then holds,
// newest first. The Enterprise Server answers the repository with the same
// default branch and opens a pull request alike, a draft by its own field,
// kept in state.pulls. state.readOnly refuses an open the way a token
// without the right to write is refused.
//
// The writes of P16: the merge request 4 and the pull request 7 show the
// diff of the files the runner sets in state.diffFiles, each { path, old,
// patch }, none unless it sets some. GitLab opens a discussion on a line its
// patch shows, refusing a line both sides share that names no old line the
// way GitLab does, and the Enterprise Server posts a review comment; each
// one posted is kept in state.published and stands among the review threads
// from then on, so the poller's read finds it. state.readOnly refuses them
// the way a token without the right to write is refused.
//
// The writes of P17: GitLab adds a note to a discussion of the merge request
// 4 and resolves one, the Enterprise Server answers a review thread and
// resolves it over GraphQL, a thread neither holds answered as GitLab and
// GitHub answer an unknown one. Each reply is kept in state.replies and
// stands at the end of its thread from then on, each thread resolved in
// state.resolved, so the poller's read finds them. state.readOnly refuses
// them the way a token without the right to write is refused, state.reporter
// refuses a GitLab resolve alone the way GitLab refuses a Reporter, and
// state.ghPending has GitHub put a reply into a pending review.

const EARLIER_HEAD = "345198abe6a66583445c27c7d9b060d6b7e23419";
const BASE_SHA = "4b449edfa7d092dd25836adba8055a2e4223fbe8";

// gitlabNote is a note of a merge request's discussion the way GitLab 17.5
// answers it, on a line of the diff when at names the head of its version and
// its line on the new or the old side.
function gitlabNote(id, author, body, { system = false, resolvable = false, resolved = false, at = null } = {}) {
  return {
    id, type: at ? "DiffNote" : resolvable ? "DiscussionNote" : null, body, author: { id: 1, username: author, name: author, state: "active", web_url: `https://gitlab.example.com/${author}` },
    created_at: `2026-10-28T23:03:${id}.551Z`, updated_at: `2026-10-28T23:03:${id}.551Z`, system, noteable_id: 25, noteable_type: "MergeRequest", project_id: 2, noteable_iid: 4,
    ...(at ? { commit_id: null, position: { base_sha: BASE_SHA, start_sha: BASE_SHA, head_sha: at.head, old_path: at.old || at.path || "src/login.go", new_path: at.path || "src/login.go", position_type: at.file ? "file" : "text", ...(at.file ? {} : { old_line: at.oldLine ?? null, new_line: at.newLine ?? null, line_range: null }) } } : {}),
    resolvable, ...(resolvable ? { resolved, resolved_by: null, resolved_at: null } : {}),
  };
}

const gitlabDiscussion = (id, individual, notes) => ({ id, individual_note: individual, notes });

// gitlabDiscussions are the merge request's discussions the fake answers
// unless the runner sets state.discussions, at the head the fake stands at.
function gitlabDiscussions(state) {
  const note = gitlabNote;
  const discussion = gitlabDiscussion;
  return [
    discussion("d1e2e", false, [note(29, "root", "New name bb?", { resolvable: true, at: { head: state.head, newLine: 4 } }), note(33, "alice", "Reply: yes, bb.", { resolvable: true, at: { head: state.head, newLine: 4 } }),
      note(38, "root", "changed this line in [version 2 of the diff](/team/sub/app/-/merge_requests/4/diffs?diff_id=78)", { system: true, at: { head: state.head, newLine: 4 } })]),
    discussion("d2e2e", false, [note(30, "root", "Added e", { resolvable: true, at: { head: EARLIER_HEAD, newLine: 7 } })]),
    discussion("d3e2e", false, [note(31, "root", "Why drop b?", { resolvable: true, resolved: true, at: { head: state.head, old: "src/old.go", oldLine: 4 } })]),
    discussion("d4e2e", false, [note(34, "root", "General thread", { resolvable: true }), note(35, "alice", "General reply", { resolvable: true })]),
    discussion("d5e2e", true, [note(36, "root", "Individual note")]),
    discussion("d6e2e", true, [note(40, "root", "added 1 commit", { system: true })]),
  ];
}

// gitlabWritten is the discussions as the writes left them: each reply a note
// at the end of its discussion where its first note stands, every note of a
// resolved one resolved.
function gitlabWritten(state, discussions) {
  return discussions.map((d) => {
    const replies = state.replies.filter((r) => r.kind === "gitlab" && r.thread === d.id);
    const resolved = state.resolved.includes(d.id);
    const notes = [...d.notes, ...replies.map((r) => ({ ...d.notes[0], id: r.id, body: r.body, system: false, author: { ...d.notes[0].author, username: "root", name: "Administrator" }, created_at: "2026-10-29T21:30:56.368Z" }))];
    return { ...d, notes: resolved ? notes.map((n) => (n.resolvable ? { ...n, resolved: true } : n)) : notes };
  });
}

// patchLines reads the lines of the new side a unified diff's hunks show,
// each with its line on the old side, 0 for an added one, and its text.
function patchLines(patch) {
  const lines = new Map();
  let oldAt = 0;
  let newAt = 0;
  for (const line of String(patch || "").split("\n")) {
    const head = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
    if (head) {
      oldAt = Number(head[1]);
      newAt = Number(head[2]);
      continue;
    }
    if (!newAt && !oldAt) continue;
    if (line.startsWith("+")) lines.set(newAt++, { old: 0, text: line.slice(1) });
    else if (line.startsWith("-")) oldAt++;
    else if (line.startsWith(" ")) lines.set(newAt++, { old: oldAt++, text: line.slice(1) });
  }
  return lines;
}

const diffLine = (state, path, line) => {
  const file = state.diffFiles.find((f) => f.path === path);
  return file ? patchLines(file.patch).get(line) : undefined;
};

// gitlabDiscussionsOf is the merge request's discussions before the writes of
// P17: the runner's or the fake's own, then one for each note published.
function gitlabDiscussionsOf(state) {
  const published = state.published.filter((n) => n.kind === "gitlab").map((n) => gitlabDiscussion(`p${n.id}`, false, [
    gitlabNote(n.id, "root", n.body, { resolvable: true, at: { head: state.head, path: n.path, newLine: n.line, oldLine: n.old } }),
  ]));
  return [...(state.discussions || gitlabDiscussions)(state), ...published];
}

function handler(token, log, state, hooks) {
  return (req, res) => {
    const url = new URL(req.url, "http://fake");
    const p = url.pathname;
    const entry = { method: req.method, path: p, query: url.search, auth: req.headers.authorization || "" };
    log.push(entry);
    const json = (code, body, headers = {}) => {
      res.writeHead(code, { "Content-Type": "application/json", ...headers });
      res.end(JSON.stringify(body));
    };
    const authed = req.headers.authorization === `Bearer ${token}`;
    if (p === "/hook" && req.method === "POST") {
      let body = "";
      req.on("data", (chunk) => (body += chunk));
      req.on("end", () => {
        hooks.push(body);
        res.end("ok");
      });
      return;
    }
    if (p === "/api/graphql" && req.method === "POST") {
      let body = "";
      req.on("data", (chunk) => (body += chunk));
      req.on("end", () => {
        let variables = {};
        let query = "";
        try {
          ({ variables = {}, query = "" } = JSON.parse(body));
        } catch {}
        entry.variables = variables;
        const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
        if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/graphql" }, headers);
        const web = "https://github.example.com/octo/app/pull/7";
        const mutation = ["addPullRequestReviewThreadReply", "resolveReviewThread"].find((m) => query.includes(m));
        if (mutation) {
          entry.mutation = mutation;
          const known = ["PRRT_kwDOe2e1", "PRRT_kwDOe2e2", "PRRT_kwDOe2e3", ...state.published.filter((n) => n.kind === "github").map((n) => `PRRT_kwDOe2ep${n.id}`)];
          if (state.readOnly) return json(200, { data: { [mutation]: null }, errors: [{ type: "FORBIDDEN", path: [mutation], extensions: { saml_failure: false }, locations: [{ line: 2, column: 3 }], message: "Resource not accessible by personal access token" }] }, headers);
          if (!known.includes(variables.thread)) return json(200, { data: { [mutation]: null }, errors: [{ type: "NOT_FOUND", path: [mutation], locations: [{ line: 2, column: 3 }], message: `Could not resolve to a node with the global id of '${variables.thread}'` }] }, headers);
          if (mutation === "resolveReviewThread") {
            state.resolved.push(variables.thread);
            return json(200, { data: { resolveReviewThread: { thread: { id: variables.thread, isResolved: true } } } }, headers);
          }
          const id = 80 + state.replies.length;
          const pending = state.ghPending;
          if (!pending) state.replies.push({ kind: "github", thread: variables.thread, id, body: variables.body });
          return json(200, { data: { addPullRequestReviewThreadReply: { comment: { id: `PRRC_kwDOe2e${id}`, state: pending ? "PENDING" : "SUBMITTED", body: variables.body, url: `${web}#discussion_r${id}`, createdAt: "2026-10-29T21:40:00Z", author: { login: "octocat" } } } } }, headers);
        }
        if (variables.owner !== "octo" || variables.name !== "app" || variables.number !== 7) {
          return json(200, { data: { repository: null }, errors: [{ type: "NOT_FOUND", path: ["repository"], message: "Could not resolve to a Repository." }] }, headers);
        }
        const comment = (id, author, body, state = "SUBMITTED") => ({ id: `PRRC_kwDOe2e${id}`, state, body, url: `${web}#discussion_r${id}`, createdAt: `2026-10-28T10:0${id}:00Z`, author: { login: author } });
        const replies = (thread) => state.replies.filter((r) => r.kind === "github" && r.thread === thread)
          .map((r) => ({ id: `PRRC_kwDOe2e${r.id}`, state: "SUBMITTED", body: r.body, url: `${web}#discussion_r${r.id}`, createdAt: "2026-10-29T21:40:00Z", author: { login: "octocat" } }));
        const written = (t) => ({ ...t, isResolved: t.isResolved || state.resolved.includes(t.id), comments: { ...t.comments, nodes: [...t.comments.nodes, ...replies(t.id)] } });
        const thread = (id, extra, hunk, comments) => written({ id: `PRRT_kwDOe2e${id}`, isResolved: false, isOutdated: false, path: "src/login.go", line: null, originalLine: null, diffSide: "RIGHT", ...extra, comments: { pageInfo: { hasNextPage: false }, nodes: comments }, opening: { nodes: [{ diffHunk: hunk }] } });
        const first = !variables.after;
        const published = state.published.filter((n) => n.kind === "github").map((n) => written({
          id: `PRRT_kwDOe2ep${n.id}`, isResolved: false, isOutdated: false, path: n.path, line: n.line, originalLine: n.line, diffSide: "RIGHT",
          comments: { pageInfo: { hasNextPage: false }, nodes: [{ id: `PRRC_kwDOe2e${n.id}`, state: "SUBMITTED", body: n.body, url: `${web}#discussion_r${n.id}`, createdAt: "2026-10-29T10:00:00Z", author: { login: "octocat" } }] },
          opening: { nodes: [{ diffHunk: `@@ -${n.line},1 +${n.line},1 @@\n ${n.text}` }] },
        }));
        const nodes = first
          ? [thread(1, { line: 4, originalLine: 4 }, "@@ -1,4 +1,4 @@\n package login\n \n func login() {\n-\tname := b\n+\tname := bb", [comment(1, "octocat", "New name bb?"), comment(2, "hubot", "Not sent yet", "PENDING")]),
            thread(2, { isOutdated: true, originalLine: 7 }, "@@ -5,2 +5,3 @@\n \tc := 3\n \td := 4\n+\te := 5", [comment(3, "octocat", "Added e")])]
          : [thread(3, { isResolved: true, line: 4, originalLine: 4, diffSide: "LEFT" }, "@@ -1,5 +1,4 @@\n package login\n \n func login() {\n-\tb := 2", [comment(4, "octocat", "Why drop b?")])];
        json(200, { data: { repository: { pullRequest: { reviewThreads: { pageInfo: { hasNextPage: first, endCursor: first ? "Y3Vyc29yOnYyOpK0" : "Y3Vyc29yOnYyOpK1" }, nodes: first ? [...nodes, ...published] : nodes } } } } }, headers);
      });
      return;
    }
    if (p.startsWith("/moved/")) {
      res.writeHead(301, { Location: `/elsewhere${p.slice("/moved".length)}?private_token=leak` });
      return res.end();
    }
    const gitlab = p.match(/^\/(gl|old)\/api\/v4\/(version|user)$/);
    if (gitlab) {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      if (gitlab[2] === "version") return json(200, { version: gitlab[1] === "old" ? "14.10.5" : "17.5.1", revision: "8ff7d9d4d4a" });
      return json(200, { id: 1, username: "root", name: "Administrator", state: "active", web_url: "https://gitlab.example.com/root" });
    }
    if (p === "/blob/actions-results/6.txt") {
      if (req.headers.authorization || url.searchParams.get("sig") !== "e2e-sig") {
        res.writeHead(403, { "Content-Type": "application/xml" });
        return res.end("<Error><Code>AuthenticationFailed</Code></Error>");
      }
      res.writeHead(200, { "Content-Type": "text/plain" });
      return res.end("\ufeff2026-10-28T09:00:00.1000000Z ##[group]Run go test ./...\n2026-10-28T09:00:01.2000000Z \x1b[36;1mgo test ./...\x1b[0m\n2026-10-28T09:00:02.3000000Z --- FAIL: TestLogin (0.00s)\n2026-10-28T09:00:03.4000000Z ##[error]Process completed with exit code 1.\n");
    }
    const readBody = (then) => {
      let body = "";
      req.on("data", (chunk) => (body += chunk));
      req.on("end", () => {
        let sent = {};
        try {
          sent = JSON.parse(body);
        } catch {}
        entry.body = sent;
        then(sent);
      });
    };
    const gitlabProject = "/gl/api/v4/projects/team%2Fsub%2Fapp";
    if (p === gitlabProject && req.method === "GET") {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      return json(200, { id: 2, path_with_namespace: "team/sub/app", default_branch: state.defaultBranch, web_url: "https://gitlab.example.com/team/sub/app" });
    }
    if (p === `${gitlabProject}/merge_requests` && req.method === "POST") {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      return readBody((sent) => {
        if (state.readOnly) return json(403, { error: "insufficient_scope", error_description: "The request requires higher privileges than provided by the access token.", scope: "api" });
        const iid = 20 + state.changes.length;
        const change = {
          id: 200 + iid, iid, project_id: 2, title: sent.title, description: sent.description || null, state: "opened", draft: /^draft:/i.test(sent.title || ""),
          source_branch: sent.source_branch, target_branch: sent.target_branch, source_project_id: 2, target_project_id: 2, sha: state.head,
          web_url: `https://gitlab.example.com/team/sub/app/-/merge_requests/${iid}`,
        };
        state.changes.push(change);
        json(201, change);
      });
    }
    if (p === "/api/v3/repos/octo/app" && req.method === "GET") {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      return json(200, { id: 2, name: "app", full_name: "octo/app", private: true, default_branch: state.defaultBranch, html_url: "https://github.example.com/octo/app" }, headers);
    }
    if (p === "/api/v3/repos/octo/app/pulls" && req.method === "POST") {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      return readBody((sent) => {
        if (state.readOnly) return json(403, { message: "Resource not accessible by personal access token", documentation_url: "https://docs.github.com/rest/pulls/pulls#create-a-pull-request", status: "403" }, headers);
        const number = 30 + state.pulls.length;
        const ref = String(sent.head || "").replace(/^octo:/, "");
        const pull = {
          number, title: sent.title, body: sent.body || null, state: "open", draft: !!sent.draft, merged_at: null, html_url: `https://github.example.com/octo/app/pull/${number}`,
          base: { ref: sent.base }, head: { label: sent.head, ref, sha: state.head, repo: { full_name: "octo/app" } },
        };
        state.pulls.push(pull);
        json(201, pull, headers);
      });
    }
    if (p === `${gitlabProject}/merge_requests/4/diffs` && req.method === "GET") {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      return json(200, state.diffFiles.map((f) => ({ diff: f.patch, new_path: f.path, old_path: f.old || f.path, a_mode: "100644", b_mode: "100644", new_file: false, renamed_file: !!f.old, deleted_file: false, generated_file: false })));
    }
    if (p === `${gitlabProject}/merge_requests/4/discussions` && req.method === "POST") {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      return readBody((sent) => {
        if (state.readOnly) return json(403, { error: "insufficient_scope", error_description: "The request requires higher privileges than provided by the access token.", scope: "api" });
        const at = sent.position || {};
        const shown = diffLine(state, at.new_path, at.new_line);
        if (at.head_sha !== state.head || at.base_sha !== BASE_SHA || at.start_sha !== BASE_SHA || !shown || (shown.old && at.old_line !== shown.old)) {
          return json(400, { message: '400 Bad request - Note {:line_code=>["can\'t be blank", "must be a valid line code"]}' });
        }
        const id = 50 + state.published.length;
        state.published.push({ kind: "gitlab", id, path: at.new_path, line: at.new_line, old: at.old_line || null, body: sent.body, text: shown.text });
        const made = gitlabNote(id, "root", sent.body, { resolvable: true, at: { head: state.head, path: at.new_path, newLine: at.new_line, oldLine: at.old_line || null } });
        json(201, gitlabDiscussion(`p${id}`, false, [made]));
      });
    }
    const discussionAt = p.match(/^\/gl\/api\/v4\/projects\/team%2Fsub%2Fapp\/merge_requests\/4\/discussions\/([^/]+)(\/notes)?$/);
    if (discussionAt && ((discussionAt[2] && req.method === "POST") || (!discussionAt[2] && req.method === "PUT"))) {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      return readBody((sent) => {
        if (state.readOnly) return json(403, { error: "insufficient_scope", error_description: "The request requires higher privileges than provided by the access token.", scope: "api" });
        if (state.reporter && !discussionAt[2]) return json(403, { message: "403 Forbidden" });
        const id = decodeURIComponent(discussionAt[1]);
        const d = gitlabDiscussionsOf(state).find((d) => d.id === id);
        if (!d) return json(404, { message: "404 Discussion Not Found" });
        if (!discussionAt[2]) {
          if (url.searchParams.get("resolved") !== "true") return json(400, { message: "400 Bad request - resolved is missing" });
          state.resolved.push(id);
          return json(200, gitlabWritten(state, [d])[0]);
        }
        const note = 70 + state.replies.length;
        state.replies.push({ kind: "gitlab", thread: id, id: note, body: sent.body });
        const made = gitlabWritten(state, [d])[0].notes.find((n) => n.id === note);
        json(201, made);
      });
    }
    const retry = req.method === "POST" && p.match(/^\/gl\/api\/v4\/projects\/team%2Fsub%2Fapp\/(pipelines\/12|jobs\/(\d+))\/retry$/);
    if (retry) {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      if (state.readOnly) return json(403, { error: "insufficient_scope", error_description: "The request requires higher privileges than provided by the access token.", scope: "api" });
      if (state.refusal) return json(409, { message: state.refusal });
      if (retry[2] && !["31", "32"].includes(retry[2])) return json(404, { message: "404 Not found" });
      state.retried.push(p.slice("/gl/api/v4/projects/team%2Fsub%2Fapp".length));
      if (!retry[2]) {
        state.status = "running";
        return json(201, { id: 12, iid: 3, project_id: 2, sha: state.head, ref: "feature", status: "failed", web_url: "https://gitlab.example.com/team/sub/app/-/pipelines/12" });
      }
      if (retry[2] === "31") state.status = "running";
      return json(201, { id: 40, status: "pending", stage: "test", name: retry[2] === "31" ? "unit" : "child", web_url: "https://gitlab.example.com/team/sub/app/-/jobs/40" });
    }
    const rerun = p.match(/^\/api\/v3\/repos\/octo\/app\/actions\/(runs|runs\/501\/rerun-failed-jobs|jobs\/6\/rerun)$/);
    if (rerun) {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      if (rerun[1] === "runs") {
        const runs = url.searchParams.get("head_sha") === state.head && state.head
          ? [{ id: 501, name: "CI", head_branch: "feature", head_sha: state.head, status: "completed", conclusion: state.ghRun, workflow_id: 3, check_suite_id: 601, html_url: "https://github.example.com/octo/app/actions/runs/501", run_attempt: 1 }]
          : [];
        return json(200, { total_count: runs.length, workflow_runs: runs }, headers);
      }
      if (state.readOnly) return json(403, { message: "Resource not accessible by personal access token", documentation_url: "https://docs.github.com/rest/actions/workflow-runs", status: "403" }, headers);
      state.retried.push(p.slice("/api/v3/repos/octo/app".length));
      state.ghTest = "in_progress";
      return json(201, {}, headers);
    }
    const project = p.match(/^\/gl\/api\/v4\/projects\/team%2Fsub%2Fapp\/(merge_requests|merge_requests\/4|merge_requests\/4\/discussions|pipelines|pipelines\/12\/jobs|pipelines\/12\/bridges|repository\/commits\/[0-9a-f]{40}\/statuses|jobs\/(\d+)\/trace)$/);
    if (project) {
      if (!authed) return json(401, { message: "401 Unauthorized" });
      const web = "https://gitlab.example.com/team/sub/app";
      const mergeRequest = (iid, title, source, draft) => ({
        id: 100 + iid, iid, project_id: 2, title, state: "opened", draft, work_in_progress: draft, source_branch: "feature", target_branch: "main",
        source_project_id: source, target_project_id: 2, sha: state.head, web_url: `${web}/-/merge_requests/${iid}`,
      });
      if (project[1] === "merge_requests") {
        const source = url.searchParams.get("source_branch");
        if (source !== "feature") return json(200, state.changes.filter((c) => c.source_branch === source).reverse());
        return json(200, [mergeRequest(9, "From a fork", 7, false), mergeRequest(4, "Fix the login", 2, true)]);
      }
      if (project[1] === "merge_requests/4") {
        return json(200, { ...mergeRequest(4, "Fix the login", 2, true), diff_refs: { base_sha: "4b449edfa7d092dd25836adba8055a2e4223fbe8", start_sha: "4b449edfa7d092dd25836adba8055a2e4223fbe8", head_sha: state.head } });
      }
      if (project[1] === "merge_requests/4/discussions") {
        return json(200, gitlabWritten(state, gitlabDiscussionsOf(state)));
      }
      if (project[1] === "pipelines") {
        if (url.searchParams.get("sha") !== state.head) return json(200, []);
        return json(200, [{ id: 12, iid: 3, project_id: 2, sha: state.head, ref: "feature", status: state.status, source: "push", web_url: `${web}/-/pipelines/12` }]);
      }
      if (project[1] === "pipelines/12/bridges") {
        return json(200, [{ id: 32, status: "failed", stage: "deploy", name: "child", allow_failure: false, web_url: `${web}/-/jobs/32`,
          downstream_pipeline: { id: 13, sha: state.head, ref: "feature", status: "failed", web_url: `${web}/-/pipelines/13` } }]);
      }
      if (project[1].startsWith("repository/")) {
        const status = (id, name, target) => ({ id, sha: state.head, ref: "feature", status: id === 30 ? "success" : "failed", name, target_url: target, allow_failure: false, pipeline_id: 12 });
        return json(200, [status(30, "build", null), status(31, "unit", null), status(32, "child", null), status(33, "ci/external", "https://ci.example.com/33")]);
      }
      if (project[2]) {
        if (project[2] !== "31") return json(404, { message: "404 Not found" });
        res.writeHead(200, { "Content-Type": "text/plain" });
        const fill = Array.from({ length: state.traceFill }, (_, i) => `ok  \tteam/app/pkg${i}\t0.01s\n`).join("");
        return res.end("\x1b[0KRunning with gitlab-runner 17.5.0\x1b[0;m\nsection_start:1730000002:step_script\r\x1b[0K\x1b[0K\x1b[36;1mExecuting \"step_script\" stage of the job script\x1b[0;m\x1b[0;m\n\x1b[32;1m$ go test ./...\x1b[0;m\n" + fill + "downloading 10%\rdownloading 100%\n--- FAIL: TestLogin (0.00s)\nsection_end:1730000003:step_script\r\x1b[0K\x1b[31;1mERROR: Job failed: exit code 1\n\x1b[0;m\n");
      }
      return json(200, [
        { id: 31, status: state.status, stage: "test", name: "unit", allow_failure: false, web_url: `${web}/-/jobs/31` },
        { id: 30, status: "success", stage: "build", name: "build", allow_failure: false, web_url: `${web}/-/jobs/30` },
      ]);
    }
    const pullSeven = p.match(/^\/api\/v3\/repos\/octo\/app\/pulls\/7(\/files|\/comments)?$/);
    if (pullSeven) {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      const web = "https://github.example.com/octo/app/pull/7";
      if (!pullSeven[1] && req.method === "GET") {
        return json(200, { number: 7, title: "Fix the login", state: "open", draft: false, merged_at: null, html_url: web, base: { ref: "main", sha: BASE_SHA }, head: { label: "octo:feature", ref: "feature", sha: state.head, repo: { full_name: "octo/app" } } }, headers);
      }
      if (pullSeven[1] === "/files" && req.method === "GET") {
        return json(200, state.diffFiles.map((f) => ({ sha: "bbcd538c8e72b8c175046e27cc8f907076331401", filename: f.path, ...(f.old ? { previous_filename: f.old, status: "renamed" } : { status: "modified" }), patch: f.patch })), headers);
      }
      if (pullSeven[1] === "/comments" && req.method === "POST") {
        return readBody((sent) => {
          if (state.readOnly) return json(403, { message: "Resource not accessible by personal access token", documentation_url: "https://docs.github.com/rest/pulls/comments", status: "403" }, headers);
          const shown = diffLine(state, sent.path, sent.line);
          if (sent.commit_id !== state.head || sent.side !== "RIGHT" || !shown) {
            return json(422, { message: "Validation Failed", errors: [{ resource: "PullRequestReviewComment", code: "custom", field: "pull_request_review_thread.line", message: "could not be resolved" }], status: "422" }, headers);
          }
          const id = 60 + state.published.length;
          state.published.push({ kind: "github", id, path: sent.path, line: sent.line, body: sent.body, text: shown.text });
          json(201, { id, node_id: `PRRC_kwDOe2e${id}`, pull_request_review_id: 90 + id, path: sent.path, commit_id: state.head, original_commit_id: state.head, user: { login: "octocat", id: 1 }, body: sent.body,
            created_at: "2026-10-29T10:00:00Z", updated_at: "2026-10-29T10:00:00Z", html_url: `${web}#discussion_r${id}`, line: sent.line, side: "RIGHT", original_line: sent.line, author_association: "OWNER" }, headers);
        });
      }
    }
    const repo = p.match(/^\/api\/v3\/repos\/octo\/app\/(pulls|issues\/7\/comments|pulls\/7\/reviews|commits\/([0-9a-f]{40})\/(check-runs|status)|actions\/jobs\/(\d+)\/logs)$/);
    if (repo) {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      const web = "https://github.example.com/octo/app";
      const pull = (number, title, repository) => ({
        number, title, state: "open", draft: false, merged_at: null, html_url: `${web}/pull/${number}`,
        base: { ref: "main" }, head: { label: "octo:feature", ref: "feature", sha: state.head, repo: { full_name: repository } },
      });
      if (repo[1] === "pulls") {
        const head = url.searchParams.get("head");
        if (head !== "octo:feature") return json(200, state.pulls.filter((pull) => pull.head.label === head).reverse(), headers);
        return json(200, [pull(8, "From a fork of the same owner", "octo/app-fork"), pull(7, "Fix the login", "octo/app")], headers);
      }
      if (repo[1] === "issues/7/comments") {
        return json(200, [{ id: 11, node_id: "IC_kwDOe2e11", html_url: `${web}/pull/7#issuecomment-11`, body: "Looks good\r\nbut see the threads", user: { login: "hubot" }, created_at: "2026-10-28T10:20:00Z", updated_at: "2026-10-28T10:20:00Z" }], headers);
      }
      if (repo[1] === "pulls/7/reviews") {
        const review = (id, kind, body, at) => ({ id, node_id: `PRR_kwDOe2e${id}`, user: { login: "octocat" }, body, state: kind, html_url: `${web}/pull/7#pullrequestreview-${id}`, ...(at ? { submitted_at: at } : {}), commit_id: state.head });
        return json(200, [review(80, "CHANGES_REQUESTED", "Please rename", "2026-10-28T10:10:00Z"), review(81, "APPROVED", "", "2026-10-28T10:30:00Z"), review(82, "PENDING", "Draft summary")], headers);
      }
      if (repo[4]) {
        if (repo[4] !== "6") return json(404, { message: "Not Found", documentation_url: "https://docs.github.com/rest", status: "404" }, headers);
        res.writeHead(302, { ...headers, Location: `http://${req.headers.host}/blob/actions-results/6.txt?sv=2025-01-05&sig=e2e-sig` });
        return res.end();
      }
      const known = repo[2] === state.head;
      if (repo[3] === "check-runs") {
        const actions = { id: 15368, slug: "github-actions", name: "GitHub Actions" };
        const runs = [
          { id: 5, name: "build", status: "completed", conclusion: "success", html_url: `${web}/runs/5`, app: actions },
          { id: 6, name: "test", status: state.ghTest === "in_progress" ? "in_progress" : "completed", conclusion: state.ghTest === "in_progress" ? null : state.ghTest, html_url: `${web}/runs/6`, app: actions },
          { id: 7, name: "scan", status: "completed", conclusion: "failure", html_url: `${web}/runs/7`, app: { id: 57789, slug: "github-advanced-security", name: "CodeQL" } },
        ];
        return json(200, { total_count: known ? runs.length : 0, check_runs: known ? runs : [] }, headers);
      }
      return json(200, { state: state.ghStatus, total_count: known ? 1 : 0, statuses: known ? [{ id: 9, context: "ci/jenkins", state: state.ghStatus, target_url: "https://ci.example.com/9" }] : [] }, headers);
    }
    if (p === "/api/v3/user") {
      const headers = { "X-GitHub-Enterprise-Version": "3.14.2" };
      if (!authed) return json(401, { message: "Bad credentials", documentation_url: "https://docs.github.com/rest", status: "401" }, headers);
      return json(200, { login: "octocat", id: 1, name: "monalisa octocat", type: "User", site_admin: false }, headers);
    }
    json(404, { message: "Not Found", documentation_url: "https://docs.github.com/rest", status: "404" });
  };
}

// startFakeForge listens on both ports of every interface, which the docker
// run publishes on the host's loopback, and answers the CA bundle that
// trusts its https side, the log, the webhooks and the state the runner sets. The
// cockpit's poller reads what a runner's project is bound to on its own, so
// a check that counts requests waits for its read, or counts the paths.
async function startFakeForge({ port, tlsPort, token }) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "forge-fake-"));
  const key = path.join(dir, "key.pem");
  const cert = path.join(dir, "cert.pem");
  execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", cert, "-days", "2",
    "-subj", "/CN=forge-fake", "-addext", "subjectAltName=IP:127.0.0.1"], { stdio: "ignore" });
  const log = [];
  const hooks = [];
  const state = { head: "", status: "failed", ghRun: "failure", ghTest: "failure", ghStatus: "pending", traceFill: 0, readOnly: false, reporter: false, refusal: "", retried: [], defaultBranch: "main", changes: [], pulls: [], diffFiles: [], published: [], replies: [], resolved: [], ghPending: false };
  const plain = http.createServer(handler(token, log, state, hooks));
  const secure = https.createServer({ key: fs.readFileSync(key), cert: fs.readFileSync(cert) }, handler(token, log, state, hooks));
  await Promise.all([
    new Promise((resolve) => plain.listen(port, "0.0.0.0", resolve)),
    new Promise((resolve) => secure.listen(tlsPort, "0.0.0.0", resolve)),
  ]);
  return { ca: fs.readFileSync(cert, "utf8"), log, hooks, state };
}

// addHost adds a host from page the way Add host does, which takes no token,
// and gives it token through its token view, whose save tests it, so the
// fake hears the test's reads of the version and the user and nothing else.
// It answers the host's id, or why a step failed.
async function addHost(page, { token, ...host }) {
  return page.evaluate(async ({ host, token }) => {
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    const send = (target, fields) => fetch(target, { method: "POST", body: new URLSearchParams({ ...fields, csrf_token: csrf }) });
    const added = await send("/settings/forges/new", host);
    const id = added.status === 200 ? new URL(added.url).pathname.match(/^\/settings\/forges\/([^/]+)\/token$/)?.[1] : "";
    if (!id) return { failed: `adding the host led to ${added.status} ${added.url}` };
    const saved = await send(`/settings/forges/${id}/token`, { token });
    if (saved.status !== 200 || new URL(saved.url).pathname !== "/settings/forges") return { failed: `the token of the host led to ${saved.status} ${saved.url}` };
    return { id: decodeURIComponent(id) };
  }, { host, token });
}

module.exports = { startFakeForge, addHost, gitlabNote, gitlabDiscussion, gitlabDiscussions, EARLIER_HEAD };
