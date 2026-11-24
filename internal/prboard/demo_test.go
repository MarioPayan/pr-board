package prboard

import (
	"slices"
	"strings"
	"testing"
)

func TestDemoStore(t *testing.T) {
	s := DemoStore(now)
	states := map[string]int{}
	cats := map[string]bool{}
	for _, p := range s.PRs {
		if !strings.HasPrefix(p.Repo, "acme/") {
			t.Errorf("%s is not in the fictional org", p.Key())
		}
		d := Derive(p, now)
		states[d.State]++
		cats[d.Category] = true
	}
	for _, st := range []string{StReady, StApprove, StMissing, StMerged} {
		if states[st] < 2 {
			t.Errorf("want at least two %s PRs, got %d (%v)", st, states[st], states)
		}
	}
	if len(states) != 4 {
		t.Errorf("states %v", states)
	}
	for _, c := range []string{"Checkout", "Design system", "Search", "Billing", ""} {
		if !cats[c] {
			t.Errorf("no PR with category %q", c)
		}
	}

	v := BuildView(s, now)
	byRepo := map[string]Group{}
	for _, g := range v.Repos {
		byRepo[g.Key] = g
	}
	if got := rowString(byRepo["acme/web"]); !strings.Contains(got, "web#412 .web#415") {
		t.Errorf("web rows %q: want the [required:] chain", got)
	}
	if got := rowString(byRepo["acme/design-system"]); !strings.Contains(got, "design-system#88 .design-system#90") {
		t.Errorf("design-system rows %q: want the stack", got)
	}
	if b := byRepo["acme/infra"].Branch; b != "production" {
		t.Errorf("infra branch %q", b)
	}
	var keys []string
	for _, g := range v.Categories {
		keys = append(keys, g.Key)
	}
	if got := strings.Join(keys, ","); got != "Billing,Checkout,Design system,Search," {
		t.Errorf("category order %q: want alphabetical, Untagged last", got)
	}
	// the cross-repo requirement: api#236 sits under design-system#88 in its category
	for _, g := range v.Categories {
		if g.Key == "Design system" {
			if got := rowString(g); !strings.Contains(got, "design-system#88 .api#236 .design-system#90") {
				t.Errorf("design system rows %q: want api#236 under design-system#88", got)
			}
			for _, r := range g.Rows {
				if r.N == 236 && (r.After != "after design-system#88" || slices.ContainsFunc(r.Signals, func(s Signal) bool { return s.K == "req_open" })) {
					t.Errorf("api#236 after %q signals %+v", r.After, r.Signals)
				}
			}
		}
	}
	// every signal kind shows up on some open card, and every card keeps the fixed order
	kinds := map[string]bool{}
	drafts := 0
	for _, g := range v.Repos {
		for _, r := range g.Rows {
			if r.Draft {
				drafts++
			}
			for i, sg := range r.Signals {
				kinds[sg.K] = true
				if i > 0 && signalSlot(r.Signals[i-1].K) > signalSlot(sg.K) {
					t.Errorf("%s: %s before %s", r.Key, r.Signals[i-1].K, sg.K)
				}
			}
		}
	}
	for _, slot := range SignalOrder {
		for _, k := range slot {
			if !kinds[k] {
				t.Errorf("no demo card shows %s", k)
			}
		}
	}
	if drafts == 0 {
		t.Error("no draft in the demo")
	}
	if v.Merged < 4 {
		t.Errorf("merged %d", v.Merged)
	}
}
