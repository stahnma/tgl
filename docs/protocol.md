# Tangled protocol notes for `tgl`

Phase 1 research writeup. Everything here is sourced from primary material, not
inferred from GitHub's model.

**Sources**

| Ref | What |
|---|---|
| `core@83cd295` | Tangled monorepo, `https://tangled.org/tangled.org/core`, commit `83cd2950465e8e612804d2874b97a035af8dbdfd` (2026-08-18). Cited as `path:line`. |
| `DOCS` | `https://docs.tangled.org/single-page` — same content as `core@83cd295:docs/DOCS.md`, cited by line. |
| `LIVE` | Direct probes of `https://api.tangled.org`, `https://knot1.tangled.sh`, and `https://plc.directory` performed 2026-08-18. |

Reproducing the clone: `git clone https://tangled.org/tangled.org/core` (it
302s to `https://knot1.tangled.sh/did:plc:j5hmlfdrwkvtxm7cjmu7j2is/`).

---

## 0. The record vocabulary, as written

These are the contract. Field names below are verbatim from the lexicon JSON.

### `sh.tangled.repo` — `core@83cd295:lexicons/repo/repo.json`
Record key: `any`. Required: `knot`, `createdAt`. Notable optional fields:

- `name` — *"Cosmetic name of the repo."* Optional. When absent the rkey is the name.
- `knot` — hostname string, e.g. `knot1.tangled.sh`.
- `repoDid` — *"DID of the repo itself, if assigned"*. This is the v1.13+ repo DID.
- `labels` — array of at-uris pointing at `sh.tangled.label.definition` records.
- `spindle`, `description`, `website`, `topics`, `source`.

### `sh.tangled.repo.issue` — `lexicons/issue/issue.json`
Record key: `tid`. Required: `repo`, `title`, `createdAt`.

- **`repo` is `format: did`** — the *repo DID*, not an at-uri, not `owner/name`.
- `body`, `mentions` (`[did]`), `references` (`[at-uri]`), `blobs` (`image/*`, ≤1 MB each).
- **There is no issue number field.** See §4.

### `sh.tangled.repo.issue.state` — `lexicons/issue/state.json`
Record key: `tid`. Required: `issue` (at-uri), `state`, `createdAt`.
`state` is one of the token NSIDs `sh.tangled.repo.issue.state.open` /
`.closed` (`lexicons/issue/open.json`, `lexicons/issue/closed.json`).
Default `open`. State is an **append-only log of state records**; the current
state is the latest one. An issue with no state record is open — confirmed
`LIVE`: `sh.tangled.repo.issue.listStates` on an open issue returns
`{"items":[],"cursor":null}`.

### `sh.tangled.repo.pull` — `lexicons/pulls/pull.json`
Record key: `tid`. Required: `target`, `title`, `createdAt`, `rounds`.

- `target`: `{repo: did (required), branch: string (required)}` — repo DID again.
- `source`: `{branch: string (required), repo: did (optional)}`.
- `rounds`: array of `{patchBlob: blob(application/gzip), createdAt}`. The
  lexicon's own description: *"revisions of this pull request, newer rounds are
  appended to this array... the blob format is gzipped text-based
  git-format-patches."* It also warns *"appviews may reject records, do not
  treat this field as append-only."*
- `dependentOn`: at-uri — this is the **stacking** link.
- `blobs`, `mentions`, `references`, `body`.

### `sh.tangled.repo.pull.status` — `lexicons/pulls/state.json`
Note the NSID is `...pull.status`, not `...pull.state`, even though the file is
`state.json`. Required: `pull` (at-uri), `status`, `createdAt`. Tokens:
`sh.tangled.repo.pull.status.open` / `.closed` / `.merged`.

### Comments — **this is the one that will bite you**

There are two comment collections, and the obvious one is the dead one.

- `sh.tangled.repo.issue.comment` (`lexicons/issue/comment.json`) and
  `sh.tangled.repo.pull.comment` (`lexicons/pulls/comment.json`) — flat
  `{issue|pull: at-uri, body: string, createdAt}`. **Legacy.**
- `sh.tangled.feed.comment` (`lexicons/feed/comment.json`) — the current one:
  ```
  subject:      com.atproto.repo.strongRef   (required)
  body:         union of [sh.tangled.markup.markdown]  (required)
  createdAt:    datetime (required)
  replyTo:      com.atproto.repo.strongRef
  pullRoundIdx: integer, "required when subject is sh.tangled.repo.pull"
  ```

