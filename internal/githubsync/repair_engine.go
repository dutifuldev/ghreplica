package githubsync

import (
	"context"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
)

type repairPassOptions struct {
	StartPage int
	MaxPages  int
	PerPage   int
	Cutoff    *time.Time
}

type repairApplyPlan struct {
	pullNumbers  []int
	issueNumbers []int
}

func accumulateRepairPassMetrics(total *repairPassMetrics, next repairPassMetrics) {
	total.PullsScanned += next.PullsScanned
	total.IssuesScanned += next.IssuesScanned
	total.PullsStale += next.PullsStale
	total.IssuesStale += next.IssuesStale
	total.PullsUnchanged += next.PullsUnchanged
	total.IssuesUnchanged += next.IssuesUnchanged
	total.PullFetches += next.PullFetches
	total.IssueFetches += next.IssueFetches
	total.PullsRepaired += next.PullsRepaired
	total.IssuesRepaired += next.IssuesRepaired
	total.ApplyWrites += next.ApplyWrites
	total.Completed = next.Completed
	total.NextPage = next.NextPage
}

func (s *Service) runPullRequestRepairPass(ctx context.Context, owner, repo string, repositoryID uint, options repairPassOptions) (repairPassMetrics, error) {
	options = normalizedRepairPassOptions(options)
	result := repairPassMetrics{NextPage: options.StartPage}
	for page := options.StartPage; page < options.StartPage+options.MaxPages; page++ {
		pageDone, err := s.runPullRequestRepairPage(ctx, owner, repo, repositoryID, page, options, &result)
		if err != nil {
			return result, err
		}
		if pageDone {
			result.Completed = true
			result.NextPage = 1
			return result, nil
		}
		result.NextPage = page + 1
	}
	return result, nil
}

func normalizedRepairPassOptions(options repairPassOptions) repairPassOptions {
	if options.StartPage <= 0 {
		options.StartPage = 1
	}
	if options.MaxPages <= 0 {
		options.MaxPages = defaultRecentPRRepairMaxPages
	}
	if options.PerPage <= 0 {
		options.PerPage = defaultRecentPRRepairPerPage
	}
	return options
}

func (s *Service) runPullRequestRepairPage(ctx context.Context, owner, repo string, repositoryID uint, page int, options repairPassOptions, result *repairPassMetrics) (bool, error) {
	pullPlan, pullDone, err := s.buildRepairPullPlan(ctx, owner, repo, repositoryID, page, options, result)
	if err != nil {
		return false, err
	}
	issuePlan, issueDone, err := s.buildRepairIssuePlan(ctx, owner, repo, repositoryID, page, options, result)
	if err != nil {
		return false, err
	}
	plan := mergeRepairPlans(pullPlan, issuePlan)
	if err := s.applyRepairPlan(ctx, owner, repo, repositoryID, plan, result); err != nil {
		return false, err
	}
	return pullDone && issueDone, nil
}

func (s *Service) buildRepairPullPlan(ctx context.Context, owner, repo string, repositoryID uint, page int, options repairPassOptions, result *repairPassMetrics) (repairApplyPlan, bool, error) {
	pulls, err := s.github.ListPullRequestsPage(ctx, owner, repo, "all", "updated", "desc", page, options.PerPage)
	if err != nil {
		return repairApplyPlan{}, false, err
	}
	currentPulls, pullDone := scanRepairPullCandidates(pulls, options.Cutoff)
	plan, err := s.detectStalePulls(ctx, repositoryID, currentPulls, result)
	return plan, pullDone, err
}

func (s *Service) buildRepairIssuePlan(ctx context.Context, owner, repo string, repositoryID uint, page int, options repairPassOptions, result *repairPassMetrics) (repairApplyPlan, bool, error) {
	issues, err := s.github.ListIssuesPage(ctx, owner, repo, "all", "updated", "desc", page, options.PerPage)
	if err != nil {
		return repairApplyPlan{}, false, err
	}
	currentIssues, issueDone := scanRepairIssueCandidates(issues, options.Cutoff)
	plan, err := s.detectStaleIssues(ctx, repositoryID, currentIssues, result)
	return plan, issueDone, err
}

func scanRepairPullCandidates(pulls []gh.PullRequestResponse, cutoff *time.Time) ([]gh.PullRequestResponse, bool) {
	if len(pulls) == 0 {
		return nil, true
	}

	current := make([]gh.PullRequestResponse, 0, len(pulls))
	pageDone := false
	for _, pull := range pulls {
		if cutoff != nil && pull.UpdatedAt.UTC().Before(cutoff.UTC()) {
			pageDone = true
			break
		}
		current = append(current, pull)
	}
	if len(current) == 0 {
		return nil, true
	}
	return current, pageDone
}

func scanRepairIssueCandidates(issues []gh.IssueResponse, cutoff *time.Time) ([]gh.IssueResponse, bool) {
	if len(issues) == 0 {
		return nil, true
	}

	current := make([]gh.IssueResponse, 0, len(issues))
	pageDone := false
	for _, issue := range issues {
		if cutoff != nil && issue.UpdatedAt.UTC().Before(cutoff.UTC()) {
			pageDone = true
			break
		}
		if issue.PullRequest == nil {
			continue
		}
		current = append(current, issue)
	}
	return current, pageDone
}

