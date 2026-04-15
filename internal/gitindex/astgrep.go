package gitindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

var (
	ErrInvalidStructuralSearchRequest = errors.New("invalid structural search request")
	ErrStructuralSearchTargetNotFound = errors.New("structural search target not found")
)

type resolvedStructuralTarget struct {
	CommitSHA        string
	ResolvedRef      string
	CandidatePaths   []string
	PathFilterActive bool
}

type astGrepJSONMatch struct {
	Text          string           `json:"text"`
	File          string           `json:"file"`
	Range         astGrepJSONRange `json:"range"`
	MetaVariables struct {
		Single      map[string]astGrepJSONNode   `json:"single"`
		Multi       map[string][]astGrepJSONNode `json:"multi"`
		Transformed map[string]astGrepJSONNode   `json:"transformed"`
	} `json:"metaVariables"`
}

type astGrepJSONRange struct {
	Start astGrepJSONPosition `json:"start"`
	End   astGrepJSONPosition `json:"end"`
}

type astGrepJSONPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type astGrepJSONNode struct {
	Text string `json:"text"`
}

func IsInvalidStructuralSearchRequest(err error) bool {
	return errors.Is(err, ErrInvalidStructuralSearchRequest)
}

func IsStructuralSearchTargetNotFound(err error) bool {
	return errors.Is(err, ErrStructuralSearchTargetNotFound)
}

func (s *Service) SearchStructural(ctx context.Context, owner, repo string, request StructuralSearchRequest) (StructuralSearchResponse, error) {
	if timeout := s.astGrepTimeout; timeout > 0 {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	request, err := normalizeStructuralSearchRequest(request)
	if err != nil {
		return StructuralSearchResponse{}, err
	}

	repository, err := s.findRepository(ctx, owner, repo)
	if err != nil {
		return StructuralSearchResponse{}, err
	}

	response := StructuralSearchResponse{
		Repository: SearchRepository{
			Owner:    repository.OwnerLogin,
			Name:     repository.Name,
			FullName: repository.FullName,
		},
		Matches: []StructuralMatch{},
	}

	var (
		mirrorPath string
		target     resolvedStructuralTarget
		tempDir    string
	)
	defer func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}()

	err = s.withRepoLock(ctx, owner, repo, func() error {
		if err := s.refreshAuthHeader(ctx); err != nil {
			return err
		}

		var err error
		mirrorPath, err = s.ensureMirror(ctx, owner, repo, repositoryGitURL(repository.HTMLURL))
		if err != nil {
			return err
		}

		target, err = s.resolveStructuralSearchTarget(ctx, mirrorPath, repository, request)
		if err != nil {
			return err
		}
		response.ResolvedCommitSHA = target.CommitSHA
		response.ResolvedRef = target.ResolvedRef
		if target.PathFilterActive && len(target.CandidatePaths) == 0 {
			return nil
		}

		tempDir, err = os.MkdirTemp("", "ghreplica-ast-grep-*")
		if err != nil {
			return err
		}
		return s.materializeCommitTree(ctx, mirrorPath, target.CommitSHA, target.CandidatePaths, tempDir)
	})
	if err != nil {
		return StructuralSearchResponse{}, err
	}

	if target.PathFilterActive && len(target.CandidatePaths) == 0 {
		return response, nil
	}

	searchPaths := []string(nil)
	if target.PathFilterActive {
		searchPaths = append(searchPaths, target.CandidatePaths...)
	}
	matches, truncated, err := s.runASTGrep(ctx, tempDir, request, searchPaths)
	if err != nil {
		return StructuralSearchResponse{}, err
	}
	response.Matches = matches
	response.Truncated = truncated
	return response, nil
}

