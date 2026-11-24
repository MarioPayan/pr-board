package prboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Runner runs a command in dir and returns its stdout. Tests replace it.
type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands; stderr is folded into the error.
func ExecRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(firstArgs(args, 3), " "), err, msg)
	}
	return out, nil
}

func firstArgs(a []string, n int) []string {
	if len(a) > n {
		return a[:n]
	}
	return a
}

// Collector reads PRs from GitHub through the gh CLI.
type Collector struct {
	Cfg Config
	Run Runner
	Now func() time.Time
}

type ref struct {
	Repo string
	N    int
}

type searchHit struct {
	Number     int `json:"number"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

// Discover lists the author's open PRs, the ones merged in the window and,
// marked in toReview, the open PRs of others that request the author's
// review. A PR the author wrote is never one to review.
func (c Collector) Discover(ctx context.Context) (refs []ref, toReview map[ref]bool, err error) {
	since := c.Now().AddDate(0, 0, -c.Cfg.MergedDays).Format("2006-01-02")
	seen := map[ref]bool{}
	toReview = map[ref]bool{}
	for _, owner := range c.Cfg.Owners {
		searches := [][]string{
			{"--author", c.Cfg.Author, "--state", "open"},
			{"--author", c.Cfg.Author, "--merged", "--merged-at", ">=" + since},
			{"--review-requested", c.Cfg.Author, "--state", "open"},
		}
		for i, extra := range searches {
			args := append([]string{"search", "prs", "--owner", owner, "--json", "number,repository", "--limit", "200"}, extra...)
			b, err := c.Run(ctx, "", "gh", args...)
			if err != nil {
				return nil, nil, err
			}
			var hits []searchHit
			if err := json.Unmarshal(b, &hits); err != nil {
				return nil, nil, fmt.Errorf("gh search prs: %w", err)
			}
			for _, h := range hits {
				r := ref{h.Repository.NameWithOwner, h.Number}
				if r.Repo == "" || seen[r] {
					continue
				}
				seen[r] = true
				refs = append(refs, r)
				if i == len(searches)-1 {
					toReview[r] = true
				}
			}
		}
	}
	return refs, toReview, nil
}

const prFragment = `fragment F on PullRequest {
  number title url isDraft state mergedAt baseRefName headRefName headRefOid author { login }
  mergeable mergeStateStatus reviewDecision updatedAt body
  headRepository { nameWithOwner }
  repository { nameWithOwner defaultBranchRef { name } }
  latestReviews(first: 30) { nodes { author { login } state } }
  reviewRequests(first: 30) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { name } ... on Bot { login } ... on Mannequin { login } } } }
  commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes { __typename ... on CheckRun { name conclusion status } ... on StatusContext { context state } } } } } } }
  reviewThreads(first: 100) { nodes { isResolved comments(first: 5) { nodes { author { login } body } } } }
}`

// batchSize PRs go in one GraphQL query, as aliases.
const batchSize = 15

// BuildQuery returns one GraphQL query for refs, aliased p0..pN.
func BuildQuery(refs []ref) string {
	var b strings.Builder
	b.WriteString("query {\n")
	for i, r := range refs {
		owner, name, _ := strings.Cut(r.Repo, "/")
		fmt.Fprintf(&b, "  p%d: repository(owner: %q, name: %q) { pullRequest(number: %d) { ...F } }\n", i, owner, name, r.N)
	}
	b.WriteString("}\n")
	b.WriteString(prFragment)
	return b.String()
}

type gqlPR struct {
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	IsDraft          bool      `json:"isDraft"`
	State            string    `json:"state"`
	MergedAt         time.Time `json:"mergedAt"`
	BaseRefName      string    `json:"baseRefName"`
	HeadRefName      string    `json:"headRefName"`
	HeadRefOid       string    `json:"headRefOid"`
	Mergeable        string    `json:"mergeable"`
	MergeStateStatus string    `json:"mergeStateStatus"`
	ReviewDecision   string    `json:"reviewDecision"`
	UpdatedAt        time.Time `json:"updatedAt"`
	Body             string    `json:"body"`
	Author           *struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	Repository struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	} `json:"repository"`
	LatestReviews struct {
		Nodes []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
				Name     string `json:"name"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Conclusion string `json:"conclusion"`
							Status     string `json:"status"`
							Context    string `json:"context"`
							State      string `json:"state"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []struct {
					Author *struct {
						Login string `json:"login"`
					} `json:"author"`
					Body string `json:"body"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
}

