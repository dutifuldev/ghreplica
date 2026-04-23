package gitindex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/sourcegraph/go-diff/diff"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	indexedAsFull      = "full"
	indexedAsPathOnly  = "paths_only"
	indexedAsMixed     = "mixed"
	indexedAsOversized = "oversized"
	indexedAsFailed    = "failed"
	indexedAsSkipped   = "skipped"

	defaultIndexTimeout   = 5 * time.Minute
	defaultASTGrepTimeout = time.Minute

	freshnessCurrent          = "current"
	freshnessStaleHeadChanged = "stale_head_changed"
	freshnessStaleBaseMoved   = "stale_base_moved"
	freshnessRebuilding       = "rebuilding"
	freshnessFailed           = "failed"

	commitIndexReasonWithinBudget            = "within_budget"
	commitIndexReasonBudgetExceeded          = "budget_exceeded"
	commitIndexReasonOversizedMerge          = "oversized_merge_commit"
	commitDetailFullMaxFiles                 = 100
	commitDetailFullMaxChangedLines          = 20_000
	commitDetailFullMaxPatchBytes            = 1_000_000
	commitDetailPathOnlyMaxFiles             = 500
	commitDetailPathOnlyMaxChangedLines      = 100_000
	mergeCommitDetailFullMaxFiles            = 25
	mergeCommitDetailFullMaxChangedLines     = 5_000
	mergeCommitDetailFullMaxPatchBytes       = 250_000
	mergeCommitDetailPathOnlyMaxFiles        = 150
	mergeCommitDetailPathOnlyMaxChangedLines = 50_000

	pullRequestChangeFileBatchSize = 100
	pullRequestChangeHunkBatchSize = 200
	commitParentFileBatchSize      = 100
	commitParentHunkBatchSize      = 200
)

type Service struct {
	db             *gorm.DB
	github         *gh.Client
	mirrorRoot     string
	gitBin         string
	authHeader     string
	indexTimeout   time.Duration
	astGrepBin     string
	astGrepTimeout time.Duration
}

func NewService(db *gorm.DB, githubClient *gh.Client, mirrorRoot string) *Service {
	if strings.TrimSpace(mirrorRoot) == "" {
		mirrorRoot = ".data/git-mirrors"
	}
	return &Service{
		db:             db,
		github:         githubClient,
		mirrorRoot:     mirrorRoot,
		gitBin:         "git",
		indexTimeout:   defaultIndexTimeout,
		astGrepBin:     "ast-grep",
		astGrepTimeout: defaultASTGrepTimeout,
	}
}

func (s *Service) WithIndexTimeout(timeout time.Duration) *Service {
	if timeout > 0 {
		s.indexTimeout = timeout
	}
	return s
}

func (s *Service) WithASTGrepTimeout(timeout time.Duration) *Service {
	if timeout > 0 {
		s.astGrepTimeout = timeout
	}
	return s
}

func (s *Service) WithASTGrepBinary(bin string) *Service {
	if strings.TrimSpace(bin) != "" {
		s.astGrepBin = strings.TrimSpace(bin)
	}
	return s
}

func (s *Service) IndexPullRequest(ctx context.Context, owner, repo string, repository database.Repository, pull database.PullRequest) error {
	if repository.ID == 0 {
		return errors.New("repository is required")
	}
	if pull.IssueID == 0 {
		return errors.New("pull request is required")
	}
	ctx, cancel := s.withIndexTimeout(ctx)
	defer cancel()
	return s.withRepoLock(ctx, owner, repo, func() error {
		mirrorPath, mergeBase, err := s.preparePullRequestIndex(ctx, owner, repo, repository, pull)
		if err != nil {
			return s.markSnapshotFailed(ctx, repository.ID, pull, "", err)
		}
		indexResult, err := s.buildPullRequestIndexResult(ctx, mirrorPath, repository.ID, pull, mergeBase)
		if err != nil {
			return s.markSnapshotFailed(ctx, repository.ID, pull, mergeBase, err)
		}
		return s.writePullRequestIndex(ctx, repository.ID, pull.Number, indexResult)
	})
}

func (s *Service) withIndexTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if timeout := s.indexTimeout; timeout > 0 {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
			return context.WithTimeout(ctx, timeout)
		}
	}
	return ctx, func() {}
}

func (s *Service) preparePullRequestIndex(ctx context.Context, owner, repo string, repository database.Repository, pull database.PullRequest) (string, string, error) {
	if err := s.refreshAuthHeader(ctx); err != nil {
		return "", "", err
	}
	mirrorPath, err := s.ensureMirror(ctx, owner, repo, repositoryGitURL(repository.HTMLURL))
	if err != nil {
		return "", "", err
	}
	if err := s.syncRefs(ctx, repository.ID, mirrorPath, pull.BaseRef, pull.Number); err != nil {
		return "", "", err
	}
	mergeBase, err := s.mergeBase(ctx, mirrorPath, pull.BaseSHA, pull.HeadSHA)
	if err != nil {
		return "", "", err
	}
	return mirrorPath, mergeBase, nil
}

type pullRequestIndexResult struct {
	files       []database.PullRequestChangeFile
	hunks       []database.PullRequestChangeHunk
	snapshot    database.PullRequestChangeSnapshot
	commitRows  []commitBundle
}

func (s *Service) buildPullRequestIndexResult(ctx context.Context, mirrorPath string, repositoryID uint, pull database.PullRequest, mergeBase string) (pullRequestIndexResult, error) {
	files, hunks, snapshot, commitRows, err := s.buildPullRequestIndex(ctx, mirrorPath, repositoryID, pull, mergeBase)
	if err != nil {
		return pullRequestIndexResult{}, err
	}
	return pullRequestIndexResult{
		files:      files,
		hunks:      hunks,
		snapshot:   snapshot,
		commitRows: commitRows,
	}, nil
}

