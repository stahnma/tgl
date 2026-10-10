package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/render"
	"tangled.org/tgl/internal/resolve"
	"tangled.org/tgl/internal/tangled"
)

// labelDefs fetches the definitions a repo subscribes to, keyed by AT-URI.
//
// The repo record's `labels` field lists definition AT-URIs; Bobbin exposes
// definitions per owning DID, so we group the URIs by authority and list each
// owner once.
func (a *App) labelDefs(ctx context.Context, repo *resolve.Repo) map[string]*tangled.LabelDefinition {
	out := map[string]*tangled.LabelDefinition{}
	if len(repo.Labels) == 0 {
		return out
	}
	owners := map[string]bool{}
	for _, uri := range repo.Labels {
		if u, err := tangled.ParseATURI(uri); err == nil {
			owners[u.Authority] = true
		}
	}
	want := map[string]bool{}
	for _, uri := range repo.Labels {
		want[uri] = true
	}
	for owner := range owners {
		items, err := bobbin.ListAll(ctx, 1000, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
			return a.Bobbin().ListLabelDefinitions(ctx, owner, limit, cursor)
		})
		if err != nil {
			continue // definitions are cosmetic; a failure just costs us names
		}
		for _, it := range items {
			if !want[it.URI] {
				continue
			}
			var d tangled.LabelDefinition
			if err := json.Unmarshal(it.Value, &d); err == nil {
				out[it.URI] = &d
			}
		}
	}
	return out
}

// labelsFor folds the label op log for one subject.
func (a *App) labelsFor(ctx context.Context, subjectURI string, defs map[string]*tangled.LabelDefinition) []render.Label {
	ops, err := bobbin.ListAll(ctx, 500, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
		return a.Bobbin().ListLabelOps(ctx, subjectURI, limit, cursor)
	})
	if err != nil {
		return nil
	}
	return render.FoldLabels(ops, defs)
}

// labelsForAll folds label logs for many subjects concurrently.
func (a *App) labelsForAll(ctx context.Context, uris []string, defs map[string]*tangled.LabelDefinition) map[string][]render.Label {
	out := map[string][]render.Label{}
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, uri := range uris {
		wg.Add(1)
		go func(uri string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ls := a.labelsFor(ctx, uri, defs)
			if len(ls) == 0 {
				return
			}
			mu.Lock()
			out[uri] = ls
			mu.Unlock()
		}(uri)
	}
	wg.Wait()
	return out
}

// matchesLabel reports whether any applied label matches the filter, comparing
// case-insensitively against both the definition name and the raw key.
func matchesLabel(ls []render.Label, filter string) bool {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return true
	}
	for _, l := range ls {
		if strings.ToLower(l.Name) == f || strings.ToLower(l.Key) == f {
			return true
		}
		if l.Value != "" && strings.ToLower(l.Name+":"+l.Value) == f {
			return true
		}
	}
	return false
}
