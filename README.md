# tgl

A `gh`-equivalent command-line interface for [Tangled](https://tangled.org),
covering issues, pull requests and repositories. Single static Go binary, no
runtime dependencies.

Built to be driven by agents as much as by people: every command takes `--json`
and emits a stable schema, every command works non-interactively, and exit codes
are meaningful.

```
$ tgl issue list --repo tangled.org/core --limit 3
3mtcfg7dsfb22  open  move reaction button to bottom right of issue comments  [area:appview]  @wangleline.com  17h ago
3mt43sbfo5kcw  open  Editing a merged pull updates the PDS but not the appview  @nnabeyang.tngl.sh  3d ago
3mt33vcqxaz22  open  Characters left unencoded in URLs  @mrichardson.tngl.sh  3d ago

3 issue(s)
```

## Install

```sh
go build -o tgl ./cmd/tgl
```

Or `make build`. Go 1.25+.

## Authenticate

`tgl` authenticates with an **app password** — a separate, revocable credential,
**not your account password** — against `com.atproto.server.createSession`,
discovering your PDS from your handle. The refresh token goes in the OS keychain,
never a dotfile.

Create the app password wherever your account lives: for a `bsky.social` account
that is <https://bsky.app/settings/app-passwords>; for a `tngl.sh` or self-hosted
account, your own PDS's settings. It looks like `xxxx-xxxx-xxxx-xxxx`.

```sh
tgl auth login --handle you.example
tgl auth status
```

For CI and agents, skip `login` entirely:

```sh
export TANGLED_HANDLE=you.example
export TANGLED_APP_PASSWORD=xxxx-xxxx-xxxx-xxxx
```

Reading needs no credentials at all.

## Usage

Repository context comes from the git remote in the working directory, so inside
a clone you can just run the commands. Use `--repo owner/name` anywhere else.

```sh
tgl issue list --state open --label bug --limit 20
tgl issue view 3ms4qpm3qjd22
tgl issue create --title "Crash on empty input" --body-file report.md
tgl issue comment 3ms4qpm3qjd22 --body "Reproduced on 1.15."
tgl issue close 3ms4qpm3qjd22

tgl pr list --state open
tgl pr view 3mszijhglya22 --patch
tgl pr checkout 3mszijhglya22
tgl pr create --title "Fix the thing" --base master
tgl pr merge 3mszijhglya22

tgl repo list onev.cat
tgl repo clone tangled.org/core
tgl repo create my-tool --description "A small Tangled tool"
tgl browse issue 405
```

`tgl repo view` shows how a repository resolves, which is the first thing to
check when something cannot be found:

```
$ tgl repo view
name          core
owner         @tangled.org
owner did     did:plc:wshs7t2adsemcrrd4snkeqli
rkey          core
at-uri        at://did:plc:wshs7t2adsemcrrd4snkeqli/sh.tangled.repo/core
repo did      did:plc:j5hmlfdrwkvtxm7cjmu7j2is
knot          knot1.tangled.sh
knot endpoint https://knot1.tangled.sh
```

### Repositories

```sh
tgl repo list                      # the owner of the repo you are in
tgl repo list onev.cat --limit 50  # or any handle or DID
tgl repo clone tangled.org/core [directory]
tgl repo clone tangled.org/core --protocol ssh
tgl repo clone tangled.org/core --permalink   # clone by repo DID
tgl repo create my-tool --knot knot1.tangled.sh --default-branch main
```

`repo list` reads `sh.tangled.repo` records, so it shows what the owner's PDS
holds — including repositories the appview never indexed.

`repo clone` resolves the repository before cloning, so the URL comes from the
record rather than a guess. HTTPS goes through the appview, which proxies git to
the knot; SSH goes to the knot host directly, with `knot1.tangled.sh` presented
as `tangled.org` exactly as the web UI does. `--permalink` clones by repo DID,
which survives a rename or a handle change.

**`repo create` is the only tgl write that is not a PDS record alone.** The knot
initialises the bare repository and mints the repo DID under a service-auth
token scoped to `sh.tangled.repo.create`; only then is the `sh.tangled.repo`
record written, naming that DID. If the record write fails, tgl asks the knot to
delete what it just made rather than leave it orphaned — the knot permits that
precisely because no record exists yet. The rkey is the lowercased name, as the
appview does it, and tgl refuses when that rkey is taken rather than overwrite
an existing repository's record.

### Browsing

```sh
tgl browse                 # the repository page
tgl browse issues
tgl browse issue 405
tgl browse pr 900 --no-open
```

The URL always goes to stdout; a browser is only launched when stdout is a
terminal and `--json` is off, so CI and agents get a URL instead. `issue` and
`pr` take the sequential number, because that is the only thing the web UI's
URLs are keyed on — an rkey is refused rather than mapped to a wrong page.

### Referring to an issue or pull request

`<ref>` accepts, in descending order of reliability:

| Form | Notes |
|---|---|
| `3ms4qpm3qjd22` | The **rkey**. This is the record's real identity — prefer it. |
| `at://did:plc:…/sh.tangled.repo.issue/3ms4…` | Full AT-URI. |
| `https://tangled.org/@owner/repo/issues/42` | Web URL. |
| `42` | The sequential number. See below. |

**About `#42`.** Tangled has no issue numbers in its data model. The number the
web UI shows is allocated by *that appview's* private counter; it appears in no
record and in no API, and it has holes where issues were deleted. Reconstructing
it from record order does not work — on `tangled.org/core`, the naive ordinal for
405 lands on a completely different issue.

So `tgl` does not guess. Given a number, it asks the appview that allocated it
and reads the record's AT-URI back off the page. That is exact, but it depends on
that page's markup: if the lookup fails, the command fails rather than acting on
the wrong record. Point `--appview` at a different instance if you run one.

`--json` always reports `rkey` and `uri`, so scripts never need to touch a
number. When you do supply one it is echoed back as `appviewNumber`, documented
as tangled.org-specific.

## Output and exit codes

`--json` puts a single JSON document on stdout. Warnings and notes always go to
stderr, so stdout stays parseable.

```sh
tgl issue list --json | jq -r '.issues[] | select(.state=="open") | .rkey'
```

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 4 | authentication failure |
| 8 | not found |

## Environment

| Variable | Effect |
|---|---|
| `TANGLED_HANDLE`, `TANGLED_APP_PASSWORD` | Credentials; both or neither. Bypasses the keychain. |
| `TANGLED_REPO` | Default `--repo`. |
| `TANGLED_KNOT` | Default `--knot` for `repo create` (default `knot1.tangled.sh`). |
| `TANGLED_BOBBIN_HOST` | Alternate read appview (default `https://api.tangled.org`). |
| `TANGLED_APPVIEW` | Alternate web appview for `#number` lookups (default `https://tangled.org`). |
| `TGL_CONFIG` | Path to the active-account pointer file. |

## How it works

`docs/protocol.md` is the research this was built from: the lexicons, the
appview's own code paths, and live probes, with a source reference for every
claim. Short version:

- **Reads** go to [Bobbin](https://api.tangled.org), the read-only unauthenticated
  appview. It covers the entire read path for issues, pulls, comments and labels.
- **Writes** are atproto records on **your own PDS** — `sh.tangled.repo.issue`,
  `sh.tangled.repo.pull`, `sh.tangled.feed.comment`,
  `sh.tangled.repo.issue.state`, `sh.tangled.repo.pull.status`. The appview and
  the knot pick them up off the firehose; nothing else needs to be called.
- **Closing is not mutation.** State is an append-only log: closing an issue
  writes a new state record. Whether that is honoured is the appview's decision
  (it accepts state records from the issue author, repo owner and collaborators),
  so `tgl` reports that the record was written, not that it was accepted.
- **Merging and repo creation** are the exceptions: knot calls
  (`sh.tangled.repo.merge`, `sh.tangled.repo.create`, and `sh.tangled.repo.delete`
  for rollback), each authorised by a ~60-second
  `com.atproto.server.getServiceAuth` token whose audience is
  `did:web:<knot-host>` and whose `lxm` is the exact method name.
- **Creating a repository is two writes that must agree.** The appview only
  indexes a `sh.tangled.repo` record whose `knot` matches the knot named by the
  repo DID's own document, and drops the record entirely when it carries no repo
  DID. tgl checks that agreement after writing and warns when it does not hold.

### Known limitations

These are protocol-level, not oversights — see `docs/plan.md`:

- **Fork-based `pr create`** is not supported. Identifying the fork needs the
  appview's private fork tables, which no documented lexicon exposes. Fork-based
  pulls can be listed, viewed and checked out.
- **Some pull records carry an undocumented `versions` field** instead of
  `rounds`, with no patch blob. `pr view --patch` reports this rather than
  showing an empty diff.
- **Stacked pulls cannot be merged.** The web UI merges a whole mergeable prefix
  in one call; `pr merge` refuses rather than send a patch that will not apply.
- **Comments on unmigrated accounts are invisible.** Tangled moved comments to
  `sh.tangled.feed.comment`; the superseded collections have no listing endpoint
  and their authors cannot be enumerated.
- **Repos predating Tangled v1.13** have no repo DID. Bobbin's issue and pull
  listings key on it, so those repos cannot be listed, and they have no
  `--permalink` clone URL.
- **`browse` cannot open a record by rkey.** Web pages are addressed by the
  appview's sequential number, and nothing maps an rkey back to one.
- **`repo create` does not fork or import.** `sh.tangled.repo.create` takes a
  `source` URL for that, but the fork relationship the appview shows lives in its
  private tables, the same reason fork-based `pr create` is out.

## Tests

```sh
make test          # unit tests: resolution and rendering against recorded fixtures
make integration   # hits a real account; see below
```

Unit tests are hermetic — fixtures in `testdata/` were recorded from the live API
and are replayed through `httptest` servers.

Integration tests write real records:

```sh
TANGLED_HANDLE=you.example \
TANGLED_APP_PASSWORD=xxxx-xxxx-xxxx-xxxx \
TGL_TEST_REPO=you.example/scratch \
  go test -tags integration ./integration/... -v
```

`TGL_TEST_REPO` must be a repo you own and do not mind polluting: the write tests
create an issue, comment on it, close it and reopen it. Without credentials the
suite skips.

`repo create` is opt-in on top of that — set `TGL_TEST_CREATE_REPO=1` — because
it leaves a real repository on the knot and a record on your PDS, and tgl has no
delete command to undo it.

### Verification

The read path, repo resolution, patch download, `pr checkout`, `repo list`,
`repo clone` (HTTPS and SSH), `browse` and `#number` resolution were exercised
against live Tangled infrastructure during development, including a cross-check
of resolved numbers against the web UI's own pages. The write path (issue and pull create/comment/close/merge, and `repo create`)
is implemented from the appview's own code paths and covered by the integration
suite, but has not been run against a live account here.
