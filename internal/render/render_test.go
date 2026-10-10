package render_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/tangled"
)

type page struct {
	Items  []bobbin.ListItem `json:"items"`
	Cursor string            `json:"cursor"`
}

func loadPage(t *testing.T, name string) page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var p page
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
	if len(p.Items) == 0 {
		t.Fatalf("fixture %s has no items", name)
	}
	return p
}

// handles stands in for DID document lookups.
func handles(m map[string]string) render.ActorFunc {
	return func(did string) render.Actor { return render.Actor{DID: did, Handle: m[did]} }
}

func TestBuildIssueFromFixture(t *testing.T) {
	p := loadPage(t, "listIssues.json")
	af := handles(map[string]string{"did:plc:btfblehri2iavuzjxahtfctk": "wangleline.com"})

	is, err := render.BuildIssue(p.Items[0], af)
	if err != nil {
		t.Fatalf("BuildIssue: %v", err)
	}
	if is.RKey == "" || !strings.HasPrefix(is.URI, "at://") {
		t.Errorf("issue identity not populated: rkey=%q uri=%q", is.RKey, is.URI)
	}
	if is.Title == "" {
		t.Error("title is empty")
	}
	if is.State != "open" {
		t.Errorf("state = %q, want open (the fixture was captured with state=open)", is.State)
	}
	// The record's repo field is a repo DID, not an AT-URI.
	if !strings.HasPrefix(is.RepoDID, "did:") {
		t.Errorf("repoDid = %q, want a DID", is.RepoDID)
	}
	if is.Author.DID == "" {
		t.Error("author DID not derived from the record's authority")
	}
	// Sequential numbers exist in no record, so nothing should invent one.
	if is.AppviewNumber != nil {
		t.Errorf("appviewNumber = %v, want nil for a record-derived issue", *is.AppviewNumber)
	}
}

func TestBuildIssueDefaultsToOpen(t *testing.T) {
	// A single-record lookup carries no derived state; absent a state record an
	// issue is open.
	item := bobbin.ListItem{
		URI:   "at://did:plc:abc/sh.tangled.repo.issue/3ms4qpm3qjd22",
		Value: json.RawMessage(`{"repo":"did:plc:repo","title":"t","createdAt":"2026-01-01T00:00:00Z"}`),
	}
	is, err := render.BuildIssue(item, handles(nil))
	if err != nil {
		t.Fatalf("BuildIssue: %v", err)
	}
	if is.State != "open" {
		t.Errorf("state = %q, want open", is.State)
	}
}

func TestBuildPullShapes(t *testing.T) {
	p := loadPage(t, "listPulls.json")
	seen := map[string]bool{}
	for _, it := range p.Items {
		pl, err := render.BuildPull(it, handles(nil))
		if err != nil {
			t.Fatalf("BuildPull: %v", err)
		}
		seen[pl.Shape] = true
		if pl.TargetBranch == "" {
			t.Errorf("pull %s has no target branch", pl.RKey)
		}
		if !strings.HasPrefix(pl.TargetRepoDID, "did:") {
			t.Errorf("pull %s target repo = %q, want a DID", pl.RKey, pl.TargetRepoDID)
		}
		if pl.Rounds == nil {
			t.Errorf("pull %s has nil rounds; the schema promises an array", pl.RKey)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no pulls classified")
	}
}

func TestPullShapeClassification(t *testing.T) {
	tests := []struct {
		name string
		rec  tangled.Pull
		want string
	}{
		{"no source is a pasted patch", tangled.Pull{Target: tangled.PullTarget{Repo: "did:plc:r"}}, "patch"},
		{"source branch only", tangled.Pull{
			Target: tangled.PullTarget{Repo: "did:plc:r"},
			Source: &tangled.PullSource{Branch: "feature"},
		}, "branch"},
		{"source repo equal to target", tangled.Pull{
			Target: tangled.PullTarget{Repo: "did:plc:r"},
			Source: &tangled.PullSource{Branch: "feature", Repo: "did:plc:r"},
		}, "branch"},
		{"source repo differs", tangled.Pull{
			Target: tangled.PullTarget{Repo: "did:plc:r"},
			Source: &tangled.PullSource{Branch: "feature", Repo: "did:plc:fork"},
		}, "fork"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := render.PullShape(&tc.rec); got != tc.want {
				t.Errorf("PullShape = %q, want %q", got, tc.want)
			}
		})
	}
}

