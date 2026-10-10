package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/tangled"
	"tangled.org/tgl/internal/tid"
)

func issueCmd() *Command {
	return &Command{
		Name:  "issue",
		Short: "Work with issues",
		Sub: []*Command{
			issueListCmd(), issueViewCmd(), issueCreateCmd(),
			issueCommentCmd(), issueStateCmd("close"), issueStateCmd("reopen"),
		},
	}
}

func repoRef(r *resolve.Repo) render.RepoRef {
	return render.RepoRef{
		Owner:        render.Actor{DID: r.OwnerDID, Handle: r.OwnerHandle},
		Name:         r.Name,
		RKey:         r.RKey,
		AtURI:        r.AtURI,
		RepoDID:      r.RepoDID,
		Knot:         r.Knot,
		KnotEndpoint: r.KnotEndpoint,
		Description:  r.Description,
		Spindle:      r.Spindle,
	}
}

func issueListCmd() *Command {
	var opts struct {
		state    string
		author   string
		label    string
		limit    int
		noLabels bool
	}
	return &Command{
		Name:  "list",
		Short: "List issues",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.state, "state", "open", "filter by state: open, closed, or all")
			fs.StringVar(&opts.author, "author", "", "filter by author handle or DID")
			fs.StringVar(&opts.label, "label", "", "filter by label name")
			fs.IntVar(&opts.limit, "limit", 30, "maximum issues to return")
			fs.BoolVar(&opts.noLabels, "no-labels", false, "skip label lookups (one fewer request per issue)")
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
			state, err := normalizeState(opts.state, "open", "closed")
			if err != nil {
				return err
			}
			author, err := app.resolveActorFilter(ctx, opts.author)
			if err != nil {
				return err
			}

			// Label filtering happens client-side, so fetch generously and trim
			// after filtering rather than truncating first.
			fetch := opts.limit
			if opts.label != "" {
				fetch = 0
			}
			items, err := bobbin.ListAll(ctx, fetch, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
				return app.Bobbin().ListIssues(ctx, bobbin.IssueQuery{
					RepoDID: subject, State: state, Author: author,
					Limit: limit, Cursor: cursor, Order: "desc",
				})
			})
			if err != nil {
				return Wrap(CodeError, err, "listing issues")
			}

			actors := app.actors()
			actors.warm(ctx, authorities(items))
			af := actors.fn()

			issues := make([]render.Issue, 0, len(items))
			uris := make([]string, 0, len(items))
			for _, it := range items {
				is, err := render.BuildIssue(it, af)
				if err != nil {
					app.Note("warning: skipping malformed issue %s: %v", it.URI, err)
					continue
				}
				issues = append(issues, is)
				uris = append(uris, is.URI)
			}

			if !opts.noLabels || opts.label != "" {
				defs := app.labelDefs(ctx, repo)
				byURI := app.labelsForAll(ctx, uris, defs)
				for i := range issues {
					issues[i].Labels = byURI[issues[i].URI]
				}
			}
			if opts.label != "" {
				kept := issues[:0]
				for _, is := range issues {
					if matchesLabel(is.Labels, opts.label) {
						kept = append(kept, is)
					}
				}
				issues = kept
			}
			if opts.limit > 0 && len(issues) > opts.limit {
				issues = issues[:opts.limit]
			}

			out := &render.IssueList{Repo: repoRef(repo), Issues: issues, Count: len(issues)}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteIssueList(app.Out, out)
			return nil
		},
	}
}

func issueViewCmd() *Command {
	var opts struct{ comments bool }
	return &Command{
		Name:  "view",
		Args:  "<ref>",
		Short: "Show one issue",
		Long: `<ref> may be an rkey (3ms4qpm3qjd22), an at:// URI, a tangled.org URL,
or a sequential number. Numbers are resolved by creation order and reported on
stderr; prefer the rkey, which is the record's real identity.`,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&opts.comments, "comments", true, "include comments")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl issue view <ref>")
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindIssue, args[0])
			if err != nil {
				return err
			}
			actors := app.actors()
			actors.warm(ctx, authorities([]bobbin.ListItem{r.Item}))
			is, err := render.BuildIssue(r.Item, actors.fn())
			if err != nil {
				return Wrap(CodeError, err, "decoding issue %s", r.URI)
			}
			is.AppviewNumber = r.Number

			defs := app.labelDefs(ctx, repo)
			is.Labels = app.labelsFor(ctx, is.URI, defs)

			if opts.comments {
				cs, err := app.comments(ctx, is.URI)
				if err != nil {
					app.Note("warning: could not load comments: %v", err)
				} else {
					is.Comments = cs
					is.CommentCount = len(cs)
				}
			}
			if app.JSON {
				return app.EmitJSON(is)
			}
			render.WriteIssue(app.Out, &is)
			return nil
		},
	}
}

