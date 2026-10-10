package cli

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/tangled"
	"tangled.org/tgl/internal/tid"
)

// kind distinguishes the two subject types a ref can name.
type kind int

const (
	kindIssue kind = iota
	kindPull
)

func (k kind) collection() string {
	if k == kindPull {
		return tangled.NSIDPull
	}
	return tangled.NSIDIssue
}

func (k kind) noun() string {
	if k == kindPull {
		return "pull request"
	}
	return "issue"
}

func (k kind) urlSegment() string {
	if k == kindPull {
		return "pulls"
	}
	return "issues"
}

// ref is a resolved issue or pull reference.
type ref struct {
	Kind kind
	URI  string
	Item bobbin.ListItem
	// Number is set when the caller supplied a sequential #42, which tgl
	// resolved through the web appview that allocates those numbers.
	Number *int
}

// resolveRef turns a user-supplied reference into a concrete record.
//
// Accepted forms:
//
//	at://did:plc:.../sh.tangled.repo.issue/3ms4...   exact
//	3ms4qpm3qjd22                                    rkey, matched within the repo
//	https://tangled.org/@owner/repo/issues/42        web URL
//	42                                               sequential number
//
// Numbers are resolved by asking the web appview, because that appview allocates
// them and nothing else knows them; see resolveNumber. The rkey is the record's
// real identity and is what --json always reports.
func (a *App) resolveRef(ctx context.Context, repo *resolve.Repo, k kind, raw string) (*ref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, Errorf(CodeError, "missing %s reference", k.noun())
	}

	if strings.HasPrefix(raw, "at://") {
		u, err := tangled.ParseATURI(raw)
		if err != nil {
			return nil, Errorf(CodeError, "%v", err)
		}
		if u.Collection != k.collection() {
			return nil, Errorf(CodeError, "%s is not a %s record", raw, k.noun())
		}
		return a.fetchRef(ctx, k, raw, nil)
	}

	if n, ok := numberFromURL(raw); ok {
		raw = strconv.Itoa(n)
	}

	if tid.Valid(raw) {
		items, err := a.listAll(ctx, repo, k)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if rkeyOf(it.URI) == raw {
				return &ref{Kind: k, URI: it.URI, Item: it}, nil
			}
		}
		return nil, NotFoundf("no %s with rkey %s in %s", k.noun(), raw, repo.Slug())
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return nil, Errorf(CodeError,
			"unrecognised %s reference %q: expected an rkey, an at:// URI, a tangled.org URL, or a number", k.noun(), raw)
	}
	uri, err := a.resolveNumber(ctx, repo, k, n)
	if err != nil {
		return nil, err
	}
	return a.fetchRef(ctx, k, uri, &n)
}

func (a *App) fetchRef(ctx context.Context, k kind, atURI string, number *int) (*ref, error) {
	var rec *bobbin.Record
	var err error
	if k == kindPull {
		rec, err = a.Bobbin().GetPull(ctx, atURI)
	} else {
		rec, err = a.Bobbin().GetIssue(ctx, atURI)
	}
	if err != nil {
		if bobbin.NotFound(err) {
			return nil, a.notFoundOrUpstream(ctx, err, "%s %s not found", k.noun(), atURI)
		}
		return nil, Wrap(CodeError, err, "fetching %s", atURI)
	}
	return &ref{Kind: k, URI: rec.URI, Number: number, Item: bobbin.ListItem{URI: rec.URI, CID: rec.CID, Value: rec.Value}}, nil
}

// notFoundOrUpstream distinguishes a genuinely missing record from a Bobbin that
// cannot reach its upstream. Bobbin reports both as 502 UpstreamFailed, so we
// check its coverage before claiming exit 8.
func (a *App) notFoundOrUpstream(ctx context.Context, err error, format string, args ...any) error {
	if cov, cerr := a.Bobbin().Coverage(ctx); cerr != nil || !cov.Ready {
		return Wrap(CodeError, err, "appview is not serving reads right now")
	}
	return NotFoundf(format, args...)
}

// listAll pages every issue or pull record for a repo.
func (a *App) listAll(ctx context.Context, repo *resolve.Repo, k kind) ([]bobbin.ListItem, error) {
	subject, err := repo.Subject()
	if err != nil {
		return nil, Errorf(CodeError, "%v", err)
	}
	items, err := bobbin.ListAll(ctx, 0, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
		if k == kindPull {
			return a.Bobbin().ListPulls(ctx, bobbin.PullQuery{RepoDID: subject, Limit: limit, Cursor: cursor, Order: "asc"})
		}
		return a.Bobbin().ListIssues(ctx, bobbin.IssueQuery{RepoDID: subject, Limit: limit, Cursor: cursor, Order: "asc"})
	})
	if err != nil {
		return nil, Wrap(CodeError, err, "listing %ss", k.noun())
	}
	return items, nil
}

func rkeyOf(atURI string) string {
	u, err := tangled.ParseATURI(atURI)
	if err != nil {
		return ""
	}
	return u.RKey
}

// numberFromURL extracts the trailing number from a tangled.org issue or pull
// URL.
func numberFromURL(raw string) (int, bool) {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return 0, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// webURL builds the tangled.org URL for a record, when the owner handle is known.
func webURL(repo *resolve.Repo, k kind, number *int) string {
	if number == nil || repo.OwnerHandle == "" {
		return ""
	}
	return fmt.Sprintf("https://tangled.org/@%s/%s/%s/%d", repo.OwnerHandle, repo.Name, k.urlSegment(), *number)
}

func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
