// Package render defines tgl's stable JSON schema and its human-readable
// rendering. Every command emits one of these shapes under --json.
package render

// Actor is a person, identified by DID. Handle is best-effort: it requires a
// DID document lookup and is omitted when that fails.
type Actor struct {
	DID    string `json:"did"`
	Handle string `json:"handle,omitempty"`
}

// RepoRef identifies a repository across all four addressing schemes.
type RepoRef struct {
	Owner        Actor  `json:"owner"`
	Name         string `json:"name"`
	RKey         string `json:"rkey"`
	AtURI        string `json:"atUri"`
	RepoDID      string `json:"repoDid,omitempty"`
	Knot         string `json:"knot,omitempty"`
	KnotEndpoint string `json:"knotEndpoint,omitempty"`
	Description  string `json:"description,omitempty"`
	Spindle      string `json:"spindle,omitempty"`
}

// Label is one applied label, folded from the sh.tangled.label.op log.
type Label struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	Color string `json:"color,omitempty"`
}

// Comment is a normalised comment. Legacy is true for records still in the
// superseded sh.tangled.repo.{issue,pull}.comment collections.
type Comment struct {
	URI          string `json:"uri"`
	CID          string `json:"cid,omitempty"`
	RKey         string `json:"rkey"`
	Author       Actor  `json:"author"`
	Body         string `json:"body"`
	CreatedAt    string `json:"createdAt"`
	ReplyTo      string `json:"replyTo,omitempty"`
	PullRoundIdx *int   `json:"pullRoundIdx,omitempty"`
	Legacy       bool   `json:"legacy,omitempty"`
}

// Issue is the schema for `tgl issue list` items and `tgl issue view`.
type Issue struct {
	URI    string `json:"uri"`
	CID    string `json:"cid,omitempty"`
	RKey   string `json:"rkey"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	State  string `json:"state"`
	Author Actor  `json:"author"`
	// RepoDID is the repo the issue targets, per the record's `repo` field.
	RepoDID        string    `json:"repoDid"`
	CreatedAt      string    `json:"createdAt"`
	StateUpdatedAt string    `json:"stateUpdatedAt,omitempty"`
	CommentCount   int       `json:"commentCount"`
	Labels         []Label   `json:"labels,omitempty"`
	References     []string  `json:"references,omitempty"`
	Comments       []Comment `json:"comments,omitempty"`
	// AppviewNumber is the sequential #42 the tangled.org web UI shows. It is
	// allocated by that appview's own counter, appears in no record and in no
	// API, and is therefore absent unless tgl was given it. Never key on it.
	AppviewNumber *int `json:"appviewNumber,omitempty"`
}

// PullRound is one revision of a pull request.
type PullRound struct {
	Index     int    `json:"index"`
	CreatedAt string `json:"createdAt"`
	BlobCID   string `json:"blobCid,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// PullVersionRange is the undocumented commit-range shape seen on some live
// pull records. It is reported verbatim and never written.
type PullVersionRange struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	CreatedAt string `json:"createdAt"`
}

// Pull is the schema for `tgl pr list` items and `tgl pr view`.
type Pull struct {
	URI           string `json:"uri"`
	CID           string `json:"cid,omitempty"`
	RKey          string `json:"rkey"`
	Title         string `json:"title"`
	Body          string `json:"body,omitempty"`
	State         string `json:"state"`
	Author        Actor  `json:"author"`
	TargetRepoDID string `json:"targetRepoDid"`
	TargetBranch  string `json:"targetBranch"`
	SourceBranch  string `json:"sourceBranch,omitempty"`
	SourceRepoDID string `json:"sourceRepoDid,omitempty"`
	// Shape is "patch", "branch", or "fork", inferred from the source field:
	// absent source means a patch was pasted, a source without a repo means a
	// branch in the same repo, and a source with a repo means a fork.
	Shape          string             `json:"shape"`
	Stacked        bool               `json:"stacked"`
	DependentOn    string             `json:"dependentOn,omitempty"`
	CreatedAt      string             `json:"createdAt"`
	StateUpdatedAt string             `json:"stateUpdatedAt,omitempty"`
	CommentCount   int                `json:"commentCount"`
	Rounds         []PullRound        `json:"rounds"`
	Versions       []PullVersionRange `json:"versions,omitempty"`
	Labels         []Label            `json:"labels,omitempty"`
	Comments       []Comment          `json:"comments,omitempty"`
	Patch          string             `json:"patch,omitempty"`
	AppviewNumber  *int               `json:"appviewNumber,omitempty"`
}

// IssueList is the `tgl issue list` envelope.
type IssueList struct {
	Repo   RepoRef `json:"repo"`
	Issues []Issue `json:"issues"`
	Count  int     `json:"count"`
	Cursor string  `json:"cursor,omitempty"`
}

// PullList is the `tgl pr list` envelope.
type PullList struct {
	Repo   RepoRef `json:"repo"`
	Pulls  []Pull  `json:"pulls"`
	Count  int     `json:"count"`
	Cursor string  `json:"cursor,omitempty"`
}

// RepoSummary is one entry in `tgl repo list`. It is the repo record as
// written, without the knot-endpoint and handle lookups `tgl repo view` does
// per repository.
type RepoSummary struct {
	Name        string `json:"name"`
	RKey        string `json:"rkey"`
	AtURI       string `json:"atUri"`
	RepoDID     string `json:"repoDid,omitempty"`
	Knot        string `json:"knot,omitempty"`
	Description string `json:"description,omitempty"`
	Spindle     string `json:"spindle,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// RepoList is the `tgl repo list` envelope.
type RepoList struct {
	Owner Actor         `json:"owner"`
	Repos []RepoSummary `json:"repos"`
	Count int           `json:"count"`
}

// Cloned is the `tgl repo clone` schema.
type Cloned struct {
	Repo      RepoRef `json:"repo"`
	URL       string  `json:"url"`
	Protocol  string  `json:"protocol"` // https or ssh
	Directory string  `json:"directory"`
}

// RepoCreated is the `tgl repo create` schema. Unlike every other write
// command, creation touches two services: the knot mints the repo DID, and only
// then is the record written to the PDS.
type RepoCreated struct {
	URI           string `json:"uri"`
	CID           string `json:"cid,omitempty"`
	RKey          string `json:"rkey"`
	Name          string `json:"name"`
	Owner         Actor  `json:"owner"`
	Knot          string `json:"knot"`
	RepoDID       string `json:"repoDid"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url,omitempty"`
	CloneURL      string `json:"cloneUrl,omitempty"`
	Action        string `json:"action"`
}

// Browsed is the `tgl browse` schema. Opened is false whenever tgl only printed
// the URL — in JSON mode, without a terminal, or with --no-open.
type Browsed struct {
	URL    string `json:"url"`
	Opened bool   `json:"opened"`
}

// Created is the envelope returned by every write command.
type Created struct {
	URI    string `json:"uri"`
	CID    string `json:"cid,omitempty"`
	RKey   string `json:"rkey"`
	Action string `json:"action"`
	// URL is the tangled.org web URL when one can be constructed.
	URL string `json:"url,omitempty"`
}

// AuthStatus is the `tgl auth status` schema.
type AuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	DID           string `json:"did,omitempty"`
	Handle        string `json:"handle,omitempty"`
	PDS           string `json:"pds,omitempty"`
	Source        string `json:"source,omitempty"` // "keychain" or "environment"
}
