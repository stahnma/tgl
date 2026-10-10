// Package resolve turns a repo reference — a --repo flag or the git remote in
// the working directory — into every identifier the various APIs want.
//
// Four identifiers are in play, and different endpoints want different ones:
//
//	owner DID   at-uri for sh.tangled.repo.getRepo, subject for listRepos
//	repo at-uri sh.tangled.repo.getRepo
//	repo DID    subject for listIssues/listPulls, issue.repo, pull.target.repo,
//	            and every knot procedure
//	knot host   knot XRPC base URL and the did:web service-auth audience
package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tangled.org/tgl/internal/bobbin"
	"tangled.org/tgl/internal/gitutil"
	"tangled.org/tgl/internal/identity"
	"tangled.org/tgl/internal/tangled"
)

// Repo is a fully resolved repository.
type Repo struct {
	OwnerHandle  string   `json:"ownerHandle,omitempty"`
	OwnerDID     string   `json:"ownerDid"`
	Name         string   `json:"name"`
	RKey         string   `json:"rkey"`
	AtURI        string   `json:"atUri"`
	RepoDID      string   `json:"repoDid,omitempty"`
	Knot         string   `json:"knot"`
	KnotEndpoint string   `json:"knotEndpoint"`
	Description  string   `json:"description,omitempty"`
	Spindle      string   `json:"spindle,omitempty"`
	Labels       []string `json:"labels,omitempty"`

	Record *tangled.Repo `json:"-"`
}

// Subject is the identifier Bobbin's listIssues/listPulls want. They key on the
// repo DID, so a repo predating v1.13 cannot be listed through Bobbin.
func (r *Repo) Subject() (string, error) {
	if r.RepoDID == "" {
		return "", fmt.Errorf("repo %s has no repoDid; Bobbin's issue and pull listings key on the repo DID, so this repo cannot be listed (it predates Tangled v1.13)", r.Slug())
	}
	return r.RepoDID, nil
}

// KnotIdentifier is the `repo` parameter for knot procedures: the repo DID when
// one exists, else "<ownerDID>/<rkey>" for knots without repo-did-input.
func (r *Repo) KnotIdentifier() string {
	if r.RepoDID != "" {
		return r.RepoDID
	}
	return r.OwnerDID + "/" + r.RKey
}

// Slug is the human-facing "owner/name" form.
func (r *Repo) Slug() string {
	owner := r.OwnerHandle
	if owner == "" {
		owner = r.OwnerDID
	}
	return owner + "/" + r.Name
}

// Resolver resolves repo references.
type Resolver struct {
	Bobbin *bobbin.Client
	IDs    *identity.Resolver
}

func New(b *bobbin.Client, ids *identity.Resolver) *Resolver {
	return &Resolver{Bobbin: b, IDs: ids}
}

// FromWorkingDir finds a Tangled remote in dir and resolves it. remote names a
// specific remote; when empty, origin is preferred, then the first remote that
// looks like Tangled, then the first remote at all.
func (r *Resolver) FromWorkingDir(ctx context.Context, dir, remote string) (*Repo, error) {
	if !gitutil.InRepo(ctx, dir) {
		return nil, fmt.Errorf("not inside a git repository; pass --repo owner/name")
	}
	rem, err := pickRemote(ctx, dir, remote)
	if err != nil {
		return nil, err
	}
	return r.FromRemote(ctx, rem)
}

func pickRemote(ctx context.Context, dir, want string) (gitutil.Remote, error) {
	if want != "" {
		u, err := gitutil.RemoteURL(ctx, dir, want)
		if err != nil {
			return gitutil.Remote{}, err
		}
		return gitutil.ParseRemote(u)
	}
	names, err := gitutil.Remotes(ctx, dir)
	if err != nil {
		return gitutil.Remote{}, err
	}
	if len(names) == 0 {
		return gitutil.Remote{}, fmt.Errorf("no git remotes configured; pass --repo owner/name")
	}
	ordered := names
	for i, n := range names {
		if n == "origin" {
			ordered = append([]string{"origin"}, append(append([]string{}, names[:i]...), names[i+1:]...)...)
			break
		}
	}
	var first gitutil.Remote
	var firstErr error
	for i, n := range ordered {
		u, err := gitutil.RemoteURL(ctx, dir, n)
		if err != nil {
			continue
		}
		parsed, err := gitutil.ParseRemote(u)
		if err != nil {
			if i == 0 {
				firstErr = err
			}
			continue
		}
		if parsed.IsTangled() {
			return parsed, nil
		}
		if first.Raw == "" {
			first = parsed
		}
	}
	if first.Raw != "" {
		return first, nil
	}
	if firstErr != nil {
		return gitutil.Remote{}, firstErr
	}
	return gitutil.Remote{}, fmt.Errorf("no usable Tangled remote found; pass --repo owner/name")
}

// FromRemote resolves a parsed git remote.
func (r *Resolver) FromRemote(ctx context.Context, rem gitutil.Remote) (*Repo, error) {
	if rem.RepoDID != "" {
		return r.FromRepoDID(ctx, rem.RepoDID)
	}
	return r.FromSpec(ctx, rem.Owner+"/"+rem.Name)
}

