package prboard

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// lineStore holds one PR per colour of yours, plus n of them missing, and
// review PRs to review.
func lineStore(missing, approve, ready, review int) *Store {
	s := &Store{Refreshed: now.Add(-time.Minute)}
	n := 0
	add := func(mod func(*PR)) {
		n++
		p := base()
		p.Number = n
		p.Title = "[Checkout] change " + string(rune('a'+n%26))
		mod(&p)
		s.PRs = append(s.PRs, p)
	}
	for range missing {
		add(func(p *PR) { p.ChecksFailing = []string{"unit"} })
	}
	for range approve {
		add(func(p *PR) { p.ReviewDecision, p.Reviews, p.Requests = "REVIEW_REQUIRED", nil, []string{"bob"} })
	}
	for range ready {
		add(func(p *PR) {})
	}
	for range review {
		add(func(p *PR) { p.ToReview, p.Author = true, "carol" })
	}
	return s
}

func plainTerm() Term {
	return Term{NoColor: true, Label: "[PRs]", Now: now, Stale: 4 * DefaultRefreshEvery, RepoChars: 12, TitleChars: 60, Max: 15}
}

func TestSegment(t *testing.T) {
	colour := plainTerm()
	colour.NoColor, colour.Link = false, "http://127.0.0.1:7878/"
	cases := []struct {
		name  string
		term  func(Term) Term
		store *Store
		want  string
	}{
		{"counts by importance: merge, missing, approve, review", nil, lineStore(2, 1, 3, 4), "[PRs] ✓3 ✗2 ◷1 ⊙4"},
		{"zeros are left out", nil, lineStore(0, 2, 0, 0), "[PRs] ◷2"},
		{"only reviews", nil, lineStore(0, 0, 0, 5), "[PRs] ⊙5"},
		{"nothing open is the label alone", nil, lineStore(0, 0, 0, 0), "[PRs]"},
		{"no label and nothing open prints nothing", func(t Term) Term { t.Label = ""; return t }, lineStore(0, 0, 0, 0), ""},
		{"no label", func(t Term) Term { t.Label = ""; return t }, lineStore(1, 0, 0, 0), "✗1"},
		{"a store never filled prints nothing", nil, &Store{}, ""},
		{"no store prints nothing", nil, nil, ""},
		{"a due refresh says nothing", nil, func() *Store { s := lineStore(1, 0, 0, 0); s.Refreshed = now.Add(-19 * time.Minute); return s }(), "[PRs] ✗1"},
		{"far past the refresh window says its age", nil, func() *Store { s := lineStore(1, 0, 0, 0); s.Refreshed = now.Add(-25 * time.Minute); return s }(), "[PRs] ✗1 (stale 25m)"},
		{"hours stale", nil, func() *Store { s := lineStore(0, 0, 1, 0); s.Refreshed = now.Add(-3 * time.Hour); return s }(), "[PRs] ✓1 (stale 3h)"},
		{"ascii", func(t Term) Term { t.ASCII = true; return t }, lineStore(1, 1, 1, 1), "[PRs] +1 x1 ~1 @1"},
		{"NO_COLOR drops the link too", func(t Term) Term { t.Link = "http://127.0.0.1:7878/"; return t }, lineStore(1, 0, 0, 0), "[PRs] ✗1"},
		{"colour and link", func(Term) Term { return colour }, lineStore(1, 0, 1, 2),
			"\x1b]8;;http://127.0.0.1:7878/\x1b\\\x1b[36m[PRs]\x1b[0m\x1b]8;;\x1b\\ \x1b[32m✓1\x1b[0m \x1b[31m✗1\x1b[0m \x1b[36m⊙2\x1b[0m"},
		{"colour without link", func(Term) Term { c := colour; c.Link = ""; return c }, lineStore(0, 0, 0, 0), "\x1b[36m[PRs]\x1b[0m"},
		{"drafts count on their own, not as something missing", nil, func() *Store {
			s := lineStore(2, 0, 1, 1)
			s.PRs[0].Draft, s.PRs[2].Draft = true, true
			return s
		}(), "[PRs] ✗1 ⊙1 ✎2"},
		{"drafts come last, after the reviews", nil, func() *Store {
			s := lineStore(1, 1, 1, 2)
			s.PRs[0].Draft = true
			s.PRs = append(s.PRs, lineStore(1, 0, 0, 0).PRs...)
			s.PRs[len(s.PRs)-1].Number = 99
			return s
		}(), "[PRs] ✓1 ✗1 ◷1 ⊙2 ✎1"},
		{"drafts in ascii", func(t Term) Term { t.ASCII = true; return t }, func() *Store {
			s := lineStore(0, 1, 0, 0)
			s.PRs[0].Draft = true
			return s
		}(), "[PRs] d1"},
		{"drafts are dim", func(Term) Term { c := colour; c.Link = ""; return c }, func() *Store {
			s := lineStore(1, 0, 0, 0)
			s.PRs[0].Draft = true
			return s
		}(), "\x1b[36m[PRs]\x1b[0m \x1b[90m✎1\x1b[0m"},
		{"merged PRs do not count", nil, func() *Store {
			s := lineStore(0, 0, 1, 0)
			s.PRs[0].State = "MERGED"
			return s
		}(), "[PRs]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm := plainTerm()
			if c.term != nil {
				tm = c.term(tm)
			}
			if got := tm.Segment(c.store); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestSegmentMatchesTheHeaderCounters(t *testing.T) {
	s := DemoStore(now)
	s.Refreshed = now
	c := StateCounts(BuildView(s, now))
	want := "[PRs]"
	for _, x := range []struct {
		n int
		g string
	}{{c[StReady], "✓"}, {c[StMissing], "✗"}, {c[StApprove], "◷"}, {BuildView(s, now).ToReview, "⊙"}, {c[StDraft], "✎"}} {
		if x.n > 0 {
			want += " " + x.g + strconv.Itoa(x.n)
		}
	}
	if got := plainTerm().Segment(s); got != want {
		t.Errorf("segment %q, counters %q", got, want)
	}
	if c[StDraft] == 0 {
		t.Error("the demo board has no drafts")
	}
	drafts := 0
	for _, p := range s.PRs {
		if p.State == "OPEN" && p.Draft && !p.ToReview {
			drafts++
		}
	}
	if c[StDraft] != drafts {
		t.Errorf("draft counter %d, open drafts %d", c[StDraft], drafts)
	}
	if c[StMissing]+c[StApprove]+c[StReady]+c[StDraft] != BuildView(s, now).Open {
		t.Errorf("counts %v do not add up to %d open", c, BuildView(s, now).Open)
	}
}

func TestList(t *testing.T) {
	mk := func(mods ...func(*Store)) *Store {
		s := &Store{Refreshed: now.Add(-time.Minute)}
		add := func(n int, repo, title string, mod func(*PR)) {
			p := base()
			p.Repo, p.Number, p.Title, p.Head = repo, n, title, "b"+strconv.Itoa(n)
			mod(&p)
			s.PRs = append(s.PRs, p)
		}
		add(1, "acme/web", "ready one", func(p *PR) {})
		add(2, "acme/api", "failing", func(p *PR) { p.ChecksFailing = []string{"unit"} })
		add(3, "acme/web", "waits on a reviewer", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.ChecksTotal = "REVIEW_REQUIRED", nil, []string{"bob"}, 0
		})
		add(4, "acme/web", "conflict and changes", func(p *PR) {
			p.Mergeable, p.ReviewDecision = "CONFLICTING", "CHANGES_REQUESTED"
			p.Reviews = []Review{{Author: "bob", State: "CHANGES_REQUESTED"}}
		})
		add(5, "acme/design-system-tokens", "a draft with a very long title that goes on and on past the cap", func(p *PR) {
			p.Draft, p.ChecksPending, p.ReviewDecision, p.Reviews = true, 2, "REVIEW_REQUIRED", nil
		})
		add(9, "acme/api", "please review", func(p *PR) { p.ToReview, p.Author, p.ReviewDecision, p.Reviews = true, "carol", "", nil })
		add(8, "acme/api", "merged long ago", func(p *PR) { p.State = "MERGED" })
		for _, m := range mods {
			m(s)
		}
		return s
	}
	cases := []struct {
		name  string
		term  func(Term) Term
		store *Store
		want  string
	}{
		{"mine then review, worst first", func(t Term) Term { t.Owners = []string{"acme"}; t.TitleChars = 30; return t }, mk(), `⑂ acme  5 mine · 1 to review
 mine   ✗✗ web          4     conflict and changes
        ✗✓ api          2     failing
        ·◷ web          3     waits on a reviewer
        ✓✓ web          1     ready one
        ◷· design-syst… 5     DRAFT a draft with a very lon…
 review ✓· api          9     please review
`},
		{"truncated with + N more", func(t Term) Term { t.Max = 2; t.ASCII = true; return t }, mk(), `PR  5 mine . 1 to review
 mine   xx web          4     conflict and changes
        x+ api          2     failing
        + 4 more
`},
		{"stale says its age in the header", nil, mk(func(s *Store) { s.Refreshed = now.Add(-2 * 24 * time.Hour) }), "⑂  5 mine · 1 to review  (stale 2d)\n"},
		{"nothing open prints nothing", nil, &Store{Refreshed: now}, ""},
		{"no store prints nothing", nil, &Store{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm := plainTerm()
			if c.term != nil {
				tm = c.term(tm)
			}
			got := tm.List(c.store)
			if c.name == "stale says its age in the header" {
				got, _, _ = strings.Cut(got, "\n")
				got += "\n"
			}
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestListColoursTheNumberByState(t *testing.T) {
	tm := plainTerm()
	tm.NoColor = false
	st := lineStore(1, 1, 2, 0)
	st.PRs[3].Draft = true
	got := tm.List(st)
	for _, want := range []string{"\x1b[31m1    \x1b[0m", "\x1b[33m2    \x1b[0m", "\x1b[32m3    \x1b[0m", "\x1b[90m4    \x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%q", want, got)
		}
	}
}

func TestLegendFollowsTheGlyphs(t *testing.T) {
	tm := plainTerm()
	if l := tm.Legend(); !strings.Contains(l, "✓2 ✗8 ◷3 ⊙6 ✎4") || !strings.Contains(l, "└ two of yours are ready to merge") || !strings.Contains(l, "✎  drafts") {
		t.Errorf("unicode legend:\n%s", l)
	}
	tm.ASCII = true
	if l := tm.Legend(); !strings.Contains(l, "+2 x8 ~3 @6 d4") || strings.ContainsAny(l, "✗◷✓✎⊙│└") {
		t.Errorf("ascii legend:\n%s", l)
	}
}
