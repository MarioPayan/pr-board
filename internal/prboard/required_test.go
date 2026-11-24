package prboard

import (
	"strings"
	"testing"
)

func reqString(rs []Requirement) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Key())
	}
	return strings.Join(out, " ")
}

func TestParseRequired(t *testing.T) {
	const self = "acme/web"
	cases := []struct{ name, body, want string }{
		{"owner/repo", "[required:acme/infra#478]", "acme/infra#478"},
		{"owner:repo", "[required:acme:infra#478]", "acme/infra#478"},
		{"repo only means the same owner", "[required:api#9]", "acme/api#9"},
		{"#N means the same repo", "[required:#4543]", "acme/web#4543"},
		{"list with spaces after commas", "[required:#1, api#2,  other/x#3]", "acme/web#1 acme/api#2 other/x#3"},
		{"case-insensitive keyword", "Depends: [Required: #5] and [REQUIRED:#6]", "acme/web#5 acme/web#6"},
		{"space around the colon and brackets", "[ required : #7 ]", "acme/web#7"},
		{"several blocks merge, duplicates once", "[required:#1]\ntext\n[required:#2,#1]", "acme/web#1 acme/web#2"},
		{"same PR in two spellings counts once", "[required:#1,web#1,acme/web#1,acme:web#1]", "acme/web#1"},
		{"fenced code is ignored", "```\n[required:#1]\n```\n[required:#2]\n~~~md\n[required:#3]\n~~~", "acme/web#2"},
		{"an unclosed fence hides the rest", "[required:#1]\n```\n[required:#2]", "acme/web#1"},
		{"a shorter fence does not close a longer one", "````\n```\n[required:#1]\n````\n[required:#2]", "acme/web#2"},
		{"bad tokens are ignored", "[required:#, abc, #0, #-3, a/b/c#1, owner/#2, #12x, x#9]", "acme/x#9"},
		{"self reference is ignored", "[required:#100]", ""},
		{"no block", "Merge after #11. required: #12", ""},
		{"empty block", "[required:]", ""},
		{"not the keyword", "[requiredx:#1] [prerequired:#2]", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reqString(ParseRequired(c.body, self, 100)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestRequiredBlocksReady(t *testing.T) {
	cases := []struct {
		name, state, label, next, signals string
	}{
		{"MERGED", StReady, "Ready to merge", "merge", "ci_pass approved req_done"},
		{"OPEN", StMissing, "Something missing", "merge infra#7 first", "ci_pass approved req_open"},
		{"CLOSED", StMissing, "Something missing", "infra#7 was closed without merging", "ci_pass approved req_closed"},
		{"", StMissing, "Something missing", "check infra#7: not found", "ci_pass approved req_unknown"},
	}
	for _, c := range cases {
		t.Run("target "+c.name, func(t *testing.T) {
			p := base()
			p.Requires = []Requirement{{Repo: "acme/infra", N: 7, State: c.name}}
			d := Derive(p, now)
			if d.State != c.state || d.Label != c.label || d.Next != c.next || sigKeys(d) != c.signals {
				t.Errorf("got %s %q %q %q", d.State, d.Label, d.Next, sigKeys(d))
			}
		})
	}
	// an open requirement does not hide a worse state
	p := base()
	p.ChecksFailing = []string{"unit"}
	p.Requires = []Requirement{{Repo: "acme/app", N: 2, State: "OPEN"}}
	if d := Derive(p, now); d.State != StMissing || d.Next != "fix failing CI" {
		t.Errorf("failing CI with a requirement: %s %q", d.State, d.Next)
	}
	// the chip names the target relative to the PR and carries its key
	p = base()
	p.Requires = []Requirement{{Repo: "acme/app", N: 2, State: "OPEN"}, {Repo: "acme/infra", N: 7, State: "MERGED"}}
	d := Derive(p, now)
	if got := sigKeys(d); got != "ci_pass approved req_open req_done" {
		t.Fatalf("signals %q", got)
	}
	first, last := d.Signals[2], d.Signals[3]
	if first.Text != "after #2" || first.Ref != "acme/app#2" || first.Title != "Merge after acme/app#2 (open)" {
		t.Errorf("open chip: %+v", first)
	}
	if last.Text != "after infra#7" || last.Title != "Merge after acme/infra#7 (merged)" {
		t.Errorf("done chip: %+v", last)
	}
}
