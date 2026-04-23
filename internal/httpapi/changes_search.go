package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type commitResponse struct {
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

type pullRequestChangeSnapshotResponse struct {
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

type compareResponse struct {
	Base     string `json:"base"`
	Head     string `json:"head"`
	Resolved struct {
		Base string `json:"base"`
		Head string `json:"head"`
	} `json:"resolved"`
	Snapshot pullRequestChangeSnapshotResponse `json:"snapshot"`
	Files    []gitindex.FileChange             `json:"files"`
}

type searchByPathsRequest struct {
	Paths []string `json:"paths"`
	State string   `json:"state"`
	Limit int      `json:"limit"`
}

type searchRange struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type searchByRangesRequest struct {
	Ranges []searchRange `json:"ranges"`
	State  string        `json:"state"`
	Limit  int           `json:"limit"`
}

type searchMentionsRequest struct {
	Query  string   `json:"query"`
	Mode   string   `json:"mode"`
	Scopes []string `json:"scopes"`
	State  string   `json:"state"`
	Author string   `json:"author"`
	Limit  int      `json:"limit"`
	Page   int      `json:"page"`
}

type searchASTGrepRequest struct {
	CommitSHA         string          `json:"commit_sha"`
	Ref               string          `json:"ref"`
	PullRequestNumber int             `json:"pull_request_number"`
	Language          string          `json:"language"`
	Rule              json.RawMessage `json:"rule"`
	Paths             []string        `json:"paths"`
	ChangedFilesOnly  bool            `json:"changed_files_only"`
	Limit             int             `json:"limit"`
}

func (s *Server) handleGetRepoChangeStatus(c echo.Context) error {
	if s.changeStatus == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Change status is not configured"})
	}
	status, err := s.changeStatus.GetRepoChangeStatus(c.Request().Context(), c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	return c.JSON(http.StatusOK, status)
}

func (s *Server) handleSearchASTGrep(c echo.Context) error {
	if s.structuralSearch == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Structural search is not configured"})
	}
	if err := ensureSearchRepositoryExists(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo")); err != nil {
		return structuralSearchErrorResponse(c, err)
	}

	request, err := bindStructuralSearchRequest(c)
	if err != nil {
		return err
	}
	response, err := s.structuralSearch.SearchStructural(c.Request().Context(), c.Param("owner"), c.Param("repo"), request)
	if err != nil {
		return structuralSearchErrorResponse(c, err)
	}
	return c.JSON(http.StatusOK, response)
}

func ensureSearchRepositoryExists(ctx context.Context, db *gorm.DB, owner, repo string) error {
	_, err := findRepository(ctx, db, owner, repo)
	return err
}

func bindStructuralSearchRequest(c echo.Context) (gitindex.StructuralSearchRequest, error) {
	var payload searchASTGrepRequest
	if err := c.Bind(&payload); err != nil {
		return gitindex.StructuralSearchRequest{}, echo.NewHTTPError(http.StatusBadRequest, map[string]string{"message": "Invalid request body"})
	}
	rule, err := parseStructuralRule(payload.Rule)
	if err != nil {
		return gitindex.StructuralSearchRequest{}, echo.NewHTTPError(http.StatusBadRequest, map[string]string{"message": "Invalid rule payload"})
	}
	return gitindex.StructuralSearchRequest{
		CommitSHA:         strings.TrimSpace(payload.CommitSHA),
		Ref:               strings.TrimSpace(payload.Ref),
		PullRequestNumber: payload.PullRequestNumber,
		Language:          strings.TrimSpace(payload.Language),
		Rule:              rule,
		Paths:             payload.Paths,
		ChangedFilesOnly:  payload.ChangedFilesOnly,
		Limit:             payload.Limit,
	}, nil
}

func parseStructuralRule(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rule map[string]any
	if err := json.Unmarshal(raw, &rule); err != nil {
		return nil, err
	}
	return rule, nil
}

func structuralSearchErrorResponse(c echo.Context, err error) error {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), gitindex.IsStructuralSearchTargetNotFound(err):
		return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
	case gitindex.IsInvalidStructuralSearchRequest(err):
		return c.JSON(http.StatusBadRequest, map[string]string{"message": err.Error()})
	default:
		return err
	}
}

