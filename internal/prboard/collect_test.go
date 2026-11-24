package prboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseQuery(t *testing.T) {
	b, err := os.ReadFile("testdata/graphql.json")
	if err != nil {
		t.Fatal(err)
	}
	prs, errs, err := ParseQuery(b, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || len(errs) != 1 {
		t.Fatalf("want 2 PRs and 1 error, got %d and %v", len(prs), errs)
	}
	p := prs[0]
	if p.Key() != "acme/app#12" || p.Head != "feat/widget" || p.HeadOID != "abc123" || p.DefaultBranch != "main" || p.HeadRepo != "acme/app" {
		t.Errorf("identity: %+v", p)
	}
	if strings.Join(p.ChecksFailing, ",") != "unit,ci/legacy" || p.ChecksPending != 2 || p.ChecksTotal != 6 {
		t.Errorf("checks: failing %v pending %d total %d", p.ChecksFailing, p.ChecksPending, p.ChecksTotal)
	}
	if strings.Join(p.Requests, ",") != "carol,web-team" {
		t.Errorf("requests: %v", p.Requests)
	}
	if len(p.Reviews) != 2 || p.Reviews[1].Author != "ghost" || p.Reviews[1].State != "DISMISSED" {
		t.Errorf("reviews: %+v", p.Reviews)
	}
	if len(p.Threads) != 1 || len(p.Threads[0].Comments) != 2 || p.Threads[0].Comments[0].Body != "Please rename this." {
		t.Errorf("threads: %+v", p.Threads)
	}
	d := Derive(p, now)
	if d.State != StMissing || sigKeys(d) != "ci_fail(2) ci_pend(2) changes dismissed rev threads(1)" || d.Category != "Widgets" {
		t.Errorf("derived: %s %q %q", d.State, sigKeys(d), d.Category)
	}
	m := prs[1]
	if m.State != "MERGED" || m.MergedAt.IsZero() || m.HeadRepo != "" || m.ChecksTotal != 0 {
		t.Errorf("merged: %+v", m)
	}
}

func TestBuildQuery(t *testing.T) {
	q := BuildQuery([]ref{{"acme/app", 12}, {"acme/infra", 3}})
	for _, want := range []string{`p0: repository(owner: "acme", name: "app") { pullRequest(number: 12) { ...F } }`,
		`p1: repository(owner: "acme", name: "infra") { pullRequest(number: 3) { ...F } }`, "fragment F on PullRequest"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q", want)
		}
	}
}

// fakeGH answers gh search and gh api graphql from canned PRs.
type fakeGH struct {
	open, merged []searchHit
	prs          map[string]map[string]any // key -> pullRequest JSON
	calls        []string
}

func hit(repo string, n int) searchHit {
	var h searchHit
	h.Number, h.Repository.NameWithOwner = n, repo
	return h
}

func gqlNode(repo string, n int, state, head string) map[string]any {
	return map[string]any{
		"number": n, "title": "PR " + head, "url": "https://github.com/" + repo + "/pull/" + head,
		"state": state, "baseRefName": "main", "headRefName": head, "headRefOid": "oid-" + head,
		"mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "reviewDecision": "APPROVED",
		"updatedAt": "2026-11-22T00:00:00Z", "body": "body " + head,
		"repository": map[string]any{"nameWithOwner": repo, "defaultBranchRef": map[string]any{"name": "main"}},
	}
}

func (f *fakeGH) run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if name != "gh" {
		return nil, errors.New("unexpected " + name)
	}
	f.calls = append(f.calls, strings.Join(args[:2], " "))
	switch {
	case args[0] == "search":
		if strings.Contains(strings.Join(args, " "), "--merged") {
			return json.Marshal(f.merged)
		}
		return json.Marshal(f.open)
	case args[0] == "api" && args[1] == "graphql":
		q := strings.TrimPrefix(args[3], "query=")
		data := map[string]any{}
		for i := 0; ; i++ {
			alias := "p" + strconv.Itoa(i) + ": repository(owner: "
			at := strings.Index(q, alias)
			if at < 0 {
				break
			}
			line := q[at:]
			line = line[:strings.Index(line, "\n")]
			var owner, repo string
			var n int
			parseAlias(line, &owner, &repo, &n)
			if pr, ok := f.prs[owner+"/"+repo+"#"+strconv.Itoa(n)]; ok {
				data["p"+strconv.Itoa(i)] = map[string]any{"pullRequest": pr}
			} else {
				data["p"+strconv.Itoa(i)] = nil
			}
		}
		return json.Marshal(map[string]any{"data": data})
	}
	return nil, errors.New("unexpected gh " + strings.Join(args, " "))
}

