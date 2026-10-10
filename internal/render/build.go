package render

import (
	"encoding/json"
	"sort"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/tangled"
)

// ActorFunc maps a DID to a display actor. Implementations should be
// best-effort: returning Actor{DID: did} when the handle cannot be resolved is
// correct behaviour, not an error.
type ActorFunc func(did string) Actor

// BuildIssue converts a Bobbin list item or record into the tgl schema.
func BuildIssue(it bobbin.ListItem, actor ActorFunc) (Issue, error) {
	var rec tangled.Issue
	if err := json.Unmarshal(it.Value, &rec); err != nil {
		return Issue{}, err
	}
	u, err := tangled.ParseATURI(it.URI)
	if err != nil {
		return Issue{}, err
	}
	state := it.State
	if state == "" {
		// Single-record lookups do not derive state; absent a state record an
		// issue is open (core:lexicons/issue/state.json default).
		state = "open"
	}
	return Issue{
		URI:            it.URI,
		CID:            it.CID,
		RKey:           u.RKey,
		Title:          rec.Title,
		Body:           rec.Body,
		State:          state,
		Author:         actor(u.Authority),
		RepoDID:        rec.Repo,
		CreatedAt:      rec.CreatedAt,
		StateUpdatedAt: it.StateUpdatedAt,
		CommentCount:   it.CommentCount,
		References:     rec.References,
	}, nil
}

// BuildPull converts a Bobbin list item or record into the tgl schema.
func BuildPull(it bobbin.ListItem, actor ActorFunc) (Pull, error) {
	var rec tangled.Pull
	if err := json.Unmarshal(it.Value, &rec); err != nil {
		return Pull{}, err
	}
	u, err := tangled.ParseATURI(it.URI)
	if err != nil {
		return Pull{}, err
	}
	state := it.State
	if state == "" {
		state = "open"
	}

	p := Pull{
		URI:            it.URI,
		CID:            it.CID,
		RKey:           u.RKey,
		Title:          rec.Title,
		Body:           rec.Body,
		State:          state,
		Author:         actor(u.Authority),
		TargetRepoDID:  rec.Target.Repo,
		TargetBranch:   rec.Target.Branch,
		Shape:          PullShape(&rec),
		Stacked:        rec.DependentOn != "",
		DependentOn:    rec.DependentOn,
		CreatedAt:      rec.CreatedAt,
		StateUpdatedAt: it.StateUpdatedAt,
		CommentCount:   it.CommentCount,
		Rounds:         []PullRound{},
	}
	if rec.Source != nil {
		p.SourceBranch = rec.Source.Branch
		p.SourceRepoDID = rec.Source.Repo
	}
	for i, r := range rec.Rounds {
		p.Rounds = append(p.Rounds, PullRound{
			Index:     i,
			CreatedAt: r.CreatedAt,
			BlobCID:   r.PatchBlob.Ref.Link,
			Size:      r.PatchBlob.Size,
		})
	}
	for _, v := range rec.Versions {
		p.Versions = append(p.Versions, PullVersionRange{Base: v.Base, Head: v.Head, CreatedAt: v.CreatedAt})
	}
	return p, nil
}

// PullShape classifies a pull by its source field, matching the three handlers
// in core:appview/pulls/create.go: no source at all means a pasted patch, a
// source branch alone means a branch in the same repo, and a source carrying a
// repo DID means a fork.
func PullShape(rec *tangled.Pull) string {
	switch {
	case rec.Source == nil:
		return "patch"
	case rec.Source.Repo != "" && rec.Source.Repo != rec.Target.Repo:
		return "fork"
	default:
		return "branch"
	}
}

// BuildComments normalises sh.tangled.feed.comment records, oldest first.
func BuildComments(items []bobbin.ListItem, actor ActorFunc) []Comment {
	out := make([]Comment, 0, len(items))
	for _, it := range items {
		u, err := tangled.ParseATURI(it.URI)
		if err != nil {
			continue
		}
		c := Comment{
			URI:    it.URI,
			CID:    it.CID,
			RKey:   u.RKey,
			Author: actor(u.Authority),
			Legacy: u.Collection != tangled.NSIDFeedComment,
		}
		if c.Legacy {
			var legacy tangled.LegacyComment
			if err := json.Unmarshal(it.Value, &legacy); err != nil {
				continue
			}
			c.Body = legacy.Body
			c.CreatedAt = legacy.CreatedAt
			c.ReplyTo = legacy.ReplyTo
		} else {
			var fc tangled.FeedComment
			if err := json.Unmarshal(it.Value, &fc); err != nil {
				continue
			}
			c.Body = fc.Body.Text
			if c.Body == "" {
				c.Body = fc.Body.Original
			}
			c.CreatedAt = fc.CreatedAt
			c.PullRoundIdx = fc.PullRoundIdx
			if fc.ReplyTo != nil {
				c.ReplyTo = fc.ReplyTo.URI
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// FoldLabels replays a label op log into the current label set.
//
// Labels are not strings on the issue record: a sh.tangled.label.definition
// declares the label, and each sh.tangled.label.op adds or deletes an operand
// pointing at that definition. defs maps definition AT-URIs to their records and
// may be nil, in which case only the key is reported.
func FoldLabels(ops []bobbin.ListItem, defs map[string]*tangled.LabelDefinition) []Label {
	type entry struct{ key, value string }
	set := map[entry]bool{}
	order := []entry{}

	for _, it := range ops {
		var op tangled.LabelOp
		if err := json.Unmarshal(it.Value, &op); err != nil {
			continue
		}
		for _, d := range op.Delete {
			e := entry{d.Key, d.Value}
			if set[e] {
				delete(set, e)
			}
		}
		for _, a := range op.Add {
			e := entry{a.Key, a.Value}
			if !set[e] {
				set[e] = true
				order = append(order, e)
			}
		}
	}

	out := []Label{}
	emitted := map[entry]bool{}
	for _, e := range order {
		// A label deleted and then re-added appears in `order` twice; emit it
		// once, at its first position.
		if !set[e] || emitted[e] {
			continue
		}
		emitted[e] = true
		l := Label{Key: e.key, Value: e.value}
		if d, ok := defs[e.key]; ok && d != nil {
			l.Name = d.Name
			l.Color = d.Color
		}
		out = append(out, l)
	}
	return out
}
