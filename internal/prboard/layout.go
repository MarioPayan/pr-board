package prboard

import (
	"cmp"
	"slices"
	"time"
)

// Store is ~/.local/share/pr-board/prs.json.
type Store struct {
	Refreshed time.Time `json:"refreshed,omitzero"`
	Errors    []string  `json:"errors,omitempty"`
	PRs       []PR      `json:"prs"`
	Cleaned   []string  `json:"cleaned"` // keys removed by cleanup; never re-added
}

// Card is one PR as the page draws it.
type Card struct {
	Key    string `json:"key"`
	Repo   string `json:"repo"`
	N      int    `json:"n"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Head   string `json:"head"`             // the PR's branch, for the page's search
	Base   string `json:"base"`             // the branch it merges into
	Parent string `json:"parent,omitempty"` // key of the open PR it waits on
	Link   string `json:"link,omitempty"`   // "stack" (base branch) or "required" ([required:] in the body)
	After  string `json:"after,omitempty"`  // "after #N" or "after repo#N", naming Parent
	Derived
}

// Row is an open card in a group's display order. Depth is the number of
// its ancestors shown above it in the same group: 0 starts a chain.
type Row struct {
	Card
	Depth int `json:"depth"`
}

// Group is one band of the board: a category or a repository.
type Group struct {
	Key    string   `json:"key"`              // category ("" for Untagged) or owner/name
	Repos  []string `json:"repos"`            // the repositories its PRs live in
	Branch string   `json:"branch,omitempty"` // a repository group's default branch
	Rows   []Row    `json:"rows"`
	Merged []Card   `json:"merged"`
}

// View is GET /api/prs. Categories and Repos hold the same PRs, grouped
// two ways; the page shows one of them.
type View struct {
	Refreshed  time.Time `json:"refreshed,omitzero"`
	Open       int       `json:"open"`
	Merged     int       `json:"merged"`
	Errors     []string  `json:"errors,omitempty"`
	Setup      string    `json:"setup,omitempty"` // the config file to edit when it names no owners
	Categories []Group   `json:"categories"`      // alphabetical, Untagged last
	Repos      []Group   `json:"repos"`           // most open PRs first
}

// BuildView chains the open PRs and groups the store by category and by repository.
func BuildView(s *Store, now time.Time) View {
	v := View{Refreshed: s.Refreshed, Errors: s.Errors, Categories: []Group{}, Repos: []Group{}}
	var open []PR
	var merged []Card
	branch := map[string]string{}
	for _, p := range s.PRs {
		if branch[p.Repo] == "" {
			branch[p.Repo] = p.DefaultBranch
		}
		switch p.State {
		case "OPEN":
			open = append(open, p)
			v.Open++
		case "MERGED":
			merged = append(merged, makeCard(p, now))
			v.Merged++
		}
	}
	cards := chainCards(open, now)
	v.Categories = groupBy(cards, merged, func(c Card) string { return c.Category })
	slices.SortStableFunc(v.Categories, func(a, b Group) int {
		if (a.Key == "") != (b.Key == "") { // Untagged last
			if a.Key == "" {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Key, b.Key)
	})
	v.Repos = groupBy(cards, merged, func(c Card) string { return c.Repo })
	for i := range v.Repos {
		v.Repos[i].Branch = branch[v.Repos[i].Key]
	}
	slices.SortStableFunc(v.Repos, func(a, b Group) int {
		if c := cmp.Compare(len(b.Rows), len(a.Rows)); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return v
}

func makeCard(p PR, now time.Time) Card {
	return Card{Key: p.Key(), Repo: p.Repo, N: p.Number, URL: p.URL, Title: p.Title, Head: p.Head, Base: p.Base, Derived: Derive(p, now)}
}

// byRepoThenNewest orders cards by repository, then newest PR first.
func byRepoThenNewest(a, b Card) int {
	if c := cmp.Compare(a.Repo, b.Repo); c != 0 {
		return c
	}
	return cmp.Compare(b.N, a.N)
}

// chainCards gives every open PR its parent: the open PR of the same
// repository whose head branch is its base, or else the first open PR on the
// board, in any repository, that its [required:] list names. A link that
// would close a loop is skipped. The parent's required chip is dropped from
// the card, since the chain shows it.
func chainCards(prs []PR, now time.Time) []Card {
	prs = slices.Clone(prs)
	slices.SortFunc(prs, func(a, b PR) int { // oldest first, so a stack loop keeps the older link
		if c := cmp.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return cmp.Compare(a.Number, b.Number)
	})
	byKey := map[string]PR{}
	byHead := map[[2]string]PR{}
	for _, p := range prs {
		byKey[p.Key()] = p
		byHead[[2]string{p.Repo, p.Head}] = p
	}
	parent, link := map[string]string{}, map[string]string{}
	loops := func(child, to string) bool { // child is to or an ancestor of to
		for range len(prs) + 1 {
			if to == child {
				return true
			}
			next, ok := parent[to]
			if !ok {
				return false
			}
			to = next
		}
		return true
	}
	for _, p := range prs {
		q, ok := byHead[[2]string{p.Repo, p.Base}]
		if ok && p.Base != p.DefaultBranch && !loops(p.Key(), q.Key()) {
			parent[p.Key()], link[p.Key()] = q.Key(), "stack"
		}
	}
	for _, p := range prs {
		if _, ok := parent[p.Key()]; ok {
			continue
		}
		for _, r := range p.Requires {
			if _, ok := byKey[r.Key()]; !ok || loops(p.Key(), r.Key()) {
				continue
			}
			parent[p.Key()], link[p.Key()] = r.Key(), "required"
			break
		}
	}
	cards := make([]Card, 0, len(prs))
	for _, p := range prs {
		c := makeCard(p, now)
		if q, ok := parent[c.Key]; ok {
			pp := byKey[q]
			c.Parent, c.Link, c.After = q, link[c.Key], "after "+shortRef(pp.Repo, p.Repo, pp.Number)
			c.Signals = slices.DeleteFunc(c.Signals, func(s Signal) bool { return s.Ref == q })
		}
		cards = append(cards, c)
	}
	return cards
}

// groupBy splits cards into groups by key and orders each group's rows as
// chains: a card whose parent is in the same group comes right after its
// parent's earlier children and their own children, one level deeper.
func groupBy(open, merged []Card, key func(Card) string) []Group {
	idx := map[string]int{}
	groups := []Group{}
	at := func(k string) *Group {
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			idx[k] = i
			groups = append(groups, Group{Key: k, Repos: []string{}, Rows: []Row{}, Merged: []Card{}})
		}
		return &groups[i]
	}
	members := map[string][]Card{}
	for _, c := range open {
		at(key(c))
		members[key(c)] = append(members[key(c)], c)
	}
	for _, c := range merged {
		g := at(key(c))
		g.Merged = append(g.Merged, c)
	}
	for i := range groups {
		g := &groups[i]
		cs := slices.Clone(members[g.Key])
		slices.SortFunc(cs, byRepoThenNewest)
		in := map[string]bool{}
		for _, c := range cs {
			in[c.Key] = true
		}
		children := map[string][]Card{}
		var roots []Card
		for _, c := range cs {
			if in[c.Parent] {
				children[c.Parent] = append(children[c.Parent], c)
			} else {
				roots = append(roots, c)
			}
		}
		var walk func(c Card, depth int)
		walk = func(c Card, depth int) {
			g.Rows = append(g.Rows, Row{Card: c, Depth: depth})
			for _, k := range children[c.Key] {
				walk(k, depth+1)
			}
		}
		for _, r := range roots {
			walk(r, 0)
		}
		slices.SortFunc(g.Merged, byRepoThenNewest)
		spans := cs // the repos of its open PRs; of its merged ones when none is open
		if len(spans) == 0 {
			spans = g.Merged
		}
		for _, c := range spans {
			if !slices.Contains(g.Repos, c.Repo) {
				g.Repos = append(g.Repos, c.Repo)
			}
		}
		slices.Sort(g.Repos)
	}
	return groups
}