func parseAlias(line string, owner, repo *string, n *int) {
	// p0: repository(owner: "acme", name: "app") { pullRequest(number: 12) { ...F } }
	parts := strings.Split(line, `"`)
	*owner, *repo = parts[1], parts[3]
	rest := parts[4][strings.Index(parts[4], "number: ")+8:]
	json.Unmarshal([]byte(rest[:strings.Index(rest, ")")]), n)
}

func TestCollectMerge(t *testing.T) {
	f := &fakeGH{
		open:   []searchHit{hit("acme/app", 1), hit("acme/app", 2), hit("acme/app", 3)},
		merged: []searchHit{hit("acme/app", 4), hit("acme/app", 5)},
		prs: map[string]map[string]any{
			"acme/app#1": gqlNode("acme/app", 1, "OPEN", "a"),
			"acme/app#2": gqlNode("acme/app", 2, "OPEN", "b"),
			"acme/app#3": gqlNode("acme/app", 3, "CLOSED", "c"),
			"acme/app#4": gqlNode("acme/app", 4, "MERGED", "d"),
			"acme/app#5": gqlNode("acme/app", 5, "MERGED", "e"),
		},
	}
	prevMerged := openPR(6, "f", "main")
	prevMerged.State = "MERGED" // merged before the window; stays until cleanup
	prev := &Store{PRs: []PR{prevMerged}, Cleaned: []string{"acme/app#5"}}
	c := Collector{Cfg: DefaultConfig(), Run: f.run, Now: func() time.Time { return now }}
	c.Cfg.Owners = []string{"acme"}
	st, err := c.Collect(context.Background(), prev)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range st.PRs {
		keys = append(keys, p.Key())
	}
	if got := strings.Join(keys, " "); got != "acme/app#6 acme/app#4 acme/app#2 acme/app#1" {
		t.Errorf("PRs: %s (closed #3 and cleaned #5 must be gone, old merged #6 kept)", got)
	}
	if strings.Join(st.Cleaned, ",") != "acme/app#5" || !st.Refreshed.Equal(now) {
		t.Errorf("cleaned %v refreshed %v", st.Cleaned, st.Refreshed)
	}
	if strings.Join(f.calls, ";") != "search prs;search prs;api graphql" {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestCollectRequired(t *testing.T) {
	p1 := gqlNode("acme/app", 1, "OPEN", "a")
	p1["body"] = "[required:#2, #4, acme/infra#7, acme/gone#1, #3]\n```\n[required:#5]\n```"
	f := &fakeGH{
		open:   []searchHit{hit("acme/app", 1), hit("acme/app", 2), hit("acme/app", 3)},
		merged: []searchHit{hit("acme/app", 4)},
		prs: map[string]map[string]any{
			"acme/app#1":   p1,
			"acme/app#2":   gqlNode("acme/app", 2, "OPEN", "b"),
			"acme/app#3":   gqlNode("acme/app", 3, "CLOSED", "c"),
			"acme/app#4":   gqlNode("acme/app", 4, "MERGED", "d"),
			"acme/infra#7": gqlNode("acme/infra", 7, "MERGED", "x"), // not discovered: only the lookup finds it
		},
	}
	c := Collector{Cfg: DefaultConfig(), Run: f.run, Now: func() time.Time { return now }}
	c.Cfg.Owners = []string{"acme"}
	st, err := c.Collect(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range st.PRs {
		for _, r := range p.Requires {
			got = append(got, fmt.Sprintf("%d>%s=%s", p.Number, r.Key(), r.State))
		}
	}
	want := "1>acme/app#2=OPEN 1>acme/app#4=MERGED 1>acme/infra#7=MERGED 1>acme/gone#1= 1>acme/app#3=CLOSED"
	if strings.Join(got, " ") != want {
		t.Errorf("requires:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if len(st.Errors) != 0 {
		t.Errorf("a target GitHub does not know is no page error: %v", st.Errors)
	}
	if strings.Join(f.calls, ";") != "search prs;search prs;api graphql;api graphql" {
		t.Errorf("calls: %v (one extra query for the two unknown targets)", f.calls)
	}
}

func TestCollectDiscoverError(t *testing.T) {
	c := Collector{Cfg: DefaultConfig(), Now: func() time.Time { return now },
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return nil, errors.New("gh: not logged in")
		}}
	c.Cfg.Owners = []string{"acme"}
	if _, err := c.Collect(context.Background(), nil); err == nil {
		t.Fatal("want the gh error")
	}
}