The appview writes `sh.tangled.feed.comment` and only that:
`appview/state/comment.go:239` sets `Collection: tangled.FeedCommentNSID`. There
is an explicit migration off the old collections,
`appview/migration/migrate_use_feed_comment.go:27-43`, and
`appview/models/comment.go:74` defines "is legacy" as
`c.Collection != tangled.FeedCommentNSID`.

Confirmed `LIVE` — comments on issue `at://did:plc:6ttw7wc77jtzshasonkzqcs2/sh.tangled.repo.issue/3ms4qpm3qjd22`
come back as `sh.tangled.feed.comment` with a nested body:
```json
{"$type":"sh.tangled.feed.comment",
 "body":{"$type":"sh.tangled.markup.markdown","original":"...","text":"..."},
 "subject":{"uri":"at://.../sh.tangled.repo.issue/3ms4qpm3qjd22","cid":"bafyrei..."}}
```

**Consequence for `tgl`:** read both collections (old issues still carry legacy
comments), but write only `sh.tangled.feed.comment`. A `strongRef` needs the
subject's **CID**, so commenting requires fetching the issue/pull record first —
the appview does exactly this backfill at `appview/state/comment.go:150-165`.

### Labels — `lexicons/label/definition.json`, `lexicons/label/op.json`
Labels are not strings on the issue. A `sh.tangled.label.definition` record
(owned by the repo owner) declares `name`, `valueType`, `scope`
(e.g. `["sh.tangled.repo.issue"]`), `color`, `multiple`. Applying a label is a
separate `sh.tangled.label.op` record: `{subject: at-uri, add: [{key, value}],
delete: [...], performedAt}` where `key` is the at-uri of the definition.
Current label set for an issue = fold the op log.

The repo record's `labels` array is the set of definitions the repo *subscribes
to* — confirmed `LIVE`, `tangled.org/core` lists ten definition at-uris.

---

## 1. Read path — what Bobbin covers

Bobbin is exactly what the docs claim: *"read-only, there is no auth, since that
should all be handled direct-to-PDS and knot respectively"* and *"Bobbin has no
permanent storage... only a glorified edge index"* (`DOCS:2688-2700`).

I extracted its complete route table from
`core@83cd295:bobbin/crates/xrpc/src/lib.rs:179-400` (88 routes). The ones that
matter to us:

| Need | Bobbin endpoint | Status |
|---|---|---|
| `issue list` | `sh.tangled.repo.listIssues` | ✅ covers it fully |
| `issue view` | `sh.tangled.repo.getIssue` (+ `getIssues` for ≤50 batch) | ✅ |
| `pr list` | `sh.tangled.repo.listPulls` | ✅ |
| `pr view` | `sh.tangled.repo.getPull` / `getPulls` | ✅ |
| comments | `sh.tangled.feed.listComments` | ✅ (see caveat) |
| labels | `sh.tangled.label.listOps`, `sh.tangled.label.listDefinitions` | ✅ |
| state history | `sh.tangled.repo.issue.listStates`, `sh.tangled.repo.pull.listStatuses` | ✅ (rarely needed) |
| repo resolution | `sh.tangled.repo.getRepo`, `getRepoByRepoDid`, `listRepos` | ✅ |
| counts | `countIssues`, `countPulls` | ✅ |

`listIssues` (`lexicons/repo/listIssues.json`) already gives us everything the
list view needs without N+1 calls — per item: `uri`, `cid`, embedded `value`,
**`state`** (*"Latest derived state"* — Bobbin folds the state log for us),
`stateUpdatedAt`, and **`commentCount`**. Query params: `subject` (repo DID,
required), `author` (DID), `state` (`open|closed`), `cursor`, `limit` (≤1000,
default 50), `order` (`asc|desc` by createdAt). `listPulls` is identical but
with `status` ∈ `open|closed|merged`.

That maps 1:1 onto the proposed `issue list --state --author --limit`. Verified
`LIVE`:
```
GET /xrpc/sh.tangled.repo.listIssues?subject=did:plc:j5hmlfdrwkvtxm7cjmu7j2is&limit=2&state=open
GET /xrpc/sh.tangled.repo.countIssues?subject=did:plc:j5hmlfdrwkvtxm7cjmu7j2is
  → {"count":710,"distinctAuthors":310}
```

**So: yes, Bobbin covers the entire read path for all four read commands.** No
PDS `listRecords` fallback is needed for normal operation.

### Caveats, all real

1. **Comments.** Bobbin exposes `sh.tangled.feed.listComments` but has **no**
   `listIssueComments` / `listPullComments` route for the legacy collections.
   Legacy comments were migrated (`appview/migration/migrate_use_feed_comment.go`),
   so `feed.listComments` on the issue at-uri should be complete — but for any
   unmigrated account, the fallback is
   `com.atproto.repo.listRecords` against the *commenter's* PDS, which we cannot
   enumerate. **Accept the gap; don't paper over it.**

