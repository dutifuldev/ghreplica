package webhooks

import (
	"context"
	"errors"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/stretchr/testify/require"
)

type recordingProjector struct {
	repositoryID               uint
	upsertedIssues             []int
	upsertedIssueComments      []int64
	upsertedPullRequests       []int
	upsertedPullRequestReviews []int64
	upsertedReviewComments     []int64
	deletedIssues              []int
	deletedIssueComments       []int64
	deletedPullRequestComments []int64
}

func (p *recordingProjector) UpsertRepository(context.Context, gh.RepositoryResponse) (database.Repository, error) {
	return database.Repository{ID: p.repositoryID}, nil
}

func (p *recordingProjector) UpsertIssue(_ context.Context, _ uint, issue gh.IssueResponse) (database.Issue, error) {
	p.upsertedIssues = append(p.upsertedIssues, issue.Number)
	return database.Issue{}, nil
}

func (p *recordingProjector) UpsertPullRequest(_ context.Context, _ uint, pull gh.PullRequestResponse) error {
	p.upsertedPullRequests = append(p.upsertedPullRequests, pull.Number)
	return nil
}

func (p *recordingProjector) UpsertIssueComment(_ context.Context, _ uint, comment gh.IssueCommentResponse) error {
	p.upsertedIssueComments = append(p.upsertedIssueComments, comment.ID)
	return nil
}

func (p *recordingProjector) UpsertPullRequestReview(_ context.Context, _ uint, _ int, review gh.PullRequestReviewResponse) error {
	p.upsertedPullRequestReviews = append(p.upsertedPullRequestReviews, review.ID)
	return nil
}

func (p *recordingProjector) UpsertPullRequestReviewComment(_ context.Context, _ uint, _ int, comment gh.PullRequestReviewCommentResponse) error {
	p.upsertedReviewComments = append(p.upsertedReviewComments, comment.ID)
	return nil
}

func (p *recordingProjector) DeleteIssue(_ context.Context, _ uint, issue gh.IssueResponse) error {
	p.deletedIssues = append(p.deletedIssues, issue.Number)
	return nil
}

func (p *recordingProjector) DeleteIssueComment(_ context.Context, _ uint, comment gh.IssueCommentResponse) error {
	p.deletedIssueComments = append(p.deletedIssueComments, comment.ID)
	return nil
}

func (p *recordingProjector) DeletePullRequestReviewComment(_ context.Context, _ uint, comment gh.PullRequestReviewCommentResponse) error {
	p.deletedPullRequestComments = append(p.deletedPullRequestComments, comment.ID)
	return nil
}

type recordingRecorder struct {
	notedRepoIDs    []uint
	enqueuedPulls   []int
	markedInventory []uint
}

func (r *recordingRecorder) NoteRepositoryWebhook(_ context.Context, repositoryID uint, _ time.Time) error {
	r.notedRepoIDs = append(r.notedRepoIDs, repositoryID)
	return nil
}

func (r *recordingRecorder) MarkInventoryNeedsRefresh(_ context.Context, repositoryID uint, _ time.Time) error {
	r.markedInventory = append(r.markedInventory, repositoryID)
	return nil
}

func (r *recordingRecorder) EnqueuePullRequestRefresh(_ context.Context, _ uint, number int, _ time.Time) error {
	r.enqueuedPulls = append(r.enqueuedPulls, number)
	return nil
}

type recordingStaler struct {
	repositoryIDs []uint
	refs          []string
}

func (s *recordingStaler) MarkBaseRefStale(_ context.Context, repositoryID uint, ref string) error {
	s.repositoryIDs = append(s.repositoryIDs, repositoryID)
	s.refs = append(s.refs, ref)
	return nil
}

type failingProjector struct {
	recordingProjector
	repoErr               error
	issueErr              error
	issueCommentErr       error
	pullErr               error
	reviewErr             error
	reviewCommentErr      error
	deleteIssueErr        error
	deleteIssueCommentErr error
	deleteReviewErr       error
}

func (p *failingProjector) UpsertRepository(context.Context, gh.RepositoryResponse) (database.Repository, error) {
	if p.repoErr != nil {
		return database.Repository{}, p.repoErr
	}
	return database.Repository{ID: p.repositoryID}, nil
}

