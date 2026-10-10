package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The service-auth audience is derived mechanically from the knot hostname; it
// is never looked up. Ports must be percent-encoded.
func TestDidWeb(t *testing.T) {
	for host, want := range map[string]string{
		"knot1.tangled.sh": "did:web:knot1.tangled.sh",
		"localhost:5555":   "did:web:localhost%3A5555",
	} {
		if got := DidWeb(host); got != want {
			t.Errorf("DidWeb(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestDocumentEndpoints(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "diddoc_repo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	// knot1.tangled.sh still publishes the legacy atproto_pds service id, so
	// Knot() must fall back to it.
	if got := doc.Knot(); got != "https://knot1.tangled.sh" {
		t.Errorf("Knot() = %q", got)
	}
}

func TestKnotPrefersModernService(t *testing.T) {
	doc := Document{Service: []service{
		{ID: "#atproto_pds", Type: LegacyKnotServiceType, ServiceEndpoint: "https://legacy.example"},
		{ID: "#tangled_knot", Type: KnotServiceType, ServiceEndpoint: "https://modern.example"},
	}}
	if got := doc.Knot(); got != "https://modern.example" {
		t.Errorf("Knot() = %q, want the tangled_knot endpoint", got)
	}
}

func TestDocumentHandle(t *testing.T) {
	doc := Document{AlsoKnownAs: []string{"at://alice.test", "https://other"}}
	if got := doc.Handle(); got != "alice.test" {
		t.Errorf("Handle() = %q", got)
	}
	if got := (&Document{}).Handle(); got != "" {
		t.Errorf("Handle() = %q, want empty", got)
	}
}

func TestResolveCachesDocuments(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"id":"did:plc:abc","alsoKnownAs":["at://alice.test"],
			"service":[{"id":"#atproto_pds","type":"AtprotoPersonalDataServer","serviceEndpoint":"https://pds.example"}]}`))
	}))
	defer srv.Close()

	prev := PLCDirectory
	PLCDirectory = srv.URL
	defer func() { PLCDirectory = prev }()

	r := NewResolver()
	for i := 0; i < 3; i++ {
		doc, err := r.Document(context.Background(), "did:plc:abc")
		if err != nil {
			t.Fatal(err)
		}
		if doc.PDS() != "https://pds.example" {
			t.Fatalf("PDS() = %q", doc.PDS())
		}
	}
	if hits != 1 {
		t.Errorf("resolved the DID document %d times, want 1 (it should be cached)", hits)
	}
}

func TestIsDID(t *testing.T) {
	if !IsDID("did:plc:abc") || IsDID("alice.test") {
		t.Error("IsDID misclassified an input")
	}
}

// Fork repo DIDs publish a knot endpoint with a path component that is neither a
// git path nor an XRPC prefix. It must be stripped, or every knot call 404s.
func TestKnotStripsPath(t *testing.T) {
	doc := Document{Service: []service{{
		ID: "#tangled_knot", Type: KnotServiceType,
		ServiceEndpoint: "https://knot1.tangled.sh/repo/d2xe2oraduwmaoakkh5w7jh6mi",
	}}}
	if got := doc.Knot(); got != "https://knot1.tangled.sh" {
		t.Errorf("Knot() = %q, want the bare host", got)
	}
}

func TestKnotDropsDefaultPort(t *testing.T) {
	doc := Document{Service: []service{{
		ID: "#tangled_knot", Type: KnotServiceType, ServiceEndpoint: "https://Knot1.Tangled.SH:443/x",
	}}}
	if got := doc.Knot(); got != "https://knot1.tangled.sh" {
		t.Errorf("Knot() = %q", got)
	}
}

func TestKnotKeepsNonDefaultPort(t *testing.T) {
	doc := Document{Service: []service{{
		ID: "#tangled_knot", Type: KnotServiceType, ServiceEndpoint: "http://localhost:5555/",
	}}}
	if got := doc.Knot(); got != "http://localhost:5555" {
		t.Errorf("Knot() = %q", got)
	}
}