func issueCreateCmd() *Command {
	var opts struct {
		title    string
		body     string
		bodyFile string
	}
	return &Command{
		Name:  "create",
		Short: "Open a new issue",
		Long: `The issue is written as a sh.tangled.repo.issue record on your own PDS.
The appview and the repo's knot pick it up off the firehose; no other call is
needed.

Note that labels cannot be applied at creation time. A label is a separate
sh.tangled.label.op record referencing a sh.tangled.label.definition, and tgl
does not yet write those.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.title, "title", "", "issue title (required)")
			fs.StringVar(&opts.body, "body", "", "issue body")
			fs.StringVar(&opts.bodyFile, "body-file", "", "read the body from a file, or - for stdin")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			if repo.RepoDID == "" {
				return Errorf(CodeError,
					"repo %s has no repoDid; sh.tangled.repo.issue.repo is a repo DID, so an issue cannot be created against a repo that predates Tangled v1.13", repo.Slug())
			}
			title := opts.title
			if title == "" && len(args) > 0 {
				title = strings.Join(args, " ")
			}
			if title == "" {
				if !app.Interactive() {
					return Errorf(CodeError, "--title is required")
				}
				if title, err = prompt(app, "Title: "); err != nil {
					return err
				}
			}
			body, err := readBody(app, opts.body, opts.bodyFile)
			if err != nil {
				return err
			}

			sess, err := app.Auth(ctx)
			if err != nil {
				return err
			}
			rkey := tid.Now()
			rec := tangled.Issue{
				Type:      tangled.NSIDIssue,
				Repo:      repo.RepoDID,
				Title:     title,
				Body:      body,
				CreatedAt: nowRFC3339(),
			}
			ref, err := sess.Client.PutRecord(ctx, sess.DID, tangled.NSIDIssue, rkey, rec, "")
			if err != nil {
				return Wrap(CodeError, err, "creating issue record")
			}
			return app.emitCreated(&render.Created{URI: ref.URI, CID: ref.CID, RKey: rkey, Action: "created issue"})
		},
	}
}

func issueCommentCmd() *Command {
	var opts struct {
		body     string
		bodyFile string
	}
	return &Command{
		Name:  "comment",
		Args:  "<ref>",
		Short: "Comment on an issue",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.body, "body", "", "comment body")
			fs.StringVar(&opts.bodyFile, "body-file", "", "read the body from a file, or - for stdin")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl issue comment <ref> --body ...")
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindIssue, args[0])
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
			return app.postComment(ctx, r.Item.URI, r.Item.CID, body, nil)
		},
	}
}

func issueStateCmd(action string) *Command {
	state := tangled.IssueStateClosed
	if action == "reopen" {
		state = tangled.IssueStateOpen
	}
	return &Command{
		Name:  action,
		Args:  "<ref>",
		Short: titleCase(action) + " an issue",
		Long: `State is an append-only log: closing writes a new
sh.tangled.repo.issue.state record on your PDS rather than mutating the issue.

Whether the change is honoured is the appview's decision — it accepts state
records from the issue author, the repo owner, and collaborators. tgl reports
that the record was written, not that the appview agreed with it.`,
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl issue %s <ref>", action)
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			r, err := app.resolveRef(ctx, repo, kindIssue, args[0])
			if err != nil {
				return err
			}
			sess, err := app.Auth(ctx)
			if err != nil {
				return err
			}
			rkey := tid.Now()
			rec := tangled.IssueState{
				Type:      tangled.NSIDIssueState,
				Issue:     r.URI,
				State:     state,
				CreatedAt: nowRFC3339(),
			}
			ref, err := sess.Client.PutRecord(ctx, sess.DID, tangled.NSIDIssueState, rkey, rec, "")
			if err != nil {
				return Wrap(CodeError, err, "writing issue state record")
			}
			return app.emitCreated(&render.Created{URI: ref.URI, CID: ref.CID, RKey: rkey, Action: action + "d issue"})
		},
	}
}

// --- shared helpers ---

// comments loads the comment thread for an issue or pull.
//
// Only sh.tangled.feed.comment is reachable: Bobbin exposes no listing for the
// superseded sh.tangled.repo.{issue,pull}.comment collections, and those records
// live on each commenter's PDS with no way to enumerate the commenters. Comments
// on accounts that were never migrated are therefore invisible here.
func (a *App) comments(ctx context.Context, subjectURI string) ([]render.Comment, error) {
	items, err := bobbin.ListAll(ctx, 1000, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
		return a.Bobbin().ListComments(ctx, subjectURI, limit, cursor)
	})
	if err != nil {
		return nil, err
	}
	actors := a.actors()
	actors.warm(ctx, authorities(items))
	return render.BuildComments(items, actors.fn()), nil
}

// postComment writes a sh.tangled.feed.comment record.
//
// The subject is a strongRef, so the subject's CID is mandatory; that is why
// every comment path resolves the target record first.
func (a *App) postComment(ctx context.Context, subjectURI, subjectCID, body string, roundIdx *int) error {
	if subjectCID == "" {
		return Errorf(CodeError, "cannot comment on %s: its CID is unknown, and sh.tangled.feed.comment.subject is a strongRef", subjectURI)
	}
	sess, err := a.Auth(ctx)
	if err != nil {
		return err
	}
	rkey := tid.Now()
	rec := tangled.FeedComment{
		Type:    tangled.NSIDFeedComment,
		Subject: tangled.StrongRef{URI: subjectURI, CID: subjectCID},
		Body: tangled.Markdown{
			Type:     tangled.NSIDMarkdown,
			Original: body,
			Text:     body,
		},
		CreatedAt:    nowRFC3339(),
		PullRoundIdx: roundIdx,
	}
	ref, err := sess.Client.PutRecord(ctx, sess.DID, tangled.NSIDFeedComment, rkey, rec, "")
	if err != nil {
		return Wrap(CodeError, err, "creating comment record")
	}
	return a.emitCreated(&render.Created{URI: ref.URI, CID: ref.CID, RKey: rkey, Action: "added comment"})
}

func (a *App) emitCreated(c *render.Created) error {
	if a.JSON {
		return a.EmitJSON(c)
	}
	render.WriteCreated(a.Out, c)
	return nil
}

// resolveActorFilter maps a handle to a DID for author filters, which Bobbin
// only accepts as DIDs.
func (a *App) resolveActorFilter(ctx context.Context, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	did, err := a.IDs().ResolveHandle(ctx, s)
	if err != nil {
		return "", Wrap(CodeNotFound, err, "resolving --author %q", s)
	}
	return did, nil
}

func normalizeState(s string, allowed ...string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "all" {
		return "", nil
	}
	for _, a := range allowed {
		if s == a {
			return s, nil
		}
	}
	return "", Errorf(CodeError, "invalid state %q: expected one of %s, all", s, strings.Join(allowed, ", "))
}

func authorities(items []bobbin.ListItem) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		u, err := tangled.ParseATURI(it.URI)
		if err != nil || seen[u.Authority] {
			continue
		}
		seen[u.Authority] = true
		out = append(out, u.Authority)
	}
	return out
}

func readBody(app *App, body, file string) (string, error) {
	if file == "" {
		return body, nil
	}
	if body != "" {
		return "", Errorf(CodeError, "--body and --body-file are mutually exclusive")
	}
	if file == "-" {
		b, err := io.ReadAll(app.Stdin)
		if err != nil {
			return "", Wrap(CodeError, err, "reading body from stdin")
		}
		return string(b), nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", Wrap(CodeError, err, "reading %s", file)
	}
	return string(b), nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }
