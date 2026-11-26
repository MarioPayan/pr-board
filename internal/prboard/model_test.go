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
		{"draft, approved and green", func(p *PR) { p.Draft = true }, StMissing, "Something missing", "ci_pass approved", "mark ready when you want review"},
		{"draft with conflicts", func(p *PR) { p.Draft = true; p.Mergeable = "CONFLICTING" }, StMissing, "Something missing", "ci_pass approved conflict", "merge main"},
		{"draft with no reviewer shows no norev", func(p *PR) {
			p.Draft, p.ReviewDecision, p.Reviews = true, "REVIEW_REQUIRED", nil
		}, StMissing, "Something missing", "ci_pass", "mark ready when you want review"},
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
		}, StMissing, "Something missing", "ci_pass", "mark ready when you want review"},
		// green edge cases
		{"no review policy, approving review", func(p *PR) { p.ReviewDecision = "" }, StReady, "Ready to merge", "ci_pass approved", "merge"},
		{"no checks at all", func(p *PR) { p.ChecksTotal = 0 }, StReady, "Ready to merge", "approved", "merge"},
		{"unresolved threads and stale", func(p *PR) {
			p.Threads = []Thread{{}, {}, {}}
			p.UpdatedAt = now.AddDate(0, 0, -11)
		}, StReady, "Ready to merge", "ci_pass approved threads(3) stale(11)", "merge"},
		{"ten days is not stale", func(p *PR) { p.UpdatedAt = now.AddDate(0, 0, -10) }, StReady, "Ready to merge", "ci_pass approved", "merge"},
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

func TestDraftIsATagNotASignal(t *testing.T) {
	p := base()
	p.Draft = true
	if d := Derive(p, now); !d.Draft || strings.Contains(sigKeys(d), "draft") {
		t.Errorf("draft: Draft=%v signals=%q", d.Draft, sigKeys(d))
	}
	if d := Derive(base(), now); d.Draft {
		t.Error("a ready PR is not a draft")
	}
}

// Every card lists its signals in one fixed order, whatever produced them.
func TestSignalOrder(t *testing.T) {
	everything := func(p *PR) {
		p.ChecksFailing, p.ChecksPending = []string{"unit"}, 2
		p.ReviewDecision = "CHANGES_REQUESTED"
		p.Reviews = []Review{{Author: "alice", State: "CHANGES_REQUESTED"}, {Author: "bob", State: "DISMISSED"}}
		p.Requests = []string{"carol"}
		p.Mergeable, p.MergeState = "CONFLICTING", "BEHIND"
		p.Threads = []Thread{{}}
		p.UpdatedAt = now.AddDate(0, 0, -12)
		p.Requires = []Requirement{{Repo: "acme/app", N: 2, State: "MERGED"}, {Repo: "acme/app", N: 3, State: "OPEN"}}
	}
	cases := []struct {
		name string
		mod  func(*PR)
		want string
	}{
		{"everything at once", everything,
			"ci_fail(1) ci_pend(2) changes dismissed rev conflict behind req_open req_done threads(1) stale(12)"},
		{"approved, rule, requirement and no reviewer slot", func(p *PR) {
			p.MergeState = "BLOCKED"
			p.Threads = []Thread{{}, {}}
			p.Requires = []Requirement{{Repo: "acme/app", N: 2, State: "CLOSED"}, {Repo: "acme/web", N: 4, State: ""}}
		}, "ci_pass approved blocked req_closed req_unknown threads(2)"},
		{"no reviewer before conflicts", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Mergeable = "REVIEW_REQUIRED", []Review{{Author: "bob", State: "DISMISSED"}}, "CONFLICTING"
		}, "ci_pass dismissed norev conflict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base()
			c.mod(&p)
			d := Derive(p, now)
			if got := sigKeys(d); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			for i := 1; i < len(d.Signals); i++ {
				if signalSlot(d.Signals[i-1].K) > signalSlot(d.Signals[i].K) {
					t.Errorf("%s before %s", d.Signals[i-1].K, d.Signals[i].K)
				}
			}
		})
	}
	// every kind Derive can emit has a slot
	seen := map[string]bool{}
	for _, kinds := range SignalOrder {
		for _, k := range kinds {
			if seen[k] {
				t.Errorf("%s appears twice in SignalOrder", k)
			}
			seen[k] = true
		}
	}
	if len(seen) != 17 {
		t.Errorf("SignalOrder has %d kinds, want 17", len(seen))
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

// Every Next has a kind, and the kind says whether the step is the user's
// own move or a wait on CI, people, another PR or GitHub.
func TestNextKind(t *testing.T) {
	req := func(state string) func(*PR) {
		return func(p *PR) { p.Requires = []Requirement{{Repo: "acme/infra", N: 7, State: state}} }
	}
	cases := []struct {
		name string
		mod  func(*PR)
		next string
		kind string
		mine bool
	}{
		{"conflict", func(p *PR) { p.Mergeable = "CONFLICTING" }, "merge main", NextConflict, true},
		{"changes", func(p *PR) { p.ReviewDecision = "CHANGES_REQUESTED" }, "address the requested changes", NextChanges, true},
		{"failing CI", func(p *PR) { p.ChecksFailing = []string{"unit"} }, "fix failing CI", NextFix, true},
		{"draft", func(p *PR) { p.Draft = true }, "mark ready when you want review", NextDraft, true},
		{"no reviewer", func(p *PR) { p.ReviewDecision, p.Reviews = "REVIEW_REQUIRED", nil }, "request a reviewer", NextRequest, true},
		{"required PR open", req("OPEN"), "merge infra#7 first", NextBlocked, false},
		{"required PR closed", req("CLOSED"), "infra#7 was closed without merging", NextRequired, true},
		{"required PR not found", req(""), "check infra#7: not found", NextRequired, true},
		{"ready", func(p *PR) {}, "merge", NextMerge, true},
		{"behind", func(p *PR) { p.MergeState = "BEHIND" }, "update from main", NextUpdate, true},
		{"branch rule", func(p *PR) { p.MergeState = "BLOCKED" }, "check which branch rule blocks it on GitHub", NextRule, true},
		{"CI running", func(p *PR) { p.ChecksPending = 2 }, "wait for CI", NextWaitCI, false},
		{"reviewer requested", func(p *PR) {
			p.ReviewDecision, p.Reviews, p.Requests = "REVIEW_REQUIRED", nil, []string{"carol"}
		}, "wait for carol", NextWaitPeople, false},
		{"mergeability unknown", func(p *PR) { p.Mergeable = "UNKNOWN" }, "refresh: GitHub is still computing mergeability", NextRefresh, false},
		{"no approval", func(p *PR) { p.ReviewDecision, p.Reviews = "", nil }, "get an approval", NextApproval, true},
		{"merged has no next", func(p *PR) { p.State = "MERGED" }, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base()
			c.mod(&p)
			d := Derive(p, now)
			if d.Next != c.next || d.NextKind != c.kind || d.YourMove != c.mine {
				t.Errorf("got next=%q kind=%q mine=%v, want next=%q kind=%q mine=%v", d.Next, d.NextKind, d.YourMove, c.next, c.kind, c.mine)
			}
		})
	}
}
