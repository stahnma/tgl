// Package knot talks to a Tangled knot's XRPC surface.
//
// Only a handful of methods matter to tgl. sh.tangled.repo.merge requires
// service auth; sh.tangled.repo.mergeCheck and sh.tangled.repo.compare are open
// endpoints (core:knotserver/xrpc/xrpc.go:47-98).
package knot

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"tangled.org/tgl/internal/httpx"
)

// Capability tokens, from core:consts/consts.go:14-15.
const (
	CapKnotACL      = "knot-acl"
	CapRepoDidInput = "repo-did-input"
)

// Lexicon method names.
const (
	NSIDMerge      = "sh.tangled.repo.merge"
	NSIDMergeCheck = "sh.tangled.repo.mergeCheck"
	NSIDCompare    = "sh.tangled.repo.compare"
	NSIDCreate     = "sh.tangled.repo.create"
	NSIDDelete     = "sh.tangled.repo.delete"
	NSIDVersion    = "sh.tangled.knot.version"
)

type Client struct {
	http *httpx.Client
	Host string // bare hostname, e.g. knot1.tangled.sh
}

// New builds a client for a knot. endpoint may be a bare hostname or a full URL.
func New(endpoint string) *Client {
	base := endpoint
	if !strings.Contains(base, "://") {
		scheme := "https://"
		if isLoopback(base) {
			scheme = "http://"
		}
		base = scheme + base
	}
	host := base
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	return &Client{http: httpx.New(base, 0), Host: host}
}

func isLoopback(h string) bool {
	return strings.HasPrefix(h, "localhost") || strings.HasPrefix(h, "127.0.0.1") || strings.HasPrefix(h, "[::1]")
}

// SetToken installs a service-auth bearer token. Tokens are scoped to a single
// lexicon method and live ~60 seconds, so mint one per call.
func (c *Client) SetToken(tok string) { c.http.Token = tok }

