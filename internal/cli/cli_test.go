package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/resolve"
)

func TestSplitArgsPermutesFlagsAfterOperands(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Bool("json", false, "")
		fs.Bool("patch", false, "")
		fs.String("repo", "", "")
		fs.Int("limit", 0, "")
		return fs
	}
	tests := []struct {
		name     string
		argv     []string
		flags    []string
		operands []string
	}{
		{
			// The stdlib flag package would stop at "3ms4..." and silently drop
			// --patch; this is the case that motivated splitArgs.
			name:     "flags after operand",
			argv:     []string{"--repo", "a/b", "3ms4qpm3qjd22", "--patch"},
			flags:    []string{"--repo", "a/b", "--patch"},
			operands: []string{"3ms4qpm3qjd22"},
		},
		{
			name:     "value flag consumes its value",
			argv:     []string{"--limit", "5", "ref"},
			flags:    []string{"--limit", "5"},
			operands: []string{"ref"},
		},
		{
			name:     "equals form is self contained",
			argv:     []string{"--limit=5", "ref", "--json"},
			flags:    []string{"--limit=5", "--json"},
			operands: []string{"ref"},
		},
		{
			name:     "bool flag does not eat the next token",
			argv:     []string{"--json", "ref"},
			flags:    []string{"--json"},
			operands: []string{"ref"},
		},
		{
			name:     "double dash ends flag parsing",
			argv:     []string{"--json", "--", "--not-a-flag"},
			flags:    []string{"--json"},
			operands: []string{"--not-a-flag"},
		},
		{
			name:     "no flags",
			argv:     []string{"ref"},
			flags:    nil,
			operands: []string{"ref"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags, operands := splitArgs(newFS(), tc.argv)
			if !eq(flags, tc.flags) {
				t.Errorf("flags = %q, want %q", flags, tc.flags)
			}
			if !eq(operands, tc.operands) {
				t.Errorf("operands = %q, want %q", operands, tc.operands)
			}
		})
	}
}