func (s *Service) writePullRequestIndex(ctx context.Context, repositoryID uint, pullNumber int, result pullRequestIndexResult) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		storedID, err := replaceStoredPullRequestSnapshot(tx, repositoryID, pullNumber, result.snapshot)
		if err != nil {
			return err
		}
		assignSnapshotIDs(result.files, result.hunks, storedID)
		if err := insertPullRequestChangeRows(tx, result.files, result.hunks); err != nil {
			return err
		}
		for _, commit := range result.commitRows {
			if err := upsertCommitBundle(tx, commit); err != nil {
				return err
			}
		}
		return nil
	})
}

func replaceStoredPullRequestSnapshot(tx *gorm.DB, repositoryID uint, pullNumber int, snapshot database.PullRequestChangeSnapshot) (uint, error) {
	if err := upsertSnapshot(tx, snapshot); err != nil {
		return 0, err
	}
	var stored database.PullRequestChangeSnapshot
	if err := tx.Where("repository_id = ? AND pull_request_number = ?", repositoryID, pullNumber).First(&stored).Error; err != nil {
		return 0, err
	}
	if err := tx.Where("snapshot_id = ?", stored.ID).Delete(&database.PullRequestChangeHunk{}).Error; err != nil {
		return 0, err
	}
	if err := tx.Where("snapshot_id = ?", stored.ID).Delete(&database.PullRequestChangeFile{}).Error; err != nil {
		return 0, err
	}
	return stored.ID, nil
}

func assignSnapshotIDs(files []database.PullRequestChangeFile, hunks []database.PullRequestChangeHunk, snapshotID uint) {
	for i := range files {
		files[i].SnapshotID = snapshotID
	}
	for i := range hunks {
		hunks[i].SnapshotID = snapshotID
	}
}

func (s *Service) MarkBaseRefStale(ctx context.Context, repositoryID uint, baseRef string) error {
	baseRef = normalizeBaseRef(baseRef)
	if repositoryID == 0 || baseRef == "" {
		return nil
	}
	return s.db.WithContext(ctx).Model(&database.PullRequestChangeSnapshot{}).
		Where("repository_id = ? AND base_ref = ?", repositoryID, baseRef).
		Updates(map[string]any{
			"index_freshness": freshnessStaleBaseMoved,
			"updated_at":      time.Now().UTC(),
		}).Error
}

func (s *Service) refreshAuthHeader(ctx context.Context) error {
	if s.github == nil {
		s.authHeader = ""
		return nil
	}
	token, err := s.github.AuthorizationToken(ctx)
	if err != nil {
		return err
	}
	s.authHeader = basicAuthHeader(token)
	return nil
}

func (s *Service) syncRefs(ctx context.Context, repositoryID uint, mirrorPath, baseRef string, pullNumber int) error {
	baseRef = normalizeBaseRef(baseRef)
	args := []string{"fetch", "--prune", "--no-tags", "origin"}
	refPatterns := make([]string, 0, 2)
	if baseRef != "" {
		args = append(args, fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", baseRef, baseRef))
		refPatterns = append(refPatterns, "refs/remotes/origin/"+baseRef)
	}
	if pullNumber > 0 {
		args = append(args, fmt.Sprintf("+refs/pull/%d/head:refs/pull/%d/head", pullNumber, pullNumber))
		refPatterns = append(refPatterns, fmt.Sprintf("refs/pull/%d", pullNumber))
	}
	if len(refPatterns) == 0 {
		return nil
	}
	if _, err := s.runGit(ctx, mirrorPath, args...); err != nil {
		return err
	}

	forEachArgs := append([]string{"for-each-ref", "--format=%(refname)%00%(objectname)%00%(objecttype)%00%(symref)%00%(*objectname)"}, refPatterns...)
	out, err := s.runGit(ctx, mirrorPath, forEachArgs...)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, tokens := range parseForEachRefRecords(out) {
		ref := database.GitRef{
			RepositoryID:    repositoryID,
			RefName:         strings.TrimSpace(tokens[0]),
			TargetOID:       strings.TrimSpace(tokens[1]),
			TargetType:      strings.TrimSpace(tokens[2]),
			SymbolicTarget:  strings.TrimSpace(tokens[3]),
			PeeledCommitSHA: strings.TrimSpace(tokens[4]),
			IsSymbolic:      strings.TrimSpace(tokens[3]) != "",
			UpdatedAt:       now,
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "repository_id"}, {Name: "ref_name"}},
			DoUpdates: clause.AssignmentColumns([]string{"target_oid", "target_type", "peeled_commit_sha", "is_symbolic", "symbolic_target", "updated_at"}),
		}).Create(&ref).Error; err != nil {
			return err
		}
	}
	return nil
}

type parsedFile struct {
	Path         string
	PreviousPath string
	Status       string
	FileKind     string
	IndexedAs    string
	OldMode      string
	NewMode      string
	HeadBlobSHA  string
	BaseBlobSHA  string
	Additions    int
	Deletions    int
	Changes      int
	PatchText    string
	Hunks        []Hunk
}

type commitBundle struct {
	Commit  database.GitCommit
	Parents []commitParentBundle
}

type commitParentBundle struct {
	Parent database.GitCommitParent
	Files  []database.GitCommitParentFile
	Hunks  []database.GitCommitParentHunk
}

