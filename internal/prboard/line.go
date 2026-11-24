package prboard

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultRefreshEvery is how often the server collects when nothing asks it to.
const DefaultRefreshEvery = 5 * time.Minute

// Term draws the board for a terminal: the statusline segment, the block
// and the legend. The zero value is not useful; see the command for defaults.
type Term struct {
	ASCII   bool
	NoColor bool   // also drops the hyperlink, which is an escape code too
	Label   string // what the segment answers to; "" drops it
	Link    string // where the label points; "" unlinks it
	Now     time.Time
	// Stale is the store age past which the output says how old it is. It is
	// a multiple of the refresh window, so a refresh that is merely due says
	// nothing and only a refresh that keeps failing does.
	Stale      time.Duration
	Owners     []string
	RepoChars  int
	TitleChars int
	Max        int // rows before "+N more"
}

type glyphs struct{ ok, pend, fail, none, ell, head, sep, eyes, draft string }

func (t Term) g() glyphs {
	if t.ASCII {
		return glyphs{ok: "+", pend: "~", fail: "x", none: ".", ell: ">", head: "PR", sep: ".", eyes: "@", draft: "d"}
	}
	return glyphs{ok: "✓", pend: "◷", fail: "✗", none: "·", ell: "…", head: "⑂", sep: "·", eyes: "⊙", draft: "✎"}
}

const (
	cRed   = "31"
	cAmber = "33"
	cGreen = "32"
	cCyan  = "36"
	cGrey  = "90"
)

