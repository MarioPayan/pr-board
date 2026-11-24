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
	if v.Merged < 4 {
		t.Errorf("merged %d", v.Merged)
	}
}