func (s *Server) handleGetPullRequestChangeStatus(c echo.Context) error {
	if s.changeStatus == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Change status is not configured"})
	}
	number := parsePositiveInt(c.Param("number"), 0)
	if number <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid pull request number"})
	}
	status, err := s.changeStatus.GetPullRequestChangeStatus(c.Request().Context(), c.Param("owner"), c.Param("repo"), number)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	return c.JSON(http.StatusOK, status)
}

func (s *Server) handleGetPullRequestChangeSnapshot(c echo.Context) error {
	repo, snapshot, err := s.loadSnapshot(c)
	if err != nil {
		return err
	}
	_ = repo

	return c.JSON(http.StatusOK, newPullRequestChangeSnapshotResponse(snapshot))
}

func (s *Server) handleListPullRequestChangeFiles(c echo.Context) error {
	_, snapshot, err := s.loadSnapshot(c)
	if err != nil {
		return err
	}
	response, err := s.loadPullRequestChangeFiles(c.Request().Context(), snapshot)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) handleCompareChanges(c echo.Context) error {
	repo, err := s.compareRepository(c)
	if err != nil {
		return err
	}
	baseInput, headInput, err := parseCompareSpec(c.Param("spec"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid compare spec"})
	}
	baseResolved, headResolved, err := s.resolveCompareRefs(c.Request().Context(), repo.ID, baseInput, headInput)
	if err != nil {
		return err
	}
	snapshot, err := s.findCompareSnapshot(c.Request().Context(), repo.ID, baseInput, headInput, baseResolved, headResolved)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	if baseResolved == baseInput && normalizeCompareRef(baseInput) == snapshot.BaseRef {
		baseResolved = snapshot.BaseSHA
	}
	if headResolved == headInput {
		headResolved = snapshot.HeadSHA
	}

	files, err := s.loadPullRequestChangeFiles(c.Request().Context(), snapshot)
	if err != nil {
		return err
	}

	response := compareResponse{
		Base:     baseInput,
		Head:     headInput,
		Snapshot: newPullRequestChangeSnapshotResponse(snapshot),
		Files:    files,
	}
	response.Resolved.Base = baseResolved
	response.Resolved.Head = headResolved
	return c.JSON(http.StatusOK, response)
}

func (s *Server) compareRepository(c echo.Context) (database.Repository, error) {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return repo, err
	}
	return database.Repository{}, echo.NewHTTPError(http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func parseCompareSpec(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	unescaped, err := url.PathUnescape(spec)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(unescaped), "...", 2)
	if len(parts) != 2 {
		return "", "", errors.New("invalid compare spec")
	}
	baseInput := strings.TrimSpace(parts[0])
	headInput := strings.TrimSpace(parts[1])
	if baseInput == "" || headInput == "" {
		return "", "", errors.New("invalid compare spec")
	}
	return baseInput, headInput, nil
}

func (s *Server) resolveCompareRefs(ctx context.Context, repositoryID uint, baseInput, headInput string) (string, string, error) {
	baseResolved, err := s.resolveGitRefOrSHA(ctx, repositoryID, baseInput)
	if err != nil {
		return "", "", err
	}
	headResolved, err := s.resolveGitRefOrSHA(ctx, repositoryID, headInput)
	if err != nil {
		return "", "", err
	}
	return baseResolved, headResolved, nil
}

func (s *Server) findCompareSnapshot(ctx context.Context, repositoryID uint, baseInput, headInput, baseResolved, headResolved string) (database.PullRequestChangeSnapshot, error) {
	var snapshot database.PullRequestChangeSnapshot
	query := s.db.WithContext(ctx).
		Where("repository_id = ?", repositoryID).
		Where("head_sha = ? OR head_sha = ?", headInput, headResolved).
		Where(
			s.db.WithContext(ctx).
				Where("base_sha = ? OR merge_base_sha = ?", baseInput, baseInput).
				Or("base_sha = ? OR merge_base_sha = ?", baseResolved, baseResolved).
				Or("base_ref = ?", normalizeCompareRef(baseInput)),
		).
		Order("updated_at DESC")
	err := query.First(&snapshot).Error
	return snapshot, err
}

func (s *Server) handleGetCommit(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	sha := strings.TrimSpace(c.Param("sha"))
	if sha == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid commit sha"})
	}

	var commit database.GitCommit
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND sha = ?", repo.ID, sha).
		First(&commit).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	var parents []database.GitCommitParent
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, sha).
		Order("parent_index ASC").
		Find(&parents).Error; err != nil {
		return err
	}
	parentSHAs := make([]string, 0, len(parents))
	parentDetails := make([]gitindex.CommitParentDetail, 0, len(parents))
	for _, parent := range parents {
		parentSHAs = append(parentSHAs, parent.ParentSHA)
		parentDetails = append(parentDetails, gitindex.CommitParentDetail{
			ParentSHA:     parent.ParentSHA,
			ParentIndex:   parent.ParentIndex,
			IndexedAs:     parent.IndexedAs,
			IndexReason:   parent.IndexReason,
			PathCount:     parent.PathCount,
			HunkCount:     parent.HunkCount,
			Additions:     parent.Additions,
			Deletions:     parent.Deletions,
			PatchBytes:    parent.PatchBytes,
			LastIndexedAt: parent.LastIndexedAt,
		})
	}

	return c.JSON(http.StatusOK, commitResponse{
		SHA:                     commit.SHA,
		TreeSHA:                 commit.TreeSHA,
		AuthorName:              commit.AuthorName,
		AuthorEmail:             commit.AuthorEmail,
		AuthoredAt:              utcTime(commit.AuthoredAt),
		AuthoredTimezoneOffset:  commit.AuthoredTimezoneOffset,
		CommitterName:           commit.CommitterName,
		CommitterEmail:          commit.CommitterEmail,
		CommittedAt:             utcTime(commit.CommittedAt),
		CommittedTimezoneOffset: commit.CommittedTimezoneOffset,
		Message:                 commit.Message,
		MessageEncoding:         commit.MessageEncoding,
		Parents:                 parentSHAs,
		ParentDetails:           parentDetails,
	})
}

