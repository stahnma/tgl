package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	"tangled.org/tgl/internal/atproto"
	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/identity"
	"tangled.org/tgl/internal/knot"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/tangled"
	"tangled.org/tgl/internal/tid"
)

func prCmd() *Command {
	return &Command{
		Name:  "pr",
		Short: "Work with pull requests",
		Sub: []*Command{
			prListCmd(), prViewCmd(), prCreateCmd(), prCommentCmd(),
			prCheckoutCmd(), prMergeCmd(), prStateCmd("close"), prStateCmd("reopen"),
		},
	}
}

func prListCmd() *Command {
	var opts struct {
		state    string
		author   string
		label    string
		limit    int
		noLabels bool
	}
	return &Command{
		Name:  "list",
		Short: "List pull requests",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.state, "state", "open", "filter by state: open, closed, merged, or all")
			fs.StringVar(&opts.author, "author", "", "filter by author handle or DID")
			fs.StringVar(&opts.label, "label", "", "filter by label name")
			fs.IntVar(&opts.limit, "limit", 30, "maximum pull requests to return")
			fs.BoolVar(&opts.noLabels, "no-labels", false, "skip label lookups")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			subject, err := repo.Subject()
			if err != nil {
				return Errorf(CodeError, "%v", err)
			}
			status, err := normalizeState(opts.state, "open", "closed", "merged")
			if err != nil {
				return err
			}
			author, err := app.resolveActorFilter(ctx, opts.author)
			if err != nil {
				return err
			}
			fetch := opts.limit
			if opts.label != "" {
				fetch = 0
			}
			items, err := bobbin.ListAll(ctx, fetch, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
				return app.Bobbin().ListPulls(ctx, bobbin.PullQuery{
					RepoDID: subject, Status: status, Author: author,
					Limit: limit, Cursor: cursor, Order: "desc",
				})
			})
			if err != nil {
				return Wrap(CodeError, err, "listing pull requests")
			}

			actors := app.actors()
			actors.warm(ctx, authorities(items))
			af := actors.fn()

			pulls := make([]render.Pull, 0, len(items))
			uris := make([]string, 0, len(items))
			for _, it := range items {
				p, err := render.BuildPull(it, af)
				if err != nil {
					app.Note("warning: skipping malformed pull %s: %v", it.URI, err)
					continue
				}
				pulls = append(pulls, p)
				uris = append(uris, p.URI)
			}
			if !opts.noLabels || opts.label != "" {
				defs := app.labelDefs(ctx, repo)
				byURI := app.labelsForAll(ctx, uris, defs)
				for i := range pulls {
					pulls[i].Labels = byURI[pulls[i].URI]
				}
			}
			if opts.label != "" {
				kept := pulls[:0]
				for _, p := range pulls {
					if matchesLabel(p.Labels, opts.label) {
						kept = append(kept, p)
					}
				}
				pulls = kept
			}
			if opts.limit > 0 && len(pulls) > opts.limit {
				pulls = pulls[:opts.limit]
			}

			out := &render.PullList{Repo: repoRef(repo), Pulls: pulls, Count: len(pulls)}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WritePullList(app.Out, out)
			return nil
		},
	}
}

func prViewCmd() *Command {
	var opts struct {
		patch    bool
		comments bool
	}
	return &Command{
		Name:  "view",
		Args:  "<ref>",
		Short: "Show one pull request",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&opts.patch, "patch", false, "include the latest round's patch")
			fs.BoolVar(&opts.comments, "comments", true, "include comments")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl pr view <ref>")
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindPull, args[0])
			if err != nil {
				return err
			}
			actors := app.actors()
			actors.warm(ctx, authorities([]bobbin.ListItem{r.Item}))
			p, err := render.BuildPull(r.Item, actors.fn())
			if err != nil {
				return Wrap(CodeError, err, "decoding pull %s", r.URI)
			}
			p.AppviewNumber = r.Number

			defs := app.labelDefs(ctx, repo)
			p.Labels = app.labelsFor(ctx, p.URI, defs)

			if opts.comments {
				cs, err := app.comments(ctx, p.URI)
				if err != nil {
					app.Note("warning: could not load comments: %v", err)
				} else {
					p.Comments = cs
					p.CommentCount = len(cs)
				}
			}
			if opts.patch {
				patch, err := app.patchFor(ctx, &p)
				if err != nil {
					return err
				}
				p.Patch = patch
			}
			if app.JSON {
				return app.EmitJSON(p)
			}
			render.WritePull(app.Out, &p)
			return nil
		},
	}
}

