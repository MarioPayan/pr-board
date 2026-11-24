package prboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cleaner plans and runs the cleanup of merged PRs: their GitHub head
// branch, local worktrees and local branches.
type Cleaner struct {
	Run      Runner
	Roots    []string // clone roots, already expanded
	Depth    int      // how deep to look for clones under a root
	Now      func() time.Time
	MinAge   time.Duration                               // a worktree whose HEAD commit is younger is kept
	Sessions func(ctx context.Context) ([]string, error) // cwd of every live Claude session
}

// Step is one action of a plan item. Skip is empty when the step will run.
type Step struct {
	Kind   string `json:"kind"` // remote, worktree, branch
	Repo   string `json:"repo,omitempty"`
	Dir    string `json:"dir,omitempty"`  // clone the local step runs in
	Path   string `json:"path,omitempty"` // worktree path
	Branch string `json:"branch"`
	Skip   string `json:"skip,omitempty"`
	Result string `json:"result,omitempty"` // after Execute: done, skipped or the error
}

// PlanItem is one merged PR and what cleaning it does.
type PlanItem struct {
	Key   string `json:"key"`
	Repo  string `json:"repo"`
	N     int    `json:"n"`
	Title string `json:"title"`
	Steps []Step `json:"steps"`
}

// Kept reports whether any step is skipped or failed, which keeps the PR on the map.
func (it PlanItem) Kept() bool {
	for _, s := range it.Steps {
		if s.Skip != "" || (s.Result != "" && s.Result != "done") {
			return true
		}
	}
	return false
}

// Describe is one line for a step, for the CLI and the page.
func (s Step) Describe() string {
	var what string
	switch s.Kind {
	case "remote":
		what = fmt.Sprintf("delete GitHub branch %s in %s", s.Branch, s.Repo)
	case "worktree":
		what = "remove worktree " + s.Path
	case "branch":
		what = fmt.Sprintf("delete local branch %s in %s", s.Branch, s.Dir)
	}
	if s.Skip != "" {
		return "skip: " + what + " (" + s.Skip + ")"
	}
	return what
}

// LiveSessionCwds lists the cwd of every session `claude agents --json` knows.
func LiveSessionCwds(run Runner) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		agents, err := ClaudeAgents(ctx, run)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, a := range agents {
			if a.Cwd != "" {
				out = append(out, a.Cwd)
			}
		}
		return out, nil
	}
}

// Agent is one entry of `claude agents --json`.
type Agent struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
}

// ClaudeAgents runs `claude agents --json`.
func ClaudeAgents(ctx context.Context, run Runner) ([]Agent, error) {
	b, err := run(ctx, "", "claude", "agents", "--json")
	if err != nil {
		return nil, err
	}
	var a []Agent
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}
	return a, nil
}

var ghRemote = regexp.MustCompile(`github\.com[:/]+([^/\s]+)/([^/\s]+?)(?:\.git)?/?(?:\s|$)`)

// Clones finds git clones under the roots and maps owner/name (lower case)
// to their directories.
func (c Cleaner) Clones(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	depth := c.Depth
	if depth == 0 {
		depth = 3
	}
	visited := map[string]bool{} // real paths: a symlinked root must not list a clone twice
	var walk func(dir string, d int)
	walk = func(dir string, d int) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || visited[real] {
			return
		}
		visited[real] = true
		dir = real
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			b, err := c.Run(ctx, dir, "git", "remote", "-v")
			if err == nil {
				seen := map[string]bool{}
				for _, m := range ghRemote.FindAllStringSubmatch(string(b), -1) {
					k := strings.ToLower(m[1] + "/" + m[2])
					if !seen[k] {
						seen[k] = true
						out[k] = append(out[k], dir)
					}
				}
			}
			return // clones inside clones are not searched
		}
		if d >= depth {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
				continue
			}
			walk(filepath.Join(dir, n), d+1)
		}
	}
	for _, r := range c.Roots {
		walk(r, 0)
	}
	return out
}

type worktree struct {
	Path   string
	Branch string // short name
	Main   bool
}

func (c Cleaner) worktrees(ctx context.Context, clone string) []worktree {
	b, err := c.Run(ctx, clone, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var out []worktree
	for i, block := range strings.Split(strings.TrimSpace(string(b)), "\n\n") {
		var w worktree
		w.Main = i == 0
		for _, line := range strings.Split(block, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				w.Path = p
			}
			if br, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				w.Branch = br
			}
		}
		if w.Path != "" {
			out = append(out, w)
		}
	}
	return out
}