2. **Not-found is `502`, not `404`.** Bobbin proxies single-record lookups to
   Slingshot. Verified `LIVE`:
   ```
   GET /xrpc/sh.tangled.repo.getIssue?issue=at://did:plc:.../sh.tangled.repo.issue/doesnotexist1
   → HTTP 502 {"error":"UpstreamFailed","message":"upstream unavailable: upstream returned status 400 Bad Request"}
   ```
   Documented at `DOCS:2833-2838`. `tgl` must map `UpstreamFailed` to exit 8
   heuristically, or better: prefer `listIssues` + local match when we can, and
   treat `getIssue` 502 as "not found" only after a positive liveness check
   (`/xrpc/sh.tangled.bobbin.getCoverage`, `DOCS:2884`).

3. **Counts can be a lower bound during warm-up** (`DOCS:2880`). `getCoverage`
   returns `{"ready":true,...}` when caught up — verified `LIVE`.

4. **Git data (patches, diffs, blobs) proxies to the knot** (`DOCS:2874`) —
   uncached, and it is the knot's availability we depend on for `pr view --patch`.

**Fallback rule for `tgl`:** Bobbin first for all reads. On Bobbin 5xx, fall back
to `com.atproto.repo.getRecord` / `listRecords` against the *record owner's* PDS
(resolved from their DID doc). Knot XRPC only for git data. This ordering is
worth building as a single `Reader` interface with two implementations from day
one.

---

## 2. Write path

**Confirmed: issues and pulls are records on the author's PDS.** Not the repo
owner's, not the repo DID's.

- Issue create: `appview/issues/issues.go:831-834` —
  `comatproto.RepoPutRecord{Collection: tangled.RepoIssueNSID, Repo: user.Did, Rkey: issue.Rkey}`.
- Pull create: `appview/pulls/create.go:320-324` — same shape,
  `Collection: tangled.RepoPullNSID, Repo: userDid.String()`.
- Comment: `appview/state/comment.go:239, 265-270`.
- Issue state (close/reopen): `appview/issues/issues.go:548-555` —
  `Collection: tangled.RepoIssueStateNSID, Repo: actorDid, Rkey: tid.TID()`.
- Pull status: `appview/pulls/state.go:36`.

Two details that are easy to get wrong:

**It is `putRecord`, not `createRecord`.** The appview mints the rkey itself
(`tid.TID()`) and calls `com.atproto.repo.putRecord` with an explicit `Rkey`
throughout. `createRecord` would work too, but `putRecord` is what the appview
does and it lets us know the at-uri before the call returns. Edits use
`putRecord` with `SwapRecord: ex.Cid` for optimistic concurrency
(`appview/issues/issues.go:307-315`).

**Rkeys are TIDs.** `key: "tid"` in every lexicon; the appview uses
`tangled.org/core/tid`. `tgl` needs a correct TID generator (base32-sortable,
microsecond timestamp + clock id, monotonic).

### Does anything else need to happen for pickup?

**No. The firehose is sufficient.** There is no "notify the appview" or
"register with the knot" call anywhere in the issue/comment/state write paths —
the appview handlers write to the PDS and then write to their *own* local DB as
an optimisation, and the ingester (`appview/ingester.go:118`) picks the record up
off the firehose independently. Bobbin likewise consumes an event stream
(Hydrant, `DOCS:2705-2712`).

The knot is not involved in issues or comments at all. Issues do not live on the
knot.

### Closing, commenting, editing

| Op | Mechanism |
|---|---|
| Close / reopen issue | New `sh.tangled.repo.issue.state` record on the actor's PDS. Never a mutation or delete. |
| Close / reopen / mark merged pull | New `sh.tangled.repo.pull.status` record. |
| Comment | New `sh.tangled.feed.comment` record with a `strongRef` to the subject. |
| Edit issue/pull | `putRecord` same rkey, `swapRecord` = current CID. Must round-trip `blobs` — `appview/issues/issues.go:298-305` re-reads the existing record and merges blob pins, otherwise attachments are dropped. |
| Delete issue | `com.atproto.repo.deleteRecord` (`appview/issues/issues.go:393-397`). |
| Label an issue | New `sh.tangled.label.op` record. |

Permission for close/reopen is enforced by the *appview*, not the PDS:
`appview/issues/issues.go:451-455` allows issue author, repo owner, or
collaborator. Anyone can technically write a state record to their own PDS; it
just won't be honoured. `tgl` should not pretend to enforce this, but should
surface that a state record was written and may be ignored.