func (p *failingProjector) UpsertIssue(context.Context, uint, gh.IssueResponse) (database.Issue, error) {
	if p.issueErr != nil {
		return database.Issue{}, p.issueErr
	}
	return database.Issue{}, nil
}

func (p *failingProjector) UpsertPullRequest(context.Context, uint, gh.PullRequestResponse) error {
	return p.pullErr
}

func (p *failingProjector) UpsertIssueComment(context.Context, uint, gh.IssueCommentResponse) error {
	return p.issueCommentErr
}

func (p *failingProjector) UpsertPullRequestReview(context.Context, uint, int, gh.PullRequestReviewResponse) error {
	return p.reviewErr
}

func (p *failingProjector) UpsertPullRequestReviewComment(context.Context, uint, int, gh.PullRequestReviewCommentResponse) error {
	return p.reviewCommentErr
}

func (p *failingProjector) DeleteIssue(context.Context, uint, gh.IssueResponse) error {
	return p.deleteIssueErr
}

func (p *failingProjector) DeleteIssueComment(context.Context, uint, gh.IssueCommentResponse) error {
	return p.deleteIssueCommentErr
}

func (p *failingProjector) DeletePullRequestReviewComment(context.Context, uint, gh.PullRequestReviewCommentResponse) error {
	return p.deleteReviewErr
}

type failingRecorder struct {
	err error
}

func (f *failingRecorder) NoteRepositoryWebhook(context.Context, uint, time.Time) error {
	return f.err
}

func (f *failingRecorder) EnqueuePullRequestRefresh(context.Context, uint, int, time.Time) error {
	return f.err
}

func (f *failingRecorder) MarkInventoryNeedsRefresh(context.Context, uint, time.Time) error {
	return f.err
}

func TestDecodeWebhookEventPolicies(t *testing.T) {
	t.Parallel()

	reviewCommentPayload := map[string]any{
		"action": "created",
		"repository": map[string]any{
			"id":        101,
			"full_name": "acme/widgets",
		},
		"pull_request": map[string]any{
			"id":       201,
			"number":   7,
			"state":    "open",
			"title":    "Review me",
			"head":     map[string]any{"ref": "feature", "sha": "head"},
			"base":     map[string]any{"ref": "main", "sha": "base"},
			"html_url": "https://github.com/acme/widgets/pull/7",
			"url":      "https://api.github.com/repos/acme/widgets/pulls/7",
		},
		"comment": map[string]any{
			"id":   301,
			"path": "app/service.go",
			"body": "looks good",
		},
	}

	tests := []struct {
		name      string
		event     string
		payload   map[string]any
		immediate bool
		action    string
	}{
		{
			name:  "ping",
			event: "ping",
			payload: map[string]any{
				"zen": "keep it logically awesome",
				"repository": map[string]any{
					"id":        101,
					"full_name": "acme/widgets",
				},
			},
			immediate: false,
		},
		{
			name:  "repository",
			event: "repository",
			payload: map[string]any{
				"action": "edited",
				"repository": map[string]any{
					"id":        101,
					"full_name": "acme/widgets",
				},
			},
			immediate: false,
			action:    "edited",
		},
		{
			name:  "issues",
			event: "issues",
			payload: map[string]any{
				"action": "opened",
				"repository": map[string]any{
					"id":        101,
					"full_name": "acme/widgets",
				},
				"issue": map[string]any{
					"id":         201,
					"number":     7,
					"title":      "Issue",
					"state":      "open",
					"html_url":   "https://github.com/acme/widgets/issues/7",
					"url":        "https://api.github.com/repos/acme/widgets/issues/7",
					"created_at": time.Now().UTC(),
					"updated_at": time.Now().UTC(),
				},
			},
			immediate: true,
			action:    "opened",
		},
		{
			name:      "pull request review comment",
			event:     "pull_request_review_comment",
			payload:   reviewCommentPayload,
			immediate: true,
			action:    "created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, ok := webhookEventPolicyFor(tt.event)
			require.True(t, ok)
			require.Equal(t, tt.immediate, policy.immediate)

			raw, err := json.Marshal(tt.payload)
			require.NoError(t, err)

			decoded, err := decodeWebhookEvent(tt.event, raw)
			require.NoError(t, err)
			require.Equal(t, tt.event, decoded.Event)
			require.Equal(t, tt.action, decoded.Action)
			require.NotNil(t, decoded.RepoRef)
			require.Equal(t, "acme/widgets", decoded.RepoRef.FullName)
		})
	}
}

