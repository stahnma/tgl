package resolve_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/gitutil"
	"tangled.org/tgl/internal/identity"
	"tangled.org/tgl/internal/resolve"
)

const (
	ownerDID = "did:plc:wshs7t2adsemcrrd4snkeqli"
	repoDID  = "did:plc:j5hmlfdrwkvtxm7cjmu7j2is"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

// newStack serves recorded Bobbin responses and DID documents, so resolution is
// exercised end to end without touching the network.
func newStack(t *testing.T) *resolve.Resolver {
	t.Helper()

	plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		did := strings.TrimPrefix(r.URL.Path, "/")
		switch did {
		case repoDID:
			w.Write(fixture(t, "diddoc_repo.json"))
		case ownerDID:
			w.Write(fixture(t, "diddoc_owner.json"))
		default:
			http.Error(w, `{"error":"NotFound"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(plc.Close)

	prev := identity.PLCDirectory
	identity.PLCDirectory = plc.URL
	t.Cleanup(func() { identity.PLCDirectory = prev })

	bob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/xrpc/") {
		case "sh.tangled.repo.getRepo":
			if q.Get("repo") == "at://"+ownerDID+"/sh.tangled.repo/core" {
				w.Write(fixture(t, "getRepo_core.json"))
				return
			}
			// Bobbin reports a missing record as 502 UpstreamFailed, not 404.
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"UpstreamFailed","message":"upstream unavailable"}`))
		case "sh.tangled.repo.getRepoByRepoDid":
			if q.Get("repoDid") == repoDID {
				w.Write(fixture(t, "getRepo_core.json"))
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"UpstreamFailed"}`))
		case "sh.tangled.repo.listRepos":
			w.Write(fixture(t, "listRepos_owner.json"))
		default:
			http.Error(w, `{"error":"MethodNotImplemented"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(bob.Close)

	return resolve.New(bobbin.New(bob.URL), identity.NewResolver())
}

func TestResolveByRkeyName(t *testing.T) {
	r := newStack(t)
	repo, err := r.FromSpec(context.Background(), ownerDID+"/core")
	if err != nil {
		t.Fatalf("FromSpec: %v", err)
	}
	if repo.Name != "core" || repo.RKey != "core" {
		t.Errorf("name=%q rkey=%q, want both \"core\"", repo.Name, repo.RKey)
	}
	if repo.OwnerDID != ownerDID {
		t.Errorf("ownerDid = %q", repo.OwnerDID)
	}
	if repo.RepoDID != repoDID {
		t.Errorf("repoDid = %q, want %q", repo.RepoDID, repoDID)
	}
	if repo.AtURI != "at://"+ownerDID+"/sh.tangled.repo/core" {
		t.Errorf("atUri = %q", repo.AtURI)
	}
}

// The knot must come from the repo DID's document, which is authoritative,
// rather than from the repo record's convenience `knot` string.
func TestResolveKnotFromDIDDocument(t *testing.T) {
	r := newStack(t)
	repo, err := r.FromSpec(context.Background(), ownerDID+"/core")
	if err != nil {
		t.Fatalf("FromSpec: %v", err)
	}
	if repo.KnotEndpoint != "https://knot1.tangled.sh" {
		t.Errorf("knotEndpoint = %q, want https://knot1.tangled.sh", repo.KnotEndpoint)
	}
	if repo.Knot != "knot1.tangled.sh" {
		t.Errorf("knot = %q", repo.Knot)
	}
}

// Repos whose cosmetic `name` differs from their rkey must still resolve, via
// the listRepos fallback.
func TestResolveByCosmeticName(t *testing.T) {
	var page struct {
		Items []struct {
			URI   string          `json:"uri"`
			Value json.RawMessage `json:"value"`
		} `json:"items"`
	}
	if err := json.Unmarshal(fixture(t, "listRepos_owner.json"), &page); err != nil {
		t.Fatal(err)
	}
	var wantName, wantRkey string
	for _, it := range page.Items {
		var v struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(it.Value, &v)
		rkey := it.URI[strings.LastIndex(it.URI, "/")+1:]
		if v.Name != "" && v.Name != rkey {
			wantName, wantRkey = v.Name, rkey
			break
		}
	}
	if wantName == "" {
		t.Skip("fixture has no repo whose name differs from its rkey")
	}

	r := newStack(t)
	repo, err := r.FromSpec(context.Background(), ownerDID+"/"+wantName)
	if err != nil {
		t.Fatalf("FromSpec(%q): %v", wantName, err)
	}
	if repo.Name != wantName || repo.RKey != wantRkey {
		t.Errorf("resolved name=%q rkey=%q, want name=%q rkey=%q", repo.Name, repo.RKey, wantName, wantRkey)
	}
}

func TestResolveByRepoDID(t *testing.T) {
	r := newStack(t)
	repo, err := r.FromRepoDID(context.Background(), repoDID)
	if err != nil {
		t.Fatalf("FromRepoDID: %v", err)
	}
	if repo.Name != "core" || repo.RepoDID != repoDID {
		t.Errorf("got name=%q repoDid=%q", repo.Name, repo.RepoDID)
	}
}

func TestResolveFromKnotCloneRemote(t *testing.T) {
	rem, err := gitutil.ParseRemote("https://knot1.tangled.sh/" + repoDID + "/")
	if err != nil {
		t.Fatalf("ParseRemote: %v", err)
	}
	repo, err := newStack(t).FromRemote(context.Background(), rem)
	if err != nil {
		t.Fatalf("FromRemote: %v", err)
	}
	if repo.RepoDID != repoDID {
		t.Errorf("repoDid = %q", repo.RepoDID)
	}
}

func TestResolveUnknownRepo(t *testing.T) {
	_, err := newStack(t).FromSpec(context.Background(), ownerDID+"/definitely-not-a-repo")
	if err == nil {
		t.Fatal("expected an error for an unknown repo")
	}
	if !strings.Contains(err.Error(), "no repository") {
		t.Errorf("error = %v, want it to name the missing repository", err)
	}
}

func TestBadSpecs(t *testing.T) {
	r := newStack(t)
	for _, spec := range []string{"", "noslash", "owner/"} {
		if _, err := r.FromSpec(context.Background(), spec); err == nil {
			t.Errorf("FromSpec(%q) should fail", spec)
		}
	}
}

func TestSubjectRequiresRepoDID(t *testing.T) {
	repo := &resolve.Repo{OwnerDID: "did:plc:o", Name: "n", RKey: "n"}
	if _, err := repo.Subject(); err == nil {
		t.Fatal("Subject() must fail without a repoDid: Bobbin's listings key on it")
	}
	repo.RepoDID = repoDID
	got, err := repo.Subject()
	if err != nil || got != repoDID {
		t.Errorf("Subject() = %q, %v", got, err)
	}
}

func TestKnotIdentifierFallback(t *testing.T) {
	repo := &resolve.Repo{OwnerDID: "did:plc:o", RKey: "rk"}
	if got := repo.KnotIdentifier(); got != "did:plc:o/rk" {
		t.Errorf("KnotIdentifier() = %q, want did:plc:o/rk for a repo with no DID", got)
	}
	repo.RepoDID = repoDID
	if got := repo.KnotIdentifier(); got != repoDID {
		t.Errorf("KnotIdentifier() = %q, want the repo DID", got)
	}
}
