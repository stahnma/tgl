package cli

import (
	"context"
	"encoding/json"
	"flag"
	"path/filepath"
	"sort"
	"strings"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/gitutil"
	"tangled.org/tgl/internal/identity"
	"tangled.org/tgl/internal/knot"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/tangled"
)

// DefaultKnot is the knot tangled.org creates repositories on when the user
// picks nothing else.
const DefaultKnot = "knot1.tangled.sh"

func repoCmd() *Command {
	return &Command{
		Name:  "repo",
		Short: "Work with repositories",
		Sub: []*Command{
			repoViewCmd(), repoListCmd(), repoCloneCmd(), repoCreateCmd(), repoStatusCmd(),
		},
	}
}

func repoViewCmd() *Command {
	return &Command{
		Name:  "view",
		Args:  "[owner/name]",
		Short: "Show how tgl resolves the current repository",
		Long: `Resolution runs handle -> owner DID -> repo record -> repo DID -> knot.
This is the first thing to check when another command cannot find a repository.`,
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 && app.RepoSpec == "" {
				app.RepoSpec = args[0]
			}
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			ref := repoRef(repo)
			if app.JSON {
				return app.EmitJSON(ref)
			}
			render.WriteRepo(app.Out, &ref)
			return nil
		},
	}
}

func repoListCmd() *Command {
	var opts struct{ limit int }
	return &Command{
		Name:  "list",
		Args:  "[owner]",
		Short: "List an account's repositories",
		Long: `owner is a handle or a DID. Without one, tgl uses the owner of the
repository in the working directory, and failing that the logged-in account.

This lists sh.tangled.repo records, so it shows what the owner's PDS holds — a
repository whose record exists but which the appview never indexed still
appears here.`,
		Flags: func(fs *flag.FlagSet) {
			fs.IntVar(&opts.limit, "limit", 30, "maximum repositories to return (0 for all)")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			var spec string
			if len(args) > 0 {
				spec = args[0]
			}
			owner, err := app.listOwner(ctx, spec)
			if err != nil {
				return err
			}

			items, err := bobbin.ListAll(ctx, 0, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
				return app.Bobbin().ListRepos(ctx, owner.DID, limit, cursor)
			})
			if err != nil {
				return Wrap(CodeError, err, "listing repositories for %s", owner)
			}

			repos := repoSummaries(items, app.Note)
			if opts.limit > 0 && len(repos) > opts.limit {
				repos = repos[:opts.limit]
			}

			out := &render.RepoList{Owner: owner, Repos: repos, Count: len(repos)}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteRepoList(app.Out, out)
			return nil
		},
	}
}

// repoSummaries decodes repo records into list entries, newest first.
//
// A repo record's name is cosmetic and often absent, in which case the rkey is
// the name the web UI shows (core:appview/models/repo.go:79-84).
func repoSummaries(items []bobbin.ListItem, warn func(string, ...any)) []render.RepoSummary {
	repos := make([]render.RepoSummary, 0, len(items))
	for _, it := range items {
		var rec tangled.Repo
		if err := json.Unmarshal(it.Value, &rec); err != nil {
			warn("warning: skipping malformed repo record %s: %v", it.URI, err)
			continue
		}
		u, err := tangled.ParseATURI(it.URI)
		if err != nil {
			warn("warning: skipping repo with an unparseable URI %s: %v", it.URI, err)
			continue
		}
		repos = append(repos, render.RepoSummary{
			Name:        rec.Slug(u.RKey),
			RKey:        u.RKey,
			AtURI:       it.URI,
			RepoDID:     rec.RepoDid,
			Knot:        rec.Knot,
			Description: rec.Description,
			Spindle:     rec.Spindle,
			CreatedAt:   rec.CreatedAt,
		})
	}
	// listRepos promises no order; newest first matches the other list commands.
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].CreatedAt > repos[j].CreatedAt })
	return repos
}

