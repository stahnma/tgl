package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/secret"
)

func authCmd() *Command {
	return &Command{
		Name:  "auth",
		Short: "Authenticate against your PDS",
		Sub:   []*Command{authLoginCmd(), authStatusCmd(), authLogoutCmd()},
	}
}

func authLoginCmd() *Command {
	var opts struct {
		handle        string
		passwordStdin bool
	}
	return &Command{
		Name:  "login",
		Short: "Log in with an app password",
		Long: `The password must be an app password -- a separate, revocable
credential -- and never your account password. Create one where your account
lives: https://bsky.app/settings/app-passwords for a bsky.social account, or your
own PDS's settings if you are on tngl.sh or self-hosting.

The refresh token is stored in the OS keychain; nothing is written to a dotfile.
For CI and agents, skip login entirely and set TANGLED_HANDLE and
TANGLED_APP_PASSWORD instead.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.handle, "handle", "", "account handle, e.g. alice.bsky.social")
			fs.BoolVar(&opts.passwordStdin, "password-stdin", false, "read the app password from stdin")
		},
		Run: func(ctx context.Context, app *App, args []string) error {
			handle := opts.handle
			if handle == "" && len(args) > 0 {
				handle = args[0]
			}
			if handle == "" {
				handle = strings.TrimSpace(os.Getenv("TANGLED_HANDLE"))
			}
			if handle == "" {
				if !app.Interactive() {
					return Errorf(CodeError, "--handle is required when stdin is not a terminal")
				}
				var err error
				handle, err = prompt(app, "Handle: ")
				if err != nil {
					return err
				}
			}

			password, err := readPassword(app, opts.passwordStdin)
			if err != nil {
				return err
			}

			sess, err := app.LoginWithPassword(ctx, handle, password, true)
			if err != nil {
				return err
			}
			out := &render.AuthStatus{
				Authenticated: true,
				DID:           sess.DID,
				Handle:        sess.Handle,
				PDS:           sess.PDS,
				Source:        "keychain",
			}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteAuthStatus(app.Out, out)
			return nil
		},
	}
}

func authStatusCmd() *Command {
	return &Command{
		Name:  "status",
		Short: "Show the active account",
		Run: func(ctx context.Context, app *App, args []string) error {
			source := "keychain"
			if os.Getenv("TANGLED_HANDLE") != "" && os.Getenv("TANGLED_APP_PASSWORD") != "" {
				source = "environment"
			}
			sess, err := app.Auth(ctx)
			if err != nil {
				out := &render.AuthStatus{Authenticated: false}
				if app.JSON {
					if jerr := app.EmitJSON(out); jerr != nil {
						return jerr
					}
					return err
				}
				render.WriteAuthStatus(app.Out, out)
				return err
			}
			out := &render.AuthStatus{
				Authenticated: true,
				DID:           sess.DID,
				Handle:        sess.Handle,
				PDS:           sess.PDS,
				Source:        source,
			}
			if app.JSON {
				return app.EmitJSON(out)
			}
			render.WriteAuthStatus(app.Out, out)
			return nil
		},
	}
}

func authLogoutCmd() *Command {
	return &Command{
		Name:  "logout",
		Short: "Remove the stored credential from the OS keychain",
		Run: func(ctx context.Context, app *App, args []string) error {
			if err := secret.Delete(""); err != nil {
				if err == secret.ErrNotFound {
					app.Print("not logged in")
					if app.JSON {
						return app.EmitJSON(&render.AuthStatus{Authenticated: false})
					}
					return nil
				}
				return Wrap(CodeError, err, "removing credential")
			}
			if app.JSON {
				return app.EmitJSON(&render.AuthStatus{Authenticated: false})
			}
			app.Print("logged out")
			return nil
		},
	}
}

func readPassword(app *App, fromStdin bool) (string, error) {
	if p := os.Getenv("TANGLED_APP_PASSWORD"); p != "" && !fromStdin {
		return p, nil
	}
	if fromStdin {
		b, err := io.ReadAll(app.Stdin)
		if err != nil {
			return "", Wrap(CodeError, err, "reading password from stdin")
		}
		p := strings.TrimRight(string(b), "\r\n")
		if p == "" {
			return "", Errorf(CodeError, "no password on stdin")
		}
		return p, nil
	}
	if !app.Interactive() {
		return "", Errorf(CodeError,
			"no app password available: pass --password-stdin, or set TANGLED_APP_PASSWORD")
	}
	fmt.Fprint(app.ErrOut, "App password: ")
	f, _ := app.Stdin.(*os.File)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(app.ErrOut)
	if err != nil {
		return "", Wrap(CodeError, err, "reading password")
	}
	p := strings.TrimSpace(string(b))
	if p == "" {
		return "", Errorf(CodeError, "empty password")
	}
	return p, nil
}

func prompt(app *App, label string) (string, error) {
	fmt.Fprint(app.ErrOut, label)
	r := bufio.NewReader(app.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", Wrap(CodeError, err, "reading input")
	}
	s := strings.TrimSpace(line)
	if s == "" {
		return "", Errorf(CodeError, "empty input")
	}
	return s, nil
}
