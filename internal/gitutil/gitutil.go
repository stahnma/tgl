// Package gitutil wraps the git commands tgl shells out to, and parses Tangled
// remote URLs.
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Run executes git in dir and returns trimmed stdout.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// RunStreaming executes git in dir with its output attached to out and errOut,
// so long-running commands show progress as they go. Commands whose output tgl
// parses should use Run instead.
func RunStreaming(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// InRepo reports whether dir is inside a git work tree.
func InRepo(ctx context.Context, dir string) bool {
	out, err := Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Remotes lists remote names in the order git reports them.
func Remotes(ctx context.Context, dir string) ([]string, error) {
	out, err := Run(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// RemoteURL returns the fetch URL for a remote.
func RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	return Run(ctx, dir, "remote", "get-url", remote)
}

// CurrentBranch returns the checked-out branch name, or "" when detached.
func CurrentBranch(ctx context.Context, dir string) string {
	out, err := Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out == "HEAD" {
		return ""
	}
	return out
}

// Remote is a parsed Tangled git remote.
type Remote struct {
	Host string // tangled.org, knot1.tangled.sh
	// Owner is a handle or a DID. Empty when the remote addresses a repo DID
	// directly, as knot clone URLs do.
	Owner string
	Name  string // repo name; empty for repo-DID remotes
	// RepoDID is set when the remote path is itself a repo DID, which is what
	// `git clone https://tangled.org/<owner>/<name>` redirects to.
	RepoDID string
	Raw     string
}

// ParseRemote understands the remote forms Tangled hands out:
//
//	git@tangled.org:handle/repo
//	ssh://git@tangled.org/handle/repo
//	https://tangled.org/handle/repo
//	https://tangled.org/@handle/repo
//	https://knot1.tangled.sh/did:plc:.../
func ParseRemote(raw string) (Remote, error) {
	r := Remote{Raw: raw}
	s := strings.TrimSpace(raw)
	if s == "" {
		return r, fmt.Errorf("empty remote URL")
	}

	var host, path string
	switch {
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "ssh://"), strings.HasPrefix(s, "git://"):
		rest := s
		for _, p := range []string{"http://", "https://", "ssh://", "git://"} {
			rest = strings.TrimPrefix(rest, p)
		}
		if i := strings.Index(rest, "@"); i >= 0 && i < strings.Index(rest+"/", "/") {
			rest = rest[i+1:]
		}
		host, path, _ = strings.Cut(rest, "/")
	case strings.Contains(s, ":") && !strings.Contains(s, "://"):
		// scp-like: [user@]host:path
		hostpart, p, ok := strings.Cut(s, ":")
		if !ok {
			return r, fmt.Errorf("unrecognised remote URL: %q", raw)
		}
		if i := strings.Index(hostpart, "@"); i >= 0 {
			hostpart = hostpart[i+1:]
		}
		host, path = hostpart, p
	default:
		return r, fmt.Errorf("unrecognised remote URL: %q", raw)
	}

	r.Host = strings.ToLower(host)
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return r, fmt.Errorf("remote %q has no repository path", raw)
	}

	parts := strings.Split(path, "/")
	if strings.HasPrefix(parts[0], "did:") && len(parts) == 1 {
		r.RepoDID = parts[0]
		return r, nil
	}
	if len(parts) < 2 {
		return r, fmt.Errorf("remote %q is missing owner or repository name", raw)
	}
	r.Owner = strings.TrimPrefix(parts[0], "@")
	r.Name = parts[1]
	return r, nil
}

// IsTangled reports whether a parsed remote plausibly points at Tangled. Used
// only to pick between several remotes, never to reject one the user named.
func (r Remote) IsTangled() bool {
	if r.RepoDID != "" {
		return true
	}
	h := r.Host
	return h == "tangled.org" || h == "tangled.sh" ||
		strings.HasSuffix(h, ".tangled.org") || strings.HasSuffix(h, ".tangled.sh")
}