func (s *Server) handleListCommitFiles(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	sha := strings.TrimSpace(c.Param("sha"))
	if sha == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid commit sha"})
	}

	var commit database.GitCommit
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND sha = ?", repo.ID, sha).
		First(&commit).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	var files []database.GitCommitParentFile
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, sha).
		Order("parent_index ASC, path ASC").
		Find(&files).Error; err != nil {
		return err
	}
	var hunks []database.GitCommitParentHunk
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, sha).
		Order("parent_index ASC, path ASC, hunk_index ASC").
		Find(&hunks).Error; err != nil {
		return err
	}
	type commitFileResponse struct {
		ParentSHA   string              `json:"parent_sha"`
		ParentIndex int                 `json:"parent_index"`
		File        gitindex.FileChange `json:"file"`
	}
	hunksByKey := map[string][]gitindex.Hunk{}
	for _, h := range hunks {
		key := commitHunkKey(h.ParentIndex, h.Path)
		hunksByKey[key] = append(hunksByKey[key], gitindex.Hunk{
			Index:    h.HunkIndex,
			DiffHunk: h.DiffHunk,
			OldStart: h.OldStart,
			OldCount: h.OldCount,
			OldEnd:   h.OldEnd,
			NewStart: h.NewStart,
			NewCount: h.NewCount,
			NewEnd:   h.NewEnd,
		})
	}
	response := make([]commitFileResponse, 0, len(files))
	for _, file := range files {
		response = append(response, commitFileResponse{
			ParentSHA:   file.ParentSHA,
			ParentIndex: file.ParentIndex,
			File: gitindex.FileChange{
				Path:         file.Path,
				PreviousPath: file.PreviousPath,
				Status:       file.Status,
				FileKind:     file.FileKind,
				IndexedAs:    file.IndexedAs,
				OldMode:      file.OldMode,
				NewMode:      file.NewMode,
				HeadBlobSHA:  file.BlobSHA,
				BaseBlobSHA:  file.PreviousBlobSHA,
				Additions:    file.Additions,
				Deletions:    file.Deletions,
				Changes:      file.Changes,
				Patch:        file.PatchText,
				Hunks:        hunksByKey[commitHunkKey(file.ParentIndex, file.Path)],
			},
		})
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) handleSearchMentions(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	if s.search == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Search is not configured"})
	}

	var request searchMentionsRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid request body"})
	}

	matches, err := s.search.SearchMentions(c.Request().Context(), repo.ID, searchindex.MentionRequest{
		Query:  request.Query,
		Mode:   request.Mode,
		Scopes: request.Scopes,
		State:  request.State,
		Author: request.Author,
		Limit:  request.Limit,
		Page:   request.Page,
	})
	if err != nil {
		if searchindex.IsInvalidRequest(err) {
			return c.JSON(http.StatusBadRequest, map[string]string{"message": err.Error()})
		}
		return err
	}
	return c.JSON(http.StatusOK, matches)
}

