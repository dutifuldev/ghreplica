package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
)

type repositoryIDLookupFunc func(context.Context, *repositoryRef) (uint, error)

type eventProjectionDependencies struct {
	projector          WebhookProjector
	repositoryIDLookup repositoryIDLookupFunc
}

type eventFollowUpDependencies struct {
	staler   BaseRefStaler
	recorder RepoChangeWebhookRecorder
}

type eventFollowUpActions struct {
	noteRepositoryWebhook    bool
	enqueuePullRequestNumber *int
	markInventoryRefresh     bool
	staleBaseRef             string
}

type eventProjectionResult struct {
	repositoryID uint
	followUp     eventFollowUpActions
}

type decodedWebhookEvent struct {
	Event   string
	Action  string
	RepoRef *repositoryRef
	Payload any
}

type decodableWebhookPayload interface {
	webhookAction() string
	webhookRepositoryRef() (*repositoryRef, error)
}

type genericWebhookPayload struct {
	Action     string                 `json:"action"`
	Repository *gh.RepositoryResponse `json:"repository"`
}

func (p genericWebhookPayload) webhookAction() string { return p.Action }

func (p genericWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(p.Repository)
}

type pushWebhookPayload struct {
	Action     string                `json:"action"`
	Repository gh.RepositoryResponse `json:"repository"`
	Ref        string                `json:"ref"`
}

func (p pushWebhookPayload) webhookAction() string { return p.Action }

