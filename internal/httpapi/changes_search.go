package httpapi

import (
	"context"
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
	SHA                     string    `json:"sha"`
	TreeSHA                 string    `json:"tree_sha"`
	AuthorName              string    `json:"author_name"`
	AuthorEmail             string    `json:"author_email"`
	AuthoredAt              time.Time `json:"authored_at"`
	AuthoredTimezoneOffset  int       `json:"authored_timezone_offset"`
	CommitterName           string    `json:"committer_name"`
	CommitterEmail          string    `json:"committer_email"`
	CommittedAt             time.Time `json:"committed_at"`
	CommittedTimezoneOffset int       `json:"committed_timezone_offset"`
	Message                 string    `json:"message"`
	MessageEncoding         string    `json:"message_encoding"`
	Parents                 []string  `json:"parents"`
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
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	spec := strings.TrimSpace(c.Param("spec"))
	if unescaped, err := url.PathUnescape(spec); err == nil {
		spec = strings.TrimSpace(unescaped)
	} else {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid compare spec"})
	}
	parts := strings.SplitN(spec, "...", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid compare spec"})
	}
	baseInput := strings.TrimSpace(parts[0])
	headInput := strings.TrimSpace(parts[1])
	baseResolved, err := s.resolveGitRefOrSHA(c.Request().Context(), repo.ID, baseInput)
	if err != nil {
		return err
	}
	headResolved, err := s.resolveGitRefOrSHA(c.Request().Context(), repo.ID, headInput)
	if err != nil {
		return err
	}

	var snapshot database.PullRequestChangeSnapshot
	query := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ?", repo.ID).
		Where("head_sha = ? OR head_sha = ?", headInput, headResolved).
		Where(
			s.db.WithContext(c.Request().Context()).
				Where("base_sha = ? OR merge_base_sha = ?", baseInput, baseInput).
				Or("base_sha = ? OR merge_base_sha = ?", baseResolved, baseResolved).
				Or("base_ref = ?", normalizeCompareRef(baseInput)),
		).
		Order("updated_at DESC")
	if err := query.First(&snapshot).Error; err != nil {
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
	for _, parent := range parents {
		parentSHAs = append(parentSHAs, parent.ParentSHA)
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

	var files []database.GitCommitParentFile
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, sha).
		Order("parent_index ASC, path ASC").
		Find(&files).Error; err != nil {
		return err
	}
	if len(files) == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
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
	var sourceFiles []database.PullRequestChangeFile
	if err := s.db.WithContext(ctx).Where("snapshot_id = ?", source.ID).Find(&sourceFiles).Error; err != nil {
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
	paths = normalizePaths(paths)
	if len(paths) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	candidates, err := s.searchByPaths(ctx, repositoryID, paths, state, max(limit*5, 50))
	if err != nil {
		return nil, err
	}
	filtered := make([]gitindex.SearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PullRequestNumber == source.PullRequestNumber {
			continue
		}
		filtered = append(filtered, candidate)
	}
	candidates = filtered
	if mode == "path_overlap" || source.IndexedAs == "paths_only" || source.IndexedAs == "oversized" {
		if len(candidates) > limit {
			return candidates[:limit], nil
		}
		return candidates, nil
	}

	var sourceHunks []database.PullRequestChangeHunk
	if err := s.db.WithContext(ctx).Where("snapshot_id = ?", source.ID).Find(&sourceHunks).Error; err != nil {
		return nil, err
	}
	if len(sourceHunks) == 0 {
		return []gitindex.SearchMatch{}, nil
	}

	candidateNums := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		candidateNums = append(candidateNums, candidate.PullRequestNumber)
	}
	var candidateHunks []database.PullRequestChangeHunk
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, candidateNums).
		Where("path IN ?", paths).
		Find(&candidateHunks).Error; err != nil {
		return nil, err
	}

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
		match := matchByPR[prNumber]
		pathSet := map[string]struct{}{}
		for _, candidateHunk := range hunks {
			for _, sourceHunk := range sourceByPath[candidateHunk.Path] {
				if hunksOverlap(sourceHunk, candidateHunk) {
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
		}
		for path := range pathSet {
			match.OverlappingPaths = append(match.OverlappingPaths, path)
		}
		sort.Strings(match.OverlappingPaths)
		match.Score += float64(match.OverlappingHunks * 20)
		if match.OverlappingHunks > 0 {
			match.Reasons = append(match.Reasons, "overlapping_hunks")
		}
		matchByPR[prNumber] = match
	}
	final := make([]gitindex.SearchMatch, 0, len(matchByPR))
	for _, match := range matchByPR {
		if match.OverlappingHunks == 0 {
			continue
		}
		final = append(final, match)
	}
	sort.SliceStable(final, func(i, j int) bool {
		if final[i].Score == final[j].Score {
			return final[i].PullRequestNumber > final[j].PullRequestNumber
		}
		return final[i].Score > final[j].Score
	})
	if len(final) > limit {
		final = final[:limit]
	}
	return final, nil
}

func (s *Server) searchByPaths(ctx context.Context, repositoryID uint, paths []string, state string, limit int) ([]gitindex.SearchMatch, error) {
	if len(paths) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	paths = normalizePaths(paths)
	var snapshots []database.PullRequestChangeSnapshot
	query := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID)
	query = applySnapshotStateFilter(query, state)
	if err := query.Find(&snapshots).Error; err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	snapshotByPR := map[int]database.PullRequestChangeSnapshot{}
	prNumbers := make([]int, 0, len(snapshots))
	for _, snapshot := range snapshots {
		snapshotByPR[snapshot.PullRequestNumber] = snapshot
		prNumbers = append(prNumbers, snapshot.PullRequestNumber)
	}
	var files []database.PullRequestChangeFile
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, prNumbers).
		Where("path IN ? OR previous_path IN ?", paths, paths).
		Find(&files).Error; err != nil {
		return nil, err
	}
	type aggregate struct {
		shared map[string]struct{}
		score  float64
	}
	agg := map[int]*aggregate{}
	for _, file := range files {
		if noisyPath(file.Path) {
			continue
		}
		entry := agg[file.PullRequestNumber]
		if entry == nil {
			entry = &aggregate{shared: map[string]struct{}{}}
			agg[file.PullRequestNumber] = entry
		}
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
	results := make([]gitindex.SearchMatch, 0, len(agg))
	for prNumber, entry := range agg {
		snapshot := snapshotByPR[prNumber]
		shared := make([]string, 0, len(entry.shared))
		for path := range entry.shared {
			shared = append(shared, path)
		}
		sort.Strings(shared)
		reasons := []string{}
		if len(shared) > 0 {
			reasons = append(reasons, "shared_paths")
		}
		results = append(results, gitindex.SearchMatch{
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
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].PullRequestNumber > results[j].PullRequestNumber
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
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
	var snapshots []database.PullRequestChangeSnapshot
	query := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID)
	query = applySnapshotStateFilter(query, state)
	if err := query.Find(&snapshots).Error; err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return []gitindex.SearchMatch{}, nil
	}
	prNumbers := make([]int, 0, len(snapshots))
	snapshotByPR := map[int]database.PullRequestChangeSnapshot{}
	for _, snapshot := range snapshots {
		prNumbers = append(prNumbers, snapshot.PullRequestNumber)
		snapshotByPR[snapshot.PullRequestNumber] = snapshot
	}
	var hunks []database.PullRequestChangeHunk
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number IN ?", repositoryID, prNumbers).
		Where("path IN ?", paths).
		Find(&hunks).Error; err != nil {
		return nil, err
	}
	byPR := map[int]*gitindex.SearchMatch{}
	for _, hunk := range hunks {
		for _, r := range ranges {
			if hunk.Path != r.Path {
				continue
			}
			if rangeOverlap(hunk.NewStart, hunk.NewEnd, r.Start, r.End) || rangeOverlap(hunk.OldStart, hunk.OldEnd, r.Start, r.End) {
				match := byPR[hunk.PullRequestNumber]
				if match == nil {
					snapshot := snapshotByPR[hunk.PullRequestNumber]
					match = &gitindex.SearchMatch{
						PullRequestNumber: hunk.PullRequestNumber,
						State:             snapshot.State,
						Draft:             snapshot.Draft,
						HeadSHA:           snapshot.HeadSHA,
						BaseRef:           snapshot.BaseRef,
						IndexedAs:         snapshot.IndexedAs,
						IndexFreshness:    snapshot.IndexFreshness,
					}
					byPR[hunk.PullRequestNumber] = match
				}
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
	}
	results := make([]gitindex.SearchMatch, 0, len(byPR))
	for _, match := range byPR {
		match.OverlappingPaths = normalizePaths(match.OverlappingPaths)
		match.Reasons = append(match.Reasons, "overlapping_ranges")
		results = append(results, *match)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].PullRequestNumber > results[j].PullRequestNumber
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
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
