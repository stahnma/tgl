package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Actor display: handle when known, DID otherwise.
func (a Actor) String() string {
	if a.Handle != "" {
		return "@" + a.Handle
	}
	return a.DID
}

func ago(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Some records carry offsets time.RFC3339 accepts and some carry
		// fractional seconds; both parse. Anything else we show raw.
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func labelNames(ls []Label) string {
	if len(ls) == 0 {
		return ""
	}
	names := make([]string, 0, len(ls))
	for _, l := range ls {
		n := l.Name
		if n == "" {
			n = l.Key
		}
		if l.Value != "" {
			n += ":" + l.Value
		}
		names = append(names, n)
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// WriteIssueList renders the list view as aligned columns.
func WriteIssueList(w io.Writer, l *IssueList) {
	if len(l.Issues) == 0 {
		fmt.Fprintf(w, "No issues found in %s\n", l.Repo.Name)
		return
	}
	rkeyW, stateW := 0, 0
	for _, is := range l.Issues {
		rkeyW = max(rkeyW, len(is.RKey))
		stateW = max(stateW, len(is.State))
	}
	for _, is := range l.Issues {
		line := fmt.Sprintf("%-*s  %-*s  %s", rkeyW, is.RKey, stateW, is.State, truncate(is.Title, 70))
		if lb := labelNames(is.Labels); lb != "" {
			line += "  " + lb
		}
		fmt.Fprintf(w, "%s  %s  %s\n", line, is.Author, ago(is.CreatedAt))
	}
	fmt.Fprintf(w, "\n%d issue(s)\n", len(l.Issues))
}

// WriteIssue renders the detail view.
func WriteIssue(w io.Writer, is *Issue) {
	fmt.Fprintf(w, "%s\n", is.Title)
	fmt.Fprintf(w, "%s · %s · opened %s", is.State, is.Author, ago(is.CreatedAt))
	if is.CommentCount == 1 {
		fmt.Fprintf(w, " · 1 comment")
	} else if is.CommentCount > 1 {
		fmt.Fprintf(w, " · %d comments", is.CommentCount)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", is.URI)
	if lb := labelNames(is.Labels); lb != "" {
		fmt.Fprintf(w, "labels: %s\n", lb)
	}
	if is.Body != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(is.Body, "\n"))
	}
	writeComments(w, is.Comments)
}

// WritePullList renders the pull list view.
func WritePullList(w io.Writer, l *PullList) {
	if len(l.Pulls) == 0 {
		fmt.Fprintf(w, "No pull requests found in %s\n", l.Repo.Name)
		return
	}
	rkeyW, stateW := 0, 0
	for _, p := range l.Pulls {
		rkeyW = max(rkeyW, len(p.RKey))
		stateW = max(stateW, len(p.State))
	}
	for _, p := range l.Pulls {
		branch := p.TargetBranch
		if p.SourceBranch != "" {
			branch = p.SourceBranch + " → " + p.TargetBranch
		}
		fmt.Fprintf(w, "%-*s  %-*s  %s  %s  %s  %s\n",
			rkeyW, p.RKey, stateW, p.State, truncate(p.Title, 55), branch, p.Author, ago(p.CreatedAt))
	}
	fmt.Fprintf(w, "\n%d pull request(s)\n", len(l.Pulls))
}

// WritePull renders the pull detail view.
func WritePull(w io.Writer, p *Pull) {
	fmt.Fprintf(w, "%s\n", p.Title)
	fmt.Fprintf(w, "%s · %s · opened %s · %s pull", p.State, p.Author, ago(p.CreatedAt), p.Shape)
	if p.CommentCount > 0 {
		fmt.Fprintf(w, " · %d comment(s)", p.CommentCount)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", p.URI)
	if p.SourceBranch != "" {
		fmt.Fprintf(w, "%s → %s\n", p.SourceBranch, p.TargetBranch)
	} else {
		fmt.Fprintf(w, "target: %s\n", p.TargetBranch)
	}
	if p.Stacked {
		fmt.Fprintf(w, "stacked on: %s\n", p.DependentOn)
	}
	switch {
	case len(p.Rounds) > 0:
		fmt.Fprintf(w, "rounds: %d (latest %s)\n", len(p.Rounds), ago(p.Rounds[len(p.Rounds)-1].CreatedAt))
	case len(p.Versions) > 0:
		v := p.Versions[len(p.Versions)-1]
		fmt.Fprintf(w, "versions: %d (latest %s..%s)\n", len(p.Versions), shortSHA(v.Base), shortSHA(v.Head))
	}
	if lb := labelNames(p.Labels); lb != "" {
		fmt.Fprintf(w, "labels: %s\n", lb)
	}
	if p.Body != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(p.Body, "\n"))
	}
	writeComments(w, p.Comments)
	if p.Patch != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(p.Patch, "\n"))
	}
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func writeComments(w io.Writer, cs []Comment) {
	if len(cs) == 0 {
		return
	}
	for _, c := range cs {
		fmt.Fprintf(w, "\n--- %s · %s", c.Author, ago(c.CreatedAt))
		if c.PullRoundIdx != nil {
			fmt.Fprintf(w, " · round %d", *c.PullRoundIdx)
		}
		if c.Legacy {
			fmt.Fprintf(w, " · legacy")
		}
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(c.Body, "\n"))
	}
}

