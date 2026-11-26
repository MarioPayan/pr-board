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

// rowString renders a group's rows as "#1 .#2 ..#3", one dot per level,
// with the repo name in front of any PR outside acme/app.
func rowString(g Group) string {
	var out []string
	for _, r := range g.Rows {
		name := ""
		if r.Repo != "acme/app" {
			_, name, _ = strings.Cut(r.Repo, "/")
		}
		out = append(out, fmt.Sprintf("%s%s#%d", strings.Repeat(".", r.Depth), name, r.N))
	}
	return strings.Join(out, " ")
}

// groupsString renders groups as "key: rows | key: rows".
func groupsString(gs []Group) string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Key+": "+rowString(g))
	}
	return strings.Join(out, " | ")
}

// in moves p to repo.
func in(repo string, p PR) PR {
	p.Repo = repo
	return p
}

// titled gives p a title.
func titled(title string, p PR) PR {
	p.Title = title
	return p
}

func TestChains(t *testing.T) {
	cases := []struct {
		name string
		prs  []PR
		want string
	}{
		{"independent PRs, newest first", []PR{openPR(1, "a", "main"), openPR(2, "b", "main")}, "#2 #1"},
		{"stack: base is another PR's head", []PR{openPR(1, "a", "main"), openPR(2, "b", "a"), openPR(3, "c", "b")}, "#1 .#2 ..#3"},
		{"two followers: both under the parent, newest first", []PR{openPR(1, "a", "main"), openPR(2, "b", "a"), openPR(3, "c", "a")}, "#1 .#3 .#2"},
		{"stack cycle still shows both", []PR{openPR(1, "a", "b"), openPR(2, "b", "a")}, "#2 .#1"},
		{"same-repo requirement chains, required PR first", []PR{openPR(10, "a", "main"), req(openPR(11, "b", "main"), "acme/app#10")}, "#10 .#11"},
		{"stack wins over a requirement", []PR{openPR(1, "a", "main"), req(openPR(2, "b", "a"), "acme/app#3"), openPR(3, "c", "main")}, "#3 #1 .#2"},
		{"two same-repo requirements chain after the first", []PR{openPR(1, "a", "main"), openPR(2, "b", "main"), req(openPR(3, "c", "main"), "acme/app#2", "acme/app#1")}, "#2 .#3 #1"},
		{"first requirement not on the board: chain after the next", []PR{openPR(1, "a", "main"), req(openPR(3, "c", "main"), "acme/app#99", "acme/app#1")}, "#1 .#3"},
		{"requirement cycle does not loop", []PR{req(openPR(1, "a", "main"), "acme/app#2"), req(openPR(2, "b", "main"), "acme/app#1")}, "#2 .#1"},
		{"cross-repo requirement chains in one category", []PR{in("acme/lib", openPR(1, "a", "main")), req(openPR(2, "b", "main"), "acme/lib#1")}, "lib#1 .#2"},
		{"a branch of the same name in another repo is no stack", []PR{in("acme/lib", openPR(1, "a", "main")), openPR(2, "b", "a")}, "#2 lib#1"},
		{"cross-repo requirement not on the board is no link", []PR{openPR(1, "a", "main"), req(openPR(2, "b", "main"), "acme/other#1")}, "#2 #1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := BuildView(&Store{PRs: c.prs}, now)
			if len(v.Categories) != 1 {
				t.Fatalf("categories: %s", groupsString(v.Categories))
			}
			if got := rowString(v.Categories[0]); got != c.want {
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
	g := v.Categories[0]
	if got := rowString(g); got != "#2 .#3 #1" {
		t.Fatalf("rows %q", got)
	}
	c := g.Rows[1].Card
	var chips []string
	for _, s := range c.Signals {
		if s.Text != "" {
			chips = append(chips, s.Text)
		}
	}
	if got := strings.Join(chips, ";"); got != "after #1;after infra#7" {
		t.Errorf("chips on #3 (the linked #2 has none): %q", got)
	}
	if c.Parent != "acme/app#2" || c.Link != "required" || c.After != "after #2" {
		t.Errorf("parent %q link %q after %q", c.Parent, c.Link, c.After)
	}
	if c.State != StMissing || c.Next != "merge #2 first" {
		t.Errorf("state %s next %q", c.State, c.Next)
	}
}

func TestGroups(t *testing.T) {
	merged := titled("[Billing] Old", openPR(5, "old", "main"))
	merged.State = "MERGED"
	release := in("acme/infra", openPR(7, "x", "release"))
	parent := titled("[Icons] Icon set", in("acme/ds", openPR(88, "icons", "main")))
	child := titled("[Icons] Serve the icons", req(openPR(36, "serve", "main"), "acme/ds#88"))
	elsewhere := titled("[Billing] Needs the icons", req(openPR(40, "bill", "main"), "acme/ds#88"))
	s := &Store{PRs: []PR{
		openPR(1, "a", "main"), titled("[Billing] b", openPR(2, "b", "a")), merged, release,
		parent, child, elsewhere, titled("[Billing] Stack top", openPR(3, "c", "b")),
	}}
	v := BuildView(s, now)
	if v.Open != 7 || v.Merged != 1 {
		t.Fatalf("counts: open %d merged %d", v.Open, v.Merged)
	}
	// alphabetical, Untagged last; a chain across repos stays together; a
	// child whose parent is in another group starts a chain of its own
	if got, want := groupsString(v.Categories), "Billing: #40 #2 .#3 | Icons: ds#88 .#36 | : #1 infra#7"; got != want {
		t.Errorf("categories\n got %q\nwant %q", got, want)
	}
	if got, want := groupsString(v.Repos), "acme/app: #40 #36 #1 .#2 ..#3 | acme/ds: ds#88 | acme/infra: infra#7"; got != want {
		t.Errorf("repos\n got %q\nwant %q", got, want)
	}
	byKey := map[string]Row{}
	for _, g := range v.Categories {
		for _, r := range g.Rows {
			byKey[r.Key] = r
		}
	}
	// the stack within Billing: #3 sits on #2, which sits on #1 in Untagged
	if r := byKey["acme/app#3"]; r.Parent != "acme/app#2" || r.Link != "stack" || r.Depth != 1 || r.After != "after #2" {
		t.Errorf("#3: parent %q link %q depth %d after %q", r.Parent, r.Link, r.Depth, r.After)
	}
	if r := byKey["acme/app#2"]; r.Parent != "acme/app#1" || r.Depth != 0 {
		t.Errorf("#2: parent %q depth %d (its parent is in Untagged)", r.Parent, r.Depth)
	}
	if r := byKey["acme/app#40"]; r.After != "after ds#88" || r.Depth != 0 {
		t.Errorf("#40 after %q depth %d", r.After, r.Depth)
	}
	if r := byKey["acme/app#36"]; r.After != "after ds#88" || r.Depth != 1 || len(r.Signals) != 2 {
		t.Errorf("#36 after %q depth %d signals %+v (no req chip)", r.After, r.Depth, r.Signals)
	}
	if r := byKey["acme/infra#7"]; r.Base != "release" {
		t.Errorf("base %q", r.Base)
	}
	bill := v.Categories[0]
	if len(bill.Merged) != 1 || bill.Merged[0].State != StMerged || strings.Join(bill.Repos, ",") != "acme/app" {
		t.Errorf("billing merged %+v repos %v", bill.Merged, bill.Repos)
	}
	if got := strings.Join(v.Categories[2].Repos, ","); got != "acme/app,acme/infra" {
		t.Errorf("untagged repos %q", got)
	}
	if v.Repos[0].Branch != "main" || len(v.Repos[0].Merged) != 1 {
		t.Errorf("app group %+v", v.Repos[0])
	}
}

func TestMergedOnlyGroup(t *testing.T) {
	merged := in("acme/old", titled("[Gone] x", openPR(5, "old", "main")))
	merged.State = "MERGED"
	v := BuildView(&Store{PRs: []PR{merged, openPR(1, "a", "main")}}, now)
	if got := groupsString(v.Categories); got != "Gone:  | : #1" {
		t.Errorf("categories %q", got)
	}
	if g := v.Categories[0]; strings.Join(g.Repos, ",") != "acme/old" || len(g.Merged) != 1 {
		t.Errorf("merged-only group %+v", g)
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

func TestReviewGroup(t *testing.T) {
	mine := openPR(1, "a", "main")
	rv := func(repo string, n int, head, baseRef string) PR {
		p := in(repo, openPR(n, head, baseRef))
		p.ToReview, p.Author = true, "bob"
		p.Title = "[Billing] review me"
		return p
	}
	merged := rv("acme/app", 9, "z", "main")
	merged.State = "MERGED"
	// #3 is based on my branch, yet a review never joins a chain or a group
	s := &Store{PRs: []PR{mine, rv("acme/app", 3, "c", "a"), rv("acme/web", 4, "d", "main"), rv("acme/app", 2, "b", "main"), merged}}
	v := BuildView(s, now)
	if v.Open != 1 || v.Merged != 0 || v.ToReview != 3 {
		t.Fatalf("counts: open %d merged %d to review %d", v.Open, v.Merged, v.ToReview)
	}
	if got, want := rowString(v.Review), "#3 #2 web#4"; got != want {
		t.Errorf("review rows %q want %q", got, want)
	}
	if got := strings.Join(v.Review.Repos, ","); got != "acme/app,acme/web" || v.Review.Key != "To review" || len(v.Review.Merged) != 0 {
		t.Errorf("review group %+v", v.Review)
	}
	if r := v.Review.Rows[0]; r.Parent != "" || r.Depth != 0 || r.Author != "bob" {
		t.Errorf("review row %+v", r)
	}
	// a PR to review is the user's move, whatever its author has to do
	for _, r := range v.Review.Rows {
		if r.Next != "review" || r.NextKind != NextReview || !r.YourMove {
			t.Errorf("review row %s: next=%q kind=%q mine=%v", r.Key, r.Next, r.NextKind, r.YourMove)
		}
	}
	if got := groupsString(v.Categories) + " / " + groupsString(v.Repos); got != ": #1 / acme/app: #1" {
		t.Errorf("groups %q: reviews must stay out", got)
	}
}