---

## 3. Knot XRPC — merge, create, delete

Only these are knot XRPC. Everything else in our scope is a PDS record write.

From `core@83cd295:knotserver/xrpc/xrpc.go:47-98`, the knot's route groups:

**Service-auth required** (`r.Use(x.ServiceAuth.VerifyServiceAuth)`):
`sh.tangled.repo.merge`, `.create`, `.delete`, `.setDefaultBranch`,
`.deleteBranch`, `.forkSync`, `.hiddenRef`, `.addCollaborator`,
`.removeCollaborator`, `sh.tangled.knot.addMember`, `.removeMember`.

**Open, no auth:** `sh.tangled.repo.mergeCheck` (there is a `TODO: should we
constrain this more?` comment on it), plus all the read endpoints —
`.tree`, `.log`, `.branches`, `.tags`, `.blob`, `.diff`, `.compare`,
`.getDefaultBranch`, `.describeRepo`, `.archive`, `.languages`,
`.listCollaborators`, `.checkPushAllowed`, `sh.tangled.knot.version`,
`.listKeys`, `.listMembers`.

### `sh.tangled.repo.merge` — `lexicons/repo/merge.json`
```
repo   (did, required)   DID of the repository        ← repo DID
did    (did)             owner DID  ─┐ "A knot without the repo-did-input
name   (string)          repo name  ─┘  capability reads this and name in place of repo"
patch  (string, required)  the actual patch text, not a blob ref
branch (string, required)  target branch
authorName, authorEmail, commitMessage, commitBody
```
The appview's call site is `appview/pulls/merge.go:74-105`: it sends `Repo`,
`Did` **and** `Name` together for compatibility, `CommitMessage: pull.Title`,
`CommitBody: pull.Body`, `AuthorName` = the *pull owner's* handle,
`AuthorEmail` = their primary email. Note the merge commit is attributed to the
PR author, not the merger.

For a stacked PR it merges `subStack.Mergeable().CombinedPatch()` — the whole
mergeable prefix of the stack in one call — and then writes a
`pull.status.merged` record for **every** pull in that prefix
(`merge.go:112-120`).

### Auth: confirmed `com.atproto.server.getServiceAuth`

`appview/oauth/oauth.go:433`:
```go
resp, err := comatproto.ServerGetServiceAuth(r.Context(), client, opts.Audience(), opts.exp, opts.lxm)
```
with, from the same file and `xrpc/serviceauth/service_auth.go:22`:

- **`aud`** = `Audience()` = `serviceauth.DidWeb(hostname)` =
  `"did:web:" + strings.ReplaceAll(hostname, ":", "%3A")`. So for
  `knot1.tangled.sh` the audience is `did:web:knot1.tangled.sh`. It is a
  `did:web` derived from the knot hostname — **not** looked up anywhere.
- **`lxm`** = the exact NSID being called, e.g. `sh.tangled.repo.merge`. The knot
  validates this: `service_auth.go:46` derives the expected lxm from
  `path.Base(r.URL.Path)` and passes it to `auth.ServiceAuthValidator.Validate`.
  A token minted for the wrong lxm is rejected.
- **`exp`** = absolute unix seconds. `WithExp(n)` means `now + n`
  (`oauth.go:377-382`), and `ServiceClient` clamps to **at least 60 seconds** in
  the future (`oauth.go:425-429`). Observed call sites use
  `oauth.WithExp(60)` (`appview/repo/settings.go:103, 491`). Merge itself passes
  no `WithExp`, so it gets the 60 s floor.

  **Token lifetime is therefore 60 seconds in practice.** Mint per call; never
  cache.
- Presented as `Authorization: Bearer <token>` (`service_auth.go:42-43`).
- The knot's own audience DID is `x.c.Server.Did()` (`knotserver/router.go:137`).

`getServiceAuth` requires a *session*, so an app-password session works — this is
plain atproto, not OAuth-specific.

### Knot capability negotiation
`sh.tangled.knot.version` (`lexicons/knot/version.json`) returns
`{version, capabilities[]}`; *"Knots that omit this field are treated as
legacy."* Capability tokens are in `consts/consts.go:14-15`: `knot-acl`,
`repo-did-input`. Verified `LIVE`:
```
GET https://knot1.tangled.sh/xrpc/sh.tangled.knot.version
→ {"version":"v1.15.0","capabilities":["knot-acl","repo-did-input"]}
```
`appview/knotcompat/version.go:192` gates the repo-DID wire format on that
capability, caching probes for 5 min / trusting for 1 h
(`knotcompat/compat.go:22-25`). `tgl` should do the same: probe `knot.version`
once per knot per run, send `repo` when `repo-did-input` is present and
`did`+`name` otherwise. Sending all three, as the appview does, is the safe move.