// WriteRepo renders resolved repo context.
func WriteRepo(w io.Writer, r *RepoRef) {
	fmt.Fprintf(w, "name          %s\n", r.Name)
	fmt.Fprintf(w, "owner         %s\n", r.Owner)
	fmt.Fprintf(w, "owner did     %s\n", r.Owner.DID)
	fmt.Fprintf(w, "rkey          %s\n", r.RKey)
	fmt.Fprintf(w, "at-uri        %s\n", r.AtURI)
	if r.RepoDID != "" {
		fmt.Fprintf(w, "repo did      %s\n", r.RepoDID)
	} else {
		fmt.Fprintf(w, "repo did      (none; repo predates Tangled v1.13)\n")
	}
	fmt.Fprintf(w, "knot          %s\n", r.Knot)
	fmt.Fprintf(w, "knot endpoint %s\n", r.KnotEndpoint)
	if r.Spindle != "" {
		fmt.Fprintf(w, "spindle       %s\n", r.Spindle)
	}
	if r.Description != "" {
		fmt.Fprintf(w, "description   %s\n", r.Description)
	}
}

// WriteRepoList renders the repo list as aligned columns.
func WriteRepoList(w io.Writer, l *RepoList) {
	if len(l.Repos) == 0 {
		fmt.Fprintf(w, "No repositories found for %s\n", l.Owner)
		return
	}
	nameW := 0
	for _, r := range l.Repos {
		nameW = max(nameW, len(r.Name))
	}
	for _, r := range l.Repos {
		line := fmt.Sprintf("%-*s", nameW, r.Name)
		if d := truncate(r.Description, 60); d != "" {
			line += "  " + d
		}
		fmt.Fprintf(w, "%s  %s  %s\n", line, r.Knot, ago(r.CreatedAt))
	}
	fmt.Fprintf(w, "\n%d repositor%s\n", len(l.Repos), plural(len(l.Repos), "y", "ies"))
}

// WriteCloned renders the result of a clone.
func WriteCloned(w io.Writer, c *Cloned) {
	fmt.Fprintf(w, "cloned %s into %s\n", c.Repo.Name, c.Directory)
	fmt.Fprintf(w, "%s\n", c.URL)
}

// WriteRepoCreated renders the result of a repo create.
func WriteRepoCreated(w io.Writer, c *RepoCreated) {
	fmt.Fprintf(w, "%s %s/%s\n", c.Action, c.Owner, c.Name)
	fmt.Fprintf(w, "%s\n", c.URI)
	fmt.Fprintf(w, "repo did      %s\n", c.RepoDID)
	fmt.Fprintf(w, "knot          %s\n", c.Knot)
	if c.DefaultBranch != "" {
		fmt.Fprintf(w, "default       %s\n", c.DefaultBranch)
	}
	if c.CloneURL != "" {
		fmt.Fprintf(w, "clone         %s\n", c.CloneURL)
	}
	if c.URL != "" {
		fmt.Fprintf(w, "%s\n", c.URL)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// WriteCreated renders the result of a write command.
func WriteCreated(w io.Writer, c *Created) {
	fmt.Fprintf(w, "%s %s\n", c.Action, c.RKey)
	fmt.Fprintf(w, "%s\n", c.URI)
	if c.URL != "" {
		fmt.Fprintf(w, "%s\n", c.URL)
	}
}

// WriteAuthStatus renders auth state.
func WriteAuthStatus(w io.Writer, s *AuthStatus) {
	if !s.Authenticated {
		fmt.Fprintf(w, "not authenticated\n")
		return
	}
	fmt.Fprintf(w, "logged in as @%s\n", s.Handle)
	fmt.Fprintf(w, "did     %s\n", s.DID)
	fmt.Fprintf(w, "pds     %s\n", s.PDS)
	fmt.Fprintf(w, "source  %s\n", s.Source)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
