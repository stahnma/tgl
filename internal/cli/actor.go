package cli

import (
	"context"
	"sync"

	"tangled.org/tgl/internal/render"
)

// actorResolver turns DIDs into display actors, resolving handles concurrently
// and tolerating failure: an unresolvable DID renders as the DID.
type actorResolver struct {
	app   *App
	mu    sync.Mutex
	cache map[string]render.Actor
}

func (a *App) actors() *actorResolver {
	return &actorResolver{app: a, cache: map[string]render.Actor{}}
}

// warm resolves a batch of DIDs concurrently so that the subsequent per-DID
// lookups are all cache hits.
func (r *actorResolver) warm(ctx context.Context, dids []string) {
	todo := map[string]bool{}
	r.mu.Lock()
	for _, d := range dids {
		if d == "" {
			continue
		}
		if _, ok := r.cache[d]; !ok {
			todo[d] = true
		}
	}
	r.mu.Unlock()
	if len(todo) == 0 {
		return
	}

	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for did := range todo {
		wg.Add(1)
		go func(did string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			a := render.Actor{DID: did}
			if doc, err := r.app.IDs().Document(ctx, did); err == nil {
				a.Handle = doc.Handle()
			}
			r.mu.Lock()
			r.cache[did] = a
			r.mu.Unlock()
		}(did)
	}
	wg.Wait()
}

// fn returns a render.ActorFunc backed by this resolver's cache.
func (r *actorResolver) fn() render.ActorFunc {
	return func(did string) render.Actor {
		r.mu.Lock()
		defer r.mu.Unlock()
		if a, ok := r.cache[did]; ok {
			return a
		}
		return render.Actor{DID: did}
	}
}