### `sh.tangled.repo.create` — `lexicons/repo/create.json`
```
rkey          (record-key, required)  rkey of the repo record to be written
name          (string, required)      repo name; the knot keys its own table on it
defaultBranch (string)                falls back to the knot's configured main branch
source        (string)                clone from here instead of init — fork/import
repoDid       (did)                   user-provided did:web only; did:plc is minted
→ { repoDid, key }
```
The appview's call site is `appview/state/state.go:551-590`: it sends
`Rkey: strings.ToLower(name)` for **both** `Rkey` and `Name`, then requires a
non-empty `repoDid` in the response — *"Knot failed to mint a repo DID. The knot
may need to be upgraded."* — and only then writes the record, via
`com.atproto.repo.createRecord` (not `putRecord`), because the rkey is the repo
name and a put would overwrite an existing repository.

Order matters, and so does rollback: on a failed record write the appview calls
`sh.tangled.repo.delete` (`state.go:598-635`). The knot honours that only while
no record exists — `knotserver/xrpc/delete_repo.go:64-70` does a
`com.atproto.repo.getRecord` against the owner's PDS and refuses if it finds one.
That is exactly the state a failed create leaves behind, so the rollback is
always permitted and needs no admin `force`.

Knot-side, `knotserver/xrpc/create_repo.go:41-48` requires
`IsRepoCreateAllowed(actorDid)` — not every knot lets every account create — and
`:66-71` validates the name with the same rules as
`appview/models/repo.go:156-188`.

### Ingest: two things must agree or the repo is never indexed

