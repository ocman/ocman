package forgehttp

import (
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// PR is the subset of the pull-request JSON the sidebar reads. Forgejo's
// API is GitHub-compatible for these fields; Merged and Head.Label are only
// populated by Forgejo, MergedAt by both.
type PR struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	State              string    `json:"state"` // "open" | "closed"
	Draft              bool      `json:"draft"`
	Merged             bool      `json:"merged"`
	MergedAt           *string   `json:"merged_at"`
	Mergeable          *bool     `json:"mergeable"`
	UpdatedAt          time.Time `json:"updated_at"`
	HTMLURL            string    `json:"html_url"`
	User               User      `json:"user"`
	Labels             []Label   `json:"labels"`
	Assignees          []User    `json:"assignees"`
	RequestedReviewers []User    `json:"requested_reviewers"`
	Head               Ref       `json:"head"`
	Base               Ref       `json:"base"`
}

// Issue is the subset of the issue JSON the sidebar reads.
type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
	HTMLURL     string    `json:"html_url"`
	User        User      `json:"user"`
	Labels      []Label   `json:"labels"`
	Assignees   []User    `json:"assignees"`
	PullRequest *struct{} `json:"pull_request,omitempty"` // present means this row is actually a PR
}

type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Ref struct {
	Ref   string `json:"ref"`
	Label string `json:"label"`
	SHA   string `json:"sha"`
	Repo  struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// ToForge converts the raw shape into the forge-agnostic PR. merged reports
// whether a closed PR was merged, which each forge signals differently.
// repo is passed in (rather than read from base.repo.full_name) so rows
// missing repo info still come out with the right host/repo.
func (r PR) ToForge(host, repo string, merged bool) forge.PR {
	status := r.State
	switch {
	case r.State == "open" && r.Draft:
		status = "draft"
	case r.State == "closed" && merged:
		status = "merged"
	}
	return forge.PR{
		Number:             r.Number,
		Title:              r.Title,
		Body:               r.Body,
		Author:             r.User.Login,
		Status:             status,
		Mergeable:          r.Mergeable,
		UpdatedAt:          r.UpdatedAt,
		Branch:             r.Head.Ref,
		BaseBranch:         r.Base.Ref,
		URL:                r.HTMLURL,
		Host:               host,
		Repo:               repo,
		HeadSHA:            r.Head.SHA,
		CrossFork:          r.Head.Repo.FullName != "" && r.Head.Repo.FullName != r.Base.Repo.FullName,
		Labels:             toLabels(r.Labels),
		Assignees:          toUsers(r.Assignees),
		RequestedReviewers: toUsers(r.RequestedReviewers),
	}
}

// ToForge converts the raw shape into the forge-agnostic Issue.
func (r Issue) ToForge(host, repo string) forge.Issue {
	return forge.Issue{
		Number:    r.Number,
		Title:     r.Title,
		Body:      r.Body,
		Author:    r.User.Login,
		Status:    r.State,
		UpdatedAt: r.UpdatedAt,
		URL:       r.HTMLURL,
		Host:      host,
		Repo:      repo,
		Labels:    toLabels(r.Labels),
		Assignees: toUsers(r.Assignees),
	}
}

// toLabels and toUsers return nil for empty input, matching append-built slices.
func toLabels(in []Label) []forge.Label {
	var out []forge.Label
	for _, l := range in {
		out = append(out, forge.Label{Name: l.Name, Color: l.Color})
	}
	return out
}

func toUsers(in []User) []forge.User {
	var out []forge.User
	for _, u := range in {
		out = append(out, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	return out
}
