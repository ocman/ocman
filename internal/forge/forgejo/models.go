package forgejo

import (
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// Forgejo's API is Gitea-compatible. These fields cover what the sidebar needs.
type fjPR struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	State              string    `json:"state"`
	Draft              bool      `json:"draft"`
	Merged             bool      `json:"merged"`
	UpdatedAt          time.Time `json:"updated_at"`
	HTMLURL            string    `json:"html_url"`
	User               fjUser    `json:"user"`
	Labels             []fjLabel `json:"labels"`
	Assignees          []fjUser  `json:"assignees"`
	RequestedReviewers []fjUser  `json:"requested_reviewers"`
	Head               fjRef     `json:"head"`
	Base               fjRef     `json:"base"`
}

type fjIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
	HTMLURL     string    `json:"html_url"`
	User        fjUser    `json:"user"`
	Labels      []fjLabel `json:"labels"`
	Assignees   []fjUser  `json:"assignees"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

type fjUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type fjLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type fjRef struct {
	Ref   string `json:"ref"`
	Label string `json:"label"`
	SHA   string `json:"sha"`
	Repo  struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

func (r fjPR) toForge(host, repo string) forge.PR {
	branch := r.Head.Ref
	// Forgejo retains the original branch in label after deleting the head branch.
	if branch == fmt.Sprintf("refs/pull/%d/head", r.Number) && r.Head.Label != "" {
		branch = r.Head.Label
	}
	status := r.State
	switch {
	case r.State == "open" && r.Draft:
		status = "draft"
	case r.State == "closed" && r.Merged:
		status = "merged"
	}
	pr := forge.PR{
		Number:     r.Number,
		Title:      r.Title,
		Body:       r.Body,
		Author:     r.User.Login,
		Status:     status,
		UpdatedAt:  r.UpdatedAt,
		Branch:     branch,
		BaseBranch: r.Base.Ref,
		URL:        r.HTMLURL,
		Host:       host,
		Repo:       repo,
		HeadSHA:    r.Head.SHA,
		CrossFork:  r.Head.Repo.FullName != "" && r.Head.Repo.FullName != r.Base.Repo.FullName,
	}
	for _, l := range r.Labels {
		pr.Labels = append(pr.Labels, forge.Label{Name: l.Name, Color: l.Color})
	}
	for _, u := range r.Assignees {
		pr.Assignees = append(pr.Assignees, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	for _, u := range r.RequestedReviewers {
		pr.RequestedReviewers = append(pr.RequestedReviewers, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	return pr
}

func (r fjIssue) toForge(host, repo string) forge.Issue {
	is := forge.Issue{
		Number:    r.Number,
		Title:     r.Title,
		Body:      r.Body,
		Author:    r.User.Login,
		Status:    r.State,
		UpdatedAt: r.UpdatedAt,
		URL:       r.HTMLURL,
		Host:      host,
		Repo:      repo,
	}
	for _, l := range r.Labels {
		is.Labels = append(is.Labels, forge.Label{Name: l.Name, Color: l.Color})
	}
	for _, u := range r.Assignees {
		is.Assignees = append(is.Assignees, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	return is
}