func prCommentCmd() *Command {
	var opts struct {
		body     string
		bodyFile string
		round    int
	}
	return &Command{
		Name:  "comment",
		Args:  "<ref>",
		Short: "Comment on a pull request",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.body, "body", "", "comment body")
			fs.StringVar(&opts.bodyFile, "body-file", "", "read the body from a file, or - for stdin")
			fs.IntVar(&opts.round, "round", -1, "round index the comment applies to (default: the latest round)")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl pr comment <ref> --body ...")
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindPull, args[0])
			if err != nil {
				return err
			}
			body, err := readBody(app, opts.body, opts.bodyFile)
			if err != nil {
				return err
			}
			if body == "" {
				return Errorf(CodeError, "--body is required")
			}

			// pullRoundIdx is required when the subject is a pull
			// (core:lexicons/feed/comment.json), so default to the latest round.
			var rec tangled.Pull
			if err := json.Unmarshal(r.Item.Value, &rec); err != nil {
				return Wrap(CodeError, err, "decoding pull %s", r.URI)
			}
			idx := opts.round
			if idx < 0 {
				idx = len(rec.Rounds) - 1
				if idx < 0 {
					idx = 0
				}
			}
			return app.postComment(ctx, r.Item.URI, r.Item.CID, body, &idx)
		},
	}
}

func prStateCmd(action string) *Command {
	status := tangled.PullStatusClosed
	if action == "reopen" {
		status = tangled.PullStatusOpen
	}
	return &Command{
		Name:  action,
		Args:  "<ref>",
		Short: titleCase(action) + " a pull request",
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl pr %s <ref>", action)
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindPull, args[0])
			if err != nil {
				return err
			}
			return app.writePullStatus(ctx, r.URI, status, action+"d pull request")
		},
	}
}

func (a *App) writePullStatus(ctx context.Context, pullURI, status, action string) error {
	sess, err := a.Auth(ctx)
	if err != nil {
		return err
	}
	rkey := tid.Now()
	rec := tangled.PullStatus{
		Type:      tangled.NSIDPullStatus,
		Pull:      pullURI,
		Status:    status,
		CreatedAt: nowRFC3339(),
	}
	ref, err := sess.Client.PutRecord(ctx, sess.DID, tangled.NSIDPullStatus, rkey, rec, "")
	if err != nil {
		return Wrap(CodeError, err, "writing pull status record")
	}
	return a.emitCreated(&render.Created{URI: ref.URI, CID: ref.CID, RKey: rkey, Action: action})
}

// patchFor downloads and decompresses the latest round's patch blob.
//
// Round blobs live on the pull author's PDS as gzipped git-format-patch text
// (core:lexicons/pulls/pull.json). Pulls carrying the undocumented `versions`
// shape have no blob at all, and there is no documented way to obtain their
// patch, so we say so rather than inventing one.
func (a *App) patchFor(ctx context.Context, p *render.Pull) (string, error) {
	if len(p.Rounds) == 0 {
		if len(p.Versions) > 0 {
			return "", Errorf(CodeError,
				"pull %s carries no patch blob: its record uses the undocumented `versions` commit-range shape, which appears in no lexicon and which tgl cannot resolve to a patch", p.RKey)
		}
		return "", Errorf(CodeError, "pull %s has no rounds", p.RKey)
	}
	round := p.Rounds[len(p.Rounds)-1]
	if round.BlobCID == "" {
		return "", Errorf(CodeError, "round %d of pull %s has no blob reference", round.Index, p.RKey)
	}
	_, pds, err := a.IDs().PDSFor(ctx, p.Author.DID)
	if err != nil {
		return "", Wrap(CodeError, err, "locating the PDS holding the patch blob")
	}
	gz, err := atproto.New(pds).GetBlob(ctx, p.Author.DID, round.BlobCID)
	if err != nil {
		return "", Wrap(CodeError, err, "downloading patch blob %s", round.BlobCID)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return "", Wrap(CodeError, err, "patch blob %s is not gzip", round.BlobCID)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return "", Wrap(CodeError, err, "decompressing patch blob")
	}
	return string(raw), nil
}