func (s *Service) resolveStructuralSearchTarget(ctx context.Context, mirrorPath string, repository database.Repository, request StructuralSearchRequest) (resolvedStructuralTarget, error) {
	switch {
	case request.PullRequestNumber > 0:
		var pull database.PullRequest
		if err := s.db.WithContext(ctx).
			Where("repository_id = ? AND number = ?", repository.ID, request.PullRequestNumber).
			First(&pull).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return resolvedStructuralTarget{}, fmt.Errorf("%w: pull request #%d", ErrStructuralSearchTargetNotFound, request.PullRequestNumber)
			}
			return resolvedStructuralTarget{}, err
		}
		if err := s.syncRefs(ctx, repository.ID, mirrorPath, pull.BaseRef, pull.Number); err != nil {
			return resolvedStructuralTarget{}, err
		}
		target := resolvedStructuralTarget{
			CommitSHA:   pull.HeadSHA,
			ResolvedRef: fmt.Sprintf("refs/pull/%d/head", pull.Number),
		}
		if request.ChangedFilesOnly {
			mergeBase, err := s.mergeBase(ctx, mirrorPath, pull.BaseSHA, pull.HeadSHA)
			if err != nil {
				return resolvedStructuralTarget{}, err
			}
			paths, err := s.listChangedPaths(ctx, mirrorPath, mergeBase, pull.HeadSHA)
			if err != nil {
				return resolvedStructuralTarget{}, err
			}
			target.CandidatePaths = paths
			target.PathFilterActive = true
		}
		if len(request.Paths) > 0 {
			if target.PathFilterActive {
				target.CandidatePaths = intersectPaths(target.CandidatePaths, request.Paths)
			} else {
				target.CandidatePaths = append([]string(nil), request.Paths...)
				target.PathFilterActive = true
			}
		}
		if target.PathFilterActive {
			existing, err := s.filterExistingPaths(ctx, mirrorPath, target.CommitSHA, target.CandidatePaths)
			if err != nil {
				return resolvedStructuralTarget{}, err
			}
			target.CandidatePaths = existing
		}
		return target, nil
	case request.Ref != "":
		resolvedRef, err := normalizeResolvedRef(request.Ref)
		if err != nil {
			return resolvedStructuralTarget{}, err
		}
		if err := s.syncResolvedRef(ctx, repository.ID, mirrorPath, resolvedRef); err != nil {
			return resolvedStructuralTarget{}, err
		}
		commitSHA, err := s.resolveGitRefOrSHA(ctx, repository.ID, resolvedRef)
		if err != nil {
			return resolvedStructuralTarget{}, err
		}
		target := resolvedStructuralTarget{
			CommitSHA:   commitSHA,
			ResolvedRef: resolvedRef,
		}
		if len(request.Paths) > 0 {
			existing, err := s.filterExistingPaths(ctx, mirrorPath, commitSHA, request.Paths)
			if err != nil {
				return resolvedStructuralTarget{}, err
			}
			target.CandidatePaths = existing
			target.PathFilterActive = true
		}
		return target, nil
	default:
		if repository.DefaultBranch != "" {
			if err := s.syncRefs(ctx, repository.ID, mirrorPath, repository.DefaultBranch, 0); err != nil {
				return resolvedStructuralTarget{}, err
			}
		}
		if err := s.ensureCommitExists(ctx, mirrorPath, request.CommitSHA); err != nil {
			return resolvedStructuralTarget{}, err
		}
		target := resolvedStructuralTarget{CommitSHA: request.CommitSHA}
		if len(request.Paths) > 0 {
			existing, err := s.filterExistingPaths(ctx, mirrorPath, request.CommitSHA, request.Paths)
			if err != nil {
				return resolvedStructuralTarget{}, err
			}
			target.CandidatePaths = existing
			target.PathFilterActive = true
		}
		return target, nil
	}
}

func normalizeStructuralSearchRequest(request StructuralSearchRequest) (StructuralSearchRequest, error) {
	request.CommitSHA = strings.TrimSpace(request.CommitSHA)
	request.Ref = strings.TrimSpace(request.Ref)
	request.Language = strings.TrimSpace(request.Language)
	request.Paths = normalizeStructuralPaths(request.Paths)
	if request.Limit <= 0 {
		request.Limit = 100
	}
	if request.Limit > 1000 {
		request.Limit = 1000
	}
	targets := 0
	if request.CommitSHA != "" {
		targets++
	}
	if request.Ref != "" {
		targets++
	}
	if request.PullRequestNumber > 0 {
		targets++
	}
	if targets != 1 {
		return StructuralSearchRequest{}, fmt.Errorf("%w: exactly one of commit_sha, ref, or pull_request_number is required", ErrInvalidStructuralSearchRequest)
	}
	if request.ChangedFilesOnly && request.PullRequestNumber <= 0 {
		return StructuralSearchRequest{}, fmt.Errorf("%w: changed_files_only requires pull_request_number", ErrInvalidStructuralSearchRequest)
	}
	if request.Language == "" {
		return StructuralSearchRequest{}, fmt.Errorf("%w: language is required", ErrInvalidStructuralSearchRequest)
	}
	if len(request.Rule) == 0 {
		return StructuralSearchRequest{}, fmt.Errorf("%w: rule is required", ErrInvalidStructuralSearchRequest)
	}
	return request, nil
}

func normalizeStructuralPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		if path == "" {
			continue
		}
		path = strings.TrimPrefix(path, "./")
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
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

func normalizeResolvedRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return "", fmt.Errorf("%w: ref is required", ErrInvalidStructuralSearchRequest)
	case strings.HasPrefix(value, "refs/pull/") && strings.HasSuffix(value, "/head"):
		return value, nil
	case strings.HasPrefix(value, "refs/heads/"):
		return value, nil
	case strings.HasPrefix(value, "refs/remotes/origin/"):
		return "refs/heads/" + strings.TrimPrefix(value, "refs/remotes/origin/"), nil
	case strings.HasPrefix(value, "refs/"):
		return "", fmt.Errorf("%w: unsupported ref %q", ErrInvalidStructuralSearchRequest, value)
	default:
		return "refs/heads/" + strings.TrimPrefix(value, "refs/heads/"), nil
	}
}

func (s *Service) syncResolvedRef(ctx context.Context, repositoryID uint, mirrorPath, ref string) error {
	if strings.HasPrefix(ref, "refs/pull/") && strings.HasSuffix(ref, "/head") {
		numberPart := strings.TrimSuffix(strings.TrimPrefix(ref, "refs/pull/"), "/head")
		number, err := strconv.Atoi(numberPart)
		if err != nil || number <= 0 {
			return fmt.Errorf("%w: invalid pull request ref %q", ErrInvalidStructuralSearchRequest, ref)
		}
		return s.syncRefs(ctx, repositoryID, mirrorPath, "", number)
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		return s.syncRefs(ctx, repositoryID, mirrorPath, strings.TrimPrefix(ref, "refs/heads/"), 0)
	}
	return fmt.Errorf("%w: unsupported ref %q", ErrInvalidStructuralSearchRequest, ref)
}

func (s *Service) findRepository(ctx context.Context, owner, repo string) (database.Repository, error) {
	fullName := strings.TrimSpace(owner) + "/" + strings.TrimSpace(repo)
	var repository database.Repository
	if err := s.db.WithContext(ctx).Where("full_name = ?", fullName).First(&repository).Error; err != nil {
		return database.Repository{}, err
	}
	return repository, nil
}

func (s *Service) resolveGitRefOrSHA(ctx context.Context, repositoryID uint, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: empty ref", ErrInvalidStructuralSearchRequest)
	}
	var ref database.GitRef
	candidates := []string{
		value,
		"refs/heads/" + strings.TrimPrefix(value, "refs/heads/"),
		"refs/remotes/origin/" + strings.TrimPrefix(strings.TrimPrefix(value, "refs/heads/"), "refs/remotes/origin/"),
	}
	if strings.HasPrefix(value, "refs/pull/") {
		candidates = append(candidates, value)
	}
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND ref_name IN ?", repositoryID, normalizeStructuralPaths(candidates)).
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

func (s *Service) ensureCommitExists(ctx context.Context, mirrorPath, commitSHA string) error {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return fmt.Errorf("%w: commit_sha is required", ErrInvalidStructuralSearchRequest)
	}
	if _, err := s.runGit(ctx, mirrorPath, "cat-file", "-e", commitSHA+"^{commit}"); err != nil {
		return fmt.Errorf("%w: commit %s is not available in the mirror", ErrStructuralSearchTargetNotFound, commitSHA)
	}
	return nil
}

func (s *Service) listChangedPaths(ctx context.Context, mirrorPath, baseSHA, headSHA string) ([]string, error) {
	out, err := s.runGit(ctx, mirrorPath, "diff", "--name-only", "-z", baseSHA+"..."+headSHA)
	if err != nil {
		return nil, err
	}
	return normalizeStructuralPaths(splitNULTokens(out)), nil
}

func (s *Service) filterExistingPaths(ctx context.Context, mirrorPath, commitSHA string, paths []string) ([]string, error) {
	paths = normalizeStructuralPaths(paths)
	if len(paths) == 0 {
		return []string{}, nil
	}
	args := []string{"ls-tree", "-r", "--name-only", commitSHA, "--"}
	args = append(args, paths...)
	out, err := s.runGit(ctx, mirrorPath, args...)
	if err != nil {
		return nil, err
	}
	return normalizeStructuralPaths(strings.Split(strings.TrimSpace(string(out)), "\n")), nil
}

func intersectPaths(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return []string{}
	}
	allowed := make(map[string]struct{}, len(right))
	for _, path := range normalizeStructuralPaths(right) {
		allowed[path] = struct{}{}
	}
	out := make([]string, 0, len(left))
	for _, path := range normalizeStructuralPaths(left) {
		if _, ok := allowed[path]; ok {
			out = append(out, path)
		}
	}
	return out
}