// A pull carrying the undocumented `versions` shape must survive decoding with
// no rounds, so the CLI can report the limitation rather than crash.
func TestBuildPullVersionsShape(t *testing.T) {
	item := bobbin.ListItem{
		URI: "at://did:plc:abc/sh.tangled.repo.pull/3mszlqgjlzsdn",
		Value: json.RawMessage(`{
			"title":"lexicons","createdAt":"2026-08-14T07:04:02.394Z","rounds":[],
			"target":{"repo":"did:plc:r","branch":"sv-fe"},
			"source":{"repo":"did:plc:r","branch":"sl/org-tangled"},
			"versions":[{"base":"912ecc15","head":"0e96abbc","createdAt":"2026-08-14T07:04:02.394Z"}]}`),
	}
	pl, err := render.BuildPull(item, handles(nil))
	if err != nil {
		t.Fatalf("BuildPull: %v", err)
	}
	if len(pl.Rounds) != 0 {
		t.Errorf("rounds = %d, want 0", len(pl.Rounds))
	}
	if len(pl.Versions) != 1 || pl.Versions[0].Head != "0e96abbc" {
		t.Errorf("versions not preserved: %+v", pl.Versions)
	}
}

func TestBuildComments(t *testing.T) {
	p := loadPage(t, "listComments.json")
	cs := render.BuildComments(p.Items, handles(nil))
	if len(cs) != len(p.Items) {
		t.Fatalf("built %d comments from %d items", len(cs), len(p.Items))
	}
	for _, c := range cs {
		if c.Body == "" {
			t.Errorf("comment %s has an empty body; the markdown union was not unwrapped", c.RKey)
		}
		if c.Legacy {
			t.Errorf("comment %s flagged legacy, but the fixture is sh.tangled.feed.comment", c.RKey)
		}
	}
	for i := 1; i < len(cs); i++ {
		if cs[i].CreatedAt < cs[i-1].CreatedAt {
			t.Errorf("comments not sorted oldest-first at index %d", i)
		}
	}
}

func TestBuildCommentsLegacyShape(t *testing.T) {
	items := []bobbin.ListItem{{
		URI:   "at://did:plc:abc/sh.tangled.repo.issue.comment/3ms4qpm3qjd22",
		Value: json.RawMessage(`{"issue":"at://x","body":"flat string body","createdAt":"2025-01-01T00:00:00Z"}`),
	}}
	cs := render.BuildComments(items, handles(nil))
	if len(cs) != 1 {
		t.Fatalf("got %d comments", len(cs))
	}
	if !cs[0].Legacy {
		t.Error("a sh.tangled.repo.issue.comment record should be flagged legacy")
	}
	if cs[0].Body != "flat string body" {
		t.Errorf("body = %q", cs[0].Body)
	}
}