func (s *Service) buildPullRequestIndex(ctx context.Context, mirrorPath string, repositoryID uint, pull database.PullRequest, mergeBase string) ([]database.PullRequestChangeFile, []database.PullRequestChangeHunk, database.PullRequestChangeSnapshot, []commitBundle, error) {
	files, indexedAs, err := s.loadPullRequestIndexFiles(ctx, mirrorPath, pull, mergeBase)
	if err != nil {
		return nil, nil, database.PullRequestChangeSnapshot{}, nil, err
	}
	fileRows, hunkRows, snapshot := buildPullRequestSnapshotRows(repositoryID, pull, mergeBase, indexedAs, files)
	commitBundles, err := s.buildCommitBundles(ctx, mirrorPath, repositoryID, mergeBase, pull.HeadSHA)
	if err != nil {
		return nil, nil, database.PullRequestChangeSnapshot{}, nil, err
	}
	return fileRows, hunkRows, snapshot, commitBundles, nil
}

func (s *Service) loadPullRequestIndexFiles(ctx context.Context, mirrorPath string, pull database.PullRequest, mergeBase string) (map[string]*parsedFile, string, error) {
	rawOut, err := s.runGit(ctx, mirrorPath, "diff", "--raw", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", mergeBase+"..."+pull.HeadSHA)
	if err != nil {
		return nil, "", err
	}
	numstatOut, err := s.runGit(ctx, mirrorPath, "diff", "--numstat", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", mergeBase+"..."+pull.HeadSHA)
	if err != nil {
		return nil, "", err
	}
	files := mergeFileMetadata(parseRawZ(rawOut), parseNumstatZ(numstatOut))
	indexedAs := pullRequestIndexedAs(files)
	if indexedAs != indexedAsFull {
		setPullRequestFilesIndexMode(files, indexedAsPathOnly)
		return files, indexedAs, nil
	}
	patchOut, err := s.runGit(ctx, mirrorPath, "diff", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", "--unified=0", mergeBase+"..."+pull.HeadSHA)
	if err != nil {
		return nil, "", err
	}
	if err := applyPatchDetails(files, patchOut); err != nil {
		return nil, "", err
	}
	if pullRequestFilesMixed(files) {
		indexedAs = indexedAsMixed
	}
	return files, indexedAs, nil
}

func pullRequestIndexedAs(files map[string]*parsedFile) string {
	totalLines := 0
	for _, file := range files {
		totalLines += file.Additions + file.Deletions
	}
	if len(files) > 5000 || totalLines > 200000 {
		return indexedAsOversized
	}
	return indexedAsFull
}

func setPullRequestFilesIndexMode(files map[string]*parsedFile, indexedAs string) {
	for _, file := range files {
		file.IndexedAs = indexedAs
	}
}

func pullRequestFilesMixed(files map[string]*parsedFile) bool {
	for _, file := range files {
		if file.IndexedAs != "" && file.IndexedAs != indexedAsFull {
			return true
		}
	}
	return false
}

func buildPullRequestSnapshotRows(repositoryID uint, pull database.PullRequest, mergeBase, indexedAs string, files map[string]*parsedFile) ([]database.PullRequestChangeFile, []database.PullRequestChangeHunk, database.PullRequestChangeSnapshot) {
	now := time.Now().UTC()
	snapshot := database.PullRequestChangeSnapshot{
		RepositoryID:      repositoryID,
		PullRequestID:     pull.IssueID,
		PullRequestNumber: pull.Number,
		HeadSHA:           pull.HeadSHA,
		BaseSHA:           pull.BaseSHA,
		MergeBaseSHA:      mergeBase,
		BaseRef:           normalizeBaseRef(pull.BaseRef),
		State:             pull.State,
		Draft:             pull.Draft,
		IndexedAs:         indexedAs,
		IndexFreshness:    freshnessCurrent,
		PathCount:         len(files),
		LastIndexedAt:     &now,
	}
	fileRows, hunkRows, indexedFiles, hunkCount, totalAdditions, totalDeletions, totalPatchBytes := pullRequestRows(files, repositoryID, pull, mergeBase)
	snapshot.IndexedFileCount = indexedFiles
	snapshot.HunkCount = hunkCount
	snapshot.Additions = totalAdditions
	snapshot.Deletions = totalDeletions
	snapshot.PatchBytes = totalPatchBytes
	if indexedAs == indexedAsFull && hunkCount == 0 && len(files) > 0 {
		snapshot.IndexedAs = indexedAsPathOnly
	}
	return fileRows, hunkRows, snapshot
}

func pullRequestRows(files map[string]*parsedFile, repositoryID uint, pull database.PullRequest, mergeBase string) ([]database.PullRequestChangeFile, []database.PullRequestChangeHunk, int, int, int, int, int) {
	fileRows := make([]database.PullRequestChangeFile, 0, len(files))
	hunkRows := make([]database.PullRequestChangeHunk, 0)
	var indexedFiles int
	var hunkCount int
	var totalAdditions int
	var totalDeletions int
	var totalPatchBytes int
	for _, file := range orderedFiles(files) {
		row, hunkSet, fullIndexed := pullRequestFileRows(repositoryID, pull, mergeBase, file)
		if file.IndexedAs == "" {
			file.IndexedAs = indexedAsFull
		}
		if fullIndexed {
			indexedFiles++
		}
		hunkCount += len(hunkSet)
		totalAdditions += file.Additions
		totalDeletions += file.Deletions
		totalPatchBytes += len(file.PatchText)
		fileRows = append(fileRows, row)
		hunkRows = append(hunkRows, hunkSet...)
	}
	return fileRows, hunkRows, indexedFiles, hunkCount, totalAdditions, totalDeletions, totalPatchBytes
}

func pullRequestFileRows(repositoryID uint, pull database.PullRequest, mergeBase string, file *parsedFile) (database.PullRequestChangeFile, []database.PullRequestChangeHunk, bool) {
	if file.IndexedAs == "" {
		file.IndexedAs = indexedAsFull
	}
	row := database.PullRequestChangeFile{
		RepositoryID:      repositoryID,
		PullRequestNumber: pull.Number,
		HeadSHA:           pull.HeadSHA,
		BaseSHA:           pull.BaseSHA,
		MergeBaseSHA:      mergeBase,
		Path:              file.Path,
		PreviousPath:      file.PreviousPath,
		Status:            file.Status,
		FileKind:          file.FileKind,
		IndexedAs:         file.IndexedAs,
		OldMode:           file.OldMode,
		NewMode:           file.NewMode,
		HeadBlobSHA:       file.HeadBlobSHA,
		BaseBlobSHA:       file.BaseBlobSHA,
		Additions:         file.Additions,
		Deletions:         file.Deletions,
		Changes:           file.Changes,
		PatchText:         file.PatchText,
	}
	if file.IndexedAs != indexedAsFull {
		return row, nil, false
	}
	return row, pullRequestHunkRows(repositoryID, pull, mergeBase, file), true
}

func pullRequestHunkRows(repositoryID uint, pull database.PullRequest, mergeBase string, file *parsedFile) []database.PullRequestChangeHunk {
	hunkRows := make([]database.PullRequestChangeHunk, 0, len(file.Hunks))
	for _, hunk := range file.Hunks {
		hunkRows = append(hunkRows, database.PullRequestChangeHunk{
			RepositoryID:      repositoryID,
			PullRequestNumber: pull.Number,
			HeadSHA:           pull.HeadSHA,
			BaseSHA:           pull.BaseSHA,
			MergeBaseSHA:      mergeBase,
			Path:              file.Path,
			HunkIndex:         hunk.Index,
			DiffHunk:          hunk.DiffHunk,
			OldStart:          hunk.OldStart,
			OldCount:          hunk.OldCount,
			OldEnd:            hunk.OldEnd,
			NewStart:          hunk.NewStart,
			NewCount:          hunk.NewCount,
			NewEnd:            hunk.NewEnd,
		})
	}
	return hunkRows
}

func (s *Service) buildCommitBundles(ctx context.Context, mirrorPath string, repositoryID uint, mergeBase, headSHA string) ([]commitBundle, error) {
	revListOut, err := s.runGit(ctx, mirrorPath, "rev-list", "--reverse", mergeBase+".."+headSHA)
	if err != nil {
		return nil, err
	}
	commitSHAs := splitLines(revListOut)
	bundles := make([]commitBundle, 0, len(commitSHAs))
	for _, sha := range commitSHAs {
		meta, parents, err := s.readCommitMetadata(ctx, mirrorPath, repositoryID, sha)
		if err != nil {
			return nil, err
		}
		bundle := commitBundle{Commit: meta}
		isMergeCommit := len(parents) > 1
		for _, parent := range parents {
			parentBundle, err := s.readCommitDiff(ctx, mirrorPath, repositoryID, sha, parent, isMergeCommit)
			if err != nil {
				return nil, err
			}
			bundle.Parents = append(bundle.Parents, parentBundle)
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

func (s *Service) readCommitMetadata(ctx context.Context, mirrorPath string, repositoryID uint, sha string) (database.GitCommit, []database.GitCommitParent, error) {
	metaOut, err := s.runGit(ctx, mirrorPath, "show", "-s", "--format=%H%x00%T%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%e%x00%B", sha)
	if err != nil {
		return database.GitCommit{}, nil, err
	}
	metaTokens := strings.SplitN(string(metaOut), "\x00", 10)
	if len(metaTokens) < 10 {
		return database.GitCommit{}, nil, fmt.Errorf("unexpected commit metadata format for %s", sha)
	}
	authoredAt, authoredOffset, err := parseGitTimestamp(metaTokens[4])
	if err != nil {
		return database.GitCommit{}, nil, err
	}
	committedAt, committedOffset, err := parseGitTimestamp(metaTokens[7])
	if err != nil {
		return database.GitCommit{}, nil, err
	}
	commit := database.GitCommit{
		RepositoryID:            repositoryID,
		SHA:                     metaTokens[0],
		TreeSHA:                 metaTokens[1],
		AuthorName:              metaTokens[2],
		AuthorEmail:             metaTokens[3],
		AuthoredAt:              authoredAt,
		AuthoredTimezoneOffset:  authoredOffset,
		CommitterName:           metaTokens[5],
		CommitterEmail:          metaTokens[6],
		CommittedAt:             committedAt,
		CommittedTimezoneOffset: committedOffset,
		MessageEncoding:         metaTokens[8],
		Message:                 metaTokens[9],
	}

	parentOut, err := s.runGit(ctx, mirrorPath, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return database.GitCommit{}, nil, err
	}
	parentParts := strings.Fields(strings.TrimSpace(string(parentOut)))
	parents := make([]database.GitCommitParent, 0, max(0, len(parentParts)-1))
	for i, parentSHA := range parentParts[1:] {
		parents = append(parents, database.GitCommitParent{
			RepositoryID: repositoryID,
			CommitSHA:    sha,
			ParentSHA:    parentSHA,
			ParentIndex:  i,
		})
	}
	return commit, parents, nil
}

func (s *Service) readCommitDiff(ctx context.Context, mirrorPath string, repositoryID uint, commitSHA string, parent database.GitCommitParent, isMergeCommit bool) (commitParentBundle, error) {
	files, err := s.loadCommitFiles(ctx, mirrorPath, parent.ParentSHA, commitSHA)
	if err != nil {
		return commitParentBundle{}, err
	}
	indexedAs, indexReason, totalAdditions, totalDeletions := summarizeCommitDetail(files, isMergeCommit)
	now := time.Now().UTC()

	parent.RepositoryID = repositoryID
	parent.CommitSHA = commitSHA
	parent.IndexedAs = indexedAs
	parent.IndexReason = indexReason
	parent.PathCount = len(files)
	parent.Additions = totalAdditions
	parent.Deletions = totalDeletions
	parent.LastIndexedAt = &now

	if indexedAs == indexedAsSkipped {
		return commitParentBundle{Parent: parent}, nil
	}

	if indexedAs == indexedAsFull {
		indexedAs, indexReason, patchBytes, err := s.applyCommitPatchDetails(ctx, mirrorPath, parent.ParentSHA, commitSHA, files, indexedAs, indexReason, isMergeCommit)
		if err != nil {
			return commitParentBundle{}, err
		}
		parent.IndexedAs = indexedAs
		parent.IndexReason = indexReason
		parent.PatchBytes = patchBytes
	}

	fileRows, hunkRows, indexedFileCount, hunkCount, patchBytes := buildCommitParentRows(repositoryID, commitSHA, parent, files)
	parent.HunkCount = hunkCount
	parent.PatchBytes = patchBytes
	if indexedAs == indexedAsPathOnly {
		parent.PatchBytes = 0
	}
	_ = indexedFileCount

	return commitParentBundle{
		Parent: parent,
		Files:  fileRows,
		Hunks:  hunkRows,
	}, nil
}

func (s *Service) loadCommitFiles(ctx context.Context, mirrorPath, parentSHA, commitSHA string) (map[string]*parsedFile, error) {
	rawOut, err := s.runGit(ctx, mirrorPath, "diff", "--raw", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", parentSHA, commitSHA)
	if err != nil {
		return nil, err
	}
	numstatOut, err := s.runGit(ctx, mirrorPath, "diff", "--numstat", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", parentSHA, commitSHA)
	if err != nil {
		return nil, err
	}
	return mergeFileMetadata(parseRawZ(rawOut), parseNumstatZ(numstatOut)), nil
}

func summarizeCommitDetail(files map[string]*parsedFile, isMergeCommit bool) (string, string, int, int) {
	totalAdditions, totalDeletions := summarizeFileCounts(files)
	totalLines := totalAdditions + totalDeletions
	indexedAs, indexReason := decideCommitDetailLevel(len(files), totalLines, isMergeCommit)
	return indexedAs, indexReason, totalAdditions, totalDeletions
}

func (s *Service) applyCommitPatchDetails(ctx context.Context, mirrorPath, parentSHA, commitSHA string, files map[string]*parsedFile, indexedAs, indexReason string, isMergeCommit bool) (string, string, int, error) {
	patchOut, err := s.runGit(ctx, mirrorPath, "diff", "-z", "--no-ext-diff", "--no-textconv", "--find-renames=50%", "-l1000", "--unified=0", parentSHA, commitSHA)
	if err != nil {
		return indexedAs, indexReason, 0, err
	}
	if err := applyPatchDetails(files, patchOut); err != nil {
		return indexedAs, indexReason, 0, err
	}
	patchBytes := sumPatchBytes(files)
	if !commitPatchBytesExceedBudget(patchBytes, isMergeCommit) && !commitFilesContainReducedDetail(files) {
		return indexedAs, indexReason, patchBytes, nil
	}
	indexReason = commitIndexReasonBudgetExceeded
	if isMergeCommit {
		indexReason = commitIndexReasonOversizedMerge
	}
	downgradeCommitFilesToPathsOnly(files)
	return indexedAsPathOnly, indexReason, 0, nil
}

func decideCommitDetailLevel(pathCount, totalLines int, isMergeCommit bool) (string, string) {
	fullMaxFiles := commitDetailFullMaxFiles
	fullMaxLines := commitDetailFullMaxChangedLines
	pathOnlyMaxFiles := commitDetailPathOnlyMaxFiles
	pathOnlyMaxLines := commitDetailPathOnlyMaxChangedLines
	reason := commitIndexReasonBudgetExceeded
	if isMergeCommit {
		fullMaxFiles = mergeCommitDetailFullMaxFiles
		fullMaxLines = mergeCommitDetailFullMaxChangedLines
		pathOnlyMaxFiles = mergeCommitDetailPathOnlyMaxFiles
		pathOnlyMaxLines = mergeCommitDetailPathOnlyMaxChangedLines
		reason = commitIndexReasonOversizedMerge
	}
	if pathCount <= fullMaxFiles && totalLines <= fullMaxLines {
		return indexedAsFull, commitIndexReasonWithinBudget
	}
	if pathCount <= pathOnlyMaxFiles && totalLines <= pathOnlyMaxLines {
		return indexedAsPathOnly, reason
	}
	return indexedAsSkipped, reason
}

func commitPatchBytesExceedBudget(patchBytes int, isMergeCommit bool) bool {
	if isMergeCommit {
		return patchBytes > mergeCommitDetailFullMaxPatchBytes
	}
	return patchBytes > commitDetailFullMaxPatchBytes
}

func summarizeFileCounts(files map[string]*parsedFile) (int, int) {
	totalAdditions := 0
	totalDeletions := 0
	for _, file := range files {
		totalAdditions += file.Additions
		totalDeletions += file.Deletions
	}
	return totalAdditions, totalDeletions
}

func sumPatchBytes(files map[string]*parsedFile) int {
	total := 0
	for _, file := range files {
		total += len(file.PatchText)
	}
	return total
}

func commitFilesContainReducedDetail(files map[string]*parsedFile) bool {
	for _, file := range files {
		if file.IndexedAs != "" && file.IndexedAs != indexedAsFull {
			return true
		}
	}
	return false
}

func downgradeCommitFilesToPathsOnly(files map[string]*parsedFile) {
	for _, file := range files {
		file.IndexedAs = indexedAsPathOnly
		file.PatchText = ""
		file.Hunks = nil
	}
}

func buildCommitParentRows(repositoryID uint, commitSHA string, parent database.GitCommitParent, files map[string]*parsedFile) ([]database.GitCommitParentFile, []database.GitCommitParentHunk, int, int, int) {
	fileRows := make([]database.GitCommitParentFile, 0, len(files))
	hunkRows := make([]database.GitCommitParentHunk, 0)
	indexedFileCount := 0
	hunkCount := 0
	patchBytes := 0
	for _, file := range orderedFiles(files) {
		if file.IndexedAs == "" {
			switch parent.IndexedAs {
			case indexedAsPathOnly, indexedAsSkipped:
				file.IndexedAs = parent.IndexedAs
			default:
				file.IndexedAs = indexedAsFull
			}
		}
		if file.IndexedAs == indexedAsFull {
			indexedFileCount++
		}
		patchBytes += len(file.PatchText)
		fileRows = append(fileRows, database.GitCommitParentFile{
			RepositoryID:    repositoryID,
			CommitSHA:       commitSHA,
			ParentSHA:       parent.ParentSHA,
			ParentIndex:     parent.ParentIndex,
			Path:            file.Path,
			PreviousPath:    file.PreviousPath,
			Status:          file.Status,
			FileKind:        file.FileKind,
			IndexedAs:       file.IndexedAs,
			OldMode:         file.OldMode,
			NewMode:         file.NewMode,
			BlobSHA:         file.HeadBlobSHA,
			PreviousBlobSHA: file.BaseBlobSHA,
			Additions:       file.Additions,
			Deletions:       file.Deletions,
			Changes:         file.Changes,
			PatchText:       file.PatchText,
		})
		if file.IndexedAs != indexedAsFull {
			continue
		}
		for _, hunk := range file.Hunks {
			hunkCount++
			hunkRows = append(hunkRows, database.GitCommitParentHunk{
				RepositoryID: repositoryID,
				CommitSHA:    commitSHA,
				ParentSHA:    parent.ParentSHA,
				ParentIndex:  parent.ParentIndex,
				Path:         file.Path,
				HunkIndex:    hunk.Index,
				DiffHunk:     hunk.DiffHunk,
				OldStart:     hunk.OldStart,
				OldCount:     hunk.OldCount,
				OldEnd:       hunk.OldEnd,
				NewStart:     hunk.NewStart,
				NewCount:     hunk.NewCount,
				NewEnd:       hunk.NewEnd,
			})
		}
	}
	return fileRows, hunkRows, indexedFileCount, hunkCount, patchBytes
}

func mergeFileMetadata(rawRecords []rawRecord, numstatRecords []numstatRecord) map[string]*parsedFile {
	files := map[string]*parsedFile{}
	for _, raw := range rawRecords {
		path := raw.Path
		file := &parsedFile{
			Path:         path,
			PreviousPath: raw.PreviousPath,
			Status:       normalizeStatus(raw.Status),
			FileKind:     classifyFileKind(raw, false, 0, 0),
			OldMode:      raw.OldMode,
			NewMode:      raw.NewMode,
			HeadBlobSHA:  raw.NewOID,
			BaseBlobSHA:  raw.OldOID,
		}
		files[path] = file
	}
	for _, stat := range numstatRecords {
		path := stat.Path
		file, ok := files[path]
		if !ok {
			file = &parsedFile{Path: path, Status: "modified"}
			files[path] = file
		}
		if file.PreviousPath == "" {
			file.PreviousPath = stat.PreviousPath
		}
		file.Additions = stat.Additions
		file.Deletions = stat.Deletions
		file.Changes = stat.Additions + stat.Deletions
		file.FileKind = classifyFileKind(rawRecord{
			Status:       file.Status,
			OldMode:      file.OldMode,
			NewMode:      file.NewMode,
			PreviousPath: file.PreviousPath,
			Path:         file.Path,
		}, stat.Binary, stat.Additions, stat.Deletions)
	}
	return files
}

func applyPatchDetails(files map[string]*parsedFile, patchOut []byte) error {
	if len(patchOut) == 0 {
		return nil
	}
	fileDiffs, err := diff.ParseMultiFileDiff(patchOut)
	if err != nil {
		return err
	}
	for _, fileDiff := range fileDiffs {
		path := normalizePatchPath(fileDiff.NewName)
		if path == "" || path == "/dev/null" {
			path = normalizePatchPath(fileDiff.OrigName)
		}
		file, ok := files[path]
		if !ok {
			file = &parsedFile{Path: path, Status: "modified"}
			files[path] = file
		}
		if err := applyFileDiffDetails(file, fileDiff); err != nil {
			return err
		}
	}
	return nil
}

func applyFileDiffDetails(file *parsedFile, fileDiff *diff.FileDiff) error {
	printed, err := diff.PrintFileDiff(fileDiff)
	if err != nil {
		return err
	}
	file.PatchText = string(printed)
	if file.FileKind == "" {
		file.FileKind = "text"
	}
	if len(printed) > 1_000_000 || file.Changes > 20_000 {
		file.IndexedAs = indexedAsPathOnly
		return nil
	}
	file.Hunks = parseFileHunks(fileDiff)
	if file.IndexedAs == "" {
		file.IndexedAs = indexedAsFull
	}
	return nil
}

func parseFileHunks(fileDiff *diff.FileDiff) []Hunk {
	hunks := make([]Hunk, 0, len(fileDiff.Hunks))
	for i, h := range fileDiff.Hunks {
		oldStart := int(h.OrigStartLine)
		oldCount := int(h.OrigLines)
		newStart := int(h.NewStartLine)
		newCount := int(h.NewLines)
		hunks = append(hunks, Hunk{
			Index:    i,
			DiffHunk: string(h.Body),
			OldStart: oldStart,
			OldCount: oldCount,
			OldEnd:   rangeEnd(oldStart, oldCount),
			NewStart: newStart,
			NewCount: newCount,
			NewEnd:   rangeEnd(newStart, newCount),
		})
	}
	return hunks
}

func upsertSnapshot(tx *gorm.DB, snapshot database.PullRequestChangeSnapshot) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}, {Name: "pull_request_number"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"pull_request_id",
			"head_sha",
			"base_sha",
			"merge_base_sha",
			"base_ref",
			"state",
			"draft",
			"indexed_as",
			"index_freshness",
			"path_count",
			"indexed_file_count",
			"hunk_count",
			"additions",
			"deletions",
			"patch_bytes",
			"last_indexed_at",
			"updated_at",
		}),
	}).Create(&snapshot).Error
}

func insertPullRequestChangeRows(tx *gorm.DB, fileRows []database.PullRequestChangeFile, hunkRows []database.PullRequestChangeHunk) error {
	if len(fileRows) > 0 {
		if err := tx.CreateInBatches(&fileRows, pullRequestChangeFileBatchSize).Error; err != nil {
			return err
		}
	}
	if len(hunkRows) > 0 {
		if err := tx.CreateInBatches(&hunkRows, pullRequestChangeHunkBatchSize).Error; err != nil {
			return err
		}
	}
	return nil
}

func upsertCommitBundle(tx *gorm.DB, bundle commitBundle) error {
	rewrite, err := shouldRewriteCommitBundle(tx, bundle)
	if err != nil {
		return err
	}
	if !rewrite {
		return nil
	}

	if err := upsertCommitRecord(tx, bundle.Commit); err != nil {
		return err
	}
	if err := deleteCommitBundleRows(tx, bundle.Commit.RepositoryID, bundle.Commit.SHA); err != nil {
		return err
	}
	return insertCommitBundleRows(tx, bundle)
}

func upsertCommitRecord(tx *gorm.DB, commit database.GitCommit) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}, {Name: "sha"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"tree_sha",
			"author_name",
			"author_email",
			"authored_at",
			"authored_timezone_offset",
			"committer_name",
			"committer_email",
			"committed_at",
			"committed_timezone_offset",
			"message",
			"message_encoding",
			"updated_at",
		}),
	}).Create(&commit).Error
}

