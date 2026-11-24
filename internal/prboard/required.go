package prboard

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Requirement is a PR that must merge before this one, named in a
// [required:...] block of its description.
type Requirement struct {
	Repo  string `json:"repo"`  // owner/name
	N     int    `json:"n"`     // PR number
	State string `json:"state"` // OPEN, MERGED, CLOSED, or "" when the lookup found nothing
}

// Key is owner/name#n.
func (r Requirement) Key() string { return fmt.Sprintf("%s#%d", r.Repo, r.N) }

var (
	requiredBlock = regexp.MustCompile(`(?i)\[\s*required\s*:([^\]\n]*)\]`)
	requiredItem  = regexp.MustCompile(`^(?:(?:([\w.-]+)[/:])?([\w.-]+))?#([0-9]+)$`)
	fence         = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
)

// ParseRequired reads every [required:...] block in body, outside fenced
// code. Items are owner/repo#N, owner:repo#N, repo#N (the PR's owner) or #N
// (the PR's repo); anything else, and the PR itself, is skipped.
func ParseRequired(body, repo string, self int) []Requirement {
	owner, name, _ := strings.Cut(repo, "/")
	var out []Requirement
	for _, text := range outsideFences(body) {
		for _, m := range requiredBlock.FindAllStringSubmatch(text, -1) {
			for item := range strings.SplitSeq(m[1], ",") {
				p := requiredItem.FindStringSubmatch(strings.TrimSpace(item))
				if p == nil {
					continue
				}
				n, err := strconv.Atoi(p[3])
				if err != nil || n <= 0 {
					continue
				}
				o, r := p[1], p[2]
				switch {
				case r == "":
					o, r = owner, name
				case o == "":
					o = owner
				}
				req := Requirement{Repo: o + "/" + r, N: n}
				if strings.EqualFold(req.Repo, repo) {
					req.Repo = repo
					if n == self {
						continue
					}
				}
				if !slices.ContainsFunc(out, func(x Requirement) bool { return strings.EqualFold(x.Key(), req.Key()) }) {
					out = append(out, req)
				}
			}
		}
	}
	return out
}

// outsideFences returns the runs of lines that are not inside a ``` or ~~~
// fenced code block. An unclosed fence runs to the end.
func outsideFences(body string) []string {
	var out []string
	var cur strings.Builder
	open := ""
	for line := range strings.SplitSeq(body, "\n") {
		m := fence.FindStringSubmatch(line)
		switch {
		case open == "" && m != nil:
			open = m[1]
			out = append(out, cur.String())
			cur.Reset()
		case open != "":
			if m != nil && m[1][0] == open[0] && len(m[1]) >= len(open) && strings.TrimSpace(line[strings.Index(line, m[1])+len(m[1]):]) == "" {
				open = ""
			}
		default:
			cur.WriteString(line)
			cur.WriteByte('\n')
		}
	}
	if open == "" {
		out = append(out, cur.String())
	}
	return out
}

// shortRef names repo#n as seen from self: "#n" in the same repo, else
// "name#n" without the owner.
func shortRef(repo, self string, n int) string {
	if repo == self {
		return fmt.Sprintf("#%d", n)
	}
	_, name, _ := strings.Cut(repo, "/")
	return fmt.Sprintf("%s#%d", name, n)
}