// FromSpec resolves "owner/name", where owner is a handle or a DID. A bare repo
// DID is also accepted.
func (r *Resolver) FromSpec(ctx context.Context, spec string) (*Repo, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty repo spec")
	}
	if strings.HasPrefix(spec, "at://") {
		return r.fromAtURI(ctx, spec)
	}
	// A bare repo DID, with no owner/name split.
	if strings.HasPrefix(spec, "did:") && !strings.Contains(spec, "/") {
		return r.FromRepoDID(ctx, spec)
	}
	owner, name, ok := strings.Cut(strings.TrimPrefix(spec, "@"), "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repo must be owner/name, got %q", spec)
	}
	name = strings.TrimSuffix(name, ".git")

	ownerDID, err := r.IDs.ResolveHandle(ctx, owner)
	if err != nil {
		return nil, err
	}

	// The common case: rkey equals the name. One request.
	atURI := tangled.MakeATURI(ownerDID, tangled.NSIDRepo, name)
	rec, err := r.Bobbin.GetRepo(ctx, atURI)
	if err == nil {
		repo, perr := r.build(ctx, rec, ownerDID, owner)
		if perr != nil {
			return nil, perr
		}
		// Guard against an rkey collision where the record's cosmetic name
		// disagrees with what the user asked for.
		if repo.Name == name {
			return repo, nil
		}
	} else if !bobbin.NotFound(err) {
		return nil, err
	}

	// Fallback: the repo has a cosmetic `name` distinct from its rkey.
	return r.findByName(ctx, ownerDID, owner, name)
}

func (r *Resolver) findByName(ctx context.Context, ownerDID, ownerHandle, name string) (*Repo, error) {
	items, err := bobbin.ListAll(ctx, 1000, func(ctx context.Context, limit int, cursor string) ([]bobbin.ListItem, string, error) {
		return r.Bobbin.ListRepos(ctx, ownerDID, limit, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("listing repos for %s: %w", ownerDID, err)
	}
	for _, it := range items {
		var rec tangled.Repo
		if err := json.Unmarshal(it.Value, &rec); err != nil {
			continue
		}
		u, err := tangled.ParseATURI(it.URI)
		if err != nil {
			continue
		}
		if rec.Slug(u.RKey) == name {
			return r.build(ctx, &bobbin.Record{URI: it.URI, CID: it.CID, Value: it.Value}, ownerDID, ownerHandle)
		}
	}
	owner := ownerHandle
	if owner == "" {
		owner = ownerDID
	}
	return nil, fmt.Errorf("no repository %q owned by %s", name, owner)
}

// FromRepoDID resolves a repo by its own DID.
func (r *Resolver) FromRepoDID(ctx context.Context, repoDID string) (*Repo, error) {
	rec, err := r.Bobbin.GetRepoByRepoDid(ctx, repoDID)
	if err != nil {
		return nil, fmt.Errorf("resolving repo DID %s: %w", repoDID, err)
	}
	u, err := tangled.ParseATURI(rec.URI)
	if err != nil {
		return nil, err
	}
	return r.build(ctx, rec, u.Authority, "")
}

func (r *Resolver) fromAtURI(ctx context.Context, atURI string) (*Repo, error) {
	u, err := tangled.ParseATURI(atURI)
	if err != nil {
		return nil, err
	}
	rec, err := r.Bobbin.GetRepo(ctx, atURI)
	if err != nil {
		return nil, err
	}
	return r.build(ctx, rec, u.Authority, "")
}

func (r *Resolver) build(ctx context.Context, rec *bobbin.Record, ownerDID, ownerHandle string) (*Repo, error) {
	var value tangled.Repo
	if err := json.Unmarshal(rec.Value, &value); err != nil {
		return nil, fmt.Errorf("decoding repo record %s: %w", rec.URI, err)
	}
	u, err := tangled.ParseATURI(rec.URI)
	if err != nil {
		return nil, err
	}
	if ownerDID == "" {
		ownerDID = u.Authority
	}
	repo := &Repo{
		OwnerHandle: ownerHandle,
		OwnerDID:    ownerDID,
		Name:        value.Slug(u.RKey),
		RKey:        u.RKey,
		AtURI:       rec.URI,
		RepoDID:     value.RepoDid,
		Knot:        value.Knot,
		Description: value.Description,
		Spindle:     value.Spindle,
		Labels:      value.Labels,
		Record:      &value,
	}

	// Prefer the knot declared by the repo DID's document; the record's `knot`
	// string is a convenience copy that can lag.
	if repo.RepoDID != "" {
		if doc, err := r.IDs.Document(ctx, repo.RepoDID); err == nil {
			if e := doc.Knot(); e != "" {
				repo.KnotEndpoint = e
				if h := hostOf(e); h != "" {
					repo.Knot = h
				}
			}
		}
	}
	if repo.KnotEndpoint == "" && repo.Knot != "" {
		repo.KnotEndpoint = schemeFor(repo.Knot) + repo.Knot
	}

	if repo.OwnerHandle == "" {
		if doc, err := r.IDs.Document(ctx, ownerDID); err == nil {
			repo.OwnerHandle = doc.Handle()
		}
	}
	return repo, nil
}

func hostOf(endpoint string) string {
	s := endpoint
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSuffix(strings.Trim(s, "/"), ":")
}

func schemeFor(host string) string {
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		return "http://"
	}
	return "https://"
}