func deleteCommitBundleRows(tx *gorm.DB, repositoryID uint, commitSHA string) error {
	if err := tx.Where("repository_id = ? AND commit_sha = ?", repositoryID, commitSHA).
		Delete(&database.GitCommitParentHunk{}).Error; err != nil {
		return err
	}
	if err := tx.Where("repository_id = ? AND commit_sha = ?", repositoryID, commitSHA).
		Delete(&database.GitCommitParentFile{}).Error; err != nil {
		return err
	}
	return tx.Where("repository_id = ? AND commit_sha = ?", repositoryID, commitSHA).
		Delete(&database.GitCommitParent{}).Error
}

func insertCommitBundleRows(tx *gorm.DB, bundle commitBundle) error {
	parentRows := make([]database.GitCommitParent, 0, len(bundle.Parents))
	fileRows := make([]database.GitCommitParentFile, 0)
	hunkRows := make([]database.GitCommitParentHunk, 0)
	for _, parent := range bundle.Parents {
		parentRows = append(parentRows, parent.Parent)
		fileRows = append(fileRows, parent.Files...)
		hunkRows = append(hunkRows, parent.Hunks...)
	}
	if len(parentRows) > 0 {
		if err := tx.Create(&parentRows).Error; err != nil {
			return err
		}
	}
	if len(fileRows) > 0 {
		if err := tx.CreateInBatches(&fileRows, commitParentFileBatchSize).Error; err != nil {
			return err
		}
	}
	if len(hunkRows) > 0 {
		if err := tx.CreateInBatches(&hunkRows, commitParentHunkBatchSize).Error; err != nil {
			return err
		}
	}
	return nil
}