func (t Term) paint(s, code string) string {
	if t.NoColor || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// link wraps s in an OSC 8 hyperlink. A terminal that does not know it drops
// the sequence and shows the text.
func (t Term) link(s string) string {
	if t.Link == "" || t.NoColor {
		return s
	}
	return "\x1b]8;;" + t.Link + "\x1b\\" + s + "\x1b]8;;\x1b\\"
}

// HumanAge is 25m, 3h or 2d.
func HumanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// staleMark is "(stale 25m)" once the store is past t.Stale, else "".
func (t Term) staleMark(refreshed time.Time) string {
	age := t.Now.Sub(refreshed)
	if t.Stale <= 0 || age < t.Stale {
		return ""
	}
	return t.paint("(stale "+HumanAge(age)+")", cGrey)
}

// StDraft is the counter drafts get on the page header and the segment,
// apart from the colours: a draft is parked, not missing something. Its row
// keeps its state (red) on the page.
const StDraft = "draft"

// counterOf is the header counter a card counts in.
func counterOf(c Card) string {
	if c.Draft {
		return StDraft
	}
	return c.State
}

// StateCounts counts the user's open PRs per header counter, as the page
// does: drafts on their own, PRs to review not at all.
func StateCounts(v View) map[string]int {
	c := map[string]int{StReady: 0, StApprove: 0, StMissing: 0, StDraft: 0}
	for _, g := range v.Repos {
		for _, r := range g.Rows {
			c[counterOf(r.Card)]++
		}
	}
	return c
}

// Segment is the statusline segment: the label, then your open PRs by
// colour (ready to merge, something missing, ready to approve), how many
// wait on your review and, last, your drafts. A zero is left out; the label
// alone means nothing is open. A store that was never filled prints nothing.
func (t Term) Segment(s *Store) string {
	if s == nil || s.Refreshed.IsZero() {
		return ""
	}
	v := BuildView(s, t.Now)
	c := StateCounts(v)
	g := t.g()
	var parts []string
	if t.Label != "" {
		parts = append(parts, t.link(t.paint(t.Label, cCyan)))
	}
	for _, x := range []struct {
		n          int
		glyph, col string
	}{
		{c[StReady], g.ok, cGreen},
		{c[StMissing], g.fail, cRed},
		{c[StApprove], g.pend, cAmber},
		{v.ToReview, g.eyes, cCyan},
		{c[StDraft], g.draft, cGrey},
	} {
		if x.n > 0 {
			parts = append(parts, t.paint(fmt.Sprintf("%s%d", x.glyph, x.n), x.col))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if m := t.staleMark(s.Refreshed); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, " ")
}

// A light is one of the two per row in the block, ordered worst first.
type light int

const (
	lFail light = iota
	lPend
	lNone
	lOK
)

func (t Term) lightText(l light) string {
	g := t.g()
	switch l {
	case lFail:
		return t.paint(g.fail, cRed)
	case lPend:
		return t.paint(g.pend, cAmber)
	case lOK:
		return t.paint(g.ok, cGreen)
	}
	return t.paint(g.none, cGrey)
}

func hasSignal(c Card, kinds ...string) bool {
	return slices.ContainsFunc(c.Signals, func(s Signal) bool { return slices.Contains(kinds, s.K) })
}

// ciLight: a conflict is red like a failing check, since it blocks the merge
// and is yours to fix; no checks at all is grey, not green.
func ciLight(c Card) light {
	switch {
	case hasSignal(c, "ci_fail", "conflict"):
		return lFail
	case hasSignal(c, "ci_pend"):
		return lPend
	case hasSignal(c, "ci_pass"):
		return lOK
	}
	return lNone
}

// reviewLight: nobody asked is grey, not "waiting".
func reviewLight(c Card) light {
	switch {
	case hasSignal(c, "changes"):
		return lFail
	case hasSignal(c, "approved"):
		return lOK
	case hasSignal(c, "rev"):
		return lPend
	}
	return lNone
}

// drafts last: they are parked
var stateRank = map[string]int{StMissing: 0, StApprove: 1, StReady: 2, StDraft: 3}

var stateColour = map[string]string{StMissing: cRed, StApprove: cAmber, StReady: cGreen, StDraft: cGrey}

func (t Term) cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + t.g().ell
}

func padr(s string, n int) string {
	if k := n - utf8.RuneCountInString(s); k > 0 {
		return s + strings.Repeat(" ", k)
	}
	return s
}

func shortRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

type listRow struct {
	group string
	card  Card
	ci    light
	rv    light
}

func sortRows(rs []listRow) {
	slices.SortStableFunc(rs, func(a, b listRow) int {
		if c := cmp.Compare(stateRank[counterOf(a.card)], stateRank[counterOf(b.card)]); c != 0 {
			return c
		}
		if c := cmp.Compare(min(a.ci, a.rv), min(b.ci, b.rv)); c != 0 {
			return c
		}
		if c := cmp.Compare(max(a.ci, a.rv), max(b.ci, b.rv)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.card.Repo, b.card.Repo); c != 0 {
			return c
		}
		return cmp.Compare(a.card.N, b.card.N)
	})
}

// List is the whole block: a header, then a row per open PR, yours first and
// then the ones waiting on your review, worst first and drafts last. Each
// row has two lights (CI, review), the repository, the number in its
// header counter's colour and the title. Nothing open prints nothing.
func (t Term) List(s *Store) string {
	if s == nil || s.Refreshed.IsZero() {
		return ""
	}
	v := BuildView(s, t.Now)
	var mine, review []listRow
	for _, g := range v.Repos {
		for _, r := range g.Rows {
			mine = append(mine, listRow{group: "mine", card: r.Card, ci: ciLight(r.Card), rv: reviewLight(r.Card)})
		}
	}
	for _, r := range v.Review.Rows {
		review = append(review, listRow{group: "review", card: r.Card, ci: ciLight(r.Card), rv: reviewLight(r.Card)})
	}
	sortRows(mine)
	sortRows(review)
	all := append(mine, review...)
	if len(all) == 0 {
		return ""
	}
	g := t.g()
	head := t.paint(g.head, cCyan)
	if len(t.Owners) > 0 {
		head += " " + strings.Join(t.Owners, ", ")
	}
	head += "  " + t.paint(fmt.Sprintf("%d mine %s %d to review", len(mine), g.sep, len(review)), cGrey)
	if m := t.staleMark(s.Refreshed); m != "" {
		head += "  " + m
	}
	lines := []string{head}
	shown := all
	if t.Max > 0 && len(shown) > t.Max {
		shown = shown[:t.Max]
	}
	for i, r := range shown {
		label := ""
		if i == 0 || shown[i-1].group != r.group {
			label = r.group
		}
		title := r.card.Title
		if r.card.Draft {
			title = "DRAFT " + title
		}
		lines = append(lines, " "+t.paint(padr(label, 7), cCyan)+
			t.lightText(r.ci)+t.lightText(r.rv)+" "+
			t.paint(padr(t.cut(shortRepo(r.card.Repo), t.RepoChars), t.RepoChars), cGrey)+" "+
			t.paint(padr(fmt.Sprint(r.card.N), 5), stateColour[counterOf(r.card)])+" "+
			t.cut(title, t.TitleChars))
	}
	if hidden := len(all) - len(shown); hidden > 0 {
		lines = append(lines, t.paint(fmt.Sprintf("        + %d more", hidden), cGrey))
	}
	return strings.Join(lines, "\n") + "\n"
}

// Legend is the key for the segment and the block, in the same glyphs.
func (t Term) Legend() string {
	g := t.g()
	bar, cnr := "│", "└"
	if t.ASCII {
		bar, cnr = "|", "\\"
	}
	label := t.Label
	if label == "" {
		label = "the segment"
	}
	// the example is fixed so the tree lines up
	return fmt.Sprintf(`    [PRs] %[3]s2 %[1]s8 %[2]s3 %[4]s6 %[9]s4
          %[5]s  %[5]s  %[5]s  %[5]s  %[6]s four of yours are drafts
          %[5]s  %[5]s  %[5]s  %[6]s six are waiting on your review
          %[5]s  %[5]s  %[6]s three of yours are ready to approve
          %[5]s  %[6]s eight of yours have something missing
          %[6]s two of yours are ready to merge

    %[3]s  ready to merge      approved, every check passed, mergeable
    %[1]s  something missing   CI failing or running, a conflict, behind, changes
                          requested, a branch rule or a required PR still open
    %[2]s  ready to approve    everything is in place except an approval
    %[4]s  to review           someone else's open PR that requests your review
    %[9]s  drafts              yours, still drafts, whatever else they need

These are the page's header counters, so the segment and the board always agree.
A count of zero is left out, so %[7]s on its own means nothing is open. The label
links to the board.

`+"`pr-board list`"+` spells it out, a row per pull request with two lights, CI on the
left and review on the right, and the number in its counter's colour, drafts last:

          CI                          review
    %[3]s     every check passed          approved
    %[2]s     still running               a reviewer is requested
    %[1]s     failing, or a conflict      changes requested
    %[8]s     the repo runs no checks     nobody has been asked

Colour repeats the shape, so a terminal without one still reads.
`, g.fail, g.pend, g.ok, g.eyes, bar, cnr, label, g.none, g.draft)
}