func TestProjectWebhookEventsAndFollowUps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	projector := &recordingProjector{repositoryID: 42}
	recorder := &recordingRecorder{}
	staler := &recordingStaler{}

	deps := eventProjectionDependencies{
		projector: projector,
		repositoryIDLookup: func(_ context.Context, repoRef *repositoryRef) (uint, error) {
			if repoRef == nil {
				return 0, nil
			}
			return 42, nil
		},
	}

	pushEvent := decodedWebhookEvent{
		Event:   "push",
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
		Payload: pushWebhookPayload{
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Ref:        "refs/heads/main",
		},
	}
	pushResult, err := projectPushWebhookEvent(ctx, deps, pushEvent)
	require.NoError(t, err)
	require.Equal(t, uint(42), pushResult.repositoryID)
	require.Equal(t, "refs/heads/main", pushResult.followUp.staleBaseRef)
	require.True(t, pushResult.followUp.markInventoryRefresh)

	prEvent := decodedWebhookEvent{
		Event: "pull_request",
		Payload: pullRequestWebhookPayload{
			Action: "opened",
			Repository: gh.RepositoryResponse{
				FullName: "acme/widgets",
			},
			PullRequest: gh.PullRequestResponse{
				ID:     501,
				Number: 9,
				State:  "open",
				Title:  "Add fast path",
				Head:   gh.PullBranch{Ref: "feature", SHA: "head"},
				Base:   gh.PullBranch{Ref: "main", SHA: "base"},
			},
		},
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	}
	prResult, err := projectPullRequestWebhookEvent(ctx, deps, prEvent)
	require.NoError(t, err)
	require.Equal(t, []int{9}, projector.upsertedPullRequests)
	require.Equal(t, []int{9}, []int{*prResult.followUp.enqueuePullRequestNumber})
	require.True(t, prResult.followUp.markInventoryRefresh)

	reviewEvent := decodedWebhookEvent{
		Event: "pull_request_review",
		Payload: pullRequestReviewWebhookPayload{
			Action: "submitted",
			Repository: gh.RepositoryResponse{
				FullName: "acme/widgets",
			},
			PullRequest: gh.PullRequestResponse{
				ID:     501,
				Number: 9,
				State:  "open",
				Title:  "Add fast path",
				Head:   gh.PullBranch{Ref: "feature", SHA: "head"},
				Base:   gh.PullBranch{Ref: "main", SHA: "base"},
			},
			Review: gh.PullRequestReviewResponse{ID: 701, State: "APPROVED"},
		},
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	}
	_, err = projectPullRequestReviewWebhookEvent(ctx, deps, reviewEvent)
	require.NoError(t, err)
	require.Equal(t, []int64{701}, projector.upsertedPullRequestReviews)

	repositoryEvent := decodedWebhookEvent{
		Event: "repository",
		Payload: repositoryWebhookPayload{
			Action: "edited",
			Repository: gh.RepositoryResponse{
				FullName: "acme/widgets",
			},
		},
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	}
	result, err := projectRepositoryWebhookEvent(ctx, deps, repositoryEvent)
	require.NoError(t, err)
	require.Equal(t, uint(42), result.repositoryID)
	require.True(t, result.followUp.noteRepositoryWebhook)

	issueDeleteEvent := decodedWebhookEvent{
		Event: "issues",
		Payload: issueWebhookPayload{
			Action: "deleted",
			Repository: gh.RepositoryResponse{
				FullName: "acme/widgets",
			},
			Issue: gh.IssueResponse{Number: 3},
		},
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	}
	_, err = projectIssuesWebhookEvent(ctx, deps, issueDeleteEvent)
	require.NoError(t, err)
	require.Equal(t, []int{3}, projector.deletedIssues)

	reviewCommentDeleteEvent := decodedWebhookEvent{
		Event: "pull_request_review_comment",
		Payload: pullRequestReviewCommentWebhookPayload{
			Action: "deleted",
			Repository: gh.RepositoryResponse{
				FullName: "acme/widgets",
			},
			PullRequest: gh.PullRequestResponse{
				ID:     501,
				Number: 9,
				State:  "open",
				Title:  "Add fast path",
				Head:   gh.PullBranch{Ref: "feature", SHA: "head"},
				Base:   gh.PullBranch{Ref: "main", SHA: "base"},
			},
			Comment: gh.PullRequestReviewCommentResponse{ID: 801},
		},
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	}
	_, err = projectPullRequestReviewCommentWebhookEvent(ctx, deps, reviewCommentDeleteEvent)
	require.NoError(t, err)
	require.Equal(t, []int64{801}, projector.deletedPullRequestComments)

	require.NoError(t, applyProjectionFollowUp(ctx, eventFollowUpDependencies{
		staler:   staler,
		recorder: recorder,
	}, eventProjectionResult{
		repositoryID: 42,
		followUp: eventFollowUpActions{
			noteRepositoryWebhook:    true,
			enqueuePullRequestNumber: intPtr(9),
			markInventoryRefresh:     true,
			staleBaseRef:             "refs/heads/main",
		},
	}, now))
	require.Equal(t, []uint{42}, recorder.notedRepoIDs)
	require.Equal(t, []int{9}, recorder.enqueuedPulls)
	require.Equal(t, []uint{42}, recorder.markedInventory)
	require.Equal(t, []string{"refs/heads/main"}, staler.refs)
}