func (s *Server) handleGetRepoSearchStatus(c echo.Context) error {
	if s.search == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Search is not configured"})
	}
	status, err := s.search.GetRepoStatus(c.Request().Context(), c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	return c.JSON(http.StatusOK, status)
}

func (s *Server) handleSearchRelatedPullRequests(c echo.Context) error {
	repo, snapshot, err := s.loadSnapshot(c)
	if err != nil {
		return err
	}
	mode := strings.TrimSpace(c.QueryParam("mode"))
	if mode == "" {
		mode = "path_overlap"
	}
	state := strings.TrimSpace(c.QueryParam("state"))
	if state == "" {
		state = "open"
	}
	limit := clamp(parsePositiveInt(c.QueryParam("limit"), 20), 1, 100)
	matches, err := s.searchRelated(c.Request().Context(), repo.ID, snapshot, mode, state, limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, matches)
}

func (s *Server) handleSearchPullRequestsByPaths(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	var req searchByPathsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid request body"})
	}
	paths := normalizePaths(req.Paths)
	if len(paths) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "paths are required"})
	}
	state := req.State
	if state == "" {
		state = "open"
	}
	limit := clamp(req.Limit, 1, 100)
	if limit == 1 && req.Limit == 0 {
		limit = 20
	}
	matches, err := s.searchByPaths(c.Request().Context(), repo.ID, paths, state, limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, matches)
}

func (s *Server) handleSearchPullRequestsByRanges(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}
	var req searchByRangesRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid request body"})
	}
	if len(req.Ranges) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "ranges are required"})
	}
	state := req.State
	if state == "" {
		state = "open"
	}
	limit := clamp(req.Limit, 1, 100)
	if limit == 1 && req.Limit == 0 {
		limit = 20
	}
	matches, err := s.searchByRanges(c.Request().Context(), repo.ID, req.Ranges, state, limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, matches)
}

