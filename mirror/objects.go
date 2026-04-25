package mirror

import "time"

const (
	ObjectTypeIssue       = "issue"
	ObjectTypePullRequest = "pull_request"
)

type UserObject struct {
	Login string `json:"login"`
}

type RepositoryObject struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	HTMLURL    string `json:"html_url"`
	Visibility string `json:"visibility"`
	Private    bool   `json:"private"`
	Owner      struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type IssueObject struct {
	ID        int64      `json:"id"`
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	HTMLURL   string     `json:"html_url"`
	UpdatedAt time.Time  `json:"updated_at"`
	User      UserObject `json:"user"`
}

type PullRequestObject struct {
	ID        int64      `json:"id"`
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	HTMLURL   string     `json:"html_url"`
	UpdatedAt time.Time  `json:"updated_at"`
	User      UserObject `json:"user"`
}

type ObjectRef struct {
	Type   string `json:"type"`
	Number int    `json:"number"`
}

type ObjectSummary struct {
	Title       string
	State       string
	HTMLURL     string
	AuthorLogin string
	UpdatedAt   time.Time
}

type ObjectResult struct {
	Type    string
	Number  int
	Found   bool
	Summary *ObjectSummary
}

func RepositoryObjectFromRow(repository Repository) RepositoryObject {
	out := RepositoryObject{
		ID:         repository.GitHubID,
		Name:       repository.Name,
		FullName:   repository.FullName,
		HTMLURL:    repository.HTMLURL,
		Visibility: repository.Visibility,
		Private:    repository.Private,
	}
	out.Owner.Login = repository.OwnerLogin
	if repository.Owner != nil && repository.Owner.Login != "" {
		out.Owner.Login = repository.Owner.Login
	}
	return out
}

func IssueObjectFromRow(issue Issue) IssueObject {
	return IssueObject{
		ID:        issue.GitHubID,
		Number:    issue.Number,
		Title:     issue.Title,
		State:     issue.State,
		HTMLURL:   issue.HTMLURL,
		UpdatedAt: issue.GitHubUpdatedAt,
		User:      UserObject{Login: authorLogin(issue.Author)},
	}
}

func PullRequestObjectFromRow(pull PullRequest) PullRequestObject {
	return PullRequestObject{
		ID:        pull.GitHubID,
		Number:    pull.Number,
		Title:     pull.Issue.Title,
		State:     pull.State,
		HTMLURL:   pull.HTMLURL,
		UpdatedAt: pull.GitHubUpdatedAt,
		User:      UserObject{Login: authorLogin(pull.Issue.Author)},
	}
}

func SummaryFromIssue(issue Issue) ObjectSummary {
	object := IssueObjectFromRow(issue)
	return ObjectSummary{
		Title:       object.Title,
		State:       object.State,
		HTMLURL:     object.HTMLURL,
		AuthorLogin: object.User.Login,
		UpdatedAt:   object.UpdatedAt,
	}
}

func SummaryFromPullRequest(pull PullRequest) ObjectSummary {
	object := PullRequestObjectFromRow(pull)
	return ObjectSummary{
		Title:       object.Title,
		State:       object.State,
		HTMLURL:     object.HTMLURL,
		AuthorLogin: object.User.Login,
		UpdatedAt:   object.UpdatedAt,
	}
}

func authorLogin(user *User) string {
	if user == nil {
		return ""
	}
	return user.Login
}