func shouldRewriteCommitBundle(tx *gorm.DB, bundle commitBundle) (bool, error) {
	var commitCount int64
	if err := tx.Model(&database.GitCommit{}).
		Where("repository_id = ? AND sha = ?", bundle.Commit.RepositoryID, bundle.Commit.SHA).
		Count(&commitCount).Error; err != nil {
		return false, err
	}
	if commitCount == 0 {
		return true, nil
	}
	if len(bundle.Parents) == 0 {
		return false, nil
	}

	var parentCount int64
	if err := tx.Model(&database.GitCommitParent{}).
		Where("repository_id = ? AND commit_sha = ?", bundle.Commit.RepositoryID, bundle.Commit.SHA).
		Count(&parentCount).Error; err != nil {
		return false, err
	}
	return parentCount != int64(len(bundle.Parents)), nil
}

func (s *Service) markSnapshotFailed(ctx context.Context, repositoryID uint, pull database.PullRequest, mergeBase string, reason error) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsertSnapshot(tx, database.PullRequestChangeSnapshot{
			RepositoryID:      repositoryID,
			PullRequestID:     pull.IssueID,
			PullRequestNumber: pull.Number,
			HeadSHA:           pull.HeadSHA,
			BaseSHA:           pull.BaseSHA,
			MergeBaseSHA:      mergeBase,
			BaseRef:           normalizeBaseRef(pull.BaseRef),
			State:             pull.State,
			Draft:             pull.Draft,
			IndexedAs:         indexedAsFailed,
			IndexFreshness:    freshnessFailed,
			LastIndexedAt:     &now,
		}); err != nil {
			return err
		}

		var stored database.PullRequestChangeSnapshot
		if err := tx.Where("repository_id = ? AND pull_request_number = ?", repositoryID, pull.Number).First(&stored).Error; err != nil {
			return err
		}
		if err := tx.Where("snapshot_id = ?", stored.ID).Delete(&database.PullRequestChangeHunk{}).Error; err != nil {
			return err
		}
		if err := tx.Where("snapshot_id = ?", stored.ID).Delete(&database.PullRequestChangeFile{}).Error; err != nil {
			return err
		}
		return nil
	})
}

