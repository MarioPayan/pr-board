package prboard

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// base is an open, approved, green, mergeable PR: ready.
func base() PR {
	return PR{
		Repo: "acme/app", Number: 1, Title: "t", State: "OPEN", Base: "main", Head: "feat", DefaultBranch: "main",
		Mergeable: "MERGEABLE", MergeState: "CLEAN", ReviewDecision: "APPROVED",
		Reviews: []Review{{Author: "alice", State: "APPROVED"}}, ChecksTotal: 3, UpdatedAt: now.Add(-time.Hour),
	}
}

func sigKeys(d Derived) string {
	var ks []string
	for _, s := range d.Signals {
		k := s.K
		if s.N != 0 {
			k += "(" + strconv.Itoa(s.N) + ")"
		}
		ks = append(ks, k)
	}
	return strings.Join(ks, " ")
}

func TestDerive(t *testing.T) {
	cases := []struct {
		name    string
		mod     func(*PR)
		state   string
		label   string
		signals string
		next    string
	}{
		{"ready", func(p *PR) {}, StReady, "Ready to merge", "ci_pass approved", "merge"},
		{"merged", func(p *PR) { p.State = "MERGED"; p.MergedAt = time.Date(2026, 11, 20, 18, 0, 0, 0, time.Local) }, StMerged, "Merged 11-20", "", ""},
		// red: something is missing, and Next names it
		{"draft, approved and green", func(p *PR) { p.Draft = true }, StMissing, "Something missing", "draft ci_pass approved", "mark ready when you want review"},
		{"draft with conflicts", func(p *PR) { p.Draft = true; p.Mergeable = "CONFLICTING" }, StMissing, "Something missing", "draft ci_pass approved conflict", "merge main"},
		{"draft with no reviewer shows no norev", func(p *PR) {
			p.Draft, p.ReviewDecision, p.Reviews = true, "REVIEW_REQUIRED", nil
		}, StMissing, "Something missing", "draft ci_pass", "mark ready when you want review"},
		{"changes requested", func(p *PR) {
			p.ReviewDecision = "CHANGES_REQUESTED"
			p.Reviews = []Review{{Author: "alice", State: "CHANGES_REQUESTED"}, {Author: "bob", State: "DISMISSED"}}
		}, StMissing, "Something missing", "ci_pass changes dismissed", "address the requested changes"},
		{"failing CI", func(p *PR) { p.ChecksFailing = []string{"lint", "unit"} }, StMissing, "Something missing", "ci_fail(2) approved", "fix failing CI"},
		{"conflicting", func(p *PR) { p.Mergeable = "CONFLICTING" }, StMissing, "Something missing", "ci_pass approved conflict", "merge main"},
		{"dirty merge state", func(p *PR) { p.MergeState = "DIRTY"; p.Mergeable = "UNKNOWN" }, StMissing, "Something missing", "ci_pass approved conflict", "merge main"},
		{"approved, CI pending", func(p *PR) { p.ChecksPending = 4 }, StMissing, "Something missing", "ci_pend(4) approved", "wait for CI"},
		{"approved, behind", func(p *PR) { p.MergeState = "BEHIND" }, StMissing, "Something missing", "ci_pass approved behind", "update from main"},
		{"approved, blocked by a branch rule", func(p *PR) { p.MergeState = "BLOCKED" }, StMissing, "Something missing", "ci_pass approved blocked", "check which branch rule blocks it on GitHub"},
		{"approved, mergeability unknown", func(p *PR) { p.Mergeable = "UNKNOWN" }, StMissing, "Something missing", "ci_pass approved", "refresh: GitHub is still computing mergeability"},
		// yellow: everything is in place except an approval
		{"needs a reviewer", func(p *PR) { p.ReviewDecision, p.Reviews = "REVIEW_REQUIRED", nil }, StApprove, "Ready to approve", "ci_pass norev", "request a reviewer"},
		{"waiting on a requested reviewer", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests = "REVIEW_REQUIRED", nil, []string{"carol"}
		}, StApprove, "Ready to approve", "ci_pass rev", "wait for carol"},
		{"no approval, review rule blocks the merge", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.MergeState = "REVIEW_REQUIRED", nil, []string{"carol"}, "BLOCKED"
		}, StApprove, "Ready to approve", "ci_pass rev", "wait for carol"},
		{"no review policy, no review yet", func(p *PR) { p.ReviewDecision, p.Reviews = "", nil }, StApprove, "Ready to approve", "ci_pass", "get an approval"},
		{"no approval, mergeability unknown", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.Mergeable = "REVIEW_REQUIRED", nil, []string{"carol"}, "UNKNOWN"
		}, StApprove, "Ready to approve", "ci_pass rev", "wait for carol"},
		// no approval and something else missing: red, not yellow
		{"no approval, CI pending", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.ChecksPending = "REVIEW_REQUIRED", nil, []string{"carol"}, 1
		}, StMissing, "Something missing", "ci_pend(1) rev", "wait for CI"},
		{"no approval, CI failing", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.ChecksFailing = "REVIEW_REQUIRED", nil, []string{"unit"}
		}, StMissing, "Something missing", "ci_fail(1) norev", "fix failing CI"},
		{"no approval, conflicts", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.Mergeable = "REVIEW_REQUIRED", nil, []string{"carol"}, "CONFLICTING"
		}, StMissing, "Something missing", "ci_pass rev conflict", "merge main"},
		{"no approval, behind", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests, p.MergeState = "REVIEW_REQUIRED", nil, []string{"carol"}, "BEHIND"
		}, StMissing, "Something missing", "ci_pass rev behind", "update from main"},
		{"draft, otherwise ready to approve", func(p *PR) {
			p.Draft, p.ReviewDecision, p.Reviews = true, "REVIEW_REQUIRED", nil
		}, StMissing, "Something missing", "draft ci_pass", "mark ready when you want review"},
		// green edge cases
		{"no review policy, approving review", func(p *PR) { p.ReviewDecision = "" }, StReady, "Ready to merge", "ci_pass approved", "merge"},
		{"no checks at all", func(p *PR) { p.ChecksTotal = 0 }, StReady, "Ready to merge", "approved", "merge"},
		{"unresolved threads and stale", func(p *PR) {
			p.Threads = []Thread{{}, {}, {}}
			p.UpdatedAt = now.AddDate(0, 0, -10)
		}, StReady, "Ready to merge", "ci_pass approved threads(3) stale(10)", "merge"},
		{"seven days is not stale", func(p *PR) { p.UpdatedAt = now.AddDate(0, 0, -7) }, StReady, "Ready to merge", "ci_pass approved", "merge"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base()
			c.mod(&p)
			d := Derive(p, now)
			if d.State != c.state || d.Label != c.label || sigKeys(d) != c.signals || d.Next != c.next {
				t.Errorf("got state=%s label=%q signals=%q next=%q\nwant state=%s label=%q signals=%q next=%q",
					d.State, d.Label, sigKeys(d), d.Next, c.state, c.label, c.signals, c.next)
			}
		})
	}
}

func TestDeriveTitles(t *testing.T) {
	p := base()
	p.ChecksFailing = []string{"unit (3.12)"}
	d := Derive(p, now)
	if d.Signals[0].Title != "1 CI job failing: unit (3.12)" {
		t.Errorf("ci_fail title: %q", d.Signals[0].Title)
	}
	if d.Signals[1].Title != "Approved by alice" {
		t.Errorf("approved title: %q", d.Signals[1].Title)
	}
}

func TestDeriveCategory(t *testing.T) {
	cases := []struct{ title, body, want string }{
		{"[Checkout] fix", "", "Checkout"},
		{"[app] [WEB-123] fix", "Tickets: WEB-6147", ""}, // no Jira-key fallback, in the title or the body
		{"fix", "[Checkout] in the body is not a category", ""},
	}
	for _, c := range cases {
		p := base()
		p.Title, p.Body = c.title, c.body
		if got := Derive(p, now).Category; got != c.want {
			t.Errorf("%q/%q: got %q want %q", c.title, c.body, got, c.want)
		}
	}
	p := base()
	p.Title, p.State = "[Checkout] merged", "MERGED"
	if got := Derive(p, now).Category; got != "Checkout" {
		t.Errorf("merged cards keep the category: %q", got)
	}
}
