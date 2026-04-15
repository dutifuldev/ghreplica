package searchindex

type MentionRequest struct {
	Query  string   `json:"query"`
	Mode   string   `json:"mode"`
	Scopes []string `json:"scopes"`
	State  string   `json:"state,omitempty"`
	Author string   `json:"author,omitempty"`
	Limit  int      `json:"limit,omitempty"`
	Page   int      `json:"page,omitempty"`
}

type MentionResource struct {
	Type    string `json:"type"`
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	APIURL  string `json:"api_url"`
	HTMLURL string `json:"html_url"`
}

type MentionMatch struct {
	Resource     MentionResource `json:"resource"`
	MatchedField string          `json:"matched_field"`
	Excerpt      string          `json:"excerpt"`
	Score        float64         `json:"score"`
}

const (
	ModeFTS   = "fts"
	ModeFuzzy = "fuzzy"
	ModeRegex = "regex"
)

const (
	ScopeIssues                    = "issues"
	ScopePullRequests              = "pull_requests"
	ScopeIssueComments             = "issue_comments"
	ScopePullRequestReviews        = "pull_request_reviews"
	ScopePullRequestReviewComments = "pull_request_review_comments"
)

const (
	DocumentTypeIssue                    = "issue"
	DocumentTypePullRequest              = "pull_request"
	DocumentTypeIssueComment             = "issue_comment"
	DocumentTypePullRequestReview        = "pull_request_review"
	DocumentTypePullRequestReviewComment = "pull_request_review_comment"
)
