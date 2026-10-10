// Package bobbin is a read client for Tangled's Bobbin appview
// (https://api.tangled.org): read-only, unauthenticated, stateless.
//
// Bobbin covers the entire read path tgl needs. Its one operational wrinkle is
// that single-record lookups proxy to Slingshot, so a missing record surfaces as
// HTTP 502 UpstreamFailed rather than a 404. See NotFound.
package bobbin

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"tangled.org/tgl/internal/httpx"
)

// DefaultHost is the public Bobbin instance.
const DefaultHost = "https://api.tangled.org"

type Client struct{ http *httpx.Client }

func New(host string) *Client {
	if host == "" {
		host = DefaultHost
	}
	return &Client{http: httpx.New(host, 0)}
}

func (c *Client) Host() string { return c.http.Base }

// Record is Bobbin's uniform single-record envelope.
type Record struct {
	URI   string          `json:"uri"`
	CID   string          `json:"cid"`
	Value json.RawMessage `json:"value"`
}

// ListItem is the shape returned by the list* aggregation endpoints. The state,
// stateUpdatedAt and commentCount fields are only populated by listIssues and
// listPulls, which derive them for us.
type ListItem struct {
	URI            string          `json:"uri"`
	CID            string          `json:"cid"`
	Value          json.RawMessage `json:"value"`
	State          string          `json:"state,omitempty"`
	StateUpdatedAt string          `json:"stateUpdatedAt,omitempty"`
	CommentCount   int             `json:"commentCount,omitempty"`
}

type listOut struct {
	Items  []ListItem `json:"items"`
	Cursor string     `json:"cursor"`
}

// Coverage reports how far along Bobbin's backfill is. Counts are a lower bound
// until Ready is true.
type Coverage struct {
	Ready           bool  `json:"ready"`
	EventsProcessed int64 `json:"eventsProcessed"`
	LastCursor      int64 `json:"lastCursor"`
}