func login(a *struct {
	Login string `json:"login"`
}) string {
	if a == nil {
		return "ghost"
	}
	return a.Login
}

// failing conclusions for check runs and states for commit statuses
var (
	runFailing    = []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"}
	statusFailing = []string{"FAILURE", "ERROR"}
)

func (g gqlPR) toPR() PR {
	p := PR{
		Repo: g.Repository.NameWithOwner, Number: g.Number, Title: g.Title, URL: g.URL,
		Draft: g.IsDraft, State: g.State, MergedAt: g.MergedAt,
		Base: g.BaseRefName, Head: g.HeadRefName, HeadOID: g.HeadRefOid,
		Mergeable: g.Mergeable, MergeState: g.MergeStateStatus, ReviewDecision: g.ReviewDecision,
		UpdatedAt: g.UpdatedAt, Body: g.Body,
		Reviews: []Review{}, Requests: []string{}, ChecksFailing: []string{}, Threads: []Thread{},
	}
	if g.Author != nil {
		p.Author = g.Author.Login
	}
	if g.HeadRepository != nil {
		p.HeadRepo = g.HeadRepository.NameWithOwner
	}
	if g.Repository.DefaultBranchRef != nil {
		p.DefaultBranch = g.Repository.DefaultBranchRef.Name
	}
	for _, r := range g.LatestReviews.Nodes {
		p.Reviews = append(p.Reviews, Review{Author: login(r.Author), State: r.State})
	}
	for _, r := range g.ReviewRequests.Nodes {
		if rr := r.RequestedReviewer; rr != nil {
			name := rr.Login
			if rr.Typename == "Team" {
				name = rr.Name
			}
			if name != "" {
				p.Requests = append(p.Requests, name)
			}
		}
	}
	for _, c := range g.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, x := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			p.ChecksTotal++
			switch x.Typename {
			case "CheckRun":
				if x.Status != "COMPLETED" {
					p.ChecksPending++
				} else if slices.Contains(runFailing, x.Conclusion) {
					p.ChecksFailing = append(p.ChecksFailing, x.Name)
				}
			case "StatusContext":
				if x.State == "PENDING" || x.State == "EXPECTED" {
					p.ChecksPending++
				} else if slices.Contains(statusFailing, x.State) {
					p.ChecksFailing = append(p.ChecksFailing, x.Context)
				}
			}
		}
	}
	for _, t := range g.ReviewThreads.Nodes {
		if t.IsResolved {
			continue
		}
		var th Thread
		for _, c := range t.Comments.Nodes {
			th.Comments = append(th.Comments, Comment{Author: login(c.Author), Body: c.Body})
		}
		p.Threads = append(p.Threads, th)
	}
	return p
}

