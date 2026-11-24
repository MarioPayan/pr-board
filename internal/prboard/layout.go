package prboard

import (
	"cmp"
	"fmt"
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
	Key   string `json:"key"`
	Repo  string `json:"repo"`
	N     int    `json:"n"`
	URL   string `json:"url"`
	Title string `json:"title"`
	Head  string `json:"head"`           // the PR's branch, for the page's search
	Base  string `json:"base,omitempty"` // set when it is neither the default branch nor a PR in the lane
	Derived
}

// Item is a lane element: a card, a link between two cards, or a reference
// to the card a lane branches from.
type Item struct {
	Card *Card  `json:"card,omitempty"`
	Link string `json:"link,omitempty"` // "stack" (base branch) or "required" ([required:] in the body); both solid
	Ref  string `json:"ref,omitempty"`  // "#123": the lane hangs off that card
}

// RepoView is one trunk.
type RepoView struct {
	Repo   string   `json:"repo"`
	Branch string   `json:"branch"`
	Lanes  [][]Item `json:"lanes"`
	Merged []Card   `json:"merged"`
}

// View is GET /api/prs.
type View struct {
	Refreshed time.Time  `json:"refreshed,omitzero"`
	Open      int        `json:"open"`
	Merged    int        `json:"merged"`
	Errors    []string   `json:"errors,omitempty"`
	Setup     string     `json:"setup,omitempty"` // the config file to edit when it names no owners
	Repos     []RepoView `json:"repos"`
}

// BuildView lays the store out as trunks and lanes.
func BuildView(s *Store, now time.Time) View {
	v := View{Refreshed: s.Refreshed, Errors: s.Errors, Repos: []RepoView{}}
	byRepo := map[string][]PR{}
	open := map[string]PR{} // key -> PR, open only
	for _, p := range s.PRs {
		byRepo[p.Repo] = append(byRepo[p.Repo], p)
		if p.State == "OPEN" {
			open[p.Key()] = p
			v.Open++
		} else if p.State == "MERGED" {
			v.Merged++
		}
	}
	for repo, prs := range byRepo {
		rv := RepoView{Repo: repo, Lanes: [][]Item{}, Merged: []Card{}}
		for _, p := range prs {
			if p.DefaultBranch != "" {
				rv.Branch = p.DefaultBranch
				break
			}
		}
		var openPRs []PR
		for _, p := range prs {
			if p.State == "OPEN" {
				openPRs = append(openPRs, p)
			} else if p.State == "MERGED" {
				rv.Merged = append(rv.Merged, makeCard(p, now))
			}
		}
		slices.SortFunc(rv.Merged, func(a, b Card) int { return cmp.Compare(b.N, a.N) })
		if l := lanes(openPRs, now); l != nil {
			rv.Lanes = l
		}
		if len(rv.Lanes) == 0 && len(rv.Merged) == 0 {
			continue
		}
		v.Repos = append(v.Repos, rv)
	}
	slices.SortFunc(v.Repos, func(a, b RepoView) int {
		if c := cmp.Compare(laneCount(b), laneCount(a)); c != 0 {
			return c
		}
		return cmp.Compare(a.Repo, b.Repo)
	})
	return v
}

func laneCount(r RepoView) int {
	n := 0
	for _, l := range r.Lanes {
		for _, it := range l {
			if it.Card != nil {
				n++
			}
		}
	}
	return n
}

func makeCard(p PR, now time.Time) Card {
	return Card{Key: p.Key(), Repo: p.Repo, N: p.Number, URL: p.URL, Title: p.Title, Head: p.Head, Derived: Derive(p, now)}
}

// lanes chains open PRs of one repo: a PR follows the PR whose head branch is
// its base, or else the first open PR of the lane its [required:] list names.
// That required PR's chip is dropped from the card, since the link shows it.
// A PR with several followers starts one lane per extra follower, headed by
// a reference to it.
func lanes(prs []PR, now time.Time) [][]Item {
	byHead := map[string]PR{}
	byNum := map[int]PR{}
	for _, p := range prs {
		byHead[p.Head] = p
		byNum[p.Number] = p
	}
	parent := map[int]int{}
	kind := map[int]string{}
	for _, p := range prs {
		if q, ok := byHead[p.Base]; ok && q.Number != p.Number && p.Base != p.DefaultBranch {
			parent[p.Number], kind[p.Number] = q.Number, "stack"
		}
	}
	isAncestor := func(a, b int) bool { // a is b or an ancestor of b
		for seen := 0; seen <= len(prs); seen++ {
			if a == b {
				return true
			}
			nb, ok := parent[b]
			if !ok {
				return false
			}
			b = nb
		}
		return true // cycle: treat as related
	}
	for _, p := range prs {
		if _, ok := parent[p.Number]; ok {
			continue
		}
		for _, r := range p.Requires {
			if r.Repo != p.Repo || r.N == p.Number {
				continue
			}
			if _, ok := byNum[r.N]; !ok || isAncestor(p.Number, r.N) {
				continue
			}
			parent[p.Number], kind[p.Number] = r.N, "required"
			break
		}
	}
	children := map[int][]int{}
	var roots []int
	for _, p := range prs {
		if q, ok := parent[p.Number]; ok {
			children[q] = append(children[q], p.Number)
		} else {
			roots = append(roots, p.Number)
		}
	}
	desc := func(a, b int) int { return cmp.Compare(b, a) }
	slices.SortFunc(roots, desc)
	for k := range children {
		slices.SortFunc(children[k], desc)
	}
	card := func(n int) *Card {
		p := byNum[n]
		c := makeCard(p, now)
		if p.Base != p.DefaultBranch && kind[n] != "stack" {
			c.Base = p.Base
		}
		if kind[n] == "required" {
			linked := fmt.Sprintf("%s#%d", p.Repo, parent[n])
			c.Signals = slices.DeleteFunc(c.Signals, func(s Signal) bool { return s.Ref == linked })
		}
		return &c
	}
	var out [][]Item
	seen := map[int]bool{}
	var walk func(n int, lane []Item)
	walk = func(n int, lane []Item) {
		seen[n] = true
		lane = append(slices.Clone(lane), Item{Card: card(n)})
		var kids []int
		for _, k := range children[n] {
			if !seen[k] {
				kids = append(kids, k)
			}
		}
		if len(kids) == 0 {
			out = append(out, lane)
			return
		}
		walk(kids[0], append(lane, Item{Link: kind[kids[0]]}))
		for _, k := range kids[1:] {
			if !seen[k] {
				walk(k, []Item{{Ref: fmt.Sprintf("#%d", n)}, {Link: kind[k]}})
			}
		}
	}
	for _, r := range roots {
		walk(r, nil)
	}
	// stacks that loop back on themselves have no root; start them anywhere
	rest := make([]int, 0)
	for _, p := range prs {
		if !seen[p.Number] {
			rest = append(rest, p.Number)
		}
	}
	slices.SortFunc(rest, desc)
	for _, n := range rest {
		if !seen[n] {
			walk(n, nil)
		}
	}
	return out
}
