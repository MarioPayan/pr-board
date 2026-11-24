package prboard

import (
	"fmt"
	"time"
)

// DemoStore is a fictional board for `pr-board demo`, the screenshots and
// the page tests: the org acme with four repositories, every colour at
// least twice, a same-repo [required:] chain, a stacked branch, a
// cross-repo requirement, a repo whose default branch is production, and
// merged PRs.
func DemoStore(now time.Time) *Store {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	day := 24 * time.Hour
	pr := func(repo string, n int, title string, mod func(*PR)) PR {
		p := PR{
			Repo: "acme/" + repo, Number: n, Title: title, State: "OPEN",
			Base: "main", DefaultBranch: "main", Head: fmt.Sprintf("feature-%d", n), HeadRepo: "acme/" + repo,
			Mergeable: "MERGEABLE", MergeState: "CLEAN", ReviewDecision: "APPROVED",
			Reviews: []Review{{Author: "alice", State: "APPROVED"}}, Requests: []string{},
			ChecksTotal: 9, ChecksFailing: []string{}, Threads: []Thread{}, UpdatedAt: ago(3 * time.Hour),
		}
		if mod != nil {
			mod(&p)
		}
		p.Requires = ParseRequired(p.Body, p.Repo, p.Number)
		for i := range p.Requires {
			p.Requires[i].State = "OPEN"
		}
		return p
	}
	unapproved := func(p *PR, requests ...string) {
		p.ReviewDecision, p.Reviews, p.Requests = "REVIEW_REQUIRED", []Review{}, append([]string{}, requests...)
		p.MergeState = "BLOCKED"
	}
	merged := func(p *PR) { p.State, p.MergedAt = "MERGED", ago(2*day) }

	prs := []PR{
		// web: #415 must wait for #412
		pr("web", 412, "[Checkout] Add saved cards to the payment step", nil),
		pr("web", 415, "[Checkout] Show the delivery estimate in the cart", func(p *PR) {
			p.Body = "Uses the saved-card lookup.\n\n[required:#412]"
		}),
		pr("web", 418, "[Search] Rank exact matches first", func(p *PR) { unapproved(p, "carol") }),
		pr("web", 420, "[Search] Typo tolerance for product names", func(p *PR) {
			unapproved(p)
			p.Draft, p.ChecksTotal, p.ChecksPending = true, 9, 2
		}),
		pr("web", 405, "[Checkout] Remove the old coupon banner", merged),
		pr("web", 401, "Bump the test runner", func(p *PR) { merged(p); p.MergedAt = ago(5 * day) }),

		// api: #236 needs a design-system PR first
		pr("api", 233, "[Billing] Prorate plan changes mid-cycle", func(p *PR) {
			p.ReviewDecision = "CHANGES_REQUESTED"
			p.Reviews = []Review{{Author: "bob", State: "CHANGES_REQUESTED"}}
			p.ChecksFailing = []string{"integration (pg16)"}
			p.Threads = []Thread{{}, {}}
		}),
		pr("api", 236, "[Design system] Serve the new icon set", func(p *PR) {
			p.Body = "[required:acme/design-system#88]"
			p.Reviews = []Review{{Author: "dave", State: "APPROVED"}}
		}),
		pr("api", 238, "[Billing] Invoice PDF footer shows the tax id", func(p *PR) { unapproved(p) }),
		pr("api", 229, "[Billing] Store the currency per account", merged),

		// design-system: #90 is stacked on #88's branch
		pr("design-system", 88, "[Design system] Icon set v2", func(p *PR) {
			p.Head = "icons-v2"
			p.Reviews = []Review{{Author: "alice", State: "APPROVED"}, {Author: "carol", State: "APPROVED"}}
		}),
		pr("design-system", 90, "[Design system] Dark theme tokens for the icons", func(p *PR) {
			p.Base = "icons-v2"
			unapproved(p, "alice")
		}),
		pr("design-system", 91, "[Design system] Button sizes follow the 4px grid", func(p *PR) {
			p.Mergeable, p.MergeState = "CONFLICTING", "DIRTY"
		}),
		pr("design-system", 84, "[Design system] Focus rings on inputs", merged),

		// infra deploys from production
		pr("infra", 57, "Raise the worker pool to 8", func(p *PR) {
			p.ChecksPending = 3
		}),
		pr("infra", 55, "[Search] Rebuild the index every night", func(p *PR) {
			unapproved(p, "bob")
			p.MergeState, p.UpdatedAt = "BEHIND", ago(12*day)
		}),
		pr("infra", 52, "Rotate the staging certificates", merged),
	}
	for i := range prs {
		if prs[i].Repo == "acme/infra" {
			prs[i].Base, prs[i].DefaultBranch = "production", "production"
		}
	}
	return &Store{Refreshed: now, PRs: prs, Cleaned: []string{}}
}
