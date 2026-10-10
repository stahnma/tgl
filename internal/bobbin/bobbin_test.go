package bobbin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"tangled.org/tgl/internal/httpx"
)

// Bobbin proxies single-record lookups to Slingshot, so a missing record comes
// back as 502 UpstreamFailed rather than a 404. Anything that maps errors to
// exit codes depends on this classification.
func TestNotFoundClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"404", &httpx.XRPCError{Status: 404}, true},
		{"400 invalid request", &httpx.XRPCError{Status: 400, Kind: "InvalidRequest"}, true},
		{"502 upstream failed", &httpx.XRPCError{Status: 502, Kind: "UpstreamFailed"}, true},
		{"502 other", &httpx.XRPCError{Status: 502, Kind: "SomethingElse"}, false},
		{"500", &httpx.XRPCError{Status: 500}, false},
		{"503", &httpx.XRPCError{Status: 503}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NotFound(tc.err); got != tc.want {
				t.Errorf("NotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestListAllPaginates(t *testing.T) {
	pages := [][]ListItem{
		{{URI: "at://a/c/1"}, {URI: "at://a/c/2"}},
		{{URI: "at://a/c/3"}},
	}
	calls := 0
	got, err := ListAll(context.Background(), 0, func(ctx context.Context, limit int, cursor string) ([]ListItem, string, error) {
		i := calls
		calls++
		if i >= len(pages) {
			return nil, "", nil
		}
		next := "cursor"
		if i == len(pages)-1 {
			next = ""
		}
		return pages[i], next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("collected %d items, want 3", len(got))
	}
}

func TestListAllRespectsMax(t *testing.T) {
	got, err := ListAll(context.Background(), 2, func(ctx context.Context, limit int, cursor string) ([]ListItem, string, error) {
		if limit > 2 {
			t.Errorf("requested limit %d exceeds the cap", limit)
		}
		return []ListItem{{URI: "at://a/c/1"}, {URI: "at://a/c/2"}, {URI: "at://a/c/3"}}, "more", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("collected %d items, want 2", len(got))
	}
}

func TestListIssuesSendsRepoDIDSubject(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, _, err := c.ListIssues(context.Background(), IssueQuery{
		RepoDID: "did:plc:repo", State: "open", Author: "did:plc:a", Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"subject=did%3Aplc%3Arepo", "state=open", "author=did%3Aplc%3Aa", "limit=10"} {
		if !contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