// listOwner picks whose repositories to list: the argument, else the owner of
// the repo in the working directory, else the logged-in account.
func (a *App) listOwner(ctx context.Context, spec string) (render.Actor, error) {
	if spec != "" {
		did, err := a.IDs().ResolveHandle(ctx, spec)
		if err != nil {
			return render.Actor{}, Wrap(CodeNotFound, err, "resolving owner %q", spec)
		}
		handle := strings.TrimPrefix(spec, "@")
		if identity.IsDID(handle) {
			handle = ""
			if doc, derr := a.IDs().Document(ctx, did); derr == nil {
				handle = doc.Handle()
			}
		}
		return render.Actor{DID: did, Handle: handle}, nil
	}
	if repo, err := a.Repo(ctx); err == nil {
		return render.Actor{DID: repo.OwnerDID, Handle: repo.OwnerHandle}, nil
	}
	sess, err := a.Auth(ctx)
	if err != nil {
		return render.Actor{}, Errorf(CodeError,
			"no owner given, no repository in the working directory, and not authenticated: pass an owner, or run `tgl auth login`")
	}
	return render.Actor{DID: sess.DID, Handle: sess.Handle}, nil
}

func repoCloneCmd() *Command {
	var opts struct {
		protocol  string
		permalink bool
	}
	return &Command{
		Name:  "clone",
		Args:  "<owner/name> [directory]",
		Short: "Clone a repository",
		Long: `The repository is resolved first, so the clone URL is built from the
record rather than guessed: HTTPS goes through the appview, which proxies git to
the knot, and SSH goes to the knot host directly (core:appview/pages/templates/
repo/fragments/cloneDropdown.html).

--permalink clones by repo DID instead of owner/name. That URL survives a rename
or a handle change, which the owner/name form does not.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.protocol, "protocol", "https", "clone protocol: https or ssh")
			fs.BoolVar(&opts.permalink, "permalink", false, "clone by repo DID rather than owner/name")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 && app.repoSpec() == "" {
				return Errorf(CodeError, "usage: tgl repo clone <owner/name> [directory]")
			}
			spec := app.repoSpec()
			var dir string
			if len(args) > 0 {
				spec = args[0]
				if len(args) > 1 {
					dir = args[1]
				}
			}
			switch opts.protocol {
			case "https", "ssh":
			default:
				return Errorf(CodeError, "invalid --protocol %q: expected https or ssh", opts.protocol)
			}

			repo, err := app.Resolver().FromSpec(ctx, spec)
			if err != nil {
				return Wrap(CodeNotFound, err, "resolving %s", spec)
			}
			url, err := app.cloneURL(repo, opts.protocol, opts.permalink)
			if err != nil {
				return err
			}
			if dir == "" {
				dir = repo.Name
			}

			// git's progress goes to stderr so that --json keeps stdout to
			// itself.
			if err := gitutil.RunStreaming(ctx, app.Dir, app.ErrOut, app.ErrOut, "clone", url, dir); err != nil {
				return Wrap(CodeError, err, "cloning %s", url)
			}
			if abs, aerr := filepath.Abs(filepath.Join(app.Dir, dir)); aerr == nil {
				dir = abs
			}

			out := &render.Cloned{Repo: repoRef(repo), URL: url, Protocol: opts.protocol, Directory: dir}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteCloned(app.Out, out)
			return nil
		},
	}
}

// cloneURL builds a git URL for a resolved repo.
//
// The forms are the ones the web UI offers. HTTPS is served by the appview for
// every knot it knows, so it uses the appview base rather than the knot host;
// SSH goes straight to the knot, with knot1.tangled.sh presented as tangled.org
// exactly as the web UI does.
func (a *App) cloneURL(repo *resolve.Repo, protocol string, permalink bool) (string, error) {
	byDID := permalink || repo.OwnerHandle == ""
	if byDID && repo.RepoDID == "" {
		if permalink {
			return "", Errorf(CodeError, "repo %s has no repoDid, so it has no permalink clone URL (it predates Tangled v1.13)", repo.Slug())
		}
		return "", Errorf(CodeError, "could not resolve a handle for owner %s, and repo %s has no repoDid to fall back on", repo.OwnerDID, repo.Name)
	}
	if protocol == "ssh" {
		host := sshCloneHost(repo.Knot)
		if host == "" {
			return "", Errorf(CodeError, "repo %s declares no knot, so tgl cannot build an SSH clone URL", repo.Slug())
		}
		if byDID {
			return "git@" + host + ":" + repo.RepoDID, nil
		}
		return "git@" + host + ":" + repo.OwnerHandle + "/" + repo.Name, nil
	}
	base := a.appviewBase()
	if byDID {
		return base + "/" + repo.RepoDID, nil
	}
	return base + "/" + repo.OwnerHandle + "/" + repo.Name, nil
}

// sshCloneHost maps a knot host to the host its SSH clone URLs use. knot1 is
// fronted by tangled.org; everything else is addressed directly, minus any port
// (core:appview/pages/templates/repo/fragments/cloneDropdown.html).
func sshCloneHost(knotHost string) string {
	if knotHost == "" {
		return ""
	}
	host, _, found := strings.Cut(knotHost, ":")
	if !found {
		host = knotHost
	}
	if host == "knot1.tangled.sh" {
		return "tangled.org"
	}
	return host
}

func repoCreateCmd() *Command {
	var opts struct {
		description   string
		knot          string
		defaultBranch string
	}
	return &Command{
		Name:  "create",
		Args:  "<name>",
		Short: "Create a repository",
		Long: `Creation is the one write in tgl that is not a PDS record alone. The knot
initialises the bare repository and mints the repo DID, authorised by a
short-lived service-auth token, and only then does tgl write the
sh.tangled.repo record naming that DID. If the record write fails, tgl asks the
knot to delete what it just created rather than leave it orphaned.

The rkey is the lowercased name, matching the appview, so it is stable and
guessable rather than a TID. tgl refuses when that rkey is already taken, since
overwriting it would rename an existing repository.

The knot must allow you to create repositories on it; the appview only indexes
the result once the repo DID's document points back at that same knot.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.description, "description", "", "repository description (140 characters or fewer)")
			fs.StringVar(&opts.knot, "knot", "", "knot to create the repository on (default: "+DefaultKnot+", or TANGLED_KNOT)")
			fs.StringVar(&opts.defaultBranch, "default-branch", "main", "default branch of the new repository")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl repo create <name>")
			}
			name := strings.TrimSuffix(args[0], ".git")
			if err := validateRepoName(name); err != nil {
				return err
			}
			if n := len([]rune(opts.description)); n > 140 {
				return Errorf(CodeError, "description is %d characters; sh.tangled.repo.description allows 140", n)
			}
			knotHost := opts.knot
			if knotHost == "" {
				knotHost = strings.TrimSpace(osGetenv("TANGLED_KNOT"))
			}
			if knotHost == "" {
				knotHost = DefaultKnot
			}
			rkey := strings.ToLower(name)

			sess, err := app.Auth(ctx)
			if err != nil {
				return err
			}

			// Check before touching the knot: createRecord would fail on a
			// collision anyway, but only after the knot had built a repository
			// we would then have to tear down.
			if _, err := sess.Client.GetRecord(ctx, sess.DID, tangled.NSIDRepo, rkey); err == nil {
				return Errorf(CodeError,
					"at://%s/%s/%s already exists: you already have a repository named %q, or a record left over from renaming one",
					sess.DID, tangled.NSIDRepo, rkey, rkey)
			}

			kc := knot.New(knotHost)
			tok, err := app.serviceAuthFor(ctx, kc, knot.NSIDCreate)
			if err != nil {
				return err
			}
			kc.SetToken(tok)
			created, err := kc.CreateRepo(ctx, &knot.CreateInput{
				RKey:          rkey,
				Name:          rkey,
				DefaultBranch: opts.defaultBranch,
			})
			if err != nil {
				return Wrap(CodeError, err, "asking %s to create the repository", kc.Host)
			}
			if created.RepoDid == "" {
				return Errorf(CodeError,
					"%s created the repository but minted no repo DID; the appview cannot index a repo without one, and the knot likely needs upgrading", kc.Host)
			}

			rec := tangled.Repo{
				Type:        tangled.NSIDRepo,
				Name:        name,
				Knot:        knotHost,
				Description: opts.description,
				RepoDid:     created.RepoDid,
				CreatedAt:   nowRFC3339(),
			}
			ref, err := sess.Client.CreateRecord(ctx, sess.DID, tangled.NSIDRepo, rkey, rec)
			if err != nil {
				app.rollbackKnotRepo(ctx, kc, created.RepoDid, sess.DID, rkey)
				return Wrap(CodeError, err, "writing the repo record (the repository on %s was rolled back)", kc.Host)
			}

			// The appview rejects a repo record whose knot disagrees with the
			// repo DID's own service endpoint
			// (core:appview/ingester_repo.go:476-478).
			if doc, derr := app.IDs().Document(ctx, created.RepoDid); derr == nil {
				if declared := hostOfEndpoint(doc.Knot()); declared != "" && !strings.EqualFold(declared, knotHost) {
					app.Note("warning: repo DID %s points at %s but the record says %s; the appview will not index this repository",
						created.RepoDid, declared, knotHost)
				}
			}

			owner := render.Actor{DID: sess.DID, Handle: sess.Handle}
			out := &render.RepoCreated{
				URI:           ref.URI,
				CID:           ref.CID,
				RKey:          rkey,
				Name:          name,
				Owner:         owner,
				Knot:          knotHost,
				RepoDID:       created.RepoDid,
				DefaultBranch: opts.defaultBranch,
				Description:   opts.description,
				Action:        "created repository",
			}
			if sess.Handle != "" {
				out.URL = app.appviewBase() + "/" + sess.Handle + "/" + name
				out.CloneURL = out.URL
			}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteRepoCreated(app.Out, out)
			return nil
		},
	}
}