func (c *Client) Coverage(ctx context.Context) (*Coverage, error) {
	var out Coverage
	if err := c.http.Query(ctx, "sh.tangled.bobbin.getCoverage", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NotFound reports whether err is Bobbin's way of saying a record is missing.
//
// Bobbin returns 502 UpstreamFailed when Slingshot cannot serve a record, which
// covers both "no such record" and "Slingshot is down". Callers that need to
// distinguish the two should check Coverage.
func NotFound(err error) bool {
	switch httpx.Status(err) {
	case 400, 404:
		return true
	case 502:
		return httpx.Kind(err) == "UpstreamFailed"
	}
	return false
}

// GetRepo fetches a sh.tangled.repo record by its AT-URI.
func (c *Client) GetRepo(ctx context.Context, atURI string) (*Record, error) {
	return c.get(ctx, "sh.tangled.repo.getRepo", "repo", atURI)
}

// GetRepoByRepoDid fetches the sh.tangled.repo record for a repo DID.
func (c *Client) GetRepoByRepoDid(ctx context.Context, repoDID string) (*Record, error) {
	return c.get(ctx, "sh.tangled.repo.getRepoByRepoDid", "repoDid", repoDID)
}

// GetIssue fetches one issue record by AT-URI.
func (c *Client) GetIssue(ctx context.Context, atURI string) (*Record, error) {
	return c.get(ctx, "sh.tangled.repo.getIssue", "issue", atURI)
}

// GetPull fetches one pull record by AT-URI.
func (c *Client) GetPull(ctx context.Context, atURI string) (*Record, error) {
	return c.get(ctx, "sh.tangled.repo.getPull", "pull", atURI)
}

func (c *Client) get(ctx context.Context, nsid, param, value string) (*Record, error) {
	var out Record
	if err := c.http.Query(ctx, nsid, url.Values{param: {value}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRepos enumerates the repo records owned by a DID.
func (c *Client) ListRepos(ctx context.Context, ownerDID string, limit int, cursor string) ([]ListItem, string, error) {
	return c.list(ctx, "sh.tangled.repo.listRepos", url.Values{"subject": {ownerDID}}, limit, cursor)
}

// IssueQuery filters sh.tangled.repo.listIssues.
type IssueQuery struct {
	RepoDID string // required
	Author  string // DID
	State   string // open | closed
	Limit   int
	Cursor  string
	Order   string // asc | desc
}

func (c *Client) ListIssues(ctx context.Context, q IssueQuery) ([]ListItem, string, error) {
	v := url.Values{"subject": {q.RepoDID}}
	if q.Author != "" {
		v.Set("author", q.Author)
	}
	if q.State != "" {
		v.Set("state", q.State)
	}
	if q.Order != "" {
		v.Set("order", q.Order)
	}
	return c.list(ctx, "sh.tangled.repo.listIssues", v, q.Limit, q.Cursor)
}

// PullQuery filters sh.tangled.repo.listPulls.
type PullQuery struct {
	RepoDID string // required
	Author  string // DID
	Status  string // open | closed | merged
	Limit   int
	Cursor  string
	Order   string
}

func (c *Client) ListPulls(ctx context.Context, q PullQuery) ([]ListItem, string, error) {
	v := url.Values{"subject": {q.RepoDID}}
	if q.Author != "" {
		v.Set("author", q.Author)
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.Order != "" {
		v.Set("order", q.Order)
	}
	return c.list(ctx, "sh.tangled.repo.listPulls", v, q.Limit, q.Cursor)
}

// ListComments returns sh.tangled.feed.comment records attached to a subject.
func (c *Client) ListComments(ctx context.Context, subjectURI string, limit int, cursor string) ([]ListItem, string, error) {
	v := url.Values{"subject": {subjectURI}, "order": {"asc"}}
	return c.list(ctx, "sh.tangled.feed.listComments", v, limit, cursor)
}

// ListLabelOps returns the label op log for a subject, oldest first, so callers
// can fold it into a current label set.
func (c *Client) ListLabelOps(ctx context.Context, subjectURI string, limit int, cursor string) ([]ListItem, string, error) {
	v := url.Values{"subject": {subjectURI}, "order": {"asc"}}
	return c.list(ctx, "sh.tangled.label.listOps", v, limit, cursor)
}

// ListLabelDefinitions returns the label definitions owned by a DID.
func (c *Client) ListLabelDefinitions(ctx context.Context, ownerDID string, limit int, cursor string) ([]ListItem, string, error) {
	v := url.Values{"subject": {ownerDID}}
	return c.list(ctx, "sh.tangled.label.listDefinitions", v, limit, cursor)
}

// ListIssueStates returns the state log for an issue.
func (c *Client) ListIssueStates(ctx context.Context, issueURI string, limit int) ([]ListItem, string, error) {
	v := url.Values{"subject": {issueURI}, "order": {"asc"}}
	return c.list(ctx, "sh.tangled.repo.issue.listStates", v, limit, "")
}

// ListPullStatuses returns the status log for a pull.
func (c *Client) ListPullStatuses(ctx context.Context, pullURI string, limit int) ([]ListItem, string, error) {
	v := url.Values{"subject": {pullURI}, "order": {"asc"}}
	return c.list(ctx, "sh.tangled.repo.pull.listStatuses", v, limit, "")
}

// Counts is the shape returned by the count* endpoints.
type Counts struct {
	Count           int `json:"count"`
	DistinctAuthors int `json:"distinctAuthors"`
}

func (c *Client) CountIssues(ctx context.Context, repoDID string) (*Counts, error) {
	return c.count(ctx, "sh.tangled.repo.countIssues", repoDID)
}

func (c *Client) CountPulls(ctx context.Context, repoDID string) (*Counts, error) {
	return c.count(ctx, "sh.tangled.repo.countPulls", repoDID)
}

func (c *Client) count(ctx context.Context, nsid, subject string) (*Counts, error) {
	var out Counts
	if err := c.http.Query(ctx, nsid, url.Values{"subject": {subject}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) list(ctx context.Context, nsid string, v url.Values, limit int, cursor string) ([]ListItem, string, error) {
	if limit > 0 {
		if limit > 1000 {
			limit = 1000
		}
		v.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	var out listOut
	if err := c.http.Query(ctx, nsid, v, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.Cursor, nil
}

// ListAll pages a list endpoint until it is exhausted or max items are
// collected. max <= 0 means no cap.
func ListAll(
	ctx context.Context,
	max int,
	page func(ctx context.Context, limit int, cursor string) ([]ListItem, string, error),
) ([]ListItem, error) {
	var all []ListItem
	cursor := ""
	for {
		want := 100
		if max > 0 && max-len(all) < want {
			want = max - len(all)
		}
		if want <= 0 {
			break
		}
		items, next, err := page(ctx, want, cursor)
		if err != nil {
			return all, err
		}
		all = append(all, items...)
		if next == "" || len(items) == 0 {
			break
		}
		cursor = next
	}
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return all, nil
}
