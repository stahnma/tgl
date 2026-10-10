# tgl — command surface and build plan

Phase 2. Written after `docs/protocol.md`, and adjusted by what that research
found. Status reflects what is actually implemented.

## What Phase 1 changed about the proposed surface

Three findings moved things:

1. **`<ref>` is an rkey, not a number.** The sequential `#42` is allocated by the
   tangled.org appview's private counter and exists in no record and no API.
   Reconstructing it from TID order is measurably wrong on a live repo, so a
   number is resolved by reading the record's AT-URI off the appview page that
   allocated it — exactly, or not at all. `--json` always carries `rkey`/`uri`.
2. **Comments are `sh.tangled.feed.comment`.** The per-collection comment records
   are legacy. Writing a comment needs the subject's CID for the `strongRef`, so
   every comment path resolves its target first.
3. **A `repo` command is load-bearing.** Resolution is four identifiers deep
   (handle → owner DID → repo record → repo DID → knot); it needed a first-class
   command for its own sake and for debugging everything else.

## Command surface

```
tgl auth login [--handle H] [--password-stdin]
tgl auth status
tgl auth logout

tgl repo view [owner/name]                    # how tgl resolves this repo
tgl repo status [--counts]                    # appview reachability, counts

tgl issue list    [--state open|closed|all] [--author H] [--label L] [--limit N] [--no-labels]
tgl issue view    <ref> [--comments=false]
tgl issue create  [--title T] [--body B | --body-file F]
tgl issue comment <ref> --body B | --body-file F
tgl issue close   <ref>
tgl issue reopen  <ref>

tgl pr list     [--state open|closed|merged|all] [--author H] [--label L] [--limit N] [--no-labels]
tgl pr view     <ref> [--patch] [--comments=false]
tgl pr create   [--title T] [--body B] [--base BRANCH] [--head BRANCH] [--patch-file F]
tgl pr comment  <ref> --body B [--round N]
tgl pr checkout <ref> [--branch NAME]
tgl pr merge    <ref> [--message M] [--skip-check]
tgl pr close    <ref>
tgl pr reopen   <ref>

tgl version
```

Global flags on every command: `--json`, `--repo owner/name`, `--remote NAME`,
`--bobbin URL`, `--appview URL`.

### Deviations from the starting point, and why

| Change | Reason |
|---|---|
| Added `repo view` / `repo status` | Resolution is the foundation everything else stands on; it needs to be inspectable. |
| Added `pr close` / `pr reopen` | `sh.tangled.repo.pull.status` has open/closed/merged tokens; closing was free once merge existed. |
| Added `--body-file` / `--patch-file` | Agents pipe content; a `--body` flag alone forces shell quoting of multi-line markdown. |
| Added `pr comment --round` | `pullRoundIdx` is *required* when a feed comment's subject is a pull. Defaults to the latest round. |
| `--label` filters client-side | Labels are a fold of the `sh.tangled.label.op` log; Bobbin has no server-side filter for them. |
| No `issue create --label` | Applying a label is a separate `sh.tangled.label.op` record. Not implemented, so not offered. |
| No fork-based `pr create` | Fork discovery goes through appview-private tables. Not expressible; left out. |

## Tracer-bullet slices

Each slice was shippable and exercised a real round trip. Read-only first, so
resolution and rendering were validated before anything wrote.

| # | Slice | Round trip proved | Status |
|---|---|---|---|
| 0 | Skeleton: dispatcher, exit codes, `--json`, `version` | — | ✅ |
| 1 | `repo view` | handle → DID → `getRepo` → repo DID → PLC → knot | ✅ |
| 2 | `issue list`, `pr list` | `listIssues` / `listPulls`, pagination, concurrent handle resolution | ✅ |
| 3 | `issue view`, `pr view` | `getIssue`/`getPull`, `feed.listComments`, `label.listOps` fold | ✅ |
| 4 | `<ref>` resolution | rkey match, at-uri passthrough, appview number lookup | ✅ |
| 5 | `auth login/status/logout` | `createSession`, `refreshSession`, OS keychain | ✅ |
| 6 | `issue create`, `issue comment`, `issue close/reopen` | `putRecord` to the author's PDS | ✅ |
| 7 | `pr view --patch`, `pr checkout` | `sync.getBlob` + gunzip; knot git fetch | ✅ |
| 8 | `pr create`, `pr comment`, `pr close/reopen` | knot `compare`, `uploadBlob`, pull record write | ✅ |
| 9 | `pr merge` | knot `mergeCheck` + `merge` under `getServiceAuth` | ✅ |

Slices 1–4, 7 and the read half of 8 were verified against the live network
during development; see the Verification section of `README.md`. Slices 5, 6, 9
and the write half of 8 need an account, and are covered by
`go test -tags integration`.

## Not built, deliberately

- **Fork-based `pr create`** — needs the appview's private fork tables.
- **Patch retrieval for `versions`-shaped pulls** — that field appears in no
  lexicon; `pr view --patch` reports the limitation instead of guessing.
- **Stacked-pull merge** — the web UI merges a whole mergeable prefix of a stack
  in one knot call. `pr merge` refuses a stacked pull rather than sending a patch
  that would not apply.
- **Round resubmission** (`pr resubmit`) — mechanically clear (read-modify-write
  the pull record with a CID swap, appending a round), just not yet written.
- **Label writes** — `sh.tangled.label.op` records. Read-only for now.
- **Legacy comment reads** — `sh.tangled.repo.{issue,pull}.comment` records on
  unmigrated accounts are unreachable: Bobbin exposes no listing for them and
  their authors cannot be enumerated.

## Next

1. `pr resubmit <ref>` — the largest remaining gap in PR workflow.
2. `issue label add/remove` — needs `sh.tangled.label.op` writes.
3. A `Reader` fallback to `com.atproto.repo.listRecords` when Bobbin is down.
   The interface seam exists; the second implementation does not.
4. Shell completions.
