package tangled

import "testing"

func TestParseATURI(t *testing.T) {
	u, err := ParseATURI("at://did:plc:abc/sh.tangled.repo.issue/3ms4qpm3qjd22")
	if err != nil {
		t.Fatal(err)
	}
	if u.Authority != "did:plc:abc" || u.Collection != "sh.tangled.repo.issue" || u.RKey != "3ms4qpm3qjd22" {
		t.Errorf("parsed = %+v", u)
	}
	if u.String() != "at://did:plc:abc/sh.tangled.repo.issue/3ms4qpm3qjd22" {
		t.Errorf("round trip = %q", u.String())
	}
}

func TestParseATURIPartial(t *testing.T) {
	u, err := ParseATURI("at://did:plc:abc")
	if err != nil {
		t.Fatal(err)
	}
	if u.Authority != "did:plc:abc" || u.Collection != "" || u.RKey != "" {
		t.Errorf("parsed = %+v", u)
	}
}

func TestParseATURIRejects(t *testing.T) {
	for _, s := range []string{"", "https://example.com/x", "at://", "did:plc:abc"} {
		if _, err := ParseATURI(s); err == nil {
			t.Errorf("ParseATURI(%q) should fail", s)
		}
	}
}

// Slug mirrors the appview: the cosmetic name when set, otherwise the rkey.
func TestRepoSlug(t *testing.T) {
	if got := (&Repo{}).Slug("core"); got != "core" {
		t.Errorf("Slug with no name = %q, want the rkey", got)
	}
	if got := (&Repo{Name: "test"}).Slug("3lzg6enurmo22"); got != "test" {
		t.Errorf("Slug with a name = %q, want the name", got)
	}
}