// rollbackKnotRepo removes a repository the knot created for a record write that
// then failed. The knot only honours this while no sh.tangled.repo record exists
// (core:knotserver/xrpc/delete_repo.go:64-70), which is exactly this situation.
func (a *App) rollbackKnotRepo(ctx context.Context, kc *knot.Client, repoDID, ownerDID, rkey string) {
	tok, err := a.serviceAuthFor(ctx, kc, knot.NSIDDelete)
	if err != nil {
		a.Note("warning: could not mint a token to roll back %s on %s: %v", repoDID, kc.Host, err)
		return
	}
	kc.SetToken(tok)
	if err := kc.DeleteRepo(ctx, &knot.DeleteInput{Repo: repoDID, Did: ownerDID, Name: rkey, RKey: rkey}); err != nil {
		a.Note("warning: %s still holds repository %s; delete it there by hand: %v", kc.Host, repoDID, err)
	}
}

// validateRepoName mirrors core:appview/models/repo.go:156-188. The knot
// enforces the same rules, so checking here only turns a remote error into a
// local one.
func validateRepoName(name string) error {
	switch {
	case name == "":
		return Errorf(CodeError, "repository name cannot be empty")
	case len(name) > 100:
		return Errorf(CodeError, "repository name must be 100 characters or fewer")
	case strings.ContainsAny(name, `/\`):
		return Errorf(CodeError, "repository name %q contains a path separator", name)
	case strings.HasPrefix(name, "."), strings.HasSuffix(name, "."):
		return Errorf(CodeError, "repository name %q may not start or end with a dot", name)
	case strings.Contains(name, ".."):
		return Errorf(CodeError, "repository name %q may not contain sequential dots", name)
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return Errorf(CodeError, "repository name %q may only contain letters, digits, periods, hyphens and underscores", name)
		}
	}
	return nil
}

// hostOfEndpoint reduces a service endpoint to its bare host for comparison.
func hostOfEndpoint(endpoint string) string {
	s := endpoint
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSuffix(strings.Trim(s, "/"), ":")
}

func repoStatusCmd() *Command {
	var opts struct{ counts bool }
	return &Command{
		Name:  "status",
		Short: "Show appview reachability and repo counts",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&opts.counts, "counts", true, "include issue and pull counts")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			out := map[string]any{"bobbin": app.Bobbin().Host()}
			cov, err := app.Bobbin().Coverage(ctx)
			if err != nil {
				out["ready"] = false
				out["error"] = err.Error()
			} else {
				out["ready"] = cov.Ready
				out["eventsProcessed"] = cov.EventsProcessed
			}
			if opts.counts {
				if repo, err := app.Repo(ctx); err == nil {
					if subject, serr := repo.Subject(); serr == nil {
						if c, cerr := app.Bobbin().CountIssues(ctx, subject); cerr == nil {
							out["issues"] = c.Count
						}
						if c, cerr := app.Bobbin().CountPulls(ctx, subject); cerr == nil {
							out["pulls"] = c.Count
						}
					}
					out["repo"] = repo.Slug()
				}
			}
			if app.JSON {
				return app.EmitJSON(out)
			}
			for _, k := range []string{"repo", "bobbin", "ready", "eventsProcessed", "issues", "pulls", "error"} {
				if v, ok := out[k]; ok {
					app.Print("%-16s %v", k, v)
				}
			}
			return nil
		},
	}
}
