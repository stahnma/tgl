package tangled

import (
	"fmt"
	"strings"
)

// ATURI is a parsed at:// URI.
type ATURI struct {
	Authority  string // DID or handle
	Collection string
	RKey       string
}

// ParseATURI parses at://<authority>[/<collection>[/<rkey>]].
func ParseATURI(s string) (ATURI, error) {
	rest, ok := strings.CutPrefix(s, "at://")
	if !ok {
		return ATURI{}, fmt.Errorf("not an AT-URI: %q", s)
	}
	parts := strings.SplitN(rest, "/", 3)
	if parts[0] == "" {
		return ATURI{}, fmt.Errorf("AT-URI has no authority: %q", s)
	}
	u := ATURI{Authority: parts[0]}
	if len(parts) > 1 {
		u.Collection = parts[1]
	}
	if len(parts) > 2 {
		u.RKey = parts[2]
	}
	return u, nil
}

func (u ATURI) String() string {
	s := "at://" + u.Authority
	if u.Collection != "" {
		s += "/" + u.Collection
	}
	if u.RKey != "" {
		s += "/" + u.RKey
	}
	return s
}

// IsZero reports whether the URI is empty.
func (u ATURI) IsZero() bool { return u.Authority == "" }

// MakeATURI builds an at:// URI from its parts.
func MakeATURI(authority, collection, rkey string) string {
	return ATURI{Authority: authority, Collection: collection, RKey: rkey}.String()
}