func under(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// worktreeSkip returns why a worktree must be kept, or "".
func (c Cleaner) worktreeSkip(ctx context.Context, w worktree, live []string) string {
	if w.Main {
		return "checked out in the main clone"
	}
	for _, cwd := range live {
		if under(cwd, w.Path) {
			return "a live Claude session works in it"
		}
	}
	st, err := c.Run(ctx, w.Path, "git", "status", "--porcelain")
	if err != nil {
		return "could not read its status"
	}
	if strings.TrimSpace(string(st)) != "" {
		return "uncommitted changes"
	}
	ct, err := c.Run(ctx, w.Path, "git", "log", "-1", "--format=%ct")
	if err != nil {
		return "could not read its HEAD commit"
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(ct)), 10, 64)
	if err != nil {
		return "could not read its HEAD commit"
	}
	if age := c.Now().Sub(time.Unix(sec, 0)); age < c.MinAge {
		return fmt.Sprintf("HEAD commit is %d minutes old", int(age.Minutes()))
	}
	return ""
}

// branchSkip returns why a local branch must be kept, or "".
func (c Cleaner) branchSkip(ctx context.Context, clone, tip, headOID string) string {
	if tip == headOID {
		return ""
	}
	if headOID == "" {
		return "PR head commit unknown"
	}
	if _, err := c.Run(ctx, clone, "git", "merge-base", "--is-ancestor", tip, headOID); err != nil {
		return fmt.Sprintf("unpushed work: tip %s is not the PR head or an ancestor of it", short(tip))
	}
	return ""
}

func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// remoteStep checks the GitHub head branch.
func (c Cleaner) remoteStep(ctx context.Context, p PR) *Step {
	s := Step{Kind: "remote", Repo: p.Repo, Branch: p.Head}
	switch {
	case p.Head == "":
		return nil
	case p.HeadRepo != "" && !strings.EqualFold(p.HeadRepo, p.Repo):
		s.Skip = "head branch is in another repository"
	case p.Head == p.DefaultBranch || p.DefaultBranch == "":
		s.Skip = "default branch"
	default:
		_, err := c.Run(ctx, "", "gh", "api", fmt.Sprintf("repos/%s/git/ref/heads/%s", p.Repo, p.Head))
		if err != nil {
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Not Found") {
				return nil // already deleted
			}
			s.Skip = "could not check it: " + clip(err.Error(), 120)
		}
	}
	return &s
}

// Plan lists what cleaning each merged PR in prs would do.
func (c Cleaner) Plan(ctx context.Context, prs []PR) []PlanItem {
	clones := c.Clones(ctx)
	var live []string
	liveErr := ""
	if c.Sessions != nil {
		var err error
		if live, err = c.Sessions(ctx); err != nil {
			liveErr = "could not list live Claude sessions"
		}
	}
	items := []PlanItem{}
	for _, p := range prs {
		if p.State != "MERGED" {
			continue
		}
		it := PlanItem{Key: p.Key(), Repo: p.Repo, N: p.Number, Title: p.Title, Steps: []Step{}}
		if s := c.remoteStep(ctx, p); s != nil {
			it.Steps = append(it.Steps, *s)
		}
		for _, clone := range clones[strings.ToLower(p.Repo)] {
			if p.Head == "" || p.DefaultBranch == "" || p.Head == p.DefaultBranch {
				break // never touch a local default branch
			}
			tipB, err := c.Run(ctx, clone, "git", "rev-parse", "--verify", "-q", "refs/heads/"+p.Head)
			tip := strings.TrimSpace(string(tipB))
			hasBranch := err == nil && tip != ""
			bskip := ""
			if hasBranch {
				bskip = c.branchSkip(ctx, clone, tip, p.HeadOID)
			}
			for _, w := range c.worktrees(ctx, clone) {
				if w.Branch != p.Head {
					continue
				}
				ws := Step{Kind: "worktree", Dir: clone, Path: w.Path, Branch: p.Head}
				switch {
				case liveErr != "":
					ws.Skip = liveErr
				case bskip != "":
					ws.Skip = bskip
				default:
					ws.Skip = c.worktreeSkip(ctx, w, live)
				}
				if ws.Skip != "" && bskip == "" {
					bskip = "checked out in a worktree that is kept"
				}
				it.Steps = append(it.Steps, ws)
			}
			if hasBranch {
				it.Steps = append(it.Steps, Step{Kind: "branch", Dir: clone, Branch: p.Head, Skip: bskip})
			}
		}
		items = append(items, it)
	}
	return items
}

// Execute runs every step that is not skipped, worktrees before branches,
// and fills in Result.
func (c Cleaner) Execute(ctx context.Context, items []PlanItem) []PlanItem {
	for i := range items {
		for j := range items[i].Steps {
			s := &items[i].Steps[j]
			if s.Skip != "" {
				s.Result = "skipped"
				continue
			}
			var err error
			switch s.Kind {
			case "remote":
				_, err = c.Run(ctx, "", "gh", "api", "-X", "DELETE", fmt.Sprintf("repos/%s/git/refs/heads/%s", s.Repo, s.Branch))
			case "worktree":
				_, err = c.Run(ctx, s.Dir, "git", "worktree", "remove", s.Path)
			case "branch":
				_, err = c.Run(ctx, s.Dir, "git", "branch", "-D", s.Branch)
			}
			if err != nil {
				s.Result = clip(err.Error(), 200)
			} else {
				s.Result = "done"
			}
		}
	}
	return items
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + " …"
	}
	return s
}
