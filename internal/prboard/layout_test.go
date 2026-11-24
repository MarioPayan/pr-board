package prboard

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func openPR(n int, head, baseRef string) PR {
	p := base()
	p.Number, p.Head, p.Base = n, head, baseRef
	return p
}

// laneString renders lanes as "#1 = #2 + #3 | ..." (= stack, + required).
func laneString(lanes [][]Item) string {
	var out []string
	for _, l := range lanes {
		var b strings.Builder
		for _, it := range l {
			switch {
			case it.Card != nil:
				fmt.Fprintf(&b, "#%d", it.Card.N)
			case it.Ref != "":
				b.WriteString("^" + it.Ref)
			case it.Link == "stack":
				b.WriteString(" = ")
			case it.Link == "required":
				b.WriteString(" + ")
			}
		}
		out = append(out, b.String())
	}
	return strings.Join(out, " | ")
}

func TestChains(t *testing.T) {
	cases := []struct {
		name string
		prs  []PR
		want string
	}{
		{"independent PRs, newest first", []PR{openPR(1, "a", "main"), openPR(2, "b", "main")}, "#2 | #1"},
		{"stack: base is another PR's head", []PR{openPR(1, "a", "main"), openPR(2, "b", "a"), openPR(3, "c", "b")}, "#1 = #2 = #3"},
		{"two followers: the second hangs off a reference", []PR{openPR(1, "a", "main"), openPR(2, "b", "a"), openPR(3, "c", "a")}, "#1 = #3 | ^#1 = #2"},
		{"stack cycle still shows both", []PR{openPR(1, "a", "b"), openPR(2, "b", "a")}, "#2 = #1"},
		{"same-repo requirement chains, required PR first", []PR{openPR(10, "a", "main"), req(openPR(11, "b", "main"), "acme/app#10")}, "#10 + #11"},
		{"stack wins over a requirement", []PR{openPR(1, "a", "main"), req(openPR(2, "b", "a"), "acme/app#3"), openPR(3, "c", "main")}, "#3 | #1 = #2"},
		{"two same-repo requirements chain after the first", []PR{openPR(1, "a", "main"), openPR(2, "b", "main"), req(openPR(3, "c", "main"), "acme/app#2", "acme/app#1")}, "#2 + #3 | #1"},
		{"first requirement not in the map: chain after the next", []PR{openPR(1, "a", "main"), req(openPR(3, "c", "main"), "acme/app#99", "acme/app#1")}, "#1 + #3"},
		{"cross-repo requirement is no link", []PR{openPR(1, "a", "main"), req(openPR(2, "b", "main"), "acme/other#1")}, "#2 | #1"},
		{"requirement cycle does not loop", []PR{req(openPR(1, "a", "main"), "acme/app#2"), req(openPR(2, "b", "main"), "acme/app#1")}, "#2 + #1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := laneString(lanes(c.prs, now)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// req gives p open requirements on keys.
func req(p PR, keys ...string) PR {
	for _, k := range keys {
		repo, n, _ := strings.Cut(k, "#")
		num, _ := strconv.Atoi(n)
		p.Requires = append(p.Requires, Requirement{Repo: repo, N: num, State: "OPEN"})
	}
	return p
}

func TestRequiredChips(t *testing.T) {
	p3 := req(openPR(3, "c", "main"), "acme/app#2", "acme/app#1", "acme/infra#7")
	v := BuildView(&Store{PRs: []PR{openPR(1, "a", "main"), openPR(2, "b", "main"), p3}}, now)
	c := v.Repos[0].Lanes[0][2].Card
	if c.N != 3 {
		t.Fatalf("lane: %s", laneString(v.Repos[0].Lanes))
	}
	var chips []string
	for _, s := range c.Signals {
		if s.Text != "" {
			chips = append(chips, s.Text)
		}
	}
	if got := strings.Join(chips, ";"); got != "after #1;after infra#7" {
		t.Errorf("chips on #3 (the linked #2 has none): %q", got)
	}
	if c.State != StMissing || c.Next != "merge #2 first" {
		t.Errorf("state %s next %q", c.State, c.Next)
	}
}

func TestBuildView(t *testing.T) {
	merged := openPR(5, "old", "main")
	merged.State = "MERGED"
	other := openPR(7, "x", "release")
	other.Repo = "acme/infra"
	s := &Store{PRs: []PR{openPR(1, "a", "main"), openPR(2, "b", "a"), merged, other}}
	v := BuildView(s, now)
	if v.Open != 3 || v.Merged != 1 {
		t.Fatalf("counts: open %d merged %d", v.Open, v.Merged)
	}
	if len(v.Repos) != 2 || v.Repos[0].Repo != "acme/app" || v.Repos[0].Branch != "main" {
		t.Fatalf("repos: %+v", v.Repos)
	}
	app, infra := v.Repos[0], v.Repos[1]
	if got := laneString(app.Lanes); got != "#1 = #2" {
		t.Errorf("app lanes %q", got)
	}
	if len(app.Merged) != 1 || app.Merged[0].State != StMerged {
		t.Errorf("merged: %+v", app.Merged)
	}
	if c := app.Lanes[0][2].Card; c.Base != "" {
		t.Errorf("stacked card should not repeat its base: %q", c.Base)
	}
	if c := infra.Lanes[0][0].Card; c.Base != "release" {
		t.Errorf("non-default base should show: %q", c.Base)
	}
}

func TestViewHasNoNullLists(t *testing.T) {
	merged := openPR(5, "old", "main")
	merged.State = "MERGED"
	for _, s := range []*Store{{}, {PRs: []PR{merged}}} {
		b, err := json.Marshal(BuildView(s, now))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("view JSON has null: %s", b)
		}
	}
}