func TestSplitBranchRejectsUnknownLeadingFlag(t *testing.T) {
	var app App
	fs := flag.NewFlagSet("g", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.registerGlobals(fs)

	if _, name, _, err := splitBranch(fs, []string{"--json", "issue", "list"}); err != nil || name != "issue" {
		t.Fatalf("splitBranch = %q, %v; want issue, nil", name, err)
	}
	// A leaf flag before the subcommand has unknown arity, so guessing would
	// mis-split the argv; reject it instead.
	if _, _, _, err := splitBranch(fs, []string{"--limit", "5", "issue"}); err == nil {
		t.Error("splitBranch should reject an unknown flag before a subcommand")
	}
}

func TestDispatchGlobalFlagBeforeSubcommand(t *testing.T) {
	var ran bool
	root := &Command{Name: "tgl", Sub: []*Command{{
		Name: "sub",
		Run: func(ctx context.Context, app *App, args []string) error {
			ran = true
			if !app.JSON {
				t.Error("--json before the subcommand was not applied")
			}
			if app.RepoSpec != "a/b" {
				t.Errorf("--repo = %q, want a/b", app.RepoSpec)
			}
			if !eq(args, []string{"pos"}) {
				t.Errorf("args = %q, want [pos]", args)
			}
			return nil
		},
	}}}
	app := &App{Out: io.Discard, ErrOut: io.Discard}
	if err := Dispatch(context.Background(), app, root, []string{"--json", "--repo", "a/b", "sub", "pos"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !ran {
		t.Fatal("subcommand did not run")
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	root := &Command{Name: "tgl", Sub: []*Command{{Name: "sub", Run: func(context.Context, *App, []string) error { return nil }}}}
	app := &App{Out: io.Discard, ErrOut: io.Discard}
	err := Dispatch(context.Background(), app, root, []string{"nope"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if CodeOf(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeOf(err), CodeError)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, CodeOK},
		{errors.New("plain"), CodeError},
		{Errorf(CodeError, "x"), CodeError},
		{AuthErrorf("nope"), CodeAuth},
		{NotFoundf("gone"), CodeNotFound},
		{Wrap(CodeNotFound, errors.New("inner"), "outer"), CodeNotFound},
	}
	for _, tc := range tests {
		if got := CodeOf(tc.err); got != tc.want {
			t.Errorf("CodeOf(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestErrorWrapping(t *testing.T) {
	inner := errors.New("boom")
	err := Wrap(CodeAuth, inner, "while doing %s", "thing")
	if !errors.Is(err, inner) {
		t.Error("Wrap should preserve the cause for errors.Is")
	}
	if !strings.Contains(err.Error(), "while doing thing") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("message = %q", err.Error())
	}
}

// resolveNumber must read the record identity off the appview page rather than
// reconstructing an ordinal, which is provably wrong on real repositories.
func TestResolveNumberFromAppviewPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "appview_issue_405.html"
		if strings.HasSuffix(r.URL.Path, "/issues/1") {
			name = "appview_issue_404.html"
			w.WriteHeader(http.StatusOK) // the appview serves its 404 page with a 200
		}
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			t.Errorf("fixture: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(b)
	}))
	defer srv.Close()

	app := &App{Out: io.Discard, ErrOut: io.Discard, Appview: srv.URL}
	repo := &resolve.Repo{OwnerHandle: "tangled.org", OwnerDID: "did:plc:o", Name: "core"}

	uri, err := app.resolveNumber(context.Background(), repo, kindIssue, 405)
	if err != nil {
		t.Fatalf("resolveNumber: %v", err)
	}
	const want = "at://did:plc:xasnlahkri4ewmbuzly2rlc5/sh.tangled.repo.issue/3mdy4uko2vk22"
	if uri != want {
		t.Errorf("resolveNumber(405) = %q, want %q", uri, want)
	}

	// A number with no record behind it is exit 8, not a nearby guess.
	_, err = app.resolveNumber(context.Background(), repo, kindIssue, 1)
	if err == nil {
		t.Fatal("expected a not-found error for a deleted number")
	}
	if CodeOf(err) != CodeNotFound {
		t.Errorf("exit code = %d, want %d", CodeOf(err), CodeNotFound)
	}
}

func TestNumberFromURL(t *testing.T) {
	tests := map[string]int{
		"https://tangled.org/@tangled.org/core/issues/405": 405,
		"https://tangled.org/tangled.org/core/pulls/900":   900,
		"https://tangled.org/a/b/issues/405/":              405,
	}
	for raw, want := range tests {
		got, ok := numberFromURL(raw)
		if !ok || got != want {
			t.Errorf("numberFromURL(%q) = %d, %v; want %d, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"405", "3ms4qpm3qjd22", "https://tangled.org/a/b"} {
		if _, ok := numberFromURL(raw); ok {
			t.Errorf("numberFromURL(%q) should not match", raw)
		}
	}
}

func TestNormalizeState(t *testing.T) {
	for _, s := range []string{"", "all", "ALL"} {
		got, err := normalizeState(s, "open", "closed")
		if err != nil || got != "" {
			t.Errorf("normalizeState(%q) = %q, %v; want \"\", nil", s, got, err)
		}
	}
	if got, err := normalizeState("Closed", "open", "closed"); err != nil || got != "closed" {
		t.Errorf("normalizeState(Closed) = %q, %v", got, err)
	}
	if _, err := normalizeState("bogus", "open", "closed"); err == nil {
		t.Error("normalizeState should reject an unknown state")
	}
}

func TestMatchesLabel(t *testing.T) {
	ls := []render.Label{
		{Key: "at://did:plc:o/sh.tangled.label.definition/bug", Name: "bug"},
		{Key: "at://did:plc:o/sh.tangled.label.definition/assignee", Name: "assignee", Value: "did:plc:x"},
	}
	for _, f := range []string{"bug", "BUG", "assignee:did:plc:x", "at://did:plc:o/sh.tangled.label.definition/bug", ""} {
		if !matchesLabel(ls, f) {
			t.Errorf("matchesLabel(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"wontfix", "assignee:someone-else"} {
		if matchesLabel(ls, f) {
			t.Errorf("matchesLabel(%q) = true, want false", f)
		}
	}
}

func TestJSONModeKeepsStdoutClean(t *testing.T) {
	var out, errOut bytes.Buffer
	app := &App{Out: &out, ErrOut: &errOut, JSON: true}
	app.Print("this must not appear on stdout")
	app.Note("this belongs on stderr")
	if out.Len() != 0 {
		t.Errorf("stdout polluted in JSON mode: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "stderr") {
		t.Errorf("Note did not reach stderr: %q", errOut.String())
	}
}

func TestFirstPatchSubject(t *testing.T) {
	patch := "From abc Mon Sep 17 00:00:00 2001\nFrom: a <a@b>\nSubject: [PATCH 1/2] make it work\n\nbody\n"
	if got := firstPatchSubject(patch); got != "make it work" {
		t.Errorf("firstPatchSubject = %q, want %q", got, "make it work")
	}
	if got := firstPatchSubject("no subject here"); got != "" {
		t.Errorf("firstPatchSubject = %q, want empty", got)
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCloneURLForms(t *testing.T) {
	app := &App{Out: io.Discard, ErrOut: io.Discard}
	repo := &resolve.Repo{
		OwnerHandle: "onev.cat",
		OwnerDID:    "did:plc:owner",
		Name:        "tang",
		RepoDID:     "did:plc:repo",
		Knot:        "knot1.tangled.sh",
	}
	tests := []struct {
		protocol  string
		permalink bool
		want      string
	}{
		// knot1 is fronted by tangled.org for SSH, and every knot's git-over-
		// HTTPS is proxied by the appview.
		{"https", false, "https://tangled.org/onev.cat/tang"},
		{"https", true, "https://tangled.org/did:plc:repo"},
		{"ssh", false, "git@tangled.org:onev.cat/tang"},
		{"ssh", true, "git@tangled.org:did:plc:repo"},
	}
	for _, tc := range tests {
		got, err := app.cloneURL(repo, tc.protocol, tc.permalink)
		if err != nil || got != tc.want {
			t.Errorf("cloneURL(%s, permalink=%v) = %q, %v; want %q", tc.protocol, tc.permalink, got, err, tc.want)
		}
	}

	// A self-hosted knot is addressed directly over SSH, port stripped.
	self := &resolve.Repo{OwnerHandle: "a.example", Name: "r", RepoDID: "did:plc:r", Knot: "knot.example:8080"}
	if got, err := app.cloneURL(self, "ssh", false); err != nil || got != "git@knot.example:a.example/r" {
		t.Errorf("cloneURL(self-hosted ssh) = %q, %v", got, err)
	}

	// No handle and no repo DID leaves nothing addressable; that is an error,
	// not a guessed URL.
	bare := &resolve.Repo{OwnerDID: "did:plc:owner", Name: "old", Knot: "knot1.tangled.sh"}
	if _, err := app.cloneURL(bare, "https", false); err == nil {
		t.Error("cloneURL should fail without a handle or a repoDid")
	}
	// --permalink on a pre-v1.13 repo has no DID to use either.
	noDID := &resolve.Repo{OwnerHandle: "a.example", Name: "old", Knot: "knot1.tangled.sh"}
	if _, err := app.cloneURL(noDID, "https", true); err == nil {
		t.Error("cloneURL --permalink should fail without a repoDid")
	}
}

func TestBrowseURL(t *testing.T) {
	app := &App{Out: io.Discard, ErrOut: io.Discard}
	repo := &resolve.Repo{OwnerHandle: "tangled.org", OwnerDID: "did:plc:o", Name: "core"}
	tests := []struct {
		args []string
		want string
	}{
		{nil, "https://tangled.org/tangled.org/core"},
		{[]string{"issues"}, "https://tangled.org/tangled.org/core/issues"},
		{[]string{"pulls"}, "https://tangled.org/tangled.org/core/pulls"},
		{[]string{"issue", "405"}, "https://tangled.org/tangled.org/core/issues/405"},
		{[]string{"issue", "#405"}, "https://tangled.org/tangled.org/core/issues/405"},
		{[]string{"pr", "https://tangled.org/tangled.org/core/pulls/900"}, "https://tangled.org/tangled.org/core/pulls/900"},
	}
	for _, tc := range tests {
		got, err := app.browseURL(repo, tc.args)
		if err != nil || got != tc.want {
			t.Errorf("browseURL(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}

	// The web UI has no rkey-addressed page and the number cannot be derived,
	// so an rkey must fail loudly rather than resolve to something plausible.
	if _, err := app.browseURL(repo, []string{"issue", "3ms4qpm3qjd22"}); err == nil {
		t.Error("browseURL should refuse an rkey")
	}
	if _, err := app.browseURL(repo, []string{"wiki"}); err == nil {
		t.Error("browseURL should reject an unknown target")
	}
}

func TestValidateRepoName(t *testing.T) {
	for _, ok := range []string{"tgl", "my-tool", "my_tool", "v1.2", "A9"} {
		if err := validateRepoName(ok); err != nil {
			t.Errorf("validateRepoName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "a/b", `a\b`, ".hidden", "trailing.", "a..b", "spaced name", strings.Repeat("x", 101)} {
		if err := validateRepoName(bad); err == nil {
			t.Errorf("validateRepoName(%q) = nil, want an error", bad)
		}
	}
}

func TestRepoSummariesFromFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "listRepos_owner.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var page struct {
		Items []bobbin.ListItem `json:"items"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}

	var warnings int
	repos := repoSummaries(page.Items, func(string, ...any) { warnings++ })
	if len(repos) == 0 {
		t.Fatal("no repos decoded from the fixture")
	}
	if warnings != 0 {
		t.Errorf("%d warnings decoding a known-good fixture", warnings)
	}
	for i := 1; i < len(repos); i++ {
		if repos[i-1].CreatedAt < repos[i].CreatedAt {
			t.Fatalf("repos are not newest first: %q before %q", repos[i-1].CreatedAt, repos[i].CreatedAt)
		}
	}
	byRKey := map[string]render.RepoSummary{}
	for _, r := range repos {
		byRKey[r.RKey] = r
	}
	// A record with a cosmetic name is shown under it...
	if got := byRKey["3lzg6enurmo22"].Name; got != "test" {
		t.Errorf("name for rkey 3lzg6enurmo22 = %q, want %q", got, "test")
	}
	// ...and one without falls back to the rkey, which is what the web UI does.
	for _, r := range repos {
		if r.Name == "" {
			t.Errorf("repo %s has no name and no rkey fallback", r.AtURI)
		}
	}
}

func TestRepoSummariesSkipsMalformed(t *testing.T) {
	items := []bobbin.ListItem{
		{URI: "at://did:plc:o/sh.tangled.repo/good", Value: []byte(`{"knot":"k","createdAt":"2025-01-01T00:00:00Z"}`)},
		{URI: "at://did:plc:o/sh.tangled.repo/bad", Value: []byte(`not json`)},
		{URI: "nonsense", Value: []byte(`{"knot":"k","createdAt":"2025-01-01T00:00:00Z"}`)},
	}
	var warnings int
	repos := repoSummaries(items, func(string, ...any) { warnings++ })
	if len(repos) != 1 || repos[0].RKey != "good" {
		t.Fatalf("repos = %+v, want just the well-formed one", repos)
	}
	if warnings != 2 {
		t.Errorf("warnings = %d, want 2", warnings)
	}
}
