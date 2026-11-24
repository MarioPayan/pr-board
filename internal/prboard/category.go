package prboard

import (
	"regexp"
	"strings"
)

var (
	bracketed  = regexp.MustCompile(`\[([^\[\]]*)\]`)
	jiraKeyTok = regexp.MustCompile(`^[A-Z][A-Z0-9]+-[0-9]+$`)
)

// Category is the effort or project named in square brackets in a PR title: the first
// bracketed token, after splitting on "|", that is not the repository's own
// name, a Jira key or a required: list.
func Category(title, repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	for _, m := range bracketed.FindAllStringSubmatch(title, -1) {
		for tok := range strings.SplitSeq(m[1], "|") {
			tok = strings.TrimSpace(tok)
			switch {
			case tok == "",
				strings.EqualFold(tok, name), strings.EqualFold(tok, repo),
				jiraKeyTok.MatchString(tok),
				strings.HasPrefix(strings.ToLower(tok), "required:"):
				continue
			}
			return tok
		}
	}
	return ""
}
