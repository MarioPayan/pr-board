package prboard

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// PR is what the collector stores for one pull request.
type PR struct {
	Repo           string        `json:"repo"` // owner/name
	Number         int           `json:"number"`
	Title          string        `json:"title"`
	URL            string        `json:"url"`
	Draft          bool          `json:"draft"`
	State          string        `json:"state"` // OPEN, MERGED, CLOSED
	MergedAt       time.Time     `json:"merged_at,omitzero"`
	Base           string        `json:"base"`
	Head           string        `json:"head"`
	HeadOID        string        `json:"head_oid"`
	HeadRepo       string        `json:"head_repo"`
	DefaultBranch  string        `json:"default_branch"`
	Mergeable      string        `json:"mergeable"`   // MERGEABLE, CONFLICTING, UNKNOWN
	MergeState     string        `json:"merge_state"` // CLEAN, BEHIND, DIRTY, BLOCKED, ...
	ReviewDecision string        `json:"review_decision"`
	Reviews        []Review      `json:"reviews"`
	Requests       []string      `json:"requests"`
	ChecksTotal    int           `json:"checks_total"`
	ChecksFailing  []string      `json:"checks_failing"`
	ChecksPending  int           `json:"checks_pending"`
	Threads        []Thread      `json:"threads"` // unresolved only
	UpdatedAt      time.Time     `json:"updated_at"`
	Body           string        `json:"body"`
	Requires       []Requirement `json:"requires,omitempty"` // from [required:...] in the body; open PRs only
	Author         string        `json:"author,omitempty"`
	ToReview       bool          `json:"to_review,omitempty"` // someone else's open PR that asks the user for a review
}

// Review is one entry of latestReviews.
type Review struct {
	Author string `json:"author"`
	State  string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
}

// Thread is an unresolved review thread with its first comments.
type Thread struct {
	Comments []Comment `json:"comments"`
}

// Comment is one review-thread comment.
type Comment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

// Key is owner/name#n.
func (p PR) Key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// Derived state classes; they are also the page's CSS classes and the
// State filter's values. An open PR is exactly one of the first three.
const (
	StReady   = "ready"   // green: ready to merge
	StApprove = "approve" // yellow: everything is in place except an approval
	StMissing = "missing" // red: anything else; the icons and Next say what
	StMerged  = "merged"  // purple
)

// Signal is one icon on a card. N is 0 when the icon has no count.
type Signal struct {
	K     string `json:"k"`
	N     int    `json:"n,omitempty"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"` // a chip's label (required PRs)
	Ref   string `json:"ref,omitempty"`  // the required PR's key
}

// SignalOrder is the fixed order of the signals on every card: CI, review
// state, reviewer, conflicts, behind, branch rule, required PRs, threads,
// stale. Kinds that share a slot keep the order Derive adds them in.
var SignalOrder = [][]string{
	{"ci_fail", "ci_pend", "ci_pass"},
	{"approved", "changes", "dismissed"},
	{"norev", "rev"},
	{"conflict"},
	{"behind"},
	{"blocked"},
	{"req_open", "req_closed", "req_unknown", "req_done"},
	{"threads"},
	{"stale"},
}

func signalSlot(k string) int {
	for i, kinds := range SignalOrder {
		if slices.Contains(kinds, k) {
			return i
		}
	}
	return len(SignalOrder)
}

// Derived is everything the page shows about a PR that follows from its data.
type Derived struct {
	State    string   `json:"state"`
	Label    string   `json:"label"`
	Signals  []Signal `json:"signals"`
	Next     string   `json:"next"`
	NextKind string   `json:"next_kind,omitempty"` // what kind of step Next is: one of the Next* constants
	YourMove bool     `json:"your_move,omitempty"` // Next is a step the user takes, not a wait
	Category string   `json:"category"`
	Draft    bool     `json:"draft,omitempty"` // shown as a tag and a dashed border, not a signal
}