func repositoryGitURL(htmlURL string) string {
	value := strings.TrimSpace(htmlURL)
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "file://") {
		return value
	}
	if strings.HasSuffix(value, ".git") {
		return value
	}
	return value + ".git"
}

func normalizeBaseRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return ref
}

func splitNULTokens(raw []byte) []string {
	parts := bytes.Split(raw, []byte{0})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		out = append(out, string(part))
	}
	return out
}

func parseForEachRefRecords(raw []byte) [][]string {
	lines := splitLines(raw)
	out := make([][]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\x00")
		if len(fields) < 5 {
			continue
		}
		out = append(out, fields[:5])
	}
	return out
}

func splitLines(raw []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	out := []string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

type rawRecord struct {
	OldMode      string
	NewMode      string
	OldOID       string
	NewOID       string
	Status       string
	Path         string
	PreviousPath string
}

func parseRawZ(raw []byte) []rawRecord {
	tokens := splitNULTokens(raw)
	out := make([]rawRecord, 0)
	for i := 0; i < len(tokens); i++ {
		header := tokens[i]
		if !strings.HasPrefix(header, ":") {
			continue
		}
		i++
		parts := strings.Fields(strings.TrimPrefix(header, ":"))
		if len(parts) < 5 || i >= len(tokens) {
			break
		}
		record := rawRecord{
			OldMode: parts[0],
			NewMode: parts[1],
			OldOID:  parts[2],
			NewOID:  parts[3],
			Status:  parts[4],
			Path:    tokens[i],
		}
		if isRenameOrCopy(record.Status) && i+1 < len(tokens) {
			record.PreviousPath = record.Path
			i++
			record.Path = tokens[i]
		}
		out = append(out, record)
	}
	return out
}

type numstatRecord struct {
	Path         string
	PreviousPath string
	Additions    int
	Deletions    int
	Binary       bool
}

func parseNumstatZ(raw []byte) []numstatRecord {
	tokens := splitNULTokens(raw)
	out := make([]numstatRecord, 0)
	for i := 0; i < len(tokens); i++ {
		record := tokens[i]
		fields := strings.Split(record, "\t")
		if len(fields) < 3 {
			continue
		}
		i++
		path := fields[2]
		previousPath := ""
		if path == "" {
			if i >= len(tokens) {
				break
			}
			previousPath = tokens[i]
			i++
			if i >= len(tokens) {
				break
			}
			path = tokens[i]
		} else {
			i--
		}
		additions, deletions, binary := parseNumstatCounts(fields[0], fields[1])
		out = append(out, numstatRecord{
			Path:         path,
			PreviousPath: previousPath,
			Additions:    additions,
			Deletions:    deletions,
			Binary:       binary,
		})
	}
	return out
}

func parseNumstatCounts(additions, deletions string) (int, int, bool) {
	if additions == "-" || deletions == "-" {
		return 0, 0, true
	}
	add, _ := strconv.Atoi(additions)
	del, _ := strconv.Atoi(deletions)
	return add, del, false
}

func classifyFileKind(raw rawRecord, binary bool, additions, deletions int) string {
	if isMode(raw, "160000") {
		return "submodule"
	}
	if isMode(raw, "120000") {
		return "symlink"
	}
	if binary {
		return "binary"
	}
	if isModeOnlyChange(raw, additions, deletions) {
		return "mode_only"
	}
	return "text"
}

func isMode(raw rawRecord, want string) bool {
	return raw.OldMode == want || raw.NewMode == want
}

func isModeOnlyChange(raw rawRecord, additions, deletions int) bool {
	if normalizeStatus(raw.Status) == "type_changed" {
		return true
	}
	return raw.OldMode != "" && raw.NewMode != "" && raw.OldMode != raw.NewMode && additions == 0 && deletions == 0
}

func normalizeStatus(status string) string {
	switch {
	case strings.HasPrefix(status, "R"):
		return "renamed"
	case strings.HasPrefix(status, "C"):
		return "copied"
	case strings.HasPrefix(status, "A"):
		return "added"
	case strings.HasPrefix(status, "D"):
		return "removed"
	case strings.HasPrefix(status, "T"):
		return "type_changed"
	default:
		return "modified"
	}
}

func isRenameOrCopy(status string) bool {
	return strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")
}

func normalizePatchPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}

func rangeEnd(start, count int) int {
	if count <= 0 {
		return start
	}
	return start + count - 1
}

func orderedFiles(files map[string]*parsedFile) []*parsedFile {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*parsedFile, 0, len(keys))
	for _, key := range keys {
		out = append(out, files[key])
	}
	return out
}

func parseGitTimestamp(raw string) (time.Time, int, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, 0, err
	}
	_, offset := t.Zone()
	return t.UTC(), offset / 60, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Service) mergeBase(ctx context.Context, mirrorPath, baseSHA, headSHA string) (string, error) {
	out, err := s.runGit(ctx, mirrorPath, "merge-base", baseSHA, headSHA)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
