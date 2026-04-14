package github

import "time"

type UserResponse struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Login     string `json:"login"`
	Type      string `json:"type"`
	SiteAdmin bool   `json:"site_admin"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	URL       string `json:"url"`
}

type RepositoryResponse struct {
	ID            int64         `json:"id"`
	NodeID        string        `json:"node_id"`
	Name          string        `json:"name"`
	FullName      string        `json:"full_name"`
	Private       bool          `json:"private"`
	Owner         *UserResponse `json:"owner"`
	HTMLURL       string        `json:"html_url"`
	Description   string        `json:"description"`
	Fork          bool          `json:"fork"`
	URL           string        `json:"url"`
	DefaultBranch string        `json:"default_branch"`
	Visibility    string        `json:"visibility"`
	Archived      bool          `json:"archived"`
	Disabled      bool          `json:"disabled"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type IssuePullRequestRef struct {
	URL string `json:"url"`
}

type IssueResponse struct {
	ID          int64                `json:"id"`
	NodeID      string               `json:"node_id"`
	Number      int                  `json:"number"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	State       string               `json:"state"`
	StateReason string               `json:"state_reason"`
	User        *UserResponse        `json:"user"`
	Locked      bool                 `json:"locked"`
	Comments    int                  `json:"comments"`
	PullRequest *IssuePullRequestRef `json:"pull_request"`
	HTMLURL     string               `json:"html_url"`
	URL         string               `json:"url"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	ClosedAt    *time.Time           `json:"closed_at"`
}

type IssueCommentResponse struct {
	ID        int64         `json:"id"`
	NodeID    string        `json:"node_id"`
	Body      string        `json:"body"`
	User      *UserResponse `json:"user"`
	IssueURL  string        `json:"issue_url"`
	HTMLURL   string        `json:"html_url"`
	URL       string        `json:"url"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type PullBranchRepository struct {
	ID            int64         `json:"id"`
	NodeID        string        `json:"node_id"`
	Name          string        `json:"name"`
	FullName      string        `json:"full_name"`
	Private       bool          `json:"private"`
	Owner         *UserResponse `json:"owner"`
	HTMLURL       string        `json:"html_url"`
	Description   string        `json:"description"`
	Fork          bool          `json:"fork"`
	URL           string        `json:"url"`
	DefaultBranch string        `json:"default_branch"`
	Visibility    string        `json:"visibility"`
	Archived      bool          `json:"archived"`
	Disabled      bool          `json:"disabled"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type PullBranch struct {
	Ref  string                `json:"ref"`
	SHA  string                `json:"sha"`
	Repo *PullBranchRepository `json:"repo"`
}

type PullRequestResponse struct {
	ID             int64         `json:"id"`
	NodeID         string        `json:"node_id"`
	Number         int           `json:"number"`
	State          string        `json:"state"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	User           *UserResponse `json:"user"`
	Draft          bool          `json:"draft"`
	Head           PullBranch    `json:"head"`
	Base           PullBranch    `json:"base"`
	Mergeable      *bool         `json:"mergeable"`
	MergeableState string        `json:"mergeable_state"`
	Merged         bool          `json:"merged"`
	MergedAt       *time.Time    `json:"merged_at"`
	MergedBy       *UserResponse `json:"merged_by"`
	MergeCommitSHA string        `json:"merge_commit_sha"`
	Additions      int           `json:"additions"`
	Deletions      int           `json:"deletions"`
	ChangedFiles   int           `json:"changed_files"`
	Commits        int           `json:"commits"`
	HTMLURL        string        `json:"html_url"`
	URL            string        `json:"url"`
	DiffURL        string        `json:"diff_url"`
	PatchURL       string        `json:"patch_url"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type PullRequestReviewResponse struct {
	ID          int64         `json:"id"`
	NodeID      string        `json:"node_id"`
	User        *UserResponse `json:"user"`
	Body        string        `json:"body"`
	State       string        `json:"state"`
	HTMLURL     string        `json:"html_url"`
	URL         string        `json:"url"`
	CommitID    string        `json:"commit_id"`
	SubmittedAt *time.Time    `json:"submitted_at"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type PullRequestReviewCommentResponse struct {
	ID                  int64         `json:"id"`
	NodeID              string        `json:"node_id"`
	PullRequestURL      string        `json:"pull_request_url"`
	PullRequestReviewID *int64        `json:"pull_request_review_id"`
	HTMLURL             string        `json:"html_url"`
	URL                 string        `json:"url"`
	Body                string        `json:"body"`
	Path                string        `json:"path"`
	DiffHunk            string        `json:"diff_hunk"`
	Position            *int          `json:"position"`
	OriginalPosition    *int          `json:"original_position"`
	Line                *int          `json:"line"`
	OriginalLine        *int          `json:"original_line"`
	Side                string        `json:"side"`
	User                *UserResponse `json:"user"`
	InReplyToID         *int64        `json:"in_reply_to_id"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}