func TestProjectPingWebhookEventFallsBackToLookupAndPayloadAsValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result, err := projectPingWebhookEvent(ctx, eventProjectionDependencies{
		repositoryIDLookup: func(_ context.Context, repoRef *repositoryRef) (uint, error) {
			require.Equal(t, "acme/widgets", repoRef.FullName)
			return 11, nil
		},
	}, decodedWebhookEvent{
		Event:   "ping",
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	})
	require.NoError(t, err)
	require.Equal(t, uint(11), result.repositoryID)
	require.True(t, result.followUp.noteRepositoryWebhook)

	_, err = payloadAs[pullRequestWebhookPayload](decodedWebhookEvent{Event: "issues", Payload: issueWebhookPayload{}})
	require.ErrorContains(t, err, "payload type mismatch")
}

func TestRepositoryLookupAndPingNoopBranches(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "cleanup-worker.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repositoryID, err := repositoryIDByRef(context.Background(), db, nil)
	require.NoError(t, err)
	require.Zero(t, repositoryID)

	repositoryID, err = repositoryIDByRef(context.Background(), db, &repositoryRef{GitHubID: 0})
	require.NoError(t, err)
	require.Zero(t, repositoryID)

	_, _, err = splitFullName("not-valid")
	require.ErrorContains(t, err, "owner/repo")

	result, err := projectPingWebhookEvent(context.Background(), eventProjectionDependencies{}, decodedWebhookEvent{
		Event:   "ping",
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
	})
	require.NoError(t, err)
	require.Zero(t, result.repositoryID)
	require.False(t, result.followUp.noteRepositoryWebhook)
}

