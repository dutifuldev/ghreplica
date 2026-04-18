package ghr

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
)

type CommitResponse struct {
	SHA                     string                        `json:"sha"`
	TreeSHA                 string                        `json:"tree_sha"`
	AuthorName              string                        `json:"author_name"`
	AuthorEmail             string                        `json:"author_email"`
	AuthoredAt              time.Time                     `json:"authored_at"`
	AuthoredTimezoneOffset  int                           `json:"authored_timezone_offset"`
	CommitterName           string                        `json:"committer_name"`
	CommitterEmail          string                        `json:"committer_email"`
	CommittedAt             time.Time                     `json:"committed_at"`
	CommittedTimezoneOffset int                           `json:"committed_timezone_offset"`
	Message                 string                        `json:"message"`
	MessageEncoding         string                        `json:"message_encoding"`
	Parents                 []string                      `json:"parents"`
	ParentDetails           []gitindex.CommitParentDetail `json:"parent_details,omitempty"`
}

type PullRequestChangeSnapshotResponse struct {
	PullRequestNumber int        `json:"pull_request_number"`
	HeadSHA           string     `json:"head_sha"`
	BaseSHA           string     `json:"base_sha"`
	MergeBaseSHA      string     `json:"merge_base_sha"`
	BaseRef           string     `json:"base_ref"`
	State             string     `json:"state"`
	Draft             bool       `json:"draft"`
	IndexedAs         string     `json:"indexed_as"`
	IndexFreshness    string     `json:"index_freshness"`
	PathCount         int        `json:"path_count"`
	IndexedFileCount  int        `json:"indexed_file_count"`
	HunkCount         int        `json:"hunk_count"`
	Additions         int        `json:"additions"`
	Deletions         int        `json:"deletions"`
	PatchBytes        int        `json:"patch_bytes"`
	LastIndexedAt     *time.Time `json:"last_indexed_at,omitempty"`
}

type RepoChangeStatusResponse = gitindex.RepoStatus

type PullRequestChangeStatusResponse = gitindex.PullRequestStatus

type CompareResponse struct {
	Base     string `json:"base"`
	Head     string `json:"head"`
	Resolved struct {
		Base string `json:"base"`
		Head string `json:"head"`
	} `json:"resolved"`
	Snapshot PullRequestChangeSnapshotResponse `json:"snapshot"`
	Files    []gitindex.FileChange             `json:"files"`
}

type SearchRange struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type searchByPathsRequest struct {
	Paths []string `json:"paths"`
	State string   `json:"state,omitempty"`
	Limit int      `json:"limit,omitempty"`
}

type searchByRangesRequest struct {
	Ranges []SearchRange `json:"ranges"`
	State  string        `json:"state,omitempty"`
	Limit  int           `json:"limit,omitempty"`
}

type MentionSearchRequest = searchindex.MentionRequest

type MentionMatch = searchindex.MentionMatch

type RepoSearchStatusResponse = searchindex.RepoStatus

type StructuralSearchRequest = gitindex.StructuralSearchRequest

type StructuralSearchResponse = gitindex.StructuralSearchResponse

func (c *Client) GetPullRequestChangeSnapshot(ctx context.Context, repo string, number int) (PullRequestChangeSnapshotResponse, error) {
	var out PullRequestChangeSnapshotResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/pulls/%d", repo, number), &out)
	return out, err
}

func (c *Client) GetRepoChangeStatus(ctx context.Context, repo string) (RepoChangeStatusResponse, error) {
	var out RepoChangeStatusResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/status", repo), &out)
	return out, err
}

func (c *Client) GetPullRequestChangeStatus(ctx context.Context, repo string, number int) (PullRequestChangeStatusResponse, error) {
	var out PullRequestChangeStatusResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/pulls/%d/status", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequestChangeFiles(ctx context.Context, repo string, number int) ([]gitindex.FileChange, error) {
	var out []gitindex.FileChange
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/pulls/%d/files", repo, number), &out)
	return out, err
}

func (c *Client) GetCommit(ctx context.Context, repo, sha string) (CommitResponse, error) {
	var out CommitResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/commits/%s", repo, url.PathEscape(sha)), &out)
	return out, err
}

func (c *Client) ListCommitFiles(ctx context.Context, repo, sha string) ([]map[string]any, error) {
	var out []map[string]any
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/commits/%s/files", repo, url.PathEscape(sha)), &out)
	return out, err
}

func (c *Client) CompareChanges(ctx context.Context, repo, spec string) (CompareResponse, error) {
	var out CompareResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/changes/repos/%s/compare/%s", repo, url.PathEscape(spec)), &out)
	return out, err
}

func (c *Client) SearchRelatedPullRequests(ctx context.Context, repo string, number int, mode, state string, limit int) ([]gitindex.SearchMatch, error) {
	path := fmt.Sprintf("/v1/search/repos/%s/pulls/%d/related", repo, number)
	q := url.Values{}
	if mode != "" {
		q.Set("mode", mode)
	}
	if state != "" {
		q.Set("state", state)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []gitindex.SearchMatch
	err := c.getJSON(ctx, path, &out)
	return out, err
}

func (c *Client) SearchPullRequestsByPaths(ctx context.Context, repo string, paths []string, state string, limit int) ([]gitindex.SearchMatch, error) {
	var out []gitindex.SearchMatch
	err := c.postJSON(ctx, fmt.Sprintf("/v1/search/repos/%s/pulls/by-paths", repo), searchByPathsRequest{
		Paths: paths,
		State: state,
		Limit: limit,
	}, &out)
	return out, err
}

func (c *Client) SearchPullRequestsByRanges(ctx context.Context, repo string, ranges []SearchRange, state string, limit int) ([]gitindex.SearchMatch, error) {
	var out []gitindex.SearchMatch
	err := c.postJSON(ctx, fmt.Sprintf("/v1/search/repos/%s/pulls/by-ranges", repo), searchByRangesRequest{
		Ranges: ranges,
		State:  state,
		Limit:  limit,
	}, &out)
	return out, err
}

func (c *Client) SearchMentions(ctx context.Context, repo string, request MentionSearchRequest) ([]MentionMatch, error) {
	var out []MentionMatch
	err := c.postJSON(ctx, fmt.Sprintf("/v1/search/repos/%s/mentions", repo), request, &out)
	return out, err
}

func (c *Client) GetRepoSearchStatus(ctx context.Context, repo string) (RepoSearchStatusResponse, error) {
	var out RepoSearchStatusResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/search/repos/%s/status", repo), &out)
	return out, err
}

func (c *Client) SearchASTGrep(ctx context.Context, repo string, request StructuralSearchRequest) (StructuralSearchResponse, error) {
	var out StructuralSearchResponse
	err := c.postJSON(ctx, fmt.Sprintf("/v1/search/repos/%s/ast-grep", repo), request, &out)
	return out, err
}