// Version is the sh.tangled.knot.version response. A knot that omits
// capabilities is legacy.
type Version struct {
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// Has reports whether the knot declares a capability.
func (v *Version) Has(cap string) bool { return slices.Contains(v.Capabilities, cap) }

var (
	verMu    sync.Mutex
	verCache = map[string]versionEntry{}
)

type versionEntry struct {
	v  *Version
	at time.Time
}

// versionTTL mirrors the appview's 5 minute freshness window
// (core:appview/knotcompat/compat.go:22-25).
const versionTTL = 5 * time.Minute

// Version probes the knot's version and capabilities, cached per host.
func (c *Client) Version(ctx context.Context) (*Version, error) {
	verMu.Lock()
	if e, ok := verCache[c.Host]; ok && time.Since(e.at) < versionTTL {
		verMu.Unlock()
		return e.v, nil
	}
	verMu.Unlock()

	var out Version
	if err := c.http.Query(ctx, NSIDVersion, nil, &out); err != nil {
		return nil, err
	}
	verMu.Lock()
	verCache[c.Host] = versionEntry{v: &out, at: time.Now()}
	verMu.Unlock()
	return &out, nil
}

// MergeInput is sh.tangled.repo.merge (core:lexicons/repo/merge.json).
//
// Repo is the repo DID. Did and Name are the owner DID and repo name, read by
// knots that lack the repo-did-input capability. The appview sends all three;
// so do we.
type MergeInput struct {
	Repo          string `json:"repo"`
	Did           string `json:"did,omitempty"`
	Name          string `json:"name,omitempty"`
	Patch         string `json:"patch"`
	Branch        string `json:"branch"`
	AuthorName    string `json:"authorName,omitempty"`
	AuthorEmail   string `json:"authorEmail,omitempty"`
	CommitMessage string `json:"commitMessage,omitempty"`
	CommitBody    string `json:"commitBody,omitempty"`
}

// Merge applies a patch to a branch. Requires a service-auth token minted for
// aud=did:web:<knot host> and lxm=sh.tangled.repo.merge.
func (c *Client) Merge(ctx context.Context, in *MergeInput) error {
	prev := c.http.HTTP.Timeout
	c.http.HTTP.Timeout = 30 * time.Second // merges are slow on large repos
	defer func() { c.http.HTTP.Timeout = prev }()
	return c.http.Procedure(ctx, NSIDMerge, in, nil)
}

// MergeCheckInput is sh.tangled.repo.mergeCheck.
type MergeCheckInput struct {
	Repo   string `json:"repo"`
	Did    string `json:"did,omitempty"`
	Name   string `json:"name,omitempty"`
	Patch  string `json:"patch"`
	Branch string `json:"branch"`
}

type Conflict struct {
	Filename string `json:"filename"`
	Reason   string `json:"reason"`
}

type MergeCheckOutput struct {
	IsConflicted bool       `json:"is_conflicted"`
	Conflicts    []Conflict `json:"conflicts"`
	Message      string     `json:"message"`
	Error        string     `json:"error"`
}

// MergeCheck asks whether a patch applies cleanly. This is an open endpoint; no
// auth needed.
func (c *Client) MergeCheck(ctx context.Context, in *MergeCheckInput) (*MergeCheckOutput, error) {
	var out MergeCheckOutput
	if err := c.http.Procedure(ctx, NSIDMergeCheck, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CompareOutput mirrors core:types.RepoFormatPatchResponse, the shape
// sh.tangled.repo.compare returns.
type CompareOutput struct {
	Rev1      string `json:"rev1,omitempty"`
	Rev2      string `json:"rev2,omitempty"`
	MergeBase string `json:"merge_base,omitempty"`
	// FormatPatch is the structured per-commit breakdown; we only need to know
	// whether it is empty ("no commits between target and source").
	FormatPatch []json.RawMessage `json:"format_patch,omitempty"`
	// FormatPatchRaw carries the json tag "patch", not "format_patch_raw".
	FormatPatchRaw   string `json:"patch,omitempty"`
	CombinedPatchRaw string `json:"combined_patch_raw,omitempty"`
	BinaryOmitted    bool   `json:"binary_omitted,omitempty"`
}

// Compare produces a format-patch between two revisions. repoIdent is the repo
// DID, or "<ownerDID>/<name>" on knots without repo-did-input.
func (c *Client) Compare(ctx context.Context, repoIdent, rev1, rev2 string) (*CompareOutput, error) {
	params := url.Values{"repo": {repoIdent}, "rev1": {rev1}, "rev2": {rev2}}
	var out CompareOutput
	if err := c.http.Query(ctx, NSIDCompare, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DefaultBranch is the sh.tangled.repo.getDefaultBranch response.
type DefaultBranch struct {
	Name      string `json:"name"`
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash,omitempty"`
	When      string `json:"when"`
	Message   string `json:"message,omitempty"`
}

// GetDefaultBranch reports a repo's default branch. Open endpoint.
func (c *Client) GetDefaultBranch(ctx context.Context, repoIdent string) (*DefaultBranch, error) {
	var out DefaultBranch
	if err := c.http.Query(ctx, "sh.tangled.repo.getDefaultBranch", url.Values{"repo": {repoIdent}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloneURL is the git URL for a repo hosted on this knot. Knots serve repos at
// /<repoDID>, which is where tangled.org's clone URLs redirect.
func (c *Client) CloneURL(repoDID string) string {
	return strings.TrimRight(c.http.Base, "/") + "/" + repoDID
}

// CreateInput is sh.tangled.repo.create (core:lexicons/repo/create.json).
//
// Name is what the knot stores and keys its own repo table on; the appview
// sends the lowercased rkey for both (core:appview/state/state.go:565-569).
// Source, when set, makes the knot clone that URL instead of initialising an
// empty repo — the fork and import path, which tgl does not use.
type CreateInput struct {
	RKey          string `json:"rkey"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	Source        string `json:"source,omitempty"`
	RepoDid       string `json:"repoDid,omitempty"`
}

// CreateOutput carries the repo DID the knot minted. A knot old enough to
// return an empty one cannot host a repo the appview will index.
type CreateOutput struct {
	RepoDid string `json:"repoDid"`
	Key     string `json:"key"`
}

// CreateRepo initialises a bare repository on the knot. Requires a service-auth
// token minted for aud=did:web:<knot host> and lxm=sh.tangled.repo.create, and
// the actor must be allowed to create repos there
// (core:knotserver/xrpc/create_repo.go:41-48).
func (c *Client) CreateRepo(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
	prev := c.http.HTTP.Timeout
	c.http.HTTP.Timeout = 30 * time.Second // the knot inits (or clones) a repo
	defer func() { c.http.HTTP.Timeout = prev }()
	var out CreateOutput
	if err := c.http.Procedure(ctx, NSIDCreate, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteInput is sh.tangled.repo.delete. Repo is the repo DID; the rest are for
// knots without repo-did-input.
type DeleteInput struct {
	Repo string `json:"repo"`
	Did  string `json:"did,omitempty"`
	Name string `json:"name,omitempty"`
	RKey string `json:"rkey,omitempty"`
}

// DeleteRepo removes a repository from the knot.
//
// The knot refuses while the sh.tangled.repo record still exists on the owner's
// PDS (core:knotserver/xrpc/delete_repo.go:64-70), which is exactly what makes
// it usable to roll back a create whose record write failed.
func (c *Client) DeleteRepo(ctx context.Context, in *DeleteInput) error {
	return c.http.Procedure(ctx, NSIDDelete, in, nil)
}
