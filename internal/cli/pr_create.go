package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"os"
	"strings"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/gitutil"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/tangled"
	"tangled.org/tgl/internal/tid"
)

func prCreateCmd() *Command {
	var opts struct {
		title     string
		body      string
		bodyFile  string
		base      string
		head      string
		patchFile string
	}
	return &Command{
		Name:  "create",
		Short: "Open a pull request",
		Long: `Two shapes are supported.

Branch-based (the default): tgl asks the repo's knot to format-patch --base
against --head, then writes a sh.tangled.repo.pull record whose single round
holds that patch, gzipped, as a blob on your PDS. Push your branch first — the
knot diffs what it has, not what is in your working tree.

Patch-based: pass --patch-file (or - for stdin) and tgl uploads that patch
directly, with no source branch recorded.

Fork-based pulls are not supported: identifying the fork requires the appview's
private fork tables, which no documented lexicon exposes.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.title, "title", "", "pull request title")
			fs.StringVar(&opts.body, "body", "", "pull request body")
			fs.StringVar(&opts.bodyFile, "body-file", "", "read the body from a file, or - for stdin")
			fs.StringVar(&opts.base, "base", "", "target branch (default: the repo's default branch)")
			fs.StringVar(&opts.head, "head", "", "source branch (default: the current branch)")
			fs.StringVar(&opts.patchFile, "patch-file", "", "create a patch-based pull from this file, or - for stdin")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			if repo.RepoDID == "" {
				return Errorf(CodeError,
					"repo %s has no repoDid; sh.tangled.repo.pull.target.repo is a repo DID, so a pull cannot target a repo that predates Tangled v1.13", repo.Slug())
			}
			kc, err := app.Knot(repo)
			if err != nil {
				return err
			}

			base := opts.base
			if base == "" {
				db, err := kc.GetDefaultBranch(ctx, repo.KnotIdentifier())
				if err != nil {
					return Wrap(CodeError, err, "determining the default branch; pass --base")
				}
				base = db.Name
			}

			var patch, sourceBranch string
			if opts.patchFile != "" {
				patch, err = readFileOrStdin(app, opts.patchFile)
				if err != nil {
					return err
				}
				if strings.TrimSpace(patch) == "" {
					return Errorf(CodeError, "patch is empty")
				}
			} else {
				head := opts.head
				if head == "" {
					head = gitutil.CurrentBranch(ctx, app.Dir)
				}
				if head == "" {
					return Errorf(CodeError, "could not determine the source branch; pass --head or --patch-file")
				}
				if head == base {
					return Errorf(CodeError, "--head and --base are both %q", base)
				}
				cmp, err := kc.Compare(ctx, repo.KnotIdentifier(), base, head)
				if err != nil {
					return Wrap(CodeError, err, "knot %s could not compare %s..%s (is %s pushed?)", kc.Host, base, head, head)
				}
				if strings.TrimSpace(cmp.FormatPatchRaw) == "" {
					return Errorf(CodeError, "no commits between %s and %s", base, head)
				}
				if cmp.BinaryOmitted {
					app.Note("warning: the knot omitted binary payloads from this patch, so it may not apply cleanly")
				}
				patch, sourceBranch = cmp.FormatPatchRaw, head
			}

			title := opts.title
			if title == "" && len(args) > 0 {
				title = strings.Join(args, " ")
			}
			if title == "" {
				title = firstPatchSubject(patch)
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

			blob, err := sess.Client.UploadBlob(ctx, "application/gzip", gzipBytes([]byte(patch)))
			if err != nil {
				return Wrap(CodeError, err, "uploading the patch blob")
			}
			var blobRef tangled.Blob
			if err := json.Unmarshal(blob, &blobRef); err != nil {
				return Wrap(CodeError, err, "decoding the uploaded blob reference")
			}

			now := nowRFC3339()
			rec := tangled.Pull{
				Type:      tangled.NSIDPull,
				Title:     title,
				Body:      body,
				Target:    tangled.PullTarget{Repo: repo.RepoDID, Branch: base},
				Rounds:    []tangled.PullRound{{CreatedAt: now, PatchBlob: blobRef}},
				CreatedAt: now,
			}
			if sourceBranch != "" {
				rec.Source = &tangled.PullSource{Branch: sourceBranch}
			}

			rkey := tid.Now()
			ref, err := sess.Client.PutRecord(ctx, sess.DID, tangled.NSIDPull, rkey, rec, "")
			if err != nil {
				return Wrap(CodeError, err, "creating pull record")
			}
			return app.emitCreated(&render.Created{URI: ref.URI, CID: ref.CID, RKey: rkey, Action: "created pull request"})
		},
	}
}

func prCheckoutCmd() *Command {
	var opts struct {
		branch string
	}
	return &Command{
		Name:  "checkout",
		Args:  "<ref>",
		Short: "Check out a pull request locally",
		Long: `For branch- and fork-based pulls tgl fetches the source branch from the
knot that hosts it. For patch-based pulls, and as a fallback, it downloads the
latest round's patch and applies it with git am onto the target branch.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.branch, "branch", "", "local branch name (default: tgl/pr-<rkey>)")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return Errorf(CodeError, "usage: tgl pr checkout <ref>")
			}
			if !gitutil.InRepo(ctx, app.Dir) {
				return Errorf(CodeError, "tgl pr checkout must run inside a git repository")
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

			local := opts.branch
			if local == "" {
				local = "tgl/pr-" + p.RKey
			}

			if p.SourceBranch != "" {
				sourceRepoDID := p.SourceRepoDID
				if sourceRepoDID == "" {
					sourceRepoDID = p.TargetRepoDID
				}
				kc, err := app.knotFor(ctx, sourceRepoDID)
				if err != nil {
					return err
				}
				url := kc.CloneURL(sourceRepoDID)
				if _, err := gitutil.Run(ctx, app.Dir, "fetch", url, p.SourceBranch); err != nil {
					return Wrap(CodeError, err, "fetching %s from %s", p.SourceBranch, url)
				}
				if _, err := gitutil.Run(ctx, app.Dir, "checkout", "-B", local, "FETCH_HEAD"); err != nil {
					return Wrap(CodeError, err, "creating branch %s", local)
				}
				app.Print("checked out %s at %s (%s from %s)", local, p.SourceBranch, p.Shape, kc.Host)
				if app.JSON {
					return app.EmitJSON(map[string]string{
						"branch": local, "source": p.SourceBranch, "shape": p.Shape, "uri": p.URI, "rkey": p.RKey,
					})
				}
				return nil
			}

			// Patch-based: apply the latest round.
			patch, err := app.patchFor(ctx, &p)
			if err != nil {
				return err
			}
			if out, _ := gitutil.Run(ctx, app.Dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
				return Errorf(CodeError, "working tree is dirty; commit or stash before checking out a patch-based pull")
			}
			if _, err := gitutil.Run(ctx, app.Dir, "checkout", "-B", local, "FETCH_HEAD"); err != nil {
				// No FETCH_HEAD yet; branch from the target instead.
				if _, err2 := gitutil.Run(ctx, app.Dir, "checkout", "-B", local, "origin/"+p.TargetBranch); err2 != nil {
					if _, err3 := gitutil.Run(ctx, app.Dir, "checkout", "-B", local); err3 != nil {
						return Wrap(CodeError, err3, "creating branch %s", local)
					}
				}
			}
			f, err := os.CreateTemp("", "tgl-*.patch")
			if err != nil {
				return Wrap(CodeError, err, "creating a temporary patch file")
			}
			defer os.Remove(f.Name())
			if _, err := f.WriteString(patch); err != nil {
				f.Close()
				return Wrap(CodeError, err, "writing the patch")
			}
			f.Close()
			if _, err := gitutil.Run(ctx, app.Dir, "am", "--3way", f.Name()); err != nil {
				return Wrap(CodeError, err, "applying the patch (run `git am --abort` to reset)")
			}
			app.Print("checked out %s with %d round(s) applied", local, len(p.Rounds))
			if app.JSON {
				return app.EmitJSON(map[string]string{
					"branch": local, "shape": p.Shape, "uri": p.URI, "rkey": p.RKey,
				})
			}
			return nil
		},
	}
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// firstPatchSubject pulls a title out of a format-patch, matching what the
// appview does when the title field is left blank.
func firstPatchSubject(patch string) string {
	for _, line := range strings.Split(patch, "\n") {
		if s, ok := strings.CutPrefix(line, "Subject: "); ok {
			s = strings.TrimSpace(s)
			// Strip the [PATCH n/m] prefix git prepends.
			if strings.HasPrefix(s, "[") {
				if i := strings.Index(s, "]"); i >= 0 {
					s = strings.TrimSpace(s[i+1:])
				}
			}
			return s
		}
	}
	return ""
}

func readFileOrStdin(app *App, path string) (string, error) {
	if path == "-" {
		b, err := readAll(app.Stdin)
		if err != nil {
			return "", Wrap(CodeError, err, "reading stdin")
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", Wrap(CodeError, err, "reading %s", path)
	}
	return string(b), nil
}
