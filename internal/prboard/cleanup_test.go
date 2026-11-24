package prboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// git runs git in dir with a fixed identity and no user or system config.
func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitEnv(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(cfg, []byte("[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
}

var old = []string{"GIT_AUTHOR_DATE=2026-11-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-11-01T00:00:00Z"}

func commit(t *testing.T, dir, file string, env []string) string {
	os.WriteFile(filepath.Join(dir, file), []byte(file+time.Now().String()), 0o644)
	git(t, dir, env, "add", file)
	git(t, dir, env, "commit", "-q", "-m", file)
	return git(t, dir, nil, "rev-parse", "HEAD")
}

func TestCleanupPlanAndExecute(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	clone := filepath.Join(root, "acme", "widgets")
	os.MkdirAll(clone, 0o755)
	git(t, clone, nil, "init", "-q")
	git(t, clone, nil, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	commit(t, clone, "README", old)
	// an unrelated clone with another remote is never touched
	other := filepath.Join(root, "other")
	os.MkdirAll(other, 0o755)
	git(t, other, nil, "init", "-q")
	git(t, other, nil, "remote", "add", "origin", "https://github.com/acme/other")

	wt := func(branch string) string {
		p := filepath.Join(root, "wt-"+branch)
		git(t, clone, nil, "worktree", "add", "-q", "-b", branch, p, "main")
		return p
	}
	heads := map[string]string{}
	// clean: old commit, tip is the PR head -> removed
	pClean := wt("clean")
	heads["clean"] = commit(t, pClean, "c", old)
	// dirty: uncommitted change -> kept
	pDirty := wt("dirty")
	heads["dirty"] = commit(t, pDirty, "d", old)
	os.WriteFile(filepath.Join(pDirty, "d"), []byte("edited"), 0o644)
	// young: HEAD commit made now -> kept
	pYoung := wt("young")
	heads["young"] = commit(t, pYoung, "y", nil)
	// live: a Claude session works in a subdirectory -> kept
	pLive := wt("live")
	heads["live"] = commit(t, pLive, "l", old)
	os.MkdirAll(filepath.Join(pLive, "sub"), 0o755)
	// unpushed: local branch has a commit past the PR head -> kept
	pUnp := wt("unpushed")
	heads["unpushed"] = commit(t, pUnp, "u1", old)
	commit(t, pUnp, "u2", old)
	git(t, clone, nil, "worktree", "remove", pUnp) // branch only, no worktree
	// ancestor: PR head moved on after the local tip (pushed from elsewhere) -> deleted
	git(t, clone, nil, "branch", "ancestor", "main")
	pAnc := filepath.Join(root, "scratch-anc")
	git(t, clone, nil, "worktree", "add", "-q", "--detach", pAnc, "main")
	heads["ancestor"] = commit(t, pAnc, "a", old)
	git(t, clone, nil, "worktree", "remove", "--force", pAnc)
	// main: checked out in the main clone -> kept
	git(t, clone, nil, "checkout", "-q", "-b", "inmain")
	heads["inmain"] = commit(t, clone, "m", old)

	var ghCalls []string
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name == "gh" {
			ghCalls = append(ghCalls, strings.Join(args, " "))
			if strings.Contains(args[len(args)-1], "heads/young") {
				return nil, errServer
			}
			if len(args) > 1 && args[1] == "-X" {
				return nil, nil
			}
			if strings.HasSuffix(args[1], "heads/dirty") {
				return []byte("gh: Not Found (HTTP 404)"), errNotFound
			}
			return []byte(`{}`), nil
		}
		return ExecRunner(ctx, dir, name, args...)
	}
	c := Cleaner{
		Run: run, Roots: []string{root}, Depth: 3, MinAge: 30 * time.Minute, Now: time.Now,
		Sessions: func(ctx context.Context) ([]string, error) { return []string{filepath.Join(pLive, "sub")}, nil },
	}
	var prs []PR
	for i, b := range []string{"clean", "dirty", "young", "live", "unpushed", "ancestor", "inmain"} {
		p := PR{Repo: "acme/widgets", Number: i + 1, State: "MERGED", Head: b, HeadOID: heads[b], HeadRepo: "acme/widgets", DefaultBranch: "main"}
		prs = append(prs, p)
	}
	fork := PR{Repo: "acme/widgets", Number: 20, State: "MERGED", Head: "patch", HeadRepo: "someone/widgets", DefaultBranch: "main"}
	// a release PR whose head is the default branch: its tip matches, yet local main must stay
	dflt := PR{Repo: "acme/widgets", Number: 21, State: "MERGED", Head: "main", HeadOID: git(t, clone, nil, "rev-parse", "main"), HeadRepo: "acme/widgets", DefaultBranch: "main"}
	open := PR{Repo: "acme/widgets", Number: 22, State: "OPEN", Head: "clean"}
	prs = append(prs, fork, dflt, open)

	items := c.Plan(context.Background(), prs)
	if len(items) != 9 {
		t.Fatalf("want 9 merged items (open PR excluded), got %d", len(items))
	}
	got := map[int]string{}
	for _, it := range items {
		var ls []string
		for _, s := range it.Steps {
			l := s.Kind
			if s.Skip != "" {
				l += " skip(" + s.Skip + ")"
			}
			ls = append(ls, l)
		}
		got[it.N] = strings.Join(ls, "; ")
	}
	want := map[int]string{
		1:  "remote; worktree; branch",
		2:  "worktree skip(uncommitted changes); branch skip(checked out in a worktree that is kept)",
		3:  "remote skip(could not check it: gh: HTTP 500); worktree skip(HEAD commit is 0 minutes old); branch skip(checked out in a worktree that is kept)",
		4:  "remote; worktree skip(a live Claude session works in it); branch skip(checked out in a worktree that is kept)",
		5:  "remote; branch skip(unpushed work: tip " + short(git(t, clone, nil, "rev-parse", "unpushed")) + " is not the PR head or an ancestor of it)",
		6:  "remote; branch",
		7:  "remote; worktree skip(checked out in the main clone); branch skip(checked out in a worktree that is kept)",
		20: "remote skip(head branch is in another repository)",
		21: "remote skip(default branch)",
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("PR %d:\n got  %s\n want %s", n, got[n], w)
		}
	}

	items = c.Execute(context.Background(), items)
	for _, it := range items {
		wantKept := it.N != 1 && it.N != 6
		if it.Kept() != wantKept {
			t.Errorf("PR %d kept=%v want %v: %+v", it.N, it.Kept(), wantKept, it.Steps)
		}
	}
	branches := git(t, clone, nil, "branch", "--format=%(refname:short)")
	for _, b := range []string{"dirty", "young", "live", "unpushed", "inmain", "main"} {
		if !strings.Contains("\n"+branches+"\n", "\n"+b+"\n") {
			t.Errorf("branch %s was deleted", b)
		}
	}
	for _, b := range []string{"clean", "ancestor"} {
		if strings.Contains("\n"+branches+"\n", "\n"+b+"\n") {
			t.Errorf("branch %s still exists", b)
		}
	}
	if _, err := os.Stat(pClean); !os.IsNotExist(err) {
		t.Errorf("clean worktree still there: %v", err)
	}
	for _, p := range []string{pDirty, pYoung, pLive} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("kept worktree %s is gone", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(pDirty, "d")); string(b) != "edited" {
		t.Error("dirty worktree content changed")
	}
	var deletes []string
	for _, c := range ghCalls {
		if strings.HasPrefix(c, "api -X DELETE") {
			deletes = append(deletes, c)
		}
	}
	// remote deletes only for branches that exist and are ours; the local
	// skip rules do not gate the GitHub branch (the work is on the PR head)
	wantDel := "api -X DELETE repos/acme/widgets/git/refs/heads/clean,api -X DELETE repos/acme/widgets/git/refs/heads/live,api -X DELETE repos/acme/widgets/git/refs/heads/unpushed,api -X DELETE repos/acme/widgets/git/refs/heads/ancestor,api -X DELETE repos/acme/widgets/git/refs/heads/inmain"
	if strings.Join(deletes, ",") != wantDel {
		t.Errorf("remote deletes:\n got  %s\n want %s", strings.Join(deletes, ","), wantDel)
	}
}

type notFound struct{}

func (notFound) Error() string { return "gh: Not Found (HTTP 404)" }

var errNotFound = notFound{}

type serverErr struct{}

func (serverErr) Error() string { return "gh: HTTP 500" }

var errServer = serverErr{}

func TestClonesMatchRemotes(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	for name, url := range map[string]string{
		"a":           "git@github.com:Acme/App.git",
		"nested/b":    "https://github.com/acme/app",
		"c":           "ssh://git@github.com/acme/infra.git",
		"d/e/f/g/too": "git@github.com:acme/app.git", // deeper than Depth
	} {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o755)
		git(t, d, nil, "init", "-q")
		git(t, d, nil, "remote", "add", "origin", url)
	}
	// a second root that is a symlink to the first must not list the clones twice
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	c := Cleaner{Run: ExecRunner, Roots: []string{root, link}, Depth: 3}
	got := c.Clones(context.Background())
	if len(got["acme/app"]) != 2 || len(got["acme/infra"]) != 1 {
		t.Errorf("clones: %v", got)
	}
}