func (s *Server) loadSnapshot(c echo.Context) (database.Repository, database.PullRequestChangeSnapshot, error) {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.Repository{}, database.PullRequestChangeSnapshot{}, echo.NewHTTPError(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return database.Repository{}, database.PullRequestChangeSnapshot{}, err
	}
	number := parsePositiveInt(c.Param("number"), 0)
	if number <= 0 {
		return database.Repository{}, database.PullRequestChangeSnapshot{}, echo.NewHTTPError(http.StatusBadRequest, map[string]string{"message": "Invalid pull request number"})
	}
	var snapshot database.PullRequestChangeSnapshot
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND pull_request_number = ?", repo.ID, number).
		First(&snapshot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.Repository{}, database.PullRequestChangeSnapshot{}, echo.NewHTTPError(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return database.Repository{}, database.PullRequestChangeSnapshot{}, err
	}
	return repo, snapshot, nil
}

func newPullRequestChangeSnapshotResponse(snapshot database.PullRequestChangeSnapshot) pullRequestChangeSnapshotResponse {
	return pullRequestChangeSnapshotResponse{
		PullRequestNumber: snapshot.PullRequestNumber,
		HeadSHA:           snapshot.HeadSHA,
		BaseSHA:           snapshot.BaseSHA,
		MergeBaseSHA:      snapshot.MergeBaseSHA,
		BaseRef:           snapshot.BaseRef,
		State:             snapshot.State,
		Draft:             snapshot.Draft,
		IndexedAs:         snapshot.IndexedAs,
		IndexFreshness:    snapshot.IndexFreshness,
		PathCount:         snapshot.PathCount,
		IndexedFileCount:  snapshot.IndexedFileCount,
		HunkCount:         snapshot.HunkCount,
		Additions:         snapshot.Additions,
		Deletions:         snapshot.Deletions,
		PatchBytes:        snapshot.PatchBytes,
		LastIndexedAt:     utcTimePtr(snapshot.LastIndexedAt),
	}
}

func (s *Server) loadPullRequestChangeFiles(ctx context.Context, snapshot database.PullRequestChangeSnapshot) ([]gitindex.FileChange, error) {
	var files []database.PullRequestChangeFile
	if err := s.db.WithContext(ctx).
		Where("snapshot_id = ?", snapshot.ID).
		Order("path ASC").
		Find(&files).Error; err != nil {
		return nil, err
	}
	var hunks []database.PullRequestChangeHunk
	if err := s.db.WithContext(ctx).
		Where("snapshot_id = ?", snapshot.ID).
		Order("path ASC, hunk_index ASC").
		Find(&hunks).Error; err != nil {
		return nil, err
	}

	hunksByPath := map[string][]gitindex.Hunk{}
	for _, h := range hunks {
		hunksByPath[h.Path] = append(hunksByPath[h.Path], gitindex.Hunk{
			Index:    h.HunkIndex,
			DiffHunk: h.DiffHunk,
			OldStart: h.OldStart,
			OldCount: h.OldCount,
			OldEnd:   h.OldEnd,
			NewStart: h.NewStart,
			NewCount: h.NewCount,
			NewEnd:   h.NewEnd,
		})
	}

	response := make([]gitindex.FileChange, 0, len(files))
	for _, file := range files {
		response = append(response, gitindex.FileChange{
			Path:         file.Path,
			PreviousPath: file.PreviousPath,
			Status:       file.Status,
			FileKind:     file.FileKind,
			IndexedAs:    file.IndexedAs,
			OldMode:      file.OldMode,
			NewMode:      file.NewMode,
			HeadBlobSHA:  file.HeadBlobSHA,
			BaseBlobSHA:  file.BaseBlobSHA,
			Additions:    file.Additions,
			Deletions:    file.Deletions,
			Changes:      file.Changes,
			Patch:        file.PatchText,
			Hunks:        hunksByPath[file.Path],
		})
	}
	return response, nil
}

func commitHunkKey(parentIndex int, path string) string {
	return strconv.Itoa(parentIndex) + ":" + path
}

func (s *Server) resolveGitRefOrSHA(ctx context.Context, repositoryID uint, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	var ref database.GitRef
	candidates := []string{
		value,
		"refs/heads/" + normalizeCompareRef(value),
		"refs/remotes/origin/" + normalizeCompareRef(value),
		"refs/pull/" + normalizeCompareRef(value) + "/head",
	}
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND ref_name IN ?", repositoryID, normalizePaths(candidates)).
		Order("updated_at DESC").
		First(&ref).Error; err == nil {
		if strings.TrimSpace(ref.PeeledCommitSHA) != "" {
			return ref.PeeledCommitSHA, nil
		}
		if strings.TrimSpace(ref.TargetOID) != "" {
			return ref.TargetOID, nil
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	return value, nil
}

func normalizeCompareRef(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "refs/heads/")
	value = strings.TrimPrefix(value, "refs/remotes/origin/")
	return value
}

func normalizePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func noisyPath(path string) bool {
	base := filepath.Base(path)
	switch base {
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
		return true
	}
	if strings.Contains(path, "/vendor/") || strings.Contains(path, "/__snapshots__/") {
		return true
	}
	return false
}

func (s *Server) searchRelated(ctx context.Context, repositoryID uint, source database.PullRequestChangeSnapshot, mode, state string, limit int) ([]gitindex.SearchMatch, error) {
	paths, err := s.relatedSourcePaths(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	candidates, err := s.relatedPathCandidates(ctx, repositoryID, source.PullRequestNumber, paths, state, limit)
	if err != nil {
		return nil, err
	}
	if mode == "path_overlap" || source.IndexedAs == "paths_only" || source.IndexedAs == "oversized" {
		return trimSearchMatches(candidates, limit), nil
	}
	sourceHunks, err := s.relatedSourceHunks(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	if len(sourceHunks) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	candidateHunks, err := s.relatedCandidateHunks(ctx, repositoryID, candidates, paths)
	if err != nil {
		return nil, err
	}
	return scoreRelatedHunkCandidates(candidates, sourceHunks, candidateHunks, limit), nil
}

func (s *Server) relatedSourcePaths(ctx context.Context, snapshotID uint) ([]string, error) {
	var sourceFiles []database.PullRequestChangeFile
	if err := s.db.WithContext(ctx).Where("snapshot_id = ?", snapshotID).Find(&sourceFiles).Error; err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(sourceFiles))
	for _, file := range sourceFiles {
		if !noisyPath(file.Path) {
			paths = append(paths, file.Path)
		}
		if file.PreviousPath != "" && !noisyPath(file.PreviousPath) {
			paths = append(paths, file.PreviousPath)
		}
	}
	return normalizePaths(paths), nil
}

func (s *Server) relatedPathCandidates(ctx context.Context, repositoryID uint, sourcePRNumber int, paths []string, state string, limit int) ([]gitindex.SearchMatch, error) {
	candidates, err := s.searchByPaths(ctx, repositoryID, paths, state, max(limit*5, 50))
	if err != nil {
		return nil, err
	}
	filtered := make([]gitindex.SearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PullRequestNumber != sourcePRNumber {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, nil
}

func (s *Server) relatedSourceHunks(ctx context.Context, snapshotID uint) ([]database.PullRequestChangeHunk, error) {
	var sourceHunks []database.PullRequestChangeHunk
	err := s.db.WithContext(ctx).Where("snapshot_id = ?", snapshotID).Find(&sourceHunks).Error
	return sourceHunks, err
}

func (s *Server) relatedCandidateHunks(ctx context.Context, repositoryID uint, candidates []gitindex.SearchMatch, paths []string) ([]database.PullRequestChangeHunk, error) {
	candidateNums := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		candidateNums = append(candidateNums, candidate.PullRequestNumber)
	}
	var candidateHunks []database.PullRequestChangeHunk
	err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, candidateNums).
		Where("path IN ?", paths).
		Find(&candidateHunks).Error
	return candidateHunks, err
}

func scoreRelatedHunkCandidates(candidates []gitindex.SearchMatch, sourceHunks, candidateHunks []database.PullRequestChangeHunk, limit int) []gitindex.SearchMatch {
	sourceByPath := map[string][]database.PullRequestChangeHunk{}
	for _, h := range sourceHunks {
		sourceByPath[h.Path] = append(sourceByPath[h.Path], h)
	}
	candidateByPR := map[int][]database.PullRequestChangeHunk{}
	for _, h := range candidateHunks {
		candidateByPR[h.PullRequestNumber] = append(candidateByPR[h.PullRequestNumber], h)
	}
	matchByPR := map[int]gitindex.SearchMatch{}
	for _, match := range candidates {
		matchByPR[match.PullRequestNumber] = match
	}
	for prNumber, hunks := range candidateByPR {
		matchByPR[prNumber] = applyRelatedHunkOverlap(matchByPR[prNumber], sourceByPath, hunks)
	}
	final := make([]gitindex.SearchMatch, 0, len(matchByPR))
	for _, match := range matchByPR {
		if match.OverlappingHunks > 0 {
			final = append(final, match)
		}
	}
	sortSearchMatches(final)
	return trimSearchMatches(final, limit)
}

func applyRelatedHunkOverlap(match gitindex.SearchMatch, sourceByPath map[string][]database.PullRequestChangeHunk, hunks []database.PullRequestChangeHunk) gitindex.SearchMatch {
	pathSet := map[string]struct{}{}
	for _, candidateHunk := range hunks {
		for _, sourceHunk := range sourceByPath[candidateHunk.Path] {
			if !hunksOverlap(sourceHunk, candidateHunk) {
				continue
			}
			match.OverlappingHunks++
			pathSet[candidateHunk.Path] = struct{}{}
			match.MatchedRanges = append(match.MatchedRanges, gitindex.MatchedPath{
				Path:     candidateHunk.Path,
				OldStart: max(sourceHunk.OldStart, candidateHunk.OldStart),
				OldEnd:   minNonZero(sourceHunk.OldEnd, candidateHunk.OldEnd),
				NewStart: max(sourceHunk.NewStart, candidateHunk.NewStart),
				NewEnd:   minNonZero(sourceHunk.NewEnd, candidateHunk.NewEnd),
			})
		}
	}
	for path := range pathSet {
		match.OverlappingPaths = append(match.OverlappingPaths, path)
	}
	sort.Strings(match.OverlappingPaths)
	match.Score += float64(match.OverlappingHunks * 20)
	if match.OverlappingHunks > 0 {
		match.Reasons = append(match.Reasons, "overlapping_hunks")
	}
	return match
}

func (s *Server) searchByPaths(ctx context.Context, repositoryID uint, paths []string, state string, limit int) ([]gitindex.SearchMatch, error) {
	if len(paths) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	paths = normalizePaths(paths)
	snapshots, err := s.searchSnapshots(ctx, repositoryID, state)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	snapshotByPR, prNumbers := searchSnapshotIndex(snapshots)
	var files []database.PullRequestChangeFile
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, prNumbers).
		Where("path IN ? OR previous_path IN ?", paths, paths).
		Find(&files).Error; err != nil {
		return nil, err
	}
	results := buildPathSearchMatches(files, paths, snapshotByPR)
	sortSearchMatches(results)
	return trimSearchMatches(results, limit), nil
}

func (s *Server) searchByRanges(ctx context.Context, repositoryID uint, ranges []searchRange, state string, limit int) ([]gitindex.SearchMatch, error) {
	paths := make([]string, 0, len(ranges))
	for _, r := range ranges {
		if strings.TrimSpace(r.Path) != "" {
			paths = append(paths, strings.TrimSpace(r.Path))
		}
	}
	paths = normalizePaths(paths)
	if len(paths) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	snapshots, err := s.searchSnapshots(ctx, repositoryID, state)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	snapshotByPR, prNumbers := searchSnapshotIndex(snapshots)
	var hunks []database.PullRequestChangeHunk
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, prNumbers).
		Where("path IN ?", paths).
		Find(&hunks).Error; err != nil {
		return nil, err
	}
	results := buildRangeSearchMatches(hunks, ranges, snapshotByPR)
	sortSearchMatches(results)
	return trimSearchMatches(results, limit), nil
}

func (s *Server) searchSnapshots(ctx context.Context, repositoryID uint, state string) ([]database.PullRequestChangeSnapshot, error) {
	var snapshots []database.PullRequestChangeSnapshot
	query := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID)
	query = applySnapshotStateFilter(query, state)
	err := query.Find(&snapshots).Error
	return snapshots, err
}

