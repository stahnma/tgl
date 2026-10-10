package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"tangled.org/tgl/internal/atproto"
	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/identity"
	"tangled.org/tgl/internal/knot"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/secret"
)

// Version is set at build time.
var Version = "dev"

// App carries global flags and lazily-built clients.
type App struct {
	// Global flags.
	JSON       bool
	RepoSpec   string
	RemoteName string
	BobbinHost string
	Appview    string
	Dir        string

	Out    io.Writer
	ErrOut io.Writer
	Stdin  io.Reader

	once struct {
		ids    sync.Once
		bob    sync.Once
		res    sync.Once
		repo   sync.Once
		client sync.Once
	}
	ids     *identity.Resolver
	bob     *bobbin.Client
	res     *resolve.Resolver
	repo    *resolve.Repo
	repoErr error

	session    *Session
	sessionErr error
}

// NewApp builds an App bound to the process streams.
func NewApp() *App {
	dir, _ := os.Getwd()
	return &App{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		Stdin:  os.Stdin,
		Dir:    dir,
	}
}

func (a *App) registerGlobals(fs *flag.FlagSet) {
	fs.BoolVar(&a.JSON, "json", a.JSON, "emit machine-readable JSON on stdout")
	fs.StringVar(&a.RepoSpec, "repo", a.RepoSpec, "repository as owner/name, overriding the git remote")
	fs.StringVar(&a.RemoteName, "remote", a.RemoteName, "git remote to read repo context from (default: origin)")
	fs.StringVar(&a.BobbinHost, "bobbin", a.BobbinHost, "Bobbin appview base URL")
	fs.StringVar(&a.Appview, "appview", a.Appview, "web appview base URL, used only to resolve sequential #numbers")
}

// IDs returns the identity resolver.
func (a *App) IDs() *identity.Resolver {
	a.once.ids.Do(func() { a.ids = identity.NewResolver() })
	return a.ids
}

// Bobbin returns the read client.
func (a *App) Bobbin() *bobbin.Client {
	a.once.bob.Do(func() {
		host := a.BobbinHost
		if host == "" {
			host = os.Getenv("TANGLED_BOBBIN_HOST")
		}
		a.bob = bobbin.New(host)
	})
	return a.bob
}

// Resolver returns the repo resolver.
func (a *App) Resolver() *resolve.Resolver {
	a.once.res.Do(func() { a.res = resolve.New(a.Bobbin(), a.IDs()) })
	return a.res
}

// Repo resolves repo context once per process: --repo when given, else the git
// remote in the working directory.
func (a *App) Repo(ctx context.Context) (*resolve.Repo, error) {
	a.once.repo.Do(func() {
		if spec := a.repoSpec(); spec != "" {
			a.repo, a.repoErr = a.Resolver().FromSpec(ctx, spec)
			return
		}
		a.repo, a.repoErr = a.Resolver().FromWorkingDir(ctx, a.Dir, a.RemoteName)
	})
	if a.repoErr != nil {
		return nil, Wrap(CodeNotFound, a.repoErr, "could not determine repository")
	}
	return a.repo, nil
}

func (a *App) repoSpec() string {
	if a.RepoSpec != "" {
		return a.RepoSpec
	}
	return os.Getenv("TANGLED_REPO")
}

// Knot returns a client for the repo's knot.
func (a *App) Knot(repo *resolve.Repo) (*knot.Client, error) {
	if repo.KnotEndpoint == "" {
		return nil, Errorf(CodeError, "repo %s declares no knot endpoint", repo.Slug())
	}
	return knot.New(repo.KnotEndpoint), nil
}

// --- output ---

// Print writes a line to stdout. It is a no-op in JSON mode so that stdout stays
// a single valid JSON document.
func (a *App) Print(format string, args ...any) {
	if a.JSON {
		return
	}
	fmt.Fprintf(a.Out, format+"\n", args...)
}

// Note writes an advisory line to stderr. Always emitted, JSON mode or not, so
// that machine consumers keep a clean stdout while humans still see warnings.
func (a *App) Note(format string, args ...any) {
	fmt.Fprintf(a.ErrOut, format+"\n", args...)
}