// ParseQuery reads a gh api graphql response for refs aliased p0..pN.
func ParseQuery(b []byte, n int) ([]PR, []string, error) {
	var resp struct {
		Data   map[string]*struct{ PullRequest *gqlPR } `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, nil, fmt.Errorf("graphql response: %w", err)
	}
	var errs []string
	for _, e := range resp.Errors {
		errs = append(errs, e.Message)
	}
	var out []PR
	for i := 0; i < n; i++ {
		r := resp.Data[fmt.Sprintf("p%d", i)]
		if r == nil || r.PullRequest == nil {
			continue
		}
		out = append(out, r.PullRequest.toPR())
	}
	return out, errs, nil
}

// Fetch reads every ref in batches. Partial GraphQL errors are returned as
// messages next to the PRs that did load.
func (c Collector) Fetch(ctx context.Context, refs []ref) ([]PR, []string, error) {
	var out []PR
	var errs []string
	for i := 0; i < len(refs); i += batchSize {
		chunk := refs[i:min(i+batchSize, len(refs))]
		b, err := c.Run(ctx, "", "gh", "api", "graphql", "-f", "query="+BuildQuery(chunk))
		if err != nil && len(b) == 0 {
			return nil, nil, err
		}
		prs, e, perr := ParseQuery(b, len(chunk))
		if perr != nil {
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, perr
		}
		out = append(out, prs...)
		errs = append(errs, e...)
	}
	return out, errs, nil
}

// stateQuery asks only for the state of each ref, aliased p0..pN.
func stateQuery(refs []ref) string {
	var b strings.Builder
	b.WriteString("query {\n")
	for i, r := range refs {
		owner, name, _ := strings.Cut(r.Repo, "/")
		fmt.Fprintf(&b, "  p%d: repository(owner: %q, name: %q) { pullRequest(number: %d) { state } }\n", i, owner, name, r.N)
	}
	b.WriteString("}\n")
	return b.String()
}

// resolveRequired parses the [required:] list of every open PR and sets each
// target's state: from the PRs already fetched (closed ones included), else
// from one small GraphQL query per 50 targets. A target GitHub does not know
// keeps an empty state. Only failed queries are returned as errors.
func (c Collector) resolveRequired(ctx context.Context, prs []PR, fetched []PR) []string {
	state := map[string]string{}
	for _, list := range [][]PR{prs, fetched} {
		for _, p := range list {
			state[strings.ToLower(p.Key())] = p.State
		}
	}
	var missing []ref
	asked := map[string]bool{}
	for i := range prs {
		if prs[i].State != "OPEN" || prs[i].ToReview {
			continue
		}
		prs[i].Requires = ParseRequired(prs[i].Body, prs[i].Repo, prs[i].Number)
		for _, r := range prs[i].Requires {
			k := strings.ToLower(r.Key())
			if _, ok := state[k]; !ok && !asked[k] {
				asked[k] = true
				missing = append(missing, ref{r.Repo, r.N})
			}
		}
	}
	var errs []string
	for i := 0; i < len(missing); i += 50 {
		chunk := missing[i:min(i+50, len(missing))]
		b, err := c.Run(ctx, "", "gh", "api", "graphql", "-f", "query="+stateQuery(chunk))
		var resp struct {
			Data map[string]*struct {
				PullRequest *struct {
					State string `json:"state"`
				} `json:"pullRequest"`
			} `json:"data"`
		}
		// a ref GitHub cannot resolve is a partial error next to the others' data
		if jerr := json.Unmarshal(b, &resp); jerr != nil || resp.Data == nil {
			if err == nil {
				err = jerr
			}
			errs = append(errs, fmt.Sprintf("required PR lookup: %v", err))
			continue
		}
		for j, r := range chunk {
			if d := resp.Data[fmt.Sprintf("p%d", j)]; d != nil && d.PullRequest != nil {
				state[strings.ToLower(fmt.Sprintf("%s#%d", r.Repo, r.N))] = d.PullRequest.State
			}
		}
	}
	for i := range prs {
		for j, r := range prs[i].Requires {
			prs[i].Requires[j].State = state[strings.ToLower(r.Key())]
		}
	}
	return errs
}

// Collect runs discovery and fetch, and merges the result into the previous
// store: the cleaned list carries over, merged PRs stay until a
// cleanup drops them, closed-unmerged PRs leave the map.
func (c Collector) Collect(ctx context.Context, prev *Store) (*Store, error) {
	refs, toReview, err := c.Discover(ctx)
	if err != nil {
		return nil, err
	}
	next := &Store{Cleaned: []string{}, PRs: []PR{}}
	if prev != nil {
		next.Cleaned = append(next.Cleaned, prev.Cleaned...)
	}
	cleaned := map[string]bool{}
	for _, k := range next.Cleaned {
		cleaned[k] = true
	}
	var want []ref
	for _, r := range refs {
		if !cleaned[fmt.Sprintf("%s#%d", r.Repo, r.N)] {
			want = append(want, r)
		}
	}
	prs, errs, err := c.Fetch(ctx, want)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, p := range prs {
		p.ToReview = toReview[ref{p.Repo, p.Number}]
		if p.State == "CLOSED" || p.ToReview && p.State != "OPEN" {
			continue // a review that is no longer open leaves the board
		}
		have[p.Key()] = true
		next.PRs = append(next.PRs, p)
	}
	if prev != nil {
		for _, p := range prev.PRs {
			if p.State == "MERGED" && !p.ToReview && !have[p.Key()] && !cleaned[p.Key()] {
				have[p.Key()] = true
				next.PRs = append(next.PRs, p)
			}
		}
	}
	errs = append(errs, c.resolveRequired(ctx, next.PRs, prs)...)
	slices.SortFunc(next.PRs, func(a, b PR) int {
		if a.Repo != b.Repo {
			return strings.Compare(a.Repo, b.Repo)
		}
		return b.Number - a.Number
	})
	next.Errors = errs
	next.Refreshed = c.Now()
	return next, nil
}