func searchSnapshotIndex(snapshots []database.PullRequestChangeSnapshot) (map[int]database.PullRequestChangeSnapshot, []int) {
	snapshotByPR := map[int]database.PullRequestChangeSnapshot{}
	prNumbers := make([]int, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshotByPR[snapshot.PullRequestNumber] = snapshot
		prNumbers = append(prNumbers, snapshot.PullRequestNumber)
	}
	return snapshotByPR, prNumbers
}

type pathAggregate struct {
	shared map[string]struct{}
	score  float64
}

func buildPathSearchMatches(files []database.PullRequestChangeFile, paths []string, snapshotByPR map[int]database.PullRequestChangeSnapshot) []gitindex.SearchMatch {
	agg := map[int]*pathAggregate{}
	for _, file := range files {
		if noisyPath(file.Path) {
			continue
		}
		entry := agg[file.PullRequestNumber]
		if entry == nil {
			entry = &pathAggregate{shared: map[string]struct{}{}}
			agg[file.PullRequestNumber] = entry
		}
		updatePathAggregate(entry, file, paths)
	}
	results := make([]gitindex.SearchMatch, 0, len(agg))
	for prNumber, entry := range agg {
		results = append(results, newPathSearchMatch(prNumber, entry, snapshotByPR[prNumber]))
	}
	return results
}

