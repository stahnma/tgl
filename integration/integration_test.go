//go:build integration

// Package integration exercises tgl against a real Tangled account.
//
// These tests write real records to a real PDS. Run them with:
//
//	TANGLED_HANDLE=you.example TANGLED_APP_PASSWORD=xxxx-xxxx-xxxx-xxxx \
//	TGL_TEST_REPO=you.example/scratch \
//	go test -tags integration ./integration/... -v
//
// TGL_TEST_REPO must be a repo you own and do not mind polluting: the write
// tests create an issue, comment on it, close it, and reopen it. Nothing is
// deleted afterwards, because deleting is itself a write worth doing by hand.
//
// TGL_TEST_CREATE_REPO=1 additionally runs `repo create` against a real knot.
// That test is opt-in because it leaves a repository behind on the knot and a
// record on your PDS, and tgl has no delete command to undo it.
package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	if os.Getenv("TANGLED_HANDLE") == "" || os.Getenv("TANGLED_APP_PASSWORD") == "" {
		os.Stderr.WriteString("integration: TANGLED_HANDLE and TANGLED_APP_PASSWORD are not set; skipping\n")
		os.Exit(0)
	}
	tmp, err := os.MkdirTemp("", "tgl-integration-")
	if err != nil {
		panic(err)
	}
	binary = tmp + "/tgl"
	build := exec.Command("go", "build", "-o", binary, "../cmd/tgl")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// run invokes tgl and returns stdout, stderr and the exit code.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Env = os.Environ()
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running tgl %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding tgl output: %v\n%s", err, s)
	}
	return v
}

func testRepo(t *testing.T) string {
	t.Helper()
	r := os.Getenv("TGL_TEST_REPO")
	if r == "" {
		t.Skip("TGL_TEST_REPO is not set")
	}
	return r
}

// readRepo is a public repo used for read-only assertions. It defaults to the
// Tangled monorepo, which is large enough to exercise pagination.
func readRepo() string {
	if r := os.Getenv("TGL_READ_REPO"); r != "" {
		return r
	}
	return "tangled.org/core"
}

func TestAuthStatus(t *testing.T) {
	out, errOut, code := run(t, "auth", "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	if v["authenticated"] != true {
		t.Fatalf("not authenticated: %s", out)
	}
	if v["source"] != "environment" {
		t.Errorf("source = %v, want environment", v["source"])
	}
	if !strings.HasPrefix(v["did"].(string), "did:") {
		t.Errorf("did = %v", v["did"])
	}
}

func TestRepoResolution(t *testing.T) {
	out, errOut, code := run(t, "repo", "view", "--repo", readRepo(), "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	for _, k := range []string{"owner", "name", "rkey", "atUri", "repoDid", "knot", "knotEndpoint"} {
		if v[k] == nil || v[k] == "" {
			t.Errorf("resolved repo is missing %q: %s", k, out)
		}
	}
	if !strings.HasPrefix(v["repoDid"].(string), "did:") {
		t.Errorf("repoDid = %v", v["repoDid"])
	}
}