// Next step kinds: Derived.NextKind says which kind of step Derived.Next is,
// so the page can draw it (an icon) and tell the user's own moves from waits.
const (
	NextConflict   = "conflict"    // merge the base branch to resolve conflicts
	NextChanges    = "changes"     // address the requested changes
	NextFix        = "fix"         // fix failing CI
	NextDraft      = "draft"       // mark the draft ready
	NextRequest    = "request"     // request a reviewer
	NextBlocked    = "blocked"     // wait: a required PR is still open
	NextRequired   = "required"    // a required PR was closed without merging or is not found
	NextMerge      = "merge"       // merge it
	NextUpdate     = "update"      // update from the base branch
	NextRule       = "rule"        // find the branch rule that blocks it
	NextWaitCI     = "wait-ci"     // wait: CI is running
	NextWaitPeople = "wait-people" // wait: reviewers are requested
	NextRefresh    = "refresh"     // wait: GitHub is still computing mergeability
	NextApproval   = "approval"    // get an approval
	NextReview     = "review"      // someone else's PR that asks the user for a review
)

// YourMove reports whether a step of this kind is one the user takes; the
// rest wait on CI, on people, on another PR or on GitHub.
func YourMove(kind string) bool {
	switch kind {
	case "", NextBlocked, NextWaitCI, NextWaitPeople, NextRefresh:
		return false
	}
	return true
}

// staleDays is how many days without an update make a PR stale.
const staleDays = 10

func (p PR) approved() bool {
	if p.ReviewDecision == "APPROVED" {
		return true
	}
	// repos without required reviews report no decision; an approving review still counts
	return p.ReviewDecision == "" && slices.ContainsFunc(p.Reviews, func(r Review) bool { return r.State == "APPROVED" })
}

func (p PR) changesRequested() bool {
	return p.ReviewDecision == "CHANGES_REQUESTED" ||
		slices.ContainsFunc(p.Reviews, func(r Review) bool { return r.State == "CHANGES_REQUESTED" })
}

func (p PR) conflict() bool { return p.Mergeable == "CONFLICTING" || p.MergeState == "DIRTY" }

func (p PR) noReviewer() bool { return p.ReviewDecision == "REVIEW_REQUIRED" && len(p.Requests) == 0 }