func (s *Service) detectStalePulls(ctx context.Context, repositoryID uint, pulls []gh.PullRequestResponse, result *repairPassMetrics) (repairApplyPlan, error) {
	if len(pulls) == 0 {
		return repairApplyPlan{}, nil
	}

	numbers := make([]int, 0, len(pulls))
	for _, pull := range pulls {
		numbers = append(numbers, pull.Number)
	}

	storedPulls, err := s.loadStoredPullRequestsByNumber(ctx, repositoryID, numbers)
	if err != nil {
		return repairApplyPlan{}, err
	}
	storedIssues, err := s.loadStoredIssuesByNumber(ctx, repositoryID, numbers)
	if err != nil {
		return repairApplyPlan{}, err
	}

	plan := repairApplyPlan{
		pullNumbers:  make([]int, 0, len(pulls)),
		issueNumbers: make([]int, 0, len(pulls)),
	}
	for _, pull := range pulls {
		result.PullsScanned++
		storedPull, pullExists := storedPulls[pull.Number]
		if !pullExists || storedPull.GitHubUpdatedAt.Before(pull.UpdatedAt.UTC()) {
			result.PullsStale++
			plan.pullNumbers = append(plan.pullNumbers, pull.Number)
		} else {
			result.PullsUnchanged++
		}
		if _, issueExists := storedIssues[pull.Number]; !issueExists {
			result.IssuesStale++
			plan.issueNumbers = append(plan.issueNumbers, pull.Number)
		} else {
			result.IssuesUnchanged++
		}
	}
	return plan, nil
}

func (s *Service) detectStaleIssues(ctx context.Context, repositoryID uint, issues []gh.IssueResponse, result *repairPassMetrics) (repairApplyPlan, error) {
	if len(issues) == 0 {
		return repairApplyPlan{}, nil
	}

	numbers := make([]int, 0, len(issues))
	for _, issue := range issues {
		numbers = append(numbers, issue.Number)
	}

	storedIssues, err := s.loadStoredIssuesByNumber(ctx, repositoryID, numbers)
	if err != nil {
		return repairApplyPlan{}, err
	}

	plan := repairApplyPlan{
		issueNumbers: make([]int, 0, len(issues)),
	}
	for _, issue := range issues {
		result.IssuesScanned++
		storedIssue, issueExists := storedIssues[issue.Number]
		if !issueExists || storedIssue.GitHubUpdatedAt.Before(issue.UpdatedAt.UTC()) {
			result.IssuesStale++
			plan.issueNumbers = append(plan.issueNumbers, issue.Number)
		} else {
			result.IssuesUnchanged++
		}
	}
	return plan, nil
}

func mergeRepairPlans(plans ...repairApplyPlan) repairApplyPlan {
	merged := repairApplyPlan{}
	pullSeen := map[int]struct{}{}
	issueSeen := map[int]struct{}{}
	for _, plan := range plans {
		for _, number := range plan.pullNumbers {
			if _, ok := pullSeen[number]; ok {
				continue
			}
			pullSeen[number] = struct{}{}
			merged.pullNumbers = append(merged.pullNumbers, number)
		}
		for _, number := range plan.issueNumbers {
			if _, ok := issueSeen[number]; ok {
				continue
			}
			issueSeen[number] = struct{}{}
			merged.issueNumbers = append(merged.issueNumbers, number)
		}
	}
	return merged
}

func (s *Service) applyRepairPlan(ctx context.Context, owner, repo string, repositoryID uint, plan repairApplyPlan, result *repairPassMetrics) error {
	for _, number := range uniqueInts(plan.issueNumbers) {
		if err := s.repairIssueObject(ctx, owner, repo, repositoryID, number); err != nil {
			return err
		}
		result.IssueFetches++
		result.IssuesRepaired++
		result.ApplyWrites++
	}
	for _, number := range uniqueInts(plan.pullNumbers) {
		if err := s.repairPullRequestObject(ctx, owner, repo, repositoryID, number); err != nil {
			return err
		}
		result.PullFetches++
		result.PullsRepaired++
		result.ApplyWrites++
	}
	return nil
}

func (s *Service) RepairRecentPullRequests(ctx context.Context, owner, repo string, since time.Time, startPage, maxPages, perPage int) (repairPassMetrics, error) {
	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return repairPassMetrics{}, err
	}
	cutoff := since.UTC()
	return s.runPullRequestRepairPass(ctx, owner, repo, repository.ID, repairPassOptions{
		StartPage: startPage,
		MaxPages:  maxPages,
		PerPage:   perPage,
		Cutoff:    &cutoff,
	})
}

func (s *Service) RepairPullRequestHistoryPage(ctx context.Context, owner, repo string, page, perPage int) (repairPassMetrics, error) {
	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return repairPassMetrics{}, err
	}
	return s.runPullRequestRepairPass(ctx, owner, repo, repository.ID, repairPassOptions{
		StartPage: page,
		MaxPages:  1,
		PerPage:   perPage,
	})
}
