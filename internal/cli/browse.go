package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/tid"
)

func browseCmd() *Command {
	var opts struct{ noOpen bool }
	return &Command{
		Name:  "browse",
		Args:  "[issues|pulls|issue <number>|pr <number>]",
		Short: "Open a repository page in a browser",
		Long: `With no argument, browse opens the repository page. The URL is always
printed on stdout; the browser is only launched when stdout is going to a
terminal and --json is off, so scripts and CI get a URL rather than a browser.

issue and pr take the sequential number the web UI shows, because that is what
its URLs are keyed on (core:appview/middleware/middleware.go:428-436). There is
no rkey-addressed page and no way to map an rkey back to a number, so an rkey is
refused here — use ` + "`tgl issue view <rkey>`" + ` to read the record instead.`,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&opts.noOpen, "no-open", false, "print the URL without launching a browser")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			repo, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			url, err := app.browseURL(repo, args)
			if err != nil {
				return err
			}

			out := &render.Browsed{URL: url}
			if !opts.noOpen && !app.JSON && app.OutIsTerminal() {
				if err := openInBrowser(ctx, url); err != nil {
					app.Note("warning: could not open a browser: %v", err)
				} else {
					out.Opened = true
				}
			}
			if app.JSON {
				return app.EmitJSON(out)
			}
			fmt.Fprintln(app.Out, url)
			return nil
		},
	}
}

// browseURL maps the browse arguments onto an appview URL.
func (a *App) browseURL(repo *resolve.Repo, args []string) (string, error) {
	base := a.repoWebURL(repo)
	if len(args) == 0 {
		return base, nil
	}
	target := strings.ToLower(args[0])
	rest := args[1:]

	switch target {
	case "issues":
		return base + "/issues", nil
	case "pulls", "prs":
		return base + "/pulls", nil
	case "issue", "pr", "pull":
		k := kindIssue
		if target != "issue" {
			k = kindPull
		}
		if len(rest) == 0 {
			return base + "/" + k.urlSegment(), nil
		}
		n, err := browseNumber(k, rest[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s/%s/%d", base, k.urlSegment(), n), nil
	default:
		return "", Errorf(CodeError,
			"unknown browse target %q: expected issues, pulls, issue <number> or pr <number>", args[0])
	}
}

// browseNumber accepts the forms a web page can be addressed by: a number, #42,
// or a URL ending in one.
func browseNumber(k kind, raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if n, ok := numberFromURL(raw); ok {
		return n, nil
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(raw, "#")); err == nil && n > 0 {
		return n, nil
	}
	if tid.Valid(raw) {
		return 0, Errorf(CodeError,
			"browse needs the appview's %s number, and %s is an rkey. The number is allocated by the appview and appears in no record, so tgl cannot map an rkey to one; run `tgl %s view %s` to read the record.",
			k.noun(), raw, browseViewCommand(k), raw)
	}
	return 0, Errorf(CodeError, "unrecognised %s number %q", k.noun(), raw)
}

func browseViewCommand(k kind) string {
	if k == kindPull {
		return "pr"
	}
	return "issue"
}

// repoWebURL is the appview page for a repository. The appview accepts both a
// handle and a DID in the owner position, so a repo whose handle did not resolve
// is still addressable.
func (a *App) repoWebURL(repo *resolve.Repo) string {
	owner := repo.OwnerHandle
	if owner == "" {
		owner = repo.OwnerDID
	}
	return a.appviewBase() + "/" + owner + "/" + repo.Name
}

// openInBrowser hands a URL to the platform opener, or to $BROWSER when set.
func openInBrowser(ctx context.Context, url string) error {
	if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" {
		return exec.CommandContext(ctx, b, url).Start()
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", url).Start()
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.CommandContext(ctx, "xdg-open", url).Start()
	}
}