func TestProjectImmediateCreatePathsAndPullRequestFollowUp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deps := eventProjectionDependencies{projector: &recordingProjector{repositoryID: 55}}

	issuesResult, err := projectIssuesWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "issues",
		Payload: issueWebhookPayload{
			Action:     "opened",
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Issue:      gh.IssueResponse{ID: 11, Number: 7, Title: "Issue"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint(55), issuesResult.repositoryID)

	projector := deps.projector.(*recordingProjector)
	require.Equal(t, []int{7}, projector.upsertedIssues)

	commentResult, err := projectIssueCommentWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "issue_comment",
		Payload: issueCommentWebhookPayload{
			Action:     "created",
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Issue:      gh.IssueResponse{ID: 11, Number: 7, Title: "Issue"},
			Comment:    gh.IssueCommentResponse{ID: 21, Body: "hello"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint(55), commentResult.repositoryID)
	require.Equal(t, []int64{21}, projector.upsertedIssueComments)

	reviewCommentResult, err := projectPullRequestReviewCommentWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "pull_request_review_comment",
		Payload: pullRequestReviewCommentWebhookPayload{
			Action:     "created",
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{
				ID:     31,
				Number: 8,
				State:  "open",
				Head:   gh.PullBranch{Ref: "feature", SHA: "head"},
				Base:   gh.PullBranch{Ref: "main", SHA: "base"},
			},
			Comment: gh.PullRequestReviewCommentResponse{ID: 41, Body: "review"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint(55), reviewCommentResult.repositoryID)
	require.Equal(t, []int{8}, projector.upsertedPullRequests)
	require.Equal(t, []int64{41}, projector.upsertedReviewComments)

	pullResult, err := projectPullRequestWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "pull_request",
		Payload: pullRequestWebhookPayload{
			Action:     "edited",
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{
				ID:     51,
				Number: 9,
				State:  "open",
				Head:   gh.PullBranch{Ref: "feature", SHA: "head"},
				Base:   gh.PullBranch{Ref: "main", SHA: "base"},
			},
			Changes: pullRequestWebhookChanges{
				Base: &struct {
					Ref *struct {
						From string `json:"from"`
					} `json:"ref"`
				}{
					Ref: &struct {
						From string `json:"from"`
					}{From: "release"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, pullResult.followUp.markInventoryRefresh)
	require.NotNil(t, pullResult.followUp.enqueuePullRequestNumber)
	require.Equal(t, 9, *pullResult.followUp.enqueuePullRequestNumber)
}

func TestProjectionFallbackAndFollowUpErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fallbackDeps := eventProjectionDependencies{
		projector: &failingProjector{
			recordingProjector: recordingProjector{repositoryID: 66},
			repoErr:            errors.New("boom"),
		},
		repositoryIDLookup: func(context.Context, *repositoryRef) (uint, error) { return 66, nil },
	}

	result, err := projectPushWebhookEvent(ctx, fallbackDeps, decodedWebhookEvent{
		Event:   "push",
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
		Payload: pushWebhookPayload{
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Ref:        "refs/heads/main",
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint(66), result.repositoryID)
	require.True(t, result.followUp.noteRepositoryWebhook)

	require.NoError(t, applyProjectionFollowUp(ctx, eventFollowUpDependencies{}, eventProjectionResult{}, time.Now().UTC()))

	err = applyProjectionFollowUp(ctx, eventFollowUpDependencies{
		recorder: &failingRecorder{err: errors.New("record failed")},
	}, eventProjectionResult{
		repositoryID: 66,
		followUp:     eventFollowUpActions{noteRepositoryWebhook: true},
	}, time.Now().UTC())
	require.ErrorContains(t, err, "record failed")

	_, err = decodeWebhookEvent("issues", []byte(`{"action":`))
	require.Error(t, err)
}

func TestProjectorRepositoryErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deps := eventProjectionDependencies{
		projector: &failingProjector{
			recordingProjector: recordingProjector{repositoryID: 77},
			repoErr:            errors.New("repo failed"),
		},
	}

	_, err := projectRepositoryWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event:   "repository",
		Payload: repositoryWebhookPayload{Repository: gh.RepositoryResponse{FullName: "acme/widgets"}},
	})
	require.ErrorContains(t, err, "repo failed")

	_, err = projectIssuesWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event:   "issues",
		Payload: issueWebhookPayload{Repository: gh.RepositoryResponse{FullName: "acme/widgets"}, Issue: gh.IssueResponse{Number: 1}},
	})
	require.ErrorContains(t, err, "repo failed")

	_, err = projectIssueCommentWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "issue_comment",
		Payload: issueCommentWebhookPayload{
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Issue:      gh.IssueResponse{Number: 1},
			Comment:    gh.IssueCommentResponse{ID: 1},
		},
	})
	require.ErrorContains(t, err, "repo failed")

	_, err = projectPullRequestWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "pull_request",
		Payload: pullRequestWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
		},
	})
	require.ErrorContains(t, err, "repo failed")

	_, err = projectPullRequestReviewWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "pull_request_review",
		Payload: pullRequestReviewWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
			Review:      gh.PullRequestReviewResponse{ID: 2},
		},
	})
	require.ErrorContains(t, err, "repo failed")

	_, err = projectPullRequestReviewCommentWebhookEvent(ctx, deps, decodedWebhookEvent{
		Event: "pull_request_review_comment",
		Payload: pullRequestReviewCommentWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
			Comment:     gh.PullRequestReviewCommentResponse{ID: 3},
		},
	})
	require.ErrorContains(t, err, "repo failed")
}

func TestProjectorNestedErrorPaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := projectIssuesWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, issueErr: errors.New("issue failed")},
	}, decodedWebhookEvent{
		Event:   "issues",
		Payload: issueWebhookPayload{Repository: gh.RepositoryResponse{FullName: "acme/widgets"}, Issue: gh.IssueResponse{Number: 1}},
	})
	require.ErrorContains(t, err, "issue failed")

	_, err = projectIssuesWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, deleteIssueErr: errors.New("delete issue failed")},
	}, decodedWebhookEvent{
		Event:   "issues",
		Payload: issueWebhookPayload{Action: "deleted", Repository: gh.RepositoryResponse{FullName: "acme/widgets"}, Issue: gh.IssueResponse{Number: 1}},
	})
	require.ErrorContains(t, err, "delete issue failed")

	_, err = projectIssueCommentWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, issueCommentErr: errors.New("comment failed")},
	}, decodedWebhookEvent{
		Event: "issue_comment",
		Payload: issueCommentWebhookPayload{
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Issue:      gh.IssueResponse{Number: 1},
			Comment:    gh.IssueCommentResponse{ID: 2},
		},
	})
	require.ErrorContains(t, err, "comment failed")

	_, err = projectIssueCommentWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, deleteIssueCommentErr: errors.New("delete comment failed")},
	}, decodedWebhookEvent{
		Event: "issue_comment",
		Payload: issueCommentWebhookPayload{
			Action:     "deleted",
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Issue:      gh.IssueResponse{Number: 1},
			Comment:    gh.IssueCommentResponse{ID: 2},
		},
	})
	require.ErrorContains(t, err, "delete comment failed")

	_, err = projectPullRequestWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, pullErr: errors.New("pull failed")},
	}, decodedWebhookEvent{
		Event: "pull_request",
		Payload: pullRequestWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
		},
	})
	require.ErrorContains(t, err, "pull failed")

	_, err = projectPullRequestReviewWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, reviewErr: errors.New("review failed")},
	}, decodedWebhookEvent{
		Event: "pull_request_review",
		Payload: pullRequestReviewWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
			Review:      gh.PullRequestReviewResponse{ID: 3},
		},
	})
	require.ErrorContains(t, err, "review failed")

	_, err = projectPullRequestReviewCommentWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, reviewCommentErr: errors.New("review comment failed")},
	}, decodedWebhookEvent{
		Event: "pull_request_review_comment",
		Payload: pullRequestReviewCommentWebhookPayload{
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
			Comment:     gh.PullRequestReviewCommentResponse{ID: 4},
		},
	})
	require.ErrorContains(t, err, "review comment failed")

	_, err = projectPullRequestReviewCommentWebhookEvent(ctx, eventProjectionDependencies{
		projector: &failingProjector{recordingProjector: recordingProjector{repositoryID: 77}, deleteReviewErr: errors.New("delete review failed")},
	}, decodedWebhookEvent{
		Event: "pull_request_review_comment",
		Payload: pullRequestReviewCommentWebhookPayload{
			Action:      "deleted",
			Repository:  gh.RepositoryResponse{FullName: "acme/widgets"},
			PullRequest: gh.PullRequestResponse{Number: 1, Head: gh.PullBranch{Ref: "head"}, Base: gh.PullBranch{Ref: "main"}},
			Comment:     gh.PullRequestReviewCommentResponse{ID: 4},
		},
	})
	require.ErrorContains(t, err, "delete review failed")
}

func TestDeliveryCleanupWorkerStartAndRepositoryRefFallbacks(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	worker := NewDeliveryCleanupWorker(db, time.Minute, time.Millisecond, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, worker.Start(ctx), context.Canceled)

	repoRef, err := repositoryRefFromGitHubRepository(&gh.RepositoryResponse{
		ID:   77,
		Name: "widgets",
		Owner: &gh.UserResponse{
			Login: "acme",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "acme/widgets", repoRef.FullName)
}
