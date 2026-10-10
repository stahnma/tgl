// Package httpx is the shared HTTP/XRPC plumbing for every Tangled service tgl
// talks to: PDSes, Bobbin, knots, and the PLC directory.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout applies to everything except merge, which the appview gives 20s
// (appview/pulls/merge.go). Patch uploads get their own longer budget.
const DefaultTimeout = 15 * time.Second

var UserAgent = "tgl/dev"

// Client wraps net/http with XRPC error decoding.
type Client struct {
	Base  string // e.g. https://api.tangled.org
	HTTP  *http.Client
	Token string // bearer token, optional
}

func New(base string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		Base: strings.TrimRight(base, "/"),
		HTTP: &http.Client{Timeout: timeout},
	}
}

// XRPCError is the standard atproto error envelope: an object with "error" and
// "message" keys.
type XRPCError struct {
	Status  int    `json:"-"`
	Kind    string `json:"error"`
	Message string `json:"message"`
}

func (e *XRPCError) Error() string {
	switch {
	case e.Kind != "" && e.Message != "":
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	case e.Kind != "":
		return e.Kind
	case e.Message != "":
		return e.Message
	default:
		return fmt.Sprintf("http %d", e.Status)
	}
}

// Query performs an XRPC query (GET) against nsid and decodes into out.
func (c *Client) Query(ctx context.Context, nsid string, params url.Values, out any) error {
	u := c.Base + "/xrpc/" + nsid
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

// Procedure performs an XRPC procedure (POST) with a JSON body.
func (c *Client) Procedure(ctx context.Context, nsid string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/xrpc/"+nsid, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

// Upload performs an XRPC procedure with a raw body of the given content type.
func (c *Client) Upload(ctx context.Context, nsid, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/xrpc/"+nsid, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	return c.do(req, out)
}

// GetRaw fetches an XRPC endpoint and returns the undecoded body. Used for
// com.atproto.sync.getBlob, which returns bytes rather than JSON.
func (c *Client) GetRaw(ctx context.Context, nsid string, params url.Values) ([]byte, error) {
	u := c.Base + "/xrpc/" + nsid
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, decodeErr(resp.StatusCode, b)
	}
	return b, nil
}

func (c *Client) decorate(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) do(req *http.Request, out any) error {
	c.decorate(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return decodeErr(resp.StatusCode, b)
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decoding %s response: %w", req.URL.Path, err)
	}
	return nil
}

func decodeErr(status int, body []byte) error {
	e := &XRPCError{Status: status}
	_ = json.Unmarshal(body, e)
	if e.Kind == "" && e.Message == "" && len(body) > 0 {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 400 {
			e.Message = e.Message[:400] + "..."
		}
	}
	return e
}

// GetJSON fetches an arbitrary (non-XRPC) URL and decodes JSON. Used for the PLC
// directory and .well-known lookups.
func GetJSON(ctx context.Context, rawurl string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: DefaultTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return decodeErr(resp.StatusCode, b)
	}
	return json.Unmarshal(b, out)
}

// GetText fetches a URL and returns its trimmed body as a string.
func GetText(ctx context.Context, rawurl string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", decodeErr(resp.StatusCode, b)
	}
	return strings.TrimSpace(string(b)), nil
}

// Status returns the HTTP status carried by err, or 0.
func Status(err error) int {
	var e *XRPCError
	if ok := asXRPC(err, &e); ok {
		return e.Status
	}
	return 0
}

// Kind returns the XRPC error tag carried by err, or "".
func Kind(err error) string {
	var e *XRPCError
	if ok := asXRPC(err, &e); ok {
		return e.Kind
	}
	return ""
}