`appview/ingester_repo.go:41-60` drops a `sh.tangled.repo` create outright when
the record has no `repoDid` (*"skipping repo create from non-DID-migrated
knot"*), then `verifyOwnership` (`:432-478`) rejects it unless the knot confirms
the record's author owns that repo DID **and** `record.knot` equals the host in
the repo DID document's service endpoint, compared case-insensitively.

So a CLI-created repository appears on the appview only if the record names the
same knot that minted the DID. `tgl repo create` resolves the repo DID document
after writing and warns when the two disagree, rather than leaving the user to
wonder why the repo never showed up.

### Clone URLs — `appview/pages/templates/repo/fragments/cloneDropdown.html`
```
HTTPS  https://tangled.org/<ownerHandle>/<slug>   permalink: https://tangled.org/<repoDid>
SSH    git@<knot>:<ownerHandle>/<slug>            permalink: git@<knot>:<repoDid>
```
with `knot1.tangled.sh` rewritten to `tangled.org` and the port stripped for SSH.
HTTPS names the appview, not the knot, for every knot: the appview proxies
`/info/refs`, `git-upload-pack` and `git-receive-pack` through to it
(`appview/state/router.go:169-172`). Verified `LIVE` — cloning
`https://tangled.org/onev.cat/tang` redirects to
`https://knot1.tangled.sh/<repoDid>/`.

---

## 4. Identity and numbering — **`#42` is not derivable**

This is the finding that shapes the CLI.

The sequential number is allocated by the **appview's own SQLite counter**, in
firehose-ingest order. `appview/db/issues.go:20-24, 55-63`:

```sql
insert or ignore into repo_issue_seqs (repo_did, next_issue_id) values (?, 1)
...
update repo_issue_seqs
set next_issue_id = next_issue_id + 1
where repo_did = ?
returning next_issue_id - 1
```

and `appview/db/db.go:1753-1767` shows the schema, `UNIQUE(repo_did, issue_id)`.
Pulls have the identical mechanism via `db.NextPullId`
(`appview/pulls/create.go:337`).

That number:

- is **not in any record** — grep the lexicons; there is no number field;
- is **not served by Bobbin** — it appears in none of the 88 routes, and
  `listIssues`' `issueListItem` has no such field
  (`lexicons/repo/listIssues.json`);
- is **not exposed by any appview XRPC** — `appview/xrpc/xrpc.go:38-96` serves
  only notifications, focus mode, account, webhooks, sites and code search;
- is **per-appview state**. A second appview indexing the same firehose would
  allocate its own numbering, and would agree with tangled.org only by
  coincidence of ingest order.

**Can a client recompute it?** No — and this is not a theoretical concern, it is
measurable. Ordering `sh.tangled.repo.issue` records for a repo by TID gives
creation order; the counter follows *ingest* order and never rewinds over
deletions. Those two disagree:

- `PutIssue` allocates on first sight, so a record that arrives late (PDS
  backfill, an appview outage and replay, a repo indexed after the fact) gets a
  higher number than its TID position;
- `DeleteIssues` (`appview/issues/issues.go:406`) removes the row but the
  sequence never rewinds, so numbers have **holes**. TID ordinals do not.

Measured on `tangled.org/core` (`LIVE`): the TID ordinal 405 lands on
*"different workflow run strategies"*, while the real
`https://tangled.org/@tangled.org/core/issues/405` is *"proposal: move db logic
to ingester"*. Issue #1 is a 404 — the holes start immediately. **Ordinal
reconstruction produces confidently wrong answers on a live repository.**

### What works instead: ask the appview that owns the number

The web appview's issue and pull pages carry the record's AT-URI in a
`data-aturi` attribute:

```html
<span title="at://did:plc:xasnlahkri4ewmbuzly2rlc5/sh.tangled.repo.issue/3mdy4uko2vk22"
      data-aturi="at://did:plc:xasnlahkri4ewmbuzly2rlc5/sh.tangled.repo.issue/3mdy4uko2vk22">
```

So `GET https://tangled.org/<owner>/<repo>/issues/<n>` → extract `data-aturi` is
an **exact** mapping from number to record, sourced from the service that
allocates the number. Verified `LIVE` against issues #123/#405/#500/#700 and
pulls #800/#900, cross-checked against each page's `<title>`.

The cost is a dependency on that page's markup rather than on a lexicon. That is
an acceptable trade only because the alternative is wrong: a scrape that breaks
fails loudly, whereas an ordinal that drifts closes the wrong issue.

### Decision for `tgl`

**rkey is the identity; numbers are a lookup.**

| Form | Resolution |
|---|---|
| `3ms4qpm3qjd22` | rkey — matched against the repo's records. Exact. |
| `at://did:plc:…/sh.tangled.repo.issue/3ms4…` | used directly |
| `https://tangled.org/@handle/repo/issues/42` | number extracted, then as below |
| `42` | resolved via the appview page's `data-aturi`. Exact, or a hard error. |

`--json` always reports `rkey` and `uri`, so an agent can round-trip without ever
touching a number. The number, when one was supplied, is echoed back as
`appviewNumber` and documented as tangled.org-specific. tgl never guesses: if the
appview page cannot be read or names more than one candidate record, the command
fails rather than picking one.


## 5. PR shapes

`appview/pulls/create.go` has exactly three entry points:

- **Patch-based** — `handlePatchBasedPull` (`create.go:92`). User supplies a
  format-patch or diff; `validatePatch` checks it; no `source` on the record.
- **Branch-based** — `handleBranchBasedPull` (`create.go:30`). Calls
  `sh.tangled.repo.compare` on the knot with `(repoIdentifier, targetBranch,
  sourceBranch)`, gets back `types.RepoFormatPatchResponse{FormatPatchRaw,
  CombinedPatchRaw, Rev2, BinaryOmitted}`, and records
  `source = {branch: sourceBranch}` with no `repo`.
- **Fork-based** — `handleForkBasedPull` (`create.go:102`). Same, but
  `source = {branch, repo: forkRepoDid}`.

All three funnel into `createPullRequest` (`create.go:210`), which:
1. Falls back to the first patch's subject/body when title/body are empty
   (`create.go:262-278`);
2. **gzips the patch and uploads it as a blob** —
   `xrpc.RepoUploadBlob(ctx, client, gz(patch), "application/gzip")`
   (`create.go:284`) — to the *author's* PDS;
3. `putRecord`s the pull with one round referencing that blob.

Note `create.go:324`: `Record: knotcompat.Pull(&record)`. That wrapper
(`appview/knotcompat/compat.go:38-60`) marshals the record with a **repoDid
shadow** injected into the nested `target` and `source` objects — a JSON-only
compatibility shim so older readers still see the field they expect. CBOR
marshalling is untouched (`compat.go:43-45`), so the signed record is the plain
lexicon shape. `tgl` writing the plain lexicon shape is correct.

### Rounds / resubmit
`resubmitPullHelper` (`appview/pulls/resubmit.go:263-350`):
1. `getRecord` the existing pull;
2. upload a **new** gzipped patch blob (`resubmit.go:321`);
3. `record.Rounds = append(record.Rounds, &tangled.RepoPull_Round{...})`
   (`resubmit.go:328`);
4. `putRecord` with `SwapRecord: ex.Cid` (`resubmit.go:333-337`).

So resubmitting a round = read-modify-write the whole pull record with a CAS on
the CID. Round index is `len(pull.Submissions)` (`resubmit.go:301`) — and this
is the same integer that `sh.tangled.feed.comment.pullRoundIdx` refers to.

### Stacking
`dependentOn` (at-uri) chains pulls. `createStackedPullRequest`
(`create.go:359`) splits a format-patch into individual commits and creates one
pull record per commit. Merge collapses the mergeable prefix into one patch
(§3).

### ⚠️ An undocumented shape is live in production

`LIVE` sampling of 40 open pulls on `tangled.org/core`:

| count | shape |
|---|---|
| 37 | `rounds` populated (1–11 rounds), no `versions` |
| **3** | **`rounds: []`, plus a `versions` array** |

Example — `at://did:plc:xasnlahkri4ewmbuzly2rlc5/sh.tangled.repo.pull/3mszlqgjlzsdn`:
```json
{"$type":"sh.tangled.repo.pull","rounds":[],
 "versions":[{"base":"912ecc15…","head":"0e96abbc…","createdAt":"2026-08-14T07:04:02.394Z"}],
 "source":{"branch":"sl/org-tangled","repo":"did:plc:j5hml…"},
 "target":{"branch":"sv-fe","repo":"did:plc:j5hml…"},
 "title":"lexicons: Tangled 1.0"}
```
`versions` appears **nowhere** in `core@83cd295` — not in `lexicons/`, not in the
generated `api/tangled/repopull.go` (whose fields are exactly
`blobs, body, createdAt, dependentOn, mentions, references, rounds, source,
target, title`). It is a commit-range-based pull shape being trialled ahead of the
lexicon, plausibly part of the "Tangled 1.0" work that PR is itself about.

**Handling for v1:** treat it as unknown-but-present. Parse `rounds` as the
patch source; when `rounds` is empty, report "no patch available for this pull
(record uses an undocumented `versions` shape)" rather than crashing or showing
an empty diff. Do not write `versions` — it is not in the contract.

### v1 scope recommendation

| Shape | v1 |
|---|---|
| `pr list`, `pr view`, `pr comment` for all shapes | ✅ read-only works regardless of shape |
| `pr view --patch` (rounds-based) | ✅ fetch blob from author's PDS via `com.atproto.sync.getBlob`, gunzip |
| `pr view --patch` (versions-based) | ❌ report the limitation |
| `pr create` patch-based | ✅ simplest, no knot round trip |
| `pr create` branch-based | ✅ needs `repo.compare` on the knot (unauthenticated) |
| `pr create` fork-based | ⏸ defer — needs fork registry lookup the appview does via its own DB (`db.GetForkByRepoDid`), which has no Bobbin equivalent |
| `pr checkout` | ✅ derive from `source.branch` / patch |
| `pr merge` | ✅ knot XRPC, service auth |
| Stacked pulls | ⏸ read and display `dependentOn`; do not create stacks in v1 |
| Resubmit a round | ⏸ defer to v1.1 — mechanically clear (§ above) but needs blob upload + CAS |

**Cannot be expressed against documented lexicons, so leaving out:** creating a
fork-based pull (fork discovery is appview-private), and `pr merge` for
non-collaborators (permission lives in the knot's RBAC, `knotserver` + `rbac/`).

---

## 6. Repo addressing

Four identifiers, and different APIs want different ones.

| Identifier | Example | Wanted by |
|---|---|---|
| owner DID | `did:plc:wshs7t2adsemcrrd4snkeqli` | `getRepo` at-uri, `listRepos` |
| repo record at-uri | `at://did:plc:wshs…/sh.tangled.repo/core` | `sh.tangled.repo.getRepo` |
| repo DID | `did:plc:j5hmlfdrwkvtxm7cjmu7j2is` | `listIssues`/`listPulls` `subject`; `issue.repo`; `pull.target.repo`; all knot procedures |
| knot host | `knot1.tangled.sh` | knot XRPC base URL, service-auth `aud` |

### From a git remote

The remote format is `git@tangled.org:<handle>/<name>` (`DOCS:142`, `DOCS:181`,
`DOCS:193`) — note **no `.git` suffix** in the documented examples. HTTPS remotes
after clone look like `https://knot1.tangled.sh/<repoDid>/`, as the redirect on
`git clone https://tangled.org/tangled.org/core` showed (`LIVE`).

Resolution chain:

```
git@tangled.org:handle/name
  │
  ├─ handle ──► owner DID
  │     standard atproto handle resolution: DNS _atproto TXT, then
  │     https://<handle>/.well-known/atproto-did, then a directory.
  │     NOTE: Bobbin cannot do this for you — sh.tangled.actor.getProfile
  │     takes the full profile record at-uri, not a bare handle
  │     (DOCS:2827). Verified LIVE: passing `actor=tangled.org` returns
  │     400 InvalidRequest "regex failure - doesn't match schema".
  │
  ├─ name ──► repo record
  │     ⚠️ name ≠ rkey in general.
  │     appview/models/repo.go:79-84:
  │         func (r Repo) Slug() string {
  │             if r.Name != "" { return r.Name }
  │             return r.Rkey
  │         }
  │     Confirmed LIVE — `tangled.org/core` has rkey "core" and NO `name`
  │     field, while another repo has rkey "3lzg6enurmo22" and name "test".
  │
  │     So: try sh.tangled.repo.getRepo?repo=at://<ownerDid>/sh.tangled.repo/<name>
  │     first (the common case, one request); on failure fall back to
  │     sh.tangled.repo.listRepos?subject=<ownerDid> and match Slug().
  │
  ├─ repoDid ──► from the repo record's `repoDid` field
  │     Optional in the lexicon ("if assigned"). Absent on pre-v1.13 repos —
  │     then fall back to owner-DID + name addressing on the knot
  │     (knotserver/xrpc/xrpc.go:113-136 accepts `did/name` when the repo
  │     param contains a "/").
  │
  └─ knot host ──► two sources, prefer the DID document
        Authoritative: resolve the *repo DID* and read its service endpoint.
        repoident/repoident.go:14-17 defines the service id/type pair
          "tangled_knot" / "TangledKnot", legacy "atproto_pds" /
          "AtprotoPersonalDataServer"
        and KnotURLFromIdentity (repoident.go:88-99) prefers the former,
        falling back to the latter.
        Verified LIVE — https://plc.directory/did:plc:j5hmlfdrwkvtxm7cjmu7j2is:
          "service":[{"id":"#atproto_pds","type":"AtprotoPersonalDataServer",
                      "serviceEndpoint":"https://knot1.tangled.sh"}]
        Convenient but weaker: the repo record's `knot` string field
        ("knot1.tangled.sh"). Use it only when there's no repoDid.
```

`ParseKnotURL` (`repoident/repoident.go:101-141`) is worth mirroring: reject
userinfo, query, fragment and (for bare knot URLs) paths; require https unless
explicitly permitted; lowercase the host and strip default ports.

### Knot-side repo parameter
`knotserver/xrpc/xrpc.go:107-136` — the `repo` param must start with `did:`. If
it contains no `/`, it is a repo DID resolved on disk. If it contains `/`, it is
`<ownerDid>/<name>`. The appview's `Repo.RepoIdentifier()`
(`appview/models/repo.go:86-92`) returns `repoDid` when set, else
`join(ownerDid, rkey)` — note **rkey**, not name, in the fallback path.

### Caching
Resolution costs 2–4 network round trips (handle→DID, getRepo, PLC lookup for
knot, knot.version probe). Cache the whole resolved bundle keyed on the remote
URL, with the DID doc's TTL. Add `tgl repo view --json` as a debugging
command; it is also the first thing to write, since everything depends on it.

---

## Summary of decisions this forces

1. **rkey, not `#42`.** The number is appview-local, sequence-allocated,
   hole-punched by deletes, and unavailable over any API. Reconstructing it from
   TID order is demonstrably wrong on a live repo. `tgl` keys on rkey/at-uri, and
   resolves a number by reading `data-aturi` off the appview page that allocated
   it — exactly, or not at all.
2. **`sh.tangled.feed.comment`, not `sh.tangled.repo.issue.comment`.** Read both;
   write only the former. Needs the subject's CID, so a read precedes every comment.
3. **Bobbin covers 100% of the read path.** PDS `listRecords` is a fallback for
   Bobbin outages, not a routine path.
4. **The firehose is sufficient for writes.** No appview or knot notification.
5. **Only `repo.merge`, `repo.create` and `repo.delete` (of our scope) are knot
   XRPC**, each with a 60-second `getServiceAuth` token,
   `aud = did:web:<knot-host>`, `lxm` = the exact NSID. Creation is also the only
   write that must be rolled back on the knot when the PDS write fails.
6. **Repo DID is the primary key** for issues, pulls and knot calls; owner DID +
   name is only an addressing convenience, and `name` may differ from rkey.
7. **Two things are out of scope because they aren't expressible:** fork-based PR
   creation (needs appview-private fork tables) and patches for `versions`-shaped
   pull records (undocumented field, live in production).
