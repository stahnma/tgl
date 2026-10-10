// Package tangled holds the sh.tangled.* record shapes tgl reads and writes.
//
// Field names and optionality follow the lexicons in the Tangled monorepo
// (core:lexicons/) exactly. Where a field is absent from the lexicon it is
// absent here; see docs/protocol.md for the audit.
package tangled

// Collection NSIDs.
const (
	NSIDRepo        = "sh.tangled.repo"
	NSIDIssue       = "sh.tangled.repo.issue"
	NSIDIssueState  = "sh.tangled.repo.issue.state"
	NSIDPull        = "sh.tangled.repo.pull"
	NSIDPullStatus  = "sh.tangled.repo.pull.status"
	NSIDFeedComment = "sh.tangled.feed.comment"
	NSIDMarkdown    = "sh.tangled.markup.markdown"
	NSIDLabelDefn   = "sh.tangled.label.definition"
	NSIDLabelOp     = "sh.tangled.label.op"

	// Legacy comment collections, superseded by sh.tangled.feed.comment.
	// tgl reads these but never writes them.
	NSIDIssueComment = "sh.tangled.repo.issue.comment"
	NSIDPullComment  = "sh.tangled.repo.pull.comment"
)

// State tokens.
const (
	IssueStateOpen   = "sh.tangled.repo.issue.state.open"
	IssueStateClosed = "sh.tangled.repo.issue.state.closed"

	PullStatusOpen   = "sh.tangled.repo.pull.status.open"
	PullStatusClosed = "sh.tangled.repo.pull.status.closed"
	PullStatusMerged = "sh.tangled.repo.pull.status.merged"
)

// Blob is an atproto blob reference.
type Blob struct {
	Type     string `json:"$type"`
	Ref      Link   `json:"ref"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

type Link struct {
	Link string `json:"$link"`
}

// StrongRef is com.atproto.repo.strongRef.
type StrongRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

// Repo is sh.tangled.repo (core:lexicons/repo/repo.json).
type Repo struct {
	Type        string   `json:"$type,omitempty"`
	Name        string   `json:"name,omitempty"`
	Knot        string   `json:"knot"`
	Spindle     string   `json:"spindle,omitempty"`
	Description string   `json:"description,omitempty"`
	Website     string   `json:"website,omitempty"`
	Topics      []string `json:"topics,omitempty"`
	Source      string   `json:"source,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	RepoDid     string   `json:"repoDid,omitempty"`
	CreatedAt   string   `json:"createdAt"`
}

// Slug is the name the web UI addresses this repo by. Mirrors
// core:appview/models/repo.go:79-84 — the cosmetic name when set, else the rkey.
func (r *Repo) Slug(rkey string) string {
	if r.Name != "" {
		return r.Name
	}
	return rkey
}

// Issue is sh.tangled.repo.issue (core:lexicons/issue/issue.json).
//
// Repo is the repo DID, not an AT-URI. There is deliberately no number field:
// the sequential #42 seen in the web UI is allocated by the appview's
// repo_issue_seqs counter and exists in no record.
type Issue struct {
	Type       string   `json:"$type,omitempty"`
	Repo       string   `json:"repo"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	CreatedAt  string   `json:"createdAt"`
	Mentions   []string `json:"mentions,omitempty"`
	References []string `json:"references,omitempty"`
	Blobs      []Blob   `json:"blobs,omitempty"`
}

// IssueState is sh.tangled.repo.issue.state.
type IssueState struct {
	Type      string `json:"$type,omitempty"`
	Issue     string `json:"issue"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
}

// PullTarget and PullSource carry repo DIDs.
type PullTarget struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

type PullSource struct {
	Branch string `json:"branch"`
	Repo   string `json:"repo,omitempty"`
}

// PullRound is one revision of a pull request. The blob is gzipped
// git-format-patch text.
type PullRound struct {
	CreatedAt string `json:"createdAt"`
	PatchBlob Blob   `json:"patchBlob"`
}

// PullVersion is an undocumented shape observed in production records: pulls
// with an empty rounds array and a commit range instead. It appears in no
// lexicon in core@83cd295. tgl preserves it on read and never writes it.
type PullVersion struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	CreatedAt string `json:"createdAt"`
}

// Pull is sh.tangled.repo.pull (core:lexicons/pulls/pull.json).
type Pull struct {
	Type        string        `json:"$type,omitempty"`
	Title       string        `json:"title"`
	Body        string        `json:"body,omitempty"`
	Rounds      []PullRound   `json:"rounds"`
	Source      *PullSource   `json:"source,omitempty"`
	Target      PullTarget    `json:"target"`
	CreatedAt   string        `json:"createdAt"`
	Mentions    []string      `json:"mentions,omitempty"`
	References  []string      `json:"references,omitempty"`
	DependentOn string        `json:"dependentOn,omitempty"`
	Blobs       []Blob        `json:"blobs,omitempty"`
	Versions    []PullVersion `json:"versions,omitempty"`
}

// PullStatus is sh.tangled.repo.pull.status. Note the NSID says "status" even
// though the lexicon file is named state.json.
type PullStatus struct {
	Type      string `json:"$type,omitempty"`
	Pull      string `json:"pull"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// Markdown is sh.tangled.markup.markdown, the only member of the feed comment
// body union.
type Markdown struct {
	Type     string `json:"$type"`
	Original string `json:"original,omitempty"`
	Text     string `json:"text"`
	Blobs    []Blob `json:"blobs,omitempty"`
}

// FeedComment is sh.tangled.feed.comment, the current comment record for both
// issues and pulls. The legacy per-collection comment records are read-only.
type FeedComment struct {
	Type         string     `json:"$type,omitempty"`
	Subject      StrongRef  `json:"subject"`
	Body         Markdown   `json:"body"`
	CreatedAt    string     `json:"createdAt"`
	ReplyTo      *StrongRef `json:"replyTo,omitempty"`
	PullRoundIdx *int       `json:"pullRoundIdx,omitempty"`
}

// LegacyComment covers both sh.tangled.repo.issue.comment and
// sh.tangled.repo.pull.comment, which share a shape apart from the subject key.
type LegacyComment struct {
	Type      string `json:"$type,omitempty"`
	Issue     string `json:"issue,omitempty"`
	Pull      string `json:"pull,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	ReplyTo   string `json:"replyTo,omitempty"`
}

// LabelDefinition is sh.tangled.label.definition.
type LabelDefinition struct {
	Type      string   `json:"$type,omitempty"`
	Name      string   `json:"name"`
	Scope     []string `json:"scope"`
	Color     string   `json:"color,omitempty"`
	Multiple  bool     `json:"multiple,omitempty"`
	CreatedAt string   `json:"createdAt"`
	ValueType struct {
		Type   string   `json:"type"`
		Format string   `json:"format"`
		Enum   []string `json:"enum,omitempty"`
	} `json:"valueType"`
}

// LabelOperand is one add/delete entry in a label op.
type LabelOperand struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// LabelOp is sh.tangled.label.op. Current labels are the fold of the op log.
type LabelOp struct {
	Type        string         `json:"$type,omitempty"`
	Subject     string         `json:"subject"`
	Add         []LabelOperand `json:"add"`
	Delete      []LabelOperand `json:"delete"`
	PerformedAt string         `json:"performedAt"`
}