func reviewers(rs []Review, state string) []string {
	var out []string
	for _, r := range rs {
		if r.State == state {
			out = append(out, r.Author)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Derive computes state, signals, next step and category tag.
func Derive(p PR, now time.Time) Derived {
	d := Derived{Category: Category(p.Title, p.Repo), Signals: []Signal{}, Draft: p.Draft && p.State == "OPEN"}
	if p.State == "MERGED" {
		d.State, d.Label = StMerged, "Merged"
		if !p.MergedAt.IsZero() {
			d.Label = "Merged " + p.MergedAt.Local().Format("01-02")
		}
		return d
	}
	failing, pending := len(p.ChecksFailing), p.ChecksPending
	approved, changes, conflict := p.approved(), p.changesRequested(), p.conflict()

	add := func(k string, n int, title string) { d.Signals = append(d.Signals, Signal{K: k, N: n, Title: title}) }
	// required PRs that still hold this one come before the merged ones
	var reqNext, reqKind string
	addReq := func(r Requirement) {
		k, state, ref := "req_unknown", "not found", shortRef(r.Repo, p.Repo, r.N)
		switch r.State {
		case "MERGED":
			k, state = "req_done", "merged"
		case "OPEN":
			k, state = "req_open", "open"
		case "CLOSED":
			k, state = "req_closed", "closed without merging"
		}
		d.Signals = append(d.Signals, Signal{K: k, Title: "Merge after " + r.Key() + " (" + state + ")", Text: "after " + ref, Ref: r.Key()})
		if reqNext == "" {
			switch r.State {
			case "MERGED":
			case "OPEN":
				reqNext, reqKind = "merge "+ref+" first", NextBlocked
			case "CLOSED":
				reqNext, reqKind = ref+" was closed without merging", NextRequired
			default:
				reqNext, reqKind = "check "+ref+": not found", NextRequired
			}
		}
	}
	for _, r := range p.Requires {
		if r.State != "MERGED" {
			addReq(r)
		}
	}
	for _, r := range p.Requires {
		if r.State == "MERGED" {
			addReq(r)
		}
	}
	if failing > 0 {
		add("ci_fail", failing, fmt.Sprintf("%d CI %s failing: %s", failing, plural(failing, "job", "jobs"), strings.Join(p.ChecksFailing, ", ")))
	}
	if pending > 0 {
		add("ci_pend", pending, fmt.Sprintf("%d CI %s running", pending, plural(pending, "job", "jobs")))
	}
	if failing == 0 && pending == 0 && p.ChecksTotal > 0 {
		add("ci_pass", 0, "CI green")
	}
	if approved {
		t := "Approved"
		if who := reviewers(p.Reviews, "APPROVED"); len(who) > 0 {
			t = "Approved by " + strings.Join(who, ", ")
		}
		add("approved", 0, t)
	}
	if changes {
		t := "Changes requested"
		if who := reviewers(p.Reviews, "CHANGES_REQUESTED"); len(who) > 0 {
			t = "Changes requested by " + strings.Join(who, ", ")
		}
		add("changes", 0, t)
	}
	if who := reviewers(p.Reviews, "DISMISSED"); len(who) > 0 {
		add("dismissed", 0, "Approval dismissed: "+strings.Join(who, ", "))
	}
	if p.noReviewer() && !p.Draft {
		add("norev", 0, "No reviewer requested")
	}
	if len(p.Requests) > 0 {
		who := strings.Join(p.Requests, ", ")
		d.Signals = append(d.Signals, Signal{K: "rev", Title: "Waiting on " + who, Text: who})
	}
	if conflict {
		add("conflict", 0, "Merge conflicts with "+p.Base)
	}
	if p.MergeState == "BEHIND" {
		add("behind", 0, "Behind "+p.Base)
	}
	if p.MergeState == "BLOCKED" && approved && failing == 0 && pending == 0 && !changes {
		add("blocked", 0, "A branch rule still blocks the merge")
	}
	if n := len(p.Threads); n > 0 {
		add("threads", n, fmt.Sprintf("%d unresolved review %s", n, plural(n, "thread", "threads")))
	}
	if days := int(now.Sub(p.UpdatedAt).Hours() / 24); !p.UpdatedAt.IsZero() && days > staleDays {
		add("stale", days, fmt.Sprintf("%d days without an update", days))
	}
	slices.SortStableFunc(d.Signals, func(a, b Signal) int { return cmp.Compare(signalSlot(a.K), signalSlot(b.K)) })

	// in place: checks passed and none pending, no conflict, not behind, no
	// requested changes and no required PR still to merge
	inPlace := failing == 0 && pending == 0 && !conflict && p.MergeState != "BEHIND" && !changes && reqNext == ""
	ready := !p.Draft && approved && inPlace && p.Mergeable == "MERGEABLE" && p.MergeState != "BLOCKED"
	switch {
	case ready:
		d.State, d.Label = StReady, "Ready to merge"
	case !p.Draft && !approved && inPlace:
		d.State, d.Label = StApprove, "Ready to approve"
	default:
		d.State, d.Label = StMissing, "Something missing"
	}

	next := func(kind, text string) { d.NextKind, d.Next = kind, text }
	switch {
	case conflict:
		next(NextConflict, "merge "+p.Base)
	case changes:
		next(NextChanges, "address the requested changes")
	case failing > 0:
		next(NextFix, "fix failing CI")
	case p.Draft:
		next(NextDraft, "mark ready when you want review")
	case p.noReviewer():
		next(NextRequest, "request a reviewer")
	case reqNext != "":
		next(reqKind, reqNext)
	case ready:
		next(NextMerge, "merge")
	case p.MergeState == "BEHIND":
		next(NextUpdate, "update from "+p.Base)
	case p.MergeState == "BLOCKED" && approved && failing == 0 && pending == 0:
		next(NextRule, "check which branch rule blocks it on GitHub")
	case pending > 0:
		next(NextWaitCI, "wait for CI")
	case len(p.Requests) > 0:
		next(NextWaitPeople, "wait for "+strings.Join(p.Requests, ", "))
	case approved && p.Mergeable == "UNKNOWN":
		next(NextRefresh, "refresh: GitHub is still computing mergeability")
	case !approved:
		next(NextApproval, "get an approval")
	}
	d.YourMove = YourMove(d.NextKind)
	return d
}
