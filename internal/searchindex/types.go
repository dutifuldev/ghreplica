package searchindex

import "time"

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

type RepoStatusResource struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

type RepoStatus struct {
	Repository         RepoStatusResource `json:"repository"`
	TextIndexStatus    string             `json:"text_index_status"`
	DocumentCount      int64              `json:"document_count"`
	LastIndexedAt      *time.Time         `json:"last_indexed_at,omitempty"`
	LastSourceUpdateAt *time.Time         `json:"last_source_update_at,omitempty"`
	Freshness          string             `json:"freshness"`
	Coverage           string             `json:"coverage"`
	LastError          string             `json:"last_error,omitempty"`
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

const (
	TextIndexStatusMissing  = "missing"
	TextIndexStatusBuilding = "building"
	TextIndexStatusReady    = "ready"
	TextIndexStatusStale    = "stale"
	TextIndexStatusFailed   = "failed"
)

const (
	TextIndexFreshnessCurrent = "current"
	TextIndexFreshnessStale   = "stale"
	TextIndexFreshnessUnknown = "unknown"
)

const (
	TextIndexCoverageEmpty    = "empty"
	TextIndexCoveragePartial  = "partial"
	TextIndexCoverageComplete = "complete"
)