func TestFoldLabels(t *testing.T) {
	defs := map[string]*tangled.LabelDefinition{
		"at://did:plc:o/sh.tangled.label.definition/bug": {Name: "bug", Color: "#f00"},
	}
	op := func(add, del [][2]string, at string) bobbin.ListItem {
		rec := tangled.LabelOp{Subject: "at://s", PerformedAt: at}
		for _, a := range add {
			rec.Add = append(rec.Add, tangled.LabelOperand{Key: a[0], Value: a[1]})
		}
		for _, d := range del {
			rec.Delete = append(rec.Delete, tangled.LabelOperand{Key: d[0], Value: d[1]})
		}
		b, _ := json.Marshal(rec)
		return bobbin.ListItem{URI: "at://did:plc:o/sh.tangled.label.op/" + at, Value: b}
	}
	bug := "at://did:plc:o/sh.tangled.label.definition/bug"
	wont := "at://did:plc:o/sh.tangled.label.definition/wontfix"

	got := render.FoldLabels([]bobbin.ListItem{
		op([][2]string{{bug, ""}, {wont, ""}}, nil, "1"),
		op(nil, [][2]string{{wont, ""}}, "2"),
	}, defs)

	if len(got) != 1 {
		t.Fatalf("folded to %d labels, want 1: %+v", len(got), got)
	}
	if got[0].Key != bug || got[0].Name != "bug" || got[0].Color != "#f00" {
		t.Errorf("label = %+v, want the bug definition resolved", got[0])
	}
}

func TestFoldLabelsReAddAfterDelete(t *testing.T) {
	key := "at://did:plc:o/sh.tangled.label.definition/x"
	mk := func(add bool) bobbin.ListItem {
		rec := tangled.LabelOp{Subject: "at://s"}
		if add {
			rec.Add = []tangled.LabelOperand{{Key: key}}
		} else {
			rec.Delete = []tangled.LabelOperand{{Key: key}}
		}
		b, _ := json.Marshal(rec)
		return bobbin.ListItem{URI: "at://x/y/z", Value: b}
	}
	got := render.FoldLabels([]bobbin.ListItem{mk(true), mk(false), mk(true)}, nil)
	if len(got) != 1 {
		t.Fatalf("re-added label lost: %+v", got)
	}
}

func TestWriteIssueListIsStable(t *testing.T) {
	var buf bytes.Buffer
	render.WriteIssueList(&buf, &render.IssueList{
		Repo: render.RepoRef{Name: "core"},
		Issues: []render.Issue{{
			RKey: "3ms4qpm3qjd22", Title: "a title", State: "open",
			Author:    render.Actor{DID: "did:plc:a", Handle: "alice.test"},
			CreatedAt: "2026-08-18T00:00:00Z",
			Labels:    []render.Label{{Key: "k", Name: "bug"}},
		}},
		Count: 1,
	})
	out := buf.String()
	for _, want := range []string{"3ms4qpm3qjd22", "open", "a title", "@alice.test", "[bug]", "1 issue(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteIssueListEmpty(t *testing.T) {
	var buf bytes.Buffer
	render.WriteIssueList(&buf, &render.IssueList{Repo: render.RepoRef{Name: "core"}, Issues: nil})
	if !strings.Contains(buf.String(), "No issues found") {
		t.Errorf("unexpected empty rendering: %q", buf.String())
	}
}

func TestWritePullReportsVersionsShape(t *testing.T) {
	var buf bytes.Buffer
	render.WritePull(&buf, &render.Pull{
		RKey: "3m", Title: "t", State: "open", Shape: "branch",
		TargetBranch: "main", CreatedAt: "2026-08-18T00:00:00Z",
		Versions: []render.PullVersionRange{{Base: "912ecc1546", Head: "0e96abbc39"}},
	})
	if !strings.Contains(buf.String(), "versions: 1") {
		t.Errorf("versions-shaped pull not surfaced:\n%s", buf.String())
	}
}

func TestJSONSchemaOmitsEmptyOptionals(t *testing.T) {
	b, err := json.Marshal(render.Issue{URI: "at://x", RKey: "r", Title: "t", State: "open", RepoDID: "did:plc:r", CreatedAt: "t"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, absent := range []string{"appviewNumber", "labels", "comments", "body"} {
		if strings.Contains(s, absent) {
			t.Errorf("empty optional %q should be omitted from the JSON schema: %s", absent, s)
		}
	}
	for _, present := range []string{"uri", "rkey", "title", "state", "repoDid", "commentCount"} {
		if !strings.Contains(s, present) {
			t.Errorf("required field %q missing from the JSON schema: %s", present, s)
		}
	}
}