// knotFor builds a knot client for a repo DID, resolving the knot endpoint from
// the repo DID's document.
func (a *App) knotFor(ctx context.Context, repoDID string) (*knot.Client, error) {
	doc, err := a.IDs().Document(ctx, repoDID)
	if err != nil {
		return nil, Wrap(CodeError, err, "resolving repo DID %s", repoDID)
	}
	e := doc.Knot()
	if e == "" {
		return nil, Errorf(CodeError, "repo DID %s declares no knot service endpoint", repoDID)
	}
	return knot.New(e), nil
}

// serviceAuthFor mints a service-auth token for one knot method.
//
// The audience is a did:web derived from the knot hostname, and lxm must equal
// the NSID being called; knots validate both
// (core:xrpc/serviceauth/service_auth.go). Tokens live about a minute, so this
// is called immediately before each request and never cached.
func (a *App) serviceAuthFor(ctx context.Context, k *knot.Client, lxm string) (string, error) {
	sess, err := a.Auth(ctx)
	if err != nil {
		return "", err
	}
	aud := identity.DidWeb(k.Host)
	tok, err := sess.Client.GetServiceAuth(ctx, aud, lxm, 60)
	if err != nil {
		return "", Wrap(CodeAuth, err, "minting service auth for %s (aud %s)", lxm, aud)
	}
	return tok, nil
}

func prMergeCmd() *Command {
	var opts struct {
		message   string
		skipCheck bool
	}
	return &Command{
		Name:  "merge",
		Args:  "<ref>",
		Short: "Merge a pull request",
		Long: `Merging is the one operation in tgl that is not a PDS record write: the
patch is applied by the repo's knot via sh.tangled.repo.merge, authorised with a
short-lived com.atproto.server.getServiceAuth token. tgl then writes a
sh.tangled.repo.pull.status record marking the pull merged.

Stacked pull requests are refused: the web UI merges a whole mergeable prefix of
a stack in one call, and getting that wrong would produce a patch that does not
apply.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.message, "message", "", "merge commit message (default: the pull title)")
			fs.BoolVar(&opts.skipCheck, "skip-check", false, "skip the pre-merge conflict check")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl pr merge <ref>")
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindPull, args[0])
			if err != nil {
				return err
			}
			actors := app.actors()
			actors.warm(ctx, authorities([]bobbin.ListItem{r.Item}))
			p, err := render.BuildPull(r.Item, actors.fn())
			if err != nil {
				return Wrap(CodeError, err, "decoding pull %s", r.URI)
			}

			if p.Stacked {
				return Errorf(CodeError,
					"pull %s is stacked on %s. Merging a stack means merging its whole mergeable prefix in one knot call; tgl does not do that yet, so merge this one in the web UI.",
					p.RKey, p.DependentOn)
			}
			if p.State == "merged" {
				return Errorf(CodeError, "pull %s is already merged", p.RKey)
			}

			patch, err := app.patchFor(ctx, &p)
			if err != nil {
				return err
			}

			kc, err := app.knotFor(ctx, p.TargetRepoDID)
			if err != nil {
				return err
			}
			// Knots without repo-did-input read did+name instead of repo, so
			// send all three, exactly as the appview does.
			base := struct{ repo, did, name string }{p.TargetRepoDID, repo.OwnerDID, repo.RKey}

			if !opts.skipCheck {
				out, err := kc.MergeCheck(ctx, &knot.MergeCheckInput{
					Repo: base.repo, Did: base.did, Name: base.name,
					Patch: patch, Branch: p.TargetBranch,
				})
				if err != nil {
					app.Note("warning: merge check failed to run (%v); continuing", err)
				} else if out.IsConflicted {
					var names []string
					for _, c := range out.Conflicts {
						names = append(names, c.Filename)
					}
					return Errorf(CodeError, "pull %s conflicts with %s: %s",
						p.RKey, p.TargetBranch, strings.Join(names, ", "))
				}
			}

			tok, err := app.serviceAuthFor(ctx, kc, knot.NSIDMerge)
			if err != nil {
				return err
			}
			kc.SetToken(tok)

			msg := opts.message
			if msg == "" {
				msg = p.Title
			}
			in := &knot.MergeInput{
				Repo: base.repo, Did: base.did, Name: base.name,
				Patch:         patch,
				Branch:        p.TargetBranch,
				CommitMessage: msg,
				CommitBody:    p.Body,
				AuthorName:    p.Author.Handle,
			}
			if err := kc.Merge(ctx, in); err != nil {
				return Wrap(CodeError, err, "knot %s refused the merge", kc.Host)
			}
			return app.writePullStatus(ctx, p.URI, tangled.PullStatusMerged, "merged pull request")
		},
	}
}