func TestIssueListAndView(t *testing.T) {
	out, errOut, code := run(t, "issue", "list", "--repo", readRepo(), "--limit", "5", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	issues, _ := v["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("no issues returned")
	}
	first := issues[0].(map[string]any)
	rkey, _ := first["rkey"].(string)
	if rkey == "" {
		t.Fatalf("issue has no rkey: %s", out)
	}

	// The rkey from the list must round-trip through view.
	out, errOut, code = run(t, "issue", "view", "--repo", readRepo(), rkey, "--json")
	if code != 0 {
		t.Fatalf("view exit %d: %s", code, errOut)
	}
	got := mustJSON(t, out)
	if got["rkey"] != rkey {
		t.Errorf("view returned rkey %v, want %v", got["rkey"], rkey)
	}
	if got["title"] != first["title"] {
		t.Errorf("title differs between list and view")
	}
}

func TestIssueStateFilters(t *testing.T) {
	for _, state := range []string{"open", "closed"} {
		out, errOut, code := run(t, "issue", "list", "--repo", readRepo(), "--state", state, "--limit", "5", "--no-labels", "--json")
		if code != 0 {
			t.Fatalf("state=%s exit %d: %s", state, code, errOut)
		}
		v := mustJSON(t, out)
		for _, raw := range v["issues"].([]any) {
			if got := raw.(map[string]any)["state"]; got != state {
				t.Errorf("--state %s returned an issue in state %v", state, got)
			}
		}
	}
}

func TestPullListAndView(t *testing.T) {
	out, errOut, code := run(t, "pr", "list", "--repo", readRepo(), "--limit", "5", "--no-labels", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	pulls, _ := v["pulls"].([]any)
	if len(pulls) == 0 {
		t.Fatal("no pulls returned")
	}
	for _, raw := range pulls {
		p := raw.(map[string]any)
		switch p["shape"] {
		case "patch", "branch", "fork":
		default:
			t.Errorf("pull %v has an unexpected shape %v", p["rkey"], p["shape"])
		}
	}
	rkey := pulls[0].(map[string]any)["rkey"].(string)
	out, errOut, code = run(t, "pr", "view", "--repo", readRepo(), rkey, "--json")
	if code != 0 {
		t.Fatalf("view exit %d: %s", code, errOut)
	}
	if mustJSON(t, out)["rkey"] != rkey {
		t.Error("pr view did not round-trip the rkey")
	}
}

// Downloading a patch exercises the blob path: locate the author's PDS, fetch
// the round blob with com.atproto.sync.getBlob, and gunzip it.
func TestPullPatchDownload(t *testing.T) {
	out, _, code := run(t, "pr", "list", "--repo", readRepo(), "--limit", "10", "--no-labels", "--json")
	if code != 0 {
		t.Skip("could not list pulls")
	}
	for _, raw := range mustJSON(t, out)["pulls"].([]any) {
		p := raw.(map[string]any)
		rounds, _ := p["rounds"].([]any)
		if len(rounds) == 0 {
			continue // versions-shaped pull; no blob exists
		}
		got, errOut, code := run(t, "pr", "view", "--repo", readRepo(), p["rkey"].(string), "--patch", "--comments=false", "--json")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		patch, _ := mustJSON(t, got)["patch"].(string)
		if !strings.Contains(patch, "diff --git") && !strings.HasPrefix(patch, "From ") {
			t.Errorf("downloaded patch does not look like a git patch:\n%.200s", patch)
		}
		return
	}
	t.Skip("no rounds-based pull found to download")
}

func TestNotFoundExitCode(t *testing.T) {
	_, _, code := run(t, "issue", "view", "--repo", readRepo(), "3zzzzzzzzzz22")
	if code != 8 {
		t.Errorf("exit %d for a missing rkey, want 8", code)
	}
	_, _, code = run(t, "issue", "list", "--repo", "tangled.org/definitely-not-a-repo")
	if code != 8 {
		t.Errorf("exit %d for a missing repo, want 8", code)
	}
}

func TestAuthFailureExitCode(t *testing.T) {
	cmd := exec.Command(binary, "issue", "create", "--repo", readRepo(), "--title", "should not be created")
	cmd.Env = append(os.Environ(), "TANGLED_HANDLE=", "TANGLED_APP_PASSWORD=", "TGL_CONFIG=/nonexistent/tgl.json")
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a failure, got %v", err)
	}
	if ee.ExitCode() != 4 {
		t.Errorf("exit %d for missing credentials, want 4", ee.ExitCode())
	}
}

// The full write path: create an issue, comment on it, close it, reopen it.
func TestIssueWriteLifecycle(t *testing.T) {
	repo := testRepo(t)
	title := "tgl integration test " + time.Now().UTC().Format(time.RFC3339)

	out, errOut, code := run(t, "issue", "create", "--repo", repo, "--title", title, "--body", "Created by tgl's integration suite.", "--json")
	if code != 0 {
		t.Fatalf("create exit %d: %s", code, errOut)
	}
	created := mustJSON(t, out)
	rkey, _ := created["rkey"].(string)
	if rkey == "" || !strings.HasPrefix(created["uri"].(string), "at://") {
		t.Fatalf("create returned %s", out)
	}
	t.Logf("created issue %s (%s)", rkey, created["uri"])

	// The appview ingests off the firehose, so the record needs a moment to
	// become visible through Bobbin.
	var view map[string]any
	for i := 0; i < 15; i++ {
		out, _, code = run(t, "issue", "view", "--repo", repo, rkey, "--json")
		if code == 0 {
			view = mustJSON(t, out)
			break
		}
		time.Sleep(2 * time.Second)
	}
	if view == nil {
		t.Fatalf("issue %s never became visible through the appview", rkey)
	}
	if view["title"] != title {
		t.Errorf("title = %v, want %q", view["title"], title)
	}
	if view["state"] != "open" {
		t.Errorf("new issue state = %v, want open", view["state"])
	}

	if _, errOut, code = run(t, "issue", "comment", "--repo", repo, rkey, "--body", "integration comment", "--json"); code != 0 {
		t.Fatalf("comment exit %d: %s", code, errOut)
	}
	if _, errOut, code = run(t, "issue", "close", "--repo", repo, rkey, "--json"); code != 0 {
		t.Fatalf("close exit %d: %s", code, errOut)
	}
	if _, errOut, code = run(t, "issue", "reopen", "--repo", repo, rkey, "--json"); code != 0 {
		t.Fatalf("reopen exit %d: %s", code, errOut)
	}
}

func TestRepoListIncludesTheReadRepo(t *testing.T) {
	owner, name, _ := strings.Cut(readRepo(), "/")

	out, errOut, code := run(t, "repo", "list", owner, "--limit", "0", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	repos, _ := v["repos"].([]any)
	if len(repos) == 0 {
		t.Fatalf("no repositories listed for %s: %s", owner, out)
	}
	var found bool
	for _, r := range repos {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == name {
			found = true
			// The name is the record's cosmetic name or its rkey; both must be
			// reported, since only the rkey addresses the record.
			if m["rkey"] == nil || m["atUri"] == nil {
				t.Errorf("listed repo %s is missing rkey or atUri: %v", name, m)
			}
		}
	}
	if !found {
		t.Errorf("%s does not appear in %s's repo list", name, owner)
	}
}

func TestRepoCloneHTTPS(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "repo", "clone", readRepo(), "--json")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, errb.String())
	}
	v := mustJSON(t, out.String())
	if v["protocol"] != "https" {
		t.Errorf("protocol = %v, want https", v["protocol"])
	}
	target, _ := v["directory"].(string)
	if target == "" {
		t.Fatalf("clone reported no directory: %s", out.String())
	}
	if _, err := os.Stat(target + "/.git"); err != nil {
		t.Errorf("cloned directory has no .git: %v", err)
	}
}

