// Package identity resolves atproto handles and DIDs to the endpoints tgl needs:
// a PDS for record reads and writes, and a knot for git operations.
//
// Repo DIDs (minted by knots since Tangled v1.13) resolve the same way as
// account DIDs, but their service entry points at a knot rather than a PDS. The
// service id/type pair is defined in core:repoident/repoident.go:14-17.
package identity

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	"tangled.org/tgl/internal/httpx"
)

// Service ids and types from core:repoident/repoident.go.
const (
	KnotServiceID         = "tangled_knot"
	KnotServiceType       = "TangledKnot"
	LegacyKnotServiceID   = "atproto_pds"
	LegacyKnotServiceType = "AtprotoPersonalDataServer"

	PDSServiceID   = "atproto_pds"
	PDSServiceType = "AtprotoPersonalDataServer"
)

// PLCDirectory is the default did:plc resolver.
var PLCDirectory = "https://plc.directory"

type service struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	ServiceEndpoint string `json:"serviceEndpoint"`
}

// Document is the subset of a DID document tgl cares about.
type Document struct {
	ID          string    `json:"id"`
	AlsoKnownAs []string  `json:"alsoKnownAs"`
	Service     []service `json:"service"`
}

// Handle returns the primary handle declared by the document, without the
// at:// prefix, or "" if there is none.
func (d *Document) Handle() string {
	for _, aka := range d.AlsoKnownAs {
		if s, ok := strings.CutPrefix(aka, "at://"); ok {
			return s
		}
	}
	return ""
}

func (d *Document) endpoint(id, typ string) string {
	for _, s := range d.Service {
		if strings.TrimPrefix(s.ID, "#") == id && s.Type == typ {
			return s.ServiceEndpoint
		}
	}
	return ""
}

// PDS returns the account's personal data server endpoint.
func (d *Document) PDS() string { return d.endpoint(PDSServiceID, PDSServiceType) }

// Knot returns the knot's base URL for a repo DID, preferring the modern
// tangled_knot service and falling back to the legacy atproto_pds entry, which
// is what knot1.tangled.sh still publishes.
//
// The declared endpoint may carry a path — forks publish
// "https://knot1.tangled.sh/repo/<opaque>" — which is not a git path and not an
// XRPC prefix. core:repoident/repoident.go:88-99 strips it, so we do too: the
// knot is addressed at scheme://host, and a repo at <knot>/<repoDID>.
func (d *Document) Knot() string {
	e := d.endpoint(KnotServiceID, KnotServiceType)
	if e == "" {
		e = d.endpoint(LegacyKnotServiceID, LegacyKnotServiceType)
	}
	return stripPath(e)
}

// stripPath reduces a service endpoint to scheme://host, canonicalising the host
// and dropping a default port.
func stripPath(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	host := strings.ToLower(u.Host)
	if port := u.Port(); port != "" {
		if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
			host = strings.TrimSuffix(host, ":"+port)
		}
	}
	return u.Scheme + "://" + host
}

// Resolver resolves handles and DIDs, caching for the life of the process.
type Resolver struct {
	mu      sync.Mutex
	handles map[string]string
	docs    map[string]*Document
}

func NewResolver() *Resolver {
	return &Resolver{
		handles: map[string]string{},
		docs:    map[string]*Document{},
	}
}

// IsDID reports whether s looks like a DID rather than a handle.
func IsDID(s string) bool { return strings.HasPrefix(s, "did:") }

// ResolveHandle maps a handle to a DID, trying DNS first and then the
// .well-known HTTPS endpoint, per the atproto handle resolution spec.
//
// Bobbin cannot do this for us: sh.tangled.actor.getProfile takes a full profile
// record AT-URI, not a bare handle (docs.tangled.org, Bobbin section).
func (r *Resolver) ResolveHandle(ctx context.Context, handle string) (string, error) {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" {
		return "", fmt.Errorf("empty handle")
	}
	if IsDID(handle) {
		return handle, nil
	}
	r.mu.Lock()
	if did, ok := r.handles[handle]; ok {
		r.mu.Unlock()
		return did, nil
	}
	r.mu.Unlock()

	did, err := resolveHandleUncached(ctx, handle)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.handles[handle] = did
	r.mu.Unlock()
	return did, nil
}

func resolveHandleUncached(ctx context.Context, handle string) (string, error) {
	var dnsErr, httpErr error
	if txts, err := net.DefaultResolver.LookupTXT(ctx, "_atproto."+handle); err == nil {
		for _, txt := range txts {
			if did, ok := strings.CutPrefix(strings.TrimSpace(txt), "did="); ok {
				if IsDID(did) {
					return did, nil
				}
			}
		}
		dnsErr = fmt.Errorf("no did= record")
	} else {
		dnsErr = err
	}

	body, err := httpx.GetText(ctx, "https://"+handle+"/.well-known/atproto-did")
	if err == nil && IsDID(body) {
		return body, nil
	}
	httpErr = err
	if err == nil {
		httpErr = fmt.Errorf("body is not a DID")
	}
	return "", fmt.Errorf("could not resolve handle %q (dns: %v; well-known: %v)", handle, dnsErr, httpErr)
}

// Resolve maps a handle or DID to its DID document.
func (r *Resolver) Resolve(ctx context.Context, handleOrDID string) (*Document, error) {
	did, err := r.ResolveHandle(ctx, handleOrDID)
	if err != nil {
		return nil, err
	}
	return r.Document(ctx, did)
}

// Document fetches and caches the DID document for did.
func (r *Resolver) Document(ctx context.Context, did string) (*Document, error) {
	r.mu.Lock()
	if doc, ok := r.docs[did]; ok {
		r.mu.Unlock()
		return doc, nil
	}
	r.mu.Unlock()

	doc, err := fetchDocument(ctx, did)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.docs[did] = doc
	r.mu.Unlock()
	return doc, nil
}

func fetchDocument(ctx context.Context, did string) (*Document, error) {
	var target string
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		target = PLCDirectory + "/" + did
	case strings.HasPrefix(did, "did:web:"):
		host, err := url.PathUnescape(strings.TrimPrefix(did, "did:web:"))
		if err != nil {
			return nil, fmt.Errorf("invalid did:web %q: %w", did, err)
		}
		target = "https://" + host + "/.well-known/did.json"
	default:
		return nil, fmt.Errorf("unsupported DID method: %s", did)
	}
	var doc Document
	if err := httpx.GetJSON(ctx, target, &doc); err != nil {
		return nil, fmt.Errorf("resolving %s: %w", did, err)
	}
	if doc.ID == "" {
		doc.ID = did
	}
	return &doc, nil
}

// PDSFor returns the PDS endpoint for an account handle or DID.
func (r *Resolver) PDSFor(ctx context.Context, handleOrDID string) (did string, pds string, err error) {
	did, err = r.ResolveHandle(ctx, handleOrDID)
	if err != nil {
		return "", "", err
	}
	doc, err := r.Document(ctx, did)
	if err != nil {
		return "", "", err
	}
	pds = doc.PDS()
	if pds == "" {
		return did, "", fmt.Errorf("%s declares no atproto PDS service endpoint", did)
	}
	return did, pds, nil
}

// DidWeb converts a hostname into the did:web audience a knot expects for
// service auth. Mirrors core:xrpc/serviceauth/service_auth.go:22.
func DidWeb(hostname string) string {
	return "did:web:" + strings.ReplaceAll(hostname, ":", "%3A")
}