func (p pushWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type repositoryWebhookPayload struct {
	Action     string                `json:"action"`
	Repository gh.RepositoryResponse `json:"repository"`
}

func (p repositoryWebhookPayload) webhookAction() string { return p.Action }

func (p repositoryWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type issueWebhookPayload struct {
	Action     string                `json:"action"`
	Repository gh.RepositoryResponse `json:"repository"`
	Issue      gh.IssueResponse      `json:"issue"`
}

func (p issueWebhookPayload) webhookAction() string { return p.Action }

func (p issueWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type issueCommentWebhookPayload struct {
	Action     string                  `json:"action"`
	Repository gh.RepositoryResponse   `json:"repository"`
	Issue      gh.IssueResponse        `json:"issue"`
	Comment    gh.IssueCommentResponse `json:"comment"`
}

func (p issueCommentWebhookPayload) webhookAction() string { return p.Action }

func (p issueCommentWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type pullRequestWebhookChanges struct {
	Base *struct {
		Ref *struct {
			From string `json:"from"`
		} `json:"ref"`
	} `json:"base"`
}

type pullRequestWebhookPayload struct {
	Action      string                    `json:"action"`
	Repository  gh.RepositoryResponse     `json:"repository"`
	PullRequest gh.PullRequestResponse    `json:"pull_request"`
	Changes     pullRequestWebhookChanges `json:"changes"`
}

func (p pullRequestWebhookPayload) webhookAction() string { return p.Action }

func (p pullRequestWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

func (p pullRequestWebhookPayload) needsInventoryRefresh() bool {
	switch strings.TrimSpace(p.Action) {
	case "opened", "closed", "reopened":
		return true
	case "edited":
		return p.Changes.Base != nil &&
			p.Changes.Base.Ref != nil &&
			strings.TrimSpace(p.Changes.Base.Ref.From) != ""
	default:
		return false
	}
}

type pullRequestReviewWebhookPayload struct {
	Action      string                       `json:"action"`
	Repository  gh.RepositoryResponse        `json:"repository"`
	PullRequest gh.PullRequestResponse       `json:"pull_request"`
	Review      gh.PullRequestReviewResponse `json:"review"`
}

func (p pullRequestReviewWebhookPayload) webhookAction() string { return p.Action }

func (p pullRequestReviewWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type pullRequestReviewCommentWebhookPayload struct {
	Action      string                              `json:"action"`
	Repository  gh.RepositoryResponse               `json:"repository"`
	PullRequest gh.PullRequestResponse              `json:"pull_request"`
	Comment     gh.PullRequestReviewCommentResponse `json:"comment"`
}

func (p pullRequestReviewCommentWebhookPayload) webhookAction() string { return p.Action }

func (p pullRequestReviewCommentWebhookPayload) webhookRepositoryRef() (*repositoryRef, error) {
	return repositoryRefFromGitHubRepository(&p.Repository)
}

type webhookEventPolicy struct {
	immediate bool
	decode    func(event string, payload []byte) (decodedWebhookEvent, error)
	project   func(context.Context, eventProjectionDependencies, decodedWebhookEvent) (eventProjectionResult, error)
}

var webhookEventPolicies = map[string]webhookEventPolicy{
	"ping": {
		decode:  decodeWebhookPayload[genericWebhookPayload],
		project: projectPingWebhookEvent,
	},
	"push": {
		decode:  decodeWebhookPayload[pushWebhookPayload],
		project: projectPushWebhookEvent,
	},
	"repository": {
		decode:  decodeWebhookPayload[repositoryWebhookPayload],
		project: projectRepositoryWebhookEvent,
	},
	"issues": {
		immediate: true,
		decode:    decodeWebhookPayload[issueWebhookPayload],
		project:   projectIssuesWebhookEvent,
	},
	"issue_comment": {
		immediate: true,
		decode:    decodeWebhookPayload[issueCommentWebhookPayload],
		project:   projectIssueCommentWebhookEvent,
	},
	"pull_request": {
		immediate: true,
		decode:    decodeWebhookPayload[pullRequestWebhookPayload],
		project:   projectPullRequestWebhookEvent,
	},
	"pull_request_review": {
		immediate: true,
		decode:    decodeWebhookPayload[pullRequestReviewWebhookPayload],
		project:   projectPullRequestReviewWebhookEvent,
	},
	"pull_request_review_comment": {
		immediate: true,
		decode:    decodeWebhookPayload[pullRequestReviewCommentWebhookPayload],
		project:   projectPullRequestReviewCommentWebhookEvent,
	},
}

func decodeWebhookEvent(event string, payload []byte) (decodedWebhookEvent, error) {
	if policy, ok := webhookEventPolicyFor(event); ok {
		return policy.decode(event, payload)
	}
	return decodeWebhookPayload[genericWebhookPayload](event, payload)
}

func webhookEventPolicyFor(event string) (webhookEventPolicy, bool) {
	policy, ok := webhookEventPolicies[event]
	return policy, ok
}

func decodeWebhookPayload[T decodableWebhookPayload](event string, payload []byte) (decodedWebhookEvent, error) {
	var decodedPayload T
	if err := json.Unmarshal(payload, &decodedPayload); err != nil {
		return decodedWebhookEvent{}, err
	}
	repoRef, err := decodedPayload.webhookRepositoryRef()
	if err != nil {
		return decodedWebhookEvent{}, err
	}
	return decodedWebhookEvent{
		Event:   event,
		Action:  decodedPayload.webhookAction(),
		RepoRef: repoRef,
		Payload: any(decodedPayload),
	}, nil
}

func payloadAs[T any](event decodedWebhookEvent) (T, error) {
	payload, ok := event.Payload.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("webhook payload type mismatch for event %q", event.Event)
	}
	return payload, nil
}

func applyProjectionFollowUp(ctx context.Context, deps eventFollowUpDependencies, result eventProjectionResult, seenAt time.Time) error {
	if result.repositoryID == 0 {
		return nil
	}
	if result.followUp.noteRepositoryWebhook && deps.recorder != nil {
		if err := deps.recorder.NoteRepositoryWebhook(ctx, result.repositoryID, seenAt); err != nil {
			return err
		}
	}
	if result.followUp.enqueuePullRequestNumber != nil && deps.recorder != nil {
		if err := deps.recorder.EnqueuePullRequestRefresh(ctx, result.repositoryID, *result.followUp.enqueuePullRequestNumber, seenAt); err != nil {
			return err
		}
	}
	if strings.TrimSpace(result.followUp.staleBaseRef) != "" && deps.staler != nil {
		if err := deps.staler.MarkBaseRefStale(ctx, result.repositoryID, result.followUp.staleBaseRef); err != nil {
			return err
		}
	}
	if result.followUp.markInventoryRefresh && deps.recorder != nil {
		if err := deps.recorder.MarkInventoryNeedsRefresh(ctx, result.repositoryID, seenAt); err != nil {
			return err
		}
	}
	return nil
}

func lookupRepositoryID(ctx context.Context, deps eventProjectionDependencies, repoRef *repositoryRef) (uint, error) {
	if deps.repositoryIDLookup == nil {
		return 0, nil
	}
	return deps.repositoryIDLookup(ctx, repoRef)
}

func projectPingWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	repositoryID, err := lookupRepositoryID(ctx, deps, event.RepoRef)
	if err != nil || repositoryID == 0 {
		return eventProjectionResult{repositoryID: repositoryID}, err
	}
	return eventProjectionResult{
		repositoryID: repositoryID,
		followUp: eventFollowUpActions{
			noteRepositoryWebhook: true,
		},
	}, nil
}

func projectPushWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[pushWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if deps.projector != nil {
		repo, upsertErr := deps.projector.UpsertRepository(ctx, payload.Repository)
		if upsertErr == nil {
			return eventProjectionResult{
				repositoryID: repo.ID,
				followUp: eventFollowUpActions{
					staleBaseRef:         payload.Ref,
					markInventoryRefresh: true,
				},
			}, nil
		}
	}
	repositoryID, err := lookupRepositoryID(ctx, deps, event.RepoRef)
	if err != nil || repositoryID == 0 {
		return eventProjectionResult{repositoryID: repositoryID}, err
	}
	return eventProjectionResult{
		repositoryID: repositoryID,
		followUp: eventFollowUpActions{
			noteRepositoryWebhook: true,
		},
	}, nil
}

func projectRepositoryWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[repositoryWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{
		repositoryID: repo.ID,
		followUp: eventFollowUpActions{
			noteRepositoryWebhook: true,
		},
	}, nil
}

func projectIssuesWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[issueWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if strings.TrimSpace(payload.Action) == "deleted" {
		if err := deps.projector.DeleteIssue(ctx, repo.ID, payload.Issue); err != nil {
			return eventProjectionResult{}, err
		}
		return eventProjectionResult{repositoryID: repo.ID}, nil
	}
	if _, err := deps.projector.UpsertIssue(ctx, repo.ID, payload.Issue); err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{repositoryID: repo.ID}, nil
}

func projectIssueCommentWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[issueCommentWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if _, err := deps.projector.UpsertIssue(ctx, repo.ID, payload.Issue); err != nil {
		return eventProjectionResult{}, err
	}
	if strings.TrimSpace(payload.Action) == "deleted" {
		if err := deps.projector.DeleteIssueComment(ctx, repo.ID, payload.Comment); err != nil {
			return eventProjectionResult{}, err
		}
		return eventProjectionResult{repositoryID: repo.ID}, nil
	}
	if err := deps.projector.UpsertIssueComment(ctx, repo.ID, payload.Comment); err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{repositoryID: repo.ID}, nil
}

func projectPullRequestWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[pullRequestWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payload.PullRequest); err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{
		repositoryID: repo.ID,
		followUp: eventFollowUpActions{
			noteRepositoryWebhook:    true,
			enqueuePullRequestNumber: intPtr(payload.PullRequest.Number),
			markInventoryRefresh:     payload.needsInventoryRefresh(),
		},
	}, nil
}

func projectPullRequestReviewWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[pullRequestReviewWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payload.PullRequest); err != nil {
		return eventProjectionResult{}, err
	}
	if err := deps.projector.UpsertPullRequestReview(ctx, repo.ID, payload.PullRequest.Number, payload.Review); err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{repositoryID: repo.ID}, nil
}

func projectPullRequestReviewCommentWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event decodedWebhookEvent) (eventProjectionResult, error) {
	payload, err := payloadAs[pullRequestReviewCommentWebhookPayload](event)
	if err != nil {
		return eventProjectionResult{}, err
	}
	repo, err := deps.projector.UpsertRepository(ctx, payload.Repository)
	if err != nil {
		return eventProjectionResult{}, err
	}
	if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payload.PullRequest); err != nil {
		return eventProjectionResult{}, err
	}
	if strings.TrimSpace(payload.Action) == "deleted" {
		if err := deps.projector.DeletePullRequestReviewComment(ctx, repo.ID, payload.Comment); err != nil {
			return eventProjectionResult{}, err
		}
		return eventProjectionResult{repositoryID: repo.ID}, nil
	}
	if err := deps.projector.UpsertPullRequestReviewComment(ctx, repo.ID, payload.PullRequest.Number, payload.Comment); err != nil {
		return eventProjectionResult{}, err
	}
	return eventProjectionResult{repositoryID: repo.ID}, nil
}

func intPtr(value int) *int {
	return &value
}