// A PR that got more commits pushed from elsewhere: the local tip is an
// ancestor of the PR head, but the head commit was never fetched. Cleanup
// fetches the pull ref before judging; a head it still cannot find is
// "could not verify", never unpushed work.
func TestCleanupFetchesMissingPRHead(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	upstream := filepath.Join(t.TempDir(), "upstream.git")
	git(t, root, nil, "init", "-q", "--bare", upstream)
	clone := filepath.Join(root, "widgets")
	os.MkdirAll(clone, 0o755)
	git(t, clone, nil, "init", "-q")
	git(t, clone, nil, "remote", "add", "origin", upstream)
	// the GitHub URL is only the push URL, so fetches go to the local upstream
	git(t, clone, nil, "remote", "set-url", "--push", "origin", "git@github.com:acme/widgets.git")
	commit(t, clone, "README", old)
	git(t, clone, nil, "push", "-q", upstream, "main")
	git(t, clone, nil, "checkout", "-q", "-b", "behind")
	tip := commit(t, clone, "b1", old)
	git(t, clone, nil, "branch", "gone", tip)
	git(t, clone, nil, "push", "-q", upstream, "behind")
	git(t, clone, nil, "checkout", "-q", "main")
	// someone else pushes b2 to the PR; the clone never sees it
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	git(t, root, nil, "clone", "-q", "-b", "behind", upstream, elsewhere)
	head := commit(t, elsewhere, "b2", old)
	git(t, elsewhere, nil, "push", "-q", "origin", "HEAD:refs/pull/7/head")
	if _, err := ExecRunner(context.Background(), clone, "git", "cat-file", "-e", head+"^{commit}"); err == nil {
		t.Fatal("setup: the PR head must not be in the clone yet")
	}

	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name == "gh" {
			return nil, errNotFound // the GitHub branches are already deleted
		}
		return ExecRunner(ctx, dir, name, args...)
	}
	c := Cleaner{Run: run, Roots: []string{root}, Depth: 3, MinAge: 30 * time.Minute, Now: time.Now}
	prs := []PR{
		{Repo: "acme/widgets", Number: 7, State: "MERGED", Head: "behind", HeadOID: head, HeadRepo: "acme/widgets", DefaultBranch: "main"},
		{Repo: "acme/widgets", Number: 8, State: "MERGED", Head: "gone", HeadOID: "0123456789abcdef0123456789abcdef01234567", HeadRepo: "acme/widgets", DefaultBranch: "main"},
	}
	got := map[int]string{}
	for _, it := range c.Plan(context.Background(), prs) {
		var ls []string
		for _, s := range it.Steps {
			ls = append(ls, s.Kind+" skip("+s.Skip+")")
		}
		got[it.N] = strings.Join(ls, "; ")
	}
	want := map[int]string{
		7: "branch skip()",
		8: "branch skip(could not verify: PR head not available locally)",
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("PR %d:\n got  %s\n want %s", n, got[n], w)
		}
	}
}

func TestCleanupSkipsReviews(t *testing.T) {
	c := Cleaner{Now: time.Now, Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		t.Errorf("unexpected %s %v", name, args)
		return nil, errServer
	}}
	p := PR{Repo: "acme/widgets", Number: 3, State: "MERGED", Head: "theirs", HeadRepo: "acme/widgets", DefaultBranch: "main", ToReview: true}
	if items := c.Plan(context.Background(), []PR{p}); len(items) != 0 {
		t.Errorf("a review must never be cleaned up: %+v", items)
	}
}