func updatePathAggregate(entry *pathAggregate, file database.PullRequestChangeFile, paths []string) {
	for _, path := range paths {
		if file.Path == path || file.PreviousPath == path {
			entry.shared[path] = struct{}{}
		}
	}
	entry.score += 10
	if file.PreviousPath != "" && containsPath(paths, file.PreviousPath) {
		entry.score += 6
	}
}

func newPathSearchMatch(prNumber int, entry *pathAggregate, snapshot database.PullRequestChangeSnapshot) gitindex.SearchMatch {
	shared := make([]string, 0, len(entry.shared))
	for path := range entry.shared {
		shared = append(shared, path)
	}
	sort.Strings(shared)
	reasons := []string{}
	if len(shared) > 0 {
		reasons = append(reasons, "shared_paths")
	}
	return gitindex.SearchMatch{
		PullRequestNumber: prNumber,
		State:             snapshot.State,
		Draft:             snapshot.Draft,
		HeadSHA:           snapshot.HeadSHA,
		BaseRef:           snapshot.BaseRef,
		IndexedAs:         snapshot.IndexedAs,
		IndexFreshness:    snapshot.IndexFreshness,
		Score:             entry.score + float64(len(shared))*2,
		SharedPaths:       shared,
		Reasons:           reasons,
	}
}