func (s *Service) materializeCommitTree(ctx context.Context, mirrorPath, commitSHA string, paths []string, dest string) error {
	args := []string{"archive", "--format=tar", commitSHA}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	archiveCmd := exec.CommandContext(ctx, s.gitBin, append([]string{"-C", mirrorPath}, args...)...)
	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", dest)
	archivePipe, err := archiveCmd.StdoutPipe()
	if err != nil {
		return err
	}
	tarCmd.Stdin = archivePipe
	var archiveStderr strings.Builder
	var tarStderr strings.Builder
	archiveCmd.Stderr = &archiveStderr
	tarCmd.Stderr = &tarStderr
	if err := archiveCmd.Start(); err != nil {
		return err
	}
	if err := tarCmd.Start(); err != nil {
		_ = archiveCmd.Wait()
		return err
	}
	archiveErr := archiveCmd.Wait()
	tarErr := tarCmd.Wait()
	if archiveErr != nil {
		return fmt.Errorf("git archive %s: %w: %s", commitSHA, archiveErr, strings.TrimSpace(archiveStderr.String()))
	}
	if tarErr != nil {
		return fmt.Errorf("tar extract %s: %w: %s", commitSHA, tarErr, strings.TrimSpace(tarStderr.String()))
	}
	return nil
}

func (s *Service) runASTGrep(ctx context.Context, root string, request StructuralSearchRequest, searchPaths []string) ([]StructuralMatch, bool, error) {
	rulePayload := map[string]any{
		"id":       "ghreplica-structural-search",
		"language": request.Language,
		"rule":     request.Rule,
	}
	ruleData, err := yaml.Marshal(rulePayload)
	if err != nil {
		return nil, false, fmt.Errorf("%w: invalid rule payload", ErrInvalidStructuralSearchRequest)
	}
	rulePath := filepath.Join(root, ".ghreplica-ast-grep-rule.yml")
	if err := os.WriteFile(rulePath, ruleData, 0o600); err != nil {
		return nil, false, err
	}

	args := []string{
		"scan",
		"--rule", rulePath,
		"--json=compact",
		"--color=never",
		"--max-results", strconv.Itoa(request.Limit + 1),
	}
	if len(searchPaths) > 0 {
		args = append(args, searchPaths...)
	} else {
		args = append(args, ".")
	}

	cmd := exec.CommandContext(ctx, s.astGrepBin, args...)
	cmd.Dir = root
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		var execErr *exec.Error
		if errors.As(err, &execErr) && execErr.Err != nil {
			return nil, false, fmt.Errorf("ast-grep is not available: %w", err)
		}
		if looksLikeInvalidASTGrepRequest(message) {
			return nil, false, fmt.Errorf("%w: %s", ErrInvalidStructuralSearchRequest, message)
		}
		if message == "" {
			message = err.Error()
		}
		return nil, false, fmt.Errorf("ast-grep search failed: %s", message)
	}

	var rawMatches []astGrepJSONMatch
	if err := json.Unmarshal([]byte(stdout.String()), &rawMatches); err != nil {
		return nil, false, err
	}
	truncated := false
	if len(rawMatches) > request.Limit {
		truncated = true
		rawMatches = rawMatches[:request.Limit]
	}
	matches := make([]StructuralMatch, 0, len(rawMatches))
	for _, match := range rawMatches {
		path := strings.TrimPrefix(filepath.ToSlash(match.File), "./")
		if filepath.IsAbs(match.File) {
			if rel, err := filepath.Rel(root, match.File); err == nil {
				path = filepath.ToSlash(rel)
			}
		}
		matches = append(matches, StructuralMatch{
			Path:        path,
			StartLine:   match.Range.Start.Line + 1,
			StartColumn: match.Range.Start.Column + 1,
			EndLine:     match.Range.End.Line + 1,
			EndColumn:   match.Range.End.Column + 1,
			Text:        match.Text,
			MetaVariables: StructuralMetaVariable{
				Single:      extractSingleMetaVariables(match.MetaVariables.Single),
				Multi:       extractMultiMetaVariables(match.MetaVariables.Multi),
				Transformed: extractSingleMetaVariables(match.MetaVariables.Transformed),
			},
		})
	}
	return matches, truncated, nil
}

func extractSingleMetaVariables(values map[string]astGrepJSONNode) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value.Text
	}
	return out
}

func extractMultiMetaVariables(values map[string][]astGrepJSONNode) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, nodes := range values {
		texts := make([]string, 0, len(nodes))
		for _, node := range nodes {
			texts = append(texts, node.Text)
		}
		out[key] = texts
	}
	return out
}

func looksLikeInvalidASTGrepRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	return strings.Contains(message, "unknown language") ||
		strings.Contains(message, "cannot parse") ||
		strings.Contains(message, "parse error") ||
		strings.Contains(message, "invalid type") ||
		strings.Contains(message, "missing field")
}