func TestBrowseBuildsURLsWithoutOpening(t *testing.T) {
	out, errOut, code := run(t, "browse", "issue", "405", "--repo", readRepo(), "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	if !strings.HasSuffix(v["url"].(string), "/issues/405") {
		t.Errorf("url = %v", v["url"])
	}
	if v["opened"] != false {
		t.Error("browse opened a browser in JSON mode")
	}

	// An rkey has no web page and no way back to a number; that must fail
	// rather than point somewhere plausible.
	if _, _, code = run(t, "browse", "issue", "3ms4qpm3qjd22", "--repo", readRepo(), "--json"); code == 0 {
		t.Error("browse accepted an rkey")
	}
}

// TestRepoCreate leaves a repository behind; it runs only with
// TGL_TEST_CREATE_REPO=1 set, and prints what you then need to delete by hand.
func TestRepoCreate(t *testing.T) {
	if os.Getenv("TGL_TEST_CREATE_REPO") != "1" {
		t.Skip("TGL_TEST_CREATE_REPO is not 1; this test leaves a repository behind")
	}
	name := "tgl-integration-" + strings.ToLower(time.Now().UTC().Format("20060102t150405"))

	out, errOut, code := run(t, "repo", "create", name, "--description", "Created by tgl's integration suite.", "--json")
	if code != 0 {
		t.Fatalf("create exit %d: %s", code, errOut)
	}
	v := mustJSON(t, out)
	repoDID, _ := v["repoDid"].(string)
	if !strings.HasPrefix(repoDID, "did:") {
		t.Fatalf("create returned no repo DID: %s", out)
	}
	if v["rkey"] != name {
		t.Errorf("rkey = %v, want %q (the lowercased name)", v["rkey"], name)
	}
	t.Logf("created %s (%s); delete it by hand", name, repoDID)

	// Resolution is the real assertion: the record, the repo DID and the knot
	// must line up well enough for every other command to address the repo.
	handle := os.Getenv("TANGLED_HANDLE")
	for i := 0; i < 15; i++ {
		out, _, code = run(t, "repo", "view", "--repo", handle+"/"+name, "--json")
		if code == 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if code != 0 {
		t.Fatalf("the new repository never resolved: %s", errOut)
	}
	view := mustJSON(t, out)
	if view["repoDid"] != repoDID {
		t.Errorf("resolved repoDid = %v, want %v", view["repoDid"], repoDID)
	}

	// Creating the same name again must be refused rather than overwrite the
	// record that now exists at that rkey.
	if _, _, code = run(t, "repo", "create", name, "--json"); code == 0 {
		t.Error("a second create with the same name succeeded; it should have been refused")
	}
}