// EmitJSON writes v to stdout as indented JSON.
func (a *App) EmitJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Interactive reports whether stdin is a terminal, which is the only condition
// under which tgl may prompt.
func (a *App) Interactive() bool {
	f, ok := a.Stdin.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// OutIsTerminal reports whether stdout is a terminal. Commands that would
// otherwise launch something — a browser — use this so that a redirected or
// piped run stays inert.
func (a *App) OutIsTerminal() bool {
	f, ok := a.Out.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// --- auth ---

// Session is an authenticated account bound to its PDS.
type Session struct {
	DID    string
	Handle string
	PDS    string
	Client *atproto.Client
}

// Auth returns an authenticated session, or an exit-4 error.
//
// Credentials come from TANGLED_HANDLE/TANGLED_APP_PASSWORD when both are set —
// the path CI and agents use — and otherwise from the OS keychain, whose stored
// refresh token is exchanged for a fresh access token.
func (a *App) Auth(ctx context.Context) (*Session, error) {
	a.once.client.Do(func() { a.session, a.sessionErr = a.authenticate(ctx) })
	if a.sessionErr != nil {
		return nil, a.sessionErr
	}
	return a.session, nil
}

func (a *App) authenticate(ctx context.Context) (*Session, error) {
	handle := strings.TrimSpace(os.Getenv("TANGLED_HANDLE"))
	password := os.Getenv("TANGLED_APP_PASSWORD")
	if handle != "" && password != "" {
		return a.loginWithPassword(ctx, handle, password)
	}
	if handle != "" || password != "" {
		return nil, AuthErrorf("TANGLED_HANDLE and TANGLED_APP_PASSWORD must both be set, or neither")
	}

	cred, err := secret.Load("")
	if err != nil {
		if err == secret.ErrNotFound {
			return nil, AuthErrorf("not authenticated: run `tgl auth login`, or set TANGLED_HANDLE and TANGLED_APP_PASSWORD")
		}
		return nil, Wrap(CodeAuth, err, "reading stored credential")
	}

	client := atproto.New(cred.PDS)
	sess, err := client.RefreshSession(ctx, cred.RefreshJwt)
	if err != nil {
		return nil, Wrap(CodeAuth, err, "stored session is no longer valid; run `tgl auth login` again")
	}
	// refreshSession rotates the refresh token, so persist the new one.
	cred.RefreshJwt = sess.RefreshJwt
	cred.Handle = sess.Handle
	if err := secret.Store(cred); err != nil {
		a.Note("warning: could not update stored credential: %v", err)
	}
	return &Session{DID: sess.DID, Handle: sess.Handle, PDS: cred.PDS, Client: client}, nil
}

// LoginWithPassword authenticates and, when persist is true, stores the refresh
// token in the OS keychain.
func (a *App) LoginWithPassword(ctx context.Context, handle, password string, persist bool) (*Session, error) {
	sess, err := a.loginWithPassword(ctx, handle, password)
	if err != nil {
		return nil, err
	}
	if persist {
		cred := &secret.Credential{
			Handle:     sess.Handle,
			DID:        sess.DID,
			PDS:        sess.PDS,
			RefreshJwt: sess.Client.RefreshJwt,
		}
		if err := secret.Store(cred); err != nil {
			return nil, Wrap(CodeError, err, "storing credential")
		}
	}
	return sess, nil
}

func (a *App) loginWithPassword(ctx context.Context, handle, password string) (*Session, error) {
	did, pds, err := a.IDs().PDSFor(ctx, handle)
	if err != nil {
		return nil, Wrap(CodeAuth, err, "discovering PDS for %s", handle)
	}
	client := atproto.New(pds)
	sess, err := client.CreateSession(ctx, handle, password)
	if err != nil {
		return nil, Wrap(CodeAuth, err, "login failed for %s", handle)
	}
	if sess.DID != did {
		// Handle resolution and the PDS disagree; trust the PDS.
		did = sess.DID
	}
	client.RefreshJwt = sess.RefreshJwt
	return &Session{DID: did, Handle: sess.Handle, PDS: pds, Client: client}, nil
}

func osGetenv(k string) string { return os.Getenv(k) }
