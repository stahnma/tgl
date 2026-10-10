package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"tangled.org/tgl/internal/httpx"
	"tangled.org/tgl/internal/resolve"
)

// DefaultAppview is the web appview that owns the sequential #42 numbering.
const DefaultAppview = "https://tangled.org"

// appviewBase returns the configured web appview base URL.
func (a *App) appviewBase() string {
	if a.Appview != "" {
		return strings.TrimRight(a.Appview, "/")
	}
	if v := strings.TrimSpace(osGetenv("TANGLED_APPVIEW")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultAppview
}

var aturiAttr = regexp.MustCompile(`data-aturi="(at://[^"]+)"`)
var aturiAny = regexp.MustCompile(`at://[^"'<>\s]+`)

// resolveNumber maps a sequential #42 to the record it names.
//
// The number is allocated by the web appview's own repo_issue_seqs counter
// (core:appview/db/issues.go:55-63); it lives in no record and no XRPC method.
// Reconstructing it from TID order does not work — that ordering is creation
// order, while the counter follows firehose-ingest order and never rewinds over
// deletions, so the two disagree on real repositories.
//
// So rather than guess, tgl asks the appview that owns the number. The issue and
// pull pages carry the record's AT-URI in a data-aturi attribute, which makes
// the mapping exact. It does mean depending on that page's markup: if the
// attribute disappears, this returns an error rather than a wrong answer.
func (a *App) resolveNumber(ctx context.Context, repo *resolve.Repo, k kind, n int) (string, error) {
	url := fmt.Sprintf("%s/%s/%d", a.repoWebURL(repo), k.urlSegment(), n)

	body, status, err := fetchPage(ctx, url)
	if err != nil {
		return "", Wrap(CodeError, err, "asking the appview to resolve %s #%d", k.noun(), n)
	}
	if status == http.StatusNotFound || strings.Contains(firstTitle(body), "404") {
		return "", NotFoundf("%s #%d not found in %s", k.noun(), n, repo.Slug())
	}
	if status >= 400 {
		return "", Errorf(CodeError, "appview returned HTTP %d for %s", status, url)
	}

	want := "/" + k.collection() + "/"
	counts := map[string]int{}
	for _, m := range aturiAttr.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], want) {
			counts[m[1]]++
		}
	}
	if len(counts) == 0 {
		// Fall back to any AT-URI of the right collection on the page.
		for _, m := range aturiAny.FindAllString(body, -1) {
			if strings.Contains(m, want) {
				counts[strings.TrimRight(m, "\"&")]++
			}
		}
	}
	switch len(counts) {
	case 0:
		return "", Errorf(CodeError,
			"could not find a %s AT-URI on %s. The sequential number is appview-local and tgl reads it off that page; use the record's rkey instead.",
			k.noun(), url)
	case 1:
		for uri := range counts {
			return uri, nil
		}
	}
	// Several candidates: the subject is the one the page repeats most.
	type kv struct {
		uri string
		n   int
	}
	var all []kv
	for uri, c := range counts {
		all = append(all, kv{uri, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].uri < all[j].uri
	})
	if all[0].n == all[1].n {
		return "", Errorf(CodeError,
			"%s #%d on %s is ambiguous: the page names %d candidate records. Use the rkey instead.",
			k.noun(), n, url, len(all))
	}
	return all[0].uri, nil
}

func fetchPage(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(b), resp.StatusCode, nil
}

var titleRe = regexp.MustCompile(`(?is)<title>(.*?)</title>`)

func firstTitle(body string) string {
	m := titleRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}
