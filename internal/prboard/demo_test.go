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
	byRepo := map[string]RepoView{}
	for _, r := range v.Repos {
		byRepo[r.Repo] = r
	}
	if got := laneString(byRepo["acme/web"].Lanes); !strings.Contains(got, "#412 + #415") {
		t.Errorf("web lanes %q: want the [required:] chain", got)
	}
	if got := laneString(byRepo["acme/design-system"].Lanes); !strings.Contains(got, "#88 = #90") {
		t.Errorf("design-system lanes %q: want the stack", got)
	}
	if b := byRepo["acme/infra"].Branch; b != "production" {
		t.Errorf("infra branch %q", b)
	}
	var chips []string
	for _, l := range byRepo["acme/api"].Lanes {
		for _, it := range l {
			if it.Card != nil && it.Card.N == 236 {
				for _, sg := range it.Card.Signals {
					chips = append(chips, sg.Text)
				}
			}
		}
	}
	if !slices.Contains(chips, "after design-system#88") {
		t.Errorf("api#236 chips %q: want the cross-repo requirement", chips)
	}
	// every signal kind shows up on some open card, and every card keeps the fixed order
	kinds := map[string]bool{}
	drafts := 0
	for _, r := range v.Repos {
		for _, l := range r.Lanes {
			for _, it := range l {
				if it.Card == nil {
					continue
				}
				if it.Card.Draft {
					drafts++
				}
				for i, sg := range it.Card.Signals {
					kinds[sg.K] = true
					if i > 0 && signalSlot(it.Card.Signals[i-1].K) > signalSlot(sg.K) {
						t.Errorf("%s: %s before %s", it.Card.Key, it.Card.Signals[i-1].K, sg.K)
					}
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