func buildRangeSearchMatches(hunks []database.PullRequestChangeHunk, ranges []searchRange, snapshotByPR map[int]database.PullRequestChangeSnapshot) []gitindex.SearchMatch {
	byPR := map[int]*gitindex.SearchMatch{}
	for _, hunk := range hunks {
		for _, r := range ranges {
			if !rangeSearchMatch(hunk, r) {
				continue
			}
			match := ensureRangeSearchMatch(byPR, hunk.PullRequestNumber, snapshotByPR[hunk.PullRequestNumber])
			match.OverlappingHunks++
			match.Score += 20
			match.OverlappingPaths = append(match.OverlappingPaths, hunk.Path)
			match.MatchedRanges = append(match.MatchedRanges, gitindex.MatchedPath{
				Path:     hunk.Path,
				OldStart: hunk.OldStart,
				OldEnd:   hunk.OldEnd,
				NewStart: hunk.NewStart,
				NewEnd:   hunk.NewEnd,
			})
		}
	}
	results := make([]gitindex.SearchMatch, 0, len(byPR))
	for _, match := range byPR {
		match.OverlappingPaths = normalizePaths(match.OverlappingPaths)
		match.Reasons = append(match.Reasons, "overlapping_ranges")
		results = append(results, *match)
	}
	return results
}

func rangeSearchMatch(hunk database.PullRequestChangeHunk, r searchRange) bool {
	if hunk.Path != r.Path {
		return false
	}
	return rangeOverlap(hunk.NewStart, hunk.NewEnd, r.Start, r.End) || rangeOverlap(hunk.OldStart, hunk.OldEnd, r.Start, r.End)
}

func ensureRangeSearchMatch(byPR map[int]*gitindex.SearchMatch, prNumber int, snapshot database.PullRequestChangeSnapshot) *gitindex.SearchMatch {
	match := byPR[prNumber]
	if match != nil {
		return match
	}
	match = &gitindex.SearchMatch{
		PullRequestNumber: prNumber,
		State:             snapshot.State,
		Draft:             snapshot.Draft,
		HeadSHA:           snapshot.HeadSHA,
		BaseRef:           snapshot.BaseRef,
		IndexedAs:         snapshot.IndexedAs,
		IndexFreshness:    snapshot.IndexFreshness,
	}
	byPR[prNumber] = match
	return match
}

func sortSearchMatches(matches []gitindex.SearchMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].PullRequestNumber > matches[j].PullRequestNumber
		}
		return matches[i].Score > matches[j].Score
	})
}

func trimSearchMatches(matches []gitindex.SearchMatch, limit int) []gitindex.SearchMatch {
	if len(matches) <= limit {
		return matches
	}
	return matches[:limit]
}

func applySnapshotStateFilter(query *gorm.DB, state string) *gorm.DB {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case "", "open":
		return query.Where("state = ?", "open")
	case "all":
		return query
	default:
		return query.Where("state = ?", state)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func hunksOverlap(a, b database.PullRequestChangeHunk) bool {
	return rangeOverlap(a.NewStart, a.NewEnd, b.NewStart, b.NewEnd) || rangeOverlap(a.OldStart, a.OldEnd, b.OldStart, b.OldEnd)
}

func rangeOverlap(aStart, aEnd, bStart, bEnd int) bool {
	if aStart == 0 && aEnd == 0 {
		return false
	}
	if bStart == 0 && bEnd == 0 {
		return false
	}
	if aEnd < aStart {
		aEnd = aStart
	}
	if bEnd < bStart {
		bEnd = bStart
	}
	return aStart <= bEnd && bStart <= aEnd
}

func minNonZero(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}
