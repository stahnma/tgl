package gitutil

import "testing"

func TestParseRemote(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		host    string
		owner   string
		repo    string
		repoDID string
		wantErr bool
	}{
		{"scp form", "git@tangled.org:user.tngl.sh/my-project", "tangled.org", "user.tngl.sh", "my-project", "", false},
		{"scp with .git", "git@tangled.org:alice.bsky.social/tgl.git", "tangled.org", "alice.bsky.social", "tgl", "", false},
		{"ssh url", "ssh://git@tangled.org/user.tngl.sh/my-project", "tangled.org", "user.tngl.sh", "my-project", "", false},
		{"https", "https://tangled.org/tangled.org/core", "tangled.org", "tangled.org", "core", "", false},
		{"https with at", "https://tangled.org/@tangled.org/core", "tangled.org", "tangled.org", "core", "", false},
		{"https trailing slash", "https://tangled.org/tangled.org/core/", "tangled.org", "tangled.org", "core", "", false},
		{"knot clone url", "https://knot1.tangled.sh/did:plc:j5hmlfdrwkvtxm7cjmu7j2is/", "knot1.tangled.sh", "", "", "did:plc:j5hmlfdrwkvtxm7cjmu7j2is", false},
		{"did owner", "git@tangled.org:did:plc:abc/repo", "tangled.org", "did:plc:abc", "repo", "", false},
		{"github passthrough", "git@github.com:someone/thing.git", "github.com", "someone", "thing", "", false},
		{"empty", "", "", "", "", "", true},
		{"no path", "https://tangled.org/", "", "", "", "", true},
		{"single segment", "https://tangled.org/onlyowner", "", "", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRemote(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRemote(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRemote(%q): %v", tc.raw, err)
			}
			if got.Host != tc.host || got.Owner != tc.owner || got.Name != tc.repo || got.RepoDID != tc.repoDID {
				t.Errorf("ParseRemote(%q) = host=%q owner=%q name=%q repoDid=%q, want host=%q owner=%q name=%q repoDid=%q",
					tc.raw, got.Host, got.Owner, got.Name, got.RepoDID, tc.host, tc.owner, tc.repo, tc.repoDID)
			}
		})
	}
}

func TestRemoteIsTangled(t *testing.T) {
	for raw, want := range map[string]bool{
		"git@tangled.org:a/b":                 true,
		"https://knot1.tangled.sh/did:plc:x/": true,
		"https://tangled.sh/a/b":              true,
		"git@github.com:a/b.git":              false,
		"https://gitlab.example.com/a/b":      false,
	} {
		r, err := ParseRemote(raw)
		if err != nil {
			t.Fatalf("ParseRemote(%q): %v", raw, err)
		}
		if got := r.IsTangled(); got != want {
			t.Errorf("IsTangled(%q) = %v, want %v", raw, got, want)
		}
	}
}
