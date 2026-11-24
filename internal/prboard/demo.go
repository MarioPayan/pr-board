package prboard

import (
	"fmt"
	"time"
)

// DemoStore is a fictional board for `pr-board demo`, the screenshots and
// the page tests: a busy week in the org acme, six repositories and six
// categories, about 30% red, 25% yellow, 20% green and 25% merged. It has a
// same-repo [required:] chain, a stack four PRs tall, two [required:]
// chains across repos, a PR that requires two open PRs, a requirement on a
// PR that is not on the board, drafts, stale PRs, PRs with many threads,
// long titles, PRs without a category, a repo whose default branch is
// production, every signal at least once (a dismissed approval, a requested
// team) and several cards whose signals overflow into "+N".
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
		return p
	}
	unapproved := func(p *PR, requests ...string) {
		p.ReviewDecision, p.Reviews, p.Requests = "REVIEW_REQUIRED", []Review{}, append([]string{}, requests...)
		p.MergeState = "BLOCKED"
	}
	merged := func(p *PR) { p.State, p.MergedAt = "MERGED", ago(2*day) }
	mergedAgo := func(d time.Duration) func(*PR) {
		return func(p *PR) { p.State, p.MergedAt = "MERGED", ago(d) }
	}
	approvedBy := func(p *PR, who ...string) {
		p.Reviews = nil
		for _, w := range who {
			p.Reviews = append(p.Reviews, Review{Author: w, State: "APPROVED"})
		}
	}
	threads := func(n int) []Thread { return make([]Thread, n) }

	prs := []PR{
		// web: #415 must wait for #412
		pr("web", 412, "[Checkout] Add saved cards to the payment step", func(p *PR) { p.Threads = []Thread{{}} }),
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
			p.Reviews = append(p.Reviews, Review{Author: "erin", State: "DISMISSED"})
			p.Requests, p.MergeState, p.UpdatedAt = []string{"Dev"}, "BEHIND", ago(9*day)
			p.ChecksFailing = []string{"integration (pg16)"}
			p.Threads = []Thread{{}, {}}
			p.Body = "[required:acme/web#399, acme/infra#40]"
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
			unapproved(p, "frontend-reviewers")
		}),
		pr("design-system", 91, "[Design system] Button sizes follow the 4px grid", func(p *PR) {
			p.Mergeable, p.MergeState = "CONFLICTING", "DIRTY"
			p.Reviews = append(p.Reviews, Review{Author: "erin", State: "DISMISSED"})
			p.Body = "Needs the stored currency.\n\n[required:acme/api#229]"
		}),
		pr("design-system", 84, "[Design system] Focus rings on inputs", merged),

		// infra deploys from production
		pr("infra", 60, "Pin the base image digest", func(p *PR) { p.MergeState = "BLOCKED" }),
		pr("infra", 57, "Raise the worker pool to 8", func(p *PR) {
			p.ChecksPending = 3
			p.Body = "[required:acme/web#430]"
		}),
		pr("infra", 55, "[Search] Rebuild the index every night", func(p *PR) {
			unapproved(p, "bob")
			p.MergeState, p.UpdatedAt = "BEHIND", ago(12*day)
		}),
		pr("infra", 52, "Rotate the staging certificates", merged),

		// web, more of the week
		pr("web", 424, "[Checkout] Apple Pay button on the review step", func(p *PR) { unapproved(p, "grace") }),
		pr("web", 427, "[Search] Highlight the matched words in suggestions", func(p *PR) { approvedBy(p, "heidi") }),
		pr("web", 431, "[Onboarding] Welcome checklist on the first dashboard visit", func(p *PR) {
			unapproved(p, "judy")
			p.ChecksFailing, p.Threads = []string{"e2e (chromium)", "lint"}, threads(2)
		}),
		pr("web", 433, "[Reporting] Export the orders table as CSV", func(p *PR) {
			approvedBy(p, "frank")
			p.Body = "Reads the new fact table.\n\n[required:acme/data#77]"
		}),
		pr("web", 436, "[Checkout] Keep the cart when a guest signs in halfway through checkout and merge it with the saved one", func(p *PR) {
			unapproved(p)
			p.UpdatedAt = ago(26 * time.Hour)
		}),
		pr("web", 438, "Upgrade the bundler to the next major and drop the legacy polyfills", func(p *PR) {
			unapproved(p)
			p.Draft, p.ChecksPending, p.Threads = true, 5, threads(1)
		}),
		pr("web", 410, "[Search] Debounce the search box", mergedAgo(20*time.Hour)),
		pr("web", 409, "[Onboarding] Ask for the team size on sign-up", mergedAgo(3*day)),

		// api, more of the week; #250 starts a chain into mobile
		pr("api", 241, "[Onboarding] Endpoint for the welcome checklist state", func(p *PR) { approvedBy(p, "frank") }),
		pr("api", 244, "[Reporting] Daily revenue rollup by region", func(p *PR) { unapproved(p, "data-eng") }),
		pr("api", 247, "[Billing] Retry failed card charges with backoff", func(p *PR) {
			p.ReviewDecision = "CHANGES_REQUESTED"
			p.Reviews = []Review{{Author: "ivan", State: "CHANGES_REQUESTED"}, {Author: "carol", State: "DISMISSED"}}
			p.Requests, p.MergeState, p.UpdatedAt = []string{"payments-team"}, "BEHIND", ago(14*day)
			p.ChecksFailing, p.Threads = []string{"unit", "contract (stripe-mock)"}, threads(6)
			p.Body = "[required:acme/infra#41, acme/web#399]"
		}),
		pr("api", 250, "[Onboarding] Invite teammates by email from the checklist", func(p *PR) { approvedBy(p, "grace", "judy") }),
		pr("api", 252, "[Billing] Webhook for refunds", func(p *PR) { unapproved(p, "bob") }),
		pr("api", 256, "Log the request id on every 5xx", func(p *PR) { unapproved(p) }),
		pr("api", 231, "[Billing] Tax id on invoices", mergedAgo(26*time.Hour)),
		pr("api", 226, "[Reporting] Cache the KPI query for five minutes", mergedAgo(4*day)),

		// design-system, more of the week
		pr("design-system", 93, "[Design system] Toast component with an action slot", func(p *PR) { unapproved(p, "carol") }),
		pr("design-system", 95, "[Onboarding] Stepper component for multi-step forms", func(p *PR) { approvedBy(p, "alice", "dave") }),
		pr("design-system", 97, "[Design system] Date picker keyboard navigation and screen reader announcements for every cell", func(p *PR) {
			p.ReviewDecision = "CHANGES_REQUESTED"
			p.Reviews = []Review{{Author: "heidi", State: "CHANGES_REQUESTED"}}
			p.Requests, p.UpdatedAt = []string{"accessibility"}, ago(11*day)
			p.Mergeable, p.MergeState = "CONFLICTING", "DIRTY"
			p.ChecksFailing, p.Threads = []string{"visual diff"}, threads(7)
			p.Reviews = append(p.Reviews, Review{Author: "alice", State: "DISMISSED"})
			p.Body = "[required:acme/design-system#84]"
		}),
		pr("design-system", 99, "[Reporting] Chart colour tokens for the dashboards", func(p *PR) { unapproved(p) }),
		pr("design-system", 86, "[Design system] Spacing scale docs", mergedAgo(3*day)),
		pr("design-system", 82, "[Checkout] Price tag component", mergedAgo(5*day)),

		// infra, more of the week
		pr("infra", 62, "[Reporting] Warehouse backups every six hours", func(p *PR) { unapproved(p, "ops-oncall") }),
		pr("infra", 64, "Move the CDN logs to cold storage after 30 days", func(p *PR) { approvedBy(p, "bob") }),
		pr("infra", 50, "Alerts for the queue depth", mergedAgo(2*day+5*time.Hour)),

		// mobile: #140 to #146 is a stack four PRs tall; #148 needs api#250,
		// #152 needs web#433 (which needs data#77)
		pr("mobile", 140, "[Onboarding] New onboarding flow shell", func(p *PR) {
			p.Head = "onboarding-shell"
			approvedBy(p, "erin")
		}),
		pr("mobile", 142, "[Onboarding] Steps for profile, team and notifications", func(p *PR) {
			p.Base, p.Head = "onboarding-shell", "onboarding-steps"
			unapproved(p, "mobile-reviewers")
		}),
		pr("mobile", 144, "[Onboarding] Let people skip a step and come back to it", func(p *PR) {
			p.Base, p.Head = "onboarding-steps", "onboarding-skip"
			unapproved(p, "erin")
			p.ChecksFailing = []string{"ios-unit"}
		}),
		pr("mobile", 146, "[Onboarding] Progress dots and the finish screen", func(p *PR) {
			p.Base, p.Head = "onboarding-skip", "onboarding-finish"
			unapproved(p)
			p.Draft, p.ChecksPending = true, 1
		}),
		pr("mobile", 148, "[Onboarding] Show pending invites in the checklist", func(p *PR) {
			approvedBy(p, "ivan")
			p.Body = "[required:acme/api#250]"
		}),
		pr("mobile", 150, "[Checkout] Native payment sheet on iOS and Android", func(p *PR) { approvedBy(p, "erin", "frank") }),
		pr("mobile", 152, "[Reporting] Revenue widget on the home screen", func(p *PR) {
			unapproved(p, "judy")
			p.Body = "[required:acme/web#433]"
			p.Threads = threads(1)
		}),
		pr("mobile", 154, "[Search] Voice search", func(p *PR) { unapproved(p, "judy") }),
		pr("mobile", 156, "Crash on launch when the locale has no region", func(p *PR) {
			approvedBy(p, "ivan")
			p.UpdatedAt = ago(40 * time.Minute)
		}),
		pr("mobile", 136, "[Onboarding] Feature flags for the new flow", mergedAgo(18*time.Hour)),

		// data: #82 requires two open PRs, data#77 and api#244
		pr("data", 77, "[Reporting] Orders fact table with refunds", func(p *PR) { unapproved(p, "data-eng") }),
		pr("data", 80, "[Onboarding] Activation events table", func(p *PR) { approvedBy(p, "grace") }),
		pr("data", 82, "[Reporting] Weekly cohort retention model", func(p *PR) {
			unapproved(p, "heidi")
			p.Body = "Needs the fact table and the rollup.\n\n[required:acme/data#77, acme/api#244]"
		}),
		pr("data", 84, "[Reporting] Backfill three years of order history in monthly batches so the dashboards stop timing out", func(p *PR) {
			approvedBy(p, "dave")
			p.MergeState, p.UpdatedAt = "BEHIND", ago(10*day+2*time.Hour)
			p.ChecksPending, p.Threads = 1, threads(5)
			p.Requests = []string{"data-eng"}
			p.Reviews = append(p.Reviews, Review{Author: "grace", State: "DISMISSED"})
			p.Body = "[required:acme/data#74, acme/api#199]"
		}),
		pr("data", 86, "[Billing] MRR by plan", func(p *PR) { unapproved(p) }),
		pr("data", 88, "Drop the unused staging tables", func(p *PR) { approvedBy(p, "dave") }),
		pr("data", 74, "[Reporting] Tests for the revenue models", mergedAgo(2*day)),
		pr("data", 72, "[Billing] Currency conversion table", mergedAgo(6*day)),
	}
	// a requirement takes the state of the PR it names: one on the board,
	// web#399 closed without merging, web#430 open (someone else's PR),
	// anything else not found
	state := map[string]string{"acme/web#399": "CLOSED", "acme/web#430": "OPEN"}
	for i := range prs {
		state[prs[i].Key()] = prs[i].State
		if prs[i].Repo == "acme/infra" {
			prs[i].Base, prs[i].DefaultBranch = "production", "production"
		}
	}
	for i := range prs {
		for j := range prs[i].Requires {
			prs[i].Requires[j].State = state[prs[i].Requires[j].Key()]
		}
	}
	return &Store{Refreshed: now, PRs: prs, Cleaned: []string{}}
}
