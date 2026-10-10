// Package atproto is a minimal com.atproto.* client: sessions, record CRUD,
// blobs, and service auth.
package atproto

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"tangled.org/tgl/internal/httpx"
)

// Client talks to a single PDS.
type Client struct {
	http *httpx.Client
	DID  string
	// RefreshJwt is the most recent refresh token seen for this client. The PDS
	// rotates it on every refresh, so callers must re-persist it.
	RefreshJwt string
}

func New(pds string) *Client {
	return &Client{http: httpx.New(pds, 0)}
}

// Host returns the PDS base URL.
func (c *Client) Host() string { return c.http.Base }

// SetToken sets the bearer token used for authenticated calls.
func (c *Client) SetToken(tok string) { c.http.Token = tok }

// Session is the result of createSession or refreshSession.
type Session struct {
	DID        string `json:"did"`
	Handle     string `json:"handle"`
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
	Email      string `json:"email,omitempty"`
}

// CreateSession exchanges an identifier and app password for a session.
func (c *Client) CreateSession(ctx context.Context, identifier, password string) (*Session, error) {
	in := map[string]string{"identifier": identifier, "password": password}
	var out Session
	if err := c.http.Procedure(ctx, "com.atproto.server.createSession", in, &out); err != nil {
		return nil, err
	}
	c.DID = out.DID
	c.RefreshJwt = out.RefreshJwt
	c.SetToken(out.AccessJwt)
	return &out, nil
}

// RefreshSession trades a refresh token for a fresh access token. The refresh
// token itself is rotated, so the caller must persist the new one.
func (c *Client) RefreshSession(ctx context.Context, refreshJwt string) (*Session, error) {
	prev := c.http.Token
	c.SetToken(refreshJwt)
	var out Session
	err := c.http.Procedure(ctx, "com.atproto.server.refreshSession", nil, &out)
	if err != nil {
		c.SetToken(prev)
		return nil, err
	}
	c.DID = out.DID
	c.RefreshJwt = out.RefreshJwt
	c.SetToken(out.AccessJwt)
	return &out, nil
}

// GetServiceAuth mints a short-lived token scoped to one audience and one
// lexicon method. Knots validate both: the audience is a did:web derived from
// the knot hostname, and the lxm must equal the NSID being called
// (core:xrpc/serviceauth/service_auth.go:46-60).
//
// expSeconds is a duration from now. The appview clamps to a 60 second floor
// (core:appview/oauth/oauth.go:425-429); we do the same.
func (c *Client) GetServiceAuth(ctx context.Context, aud, lxm string, expSeconds int64) (string, error) {
	if expSeconds < 60 {
		expSeconds = 60
	}
	params := url.Values{
		"aud": {aud},
		"exp": {strconv.FormatInt(time.Now().Unix()+expSeconds, 10)},
		"lxm": {lxm},
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.http.Query(ctx, "com.atproto.server.getServiceAuth", params, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// RecordRef identifies a record written to a PDS.
type RecordRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

// GetRecordOut is the com.atproto.repo.getRecord response.
type GetRecordOut struct {
	URI   string          `json:"uri"`
	CID   string          `json:"cid"`
	Value json.RawMessage `json:"value"`
}

// GetRecord fetches a single record.
func (c *Client) GetRecord(ctx context.Context, repo, collection, rkey string) (*GetRecordOut, error) {
	params := url.Values{"repo": {repo}, "collection": {collection}, "rkey": {rkey}}
	var out GetRecordOut
	if err := c.http.Query(ctx, "com.atproto.repo.getRecord", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutRecord writes a record at an explicit rkey. This is what the appview does
// everywhere (it mints its own TID rkeys), rather than createRecord.
//
// swapCID, when non-empty, makes the write a compare-and-swap against the
// record's current CID.
func (c *Client) PutRecord(ctx context.Context, repo, collection, rkey string, record any, swapCID string) (*RecordRef, error) {
	in := map[string]any{
		"repo":       repo,
		"collection": collection,
		"rkey":       rkey,
		"record":     record,
	}
	if swapCID != "" {
		in["swapRecord"] = swapCID
	}
	var out RecordRef
	if err := c.http.Procedure(ctx, "com.atproto.repo.putRecord", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRecord writes a record at an explicit rkey and fails if one already
// exists there.
//
// Repo creation uses this rather than PutRecord: the rkey of a
// sh.tangled.repo record is the repo name, not a TID, so a put would silently
// overwrite an existing repository's record. The appview makes the same choice
// (core:appview/state/state.go:641-649).
func (c *Client) CreateRecord(ctx context.Context, repo, collection, rkey string, record any) (*RecordRef, error) {
	in := map[string]any{
		"repo":       repo,
		"collection": collection,
		"rkey":       rkey,
		"record":     record,
	}
	var out RecordRef
	if err := c.http.Procedure(ctx, "com.atproto.repo.createRecord", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRecord removes a record.
func (c *Client) DeleteRecord(ctx context.Context, repo, collection, rkey string) error {
	in := map[string]any{"repo": repo, "collection": collection, "rkey": rkey}
	return c.http.Procedure(ctx, "com.atproto.repo.deleteRecord", in, nil)
}

// ListRecordsItem is one entry from com.atproto.repo.listRecords.
type ListRecordsItem struct {
	URI   string          `json:"uri"`
	CID   string          `json:"cid"`
	Value json.RawMessage `json:"value"`
}

// ListRecords enumerates a collection on a PDS. This is the fallback read path
// when Bobbin is unavailable.
func (c *Client) ListRecords(ctx context.Context, repo, collection string, limit int, cursor string) ([]ListRecordsItem, string, error) {
	params := url.Values{"repo": {repo}, "collection": {collection}}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	var out struct {
		Records []ListRecordsItem `json:"records"`
		Cursor  string            `json:"cursor"`
	}
	if err := c.http.Query(ctx, "com.atproto.repo.listRecords", params, &out); err != nil {
		return nil, "", err
	}
	return out.Records, out.Cursor, nil
}

// UploadBlob stores bytes on the authenticated user's PDS and returns the blob
// reference to embed in a record.
func (c *Client) UploadBlob(ctx context.Context, contentType string, data []byte) (json.RawMessage, error) {
	var out struct {
		Blob json.RawMessage `json:"blob"`
	}
	// Patch blobs can be large; give uploads their own budget.
	prev := c.http.HTTP.Timeout
	c.http.HTTP.Timeout = 60 * time.Second
	defer func() { c.http.HTTP.Timeout = prev }()

	if err := c.http.Upload(ctx, "com.atproto.repo.uploadBlob", contentType, data, &out); err != nil {
		return nil, err
	}
	if len(out.Blob) == 0 {
		return nil, fmt.Errorf("uploadBlob returned no blob")
	}
	return out.Blob, nil
}

// GetBlob fetches blob bytes from the PDS that holds them.
func (c *Client) GetBlob(ctx context.Context, did, cid string) ([]byte, error) {
	prev := c.http.HTTP.Timeout
	c.http.HTTP.Timeout = 60 * time.Second
	defer func() { c.http.HTTP.Timeout = prev }()
	return c.http.GetRaw(ctx, "com.atproto.sync.getBlob", url.Values{"did": {did}, "cid": {cid}})
}
