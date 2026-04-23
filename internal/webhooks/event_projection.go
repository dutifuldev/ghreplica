package webhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"gorm.io/gorm"
)

type eventProjectionDependencies struct {
	db        *gorm.DB
	projector WebhookProjector
	staler    BaseRefStaler
	recorder  RepoChangeWebhookRecorder
	search    *searchindex.Service
}

func supportsImmediateProjection(event string) bool {
	switch event {
	case "issues", "issue_comment", "pull_request", "pull_request_review", "pull_request_review_comment":
		return true
	default:
		return false
	}
}

func projectWebhookEvent(ctx context.Context, deps eventProjectionDependencies, event, action string, payload []byte, repoRef *repositoryRef) (uint, error) {
	if deps.projector == nil {
		return 0, nil
	}

	switch event {
	case "ping", "push":
		if event == "push" && deps.staler != nil {
			var payloadEnvelope struct {
				Repository gh.RepositoryResponse `json:"repository"`
				Ref        string                `json:"ref"`
			}
			if err := json.Unmarshal(payload, &payloadEnvelope); err == nil {
				repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
				if err == nil {
					seenAt := time.Now().UTC()
					_ = deps.staler.MarkBaseRefStale(ctx, repo.ID, payloadEnvelope.Ref)
					if deps.recorder != nil {
						_ = deps.recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt)
					}
					return repo.ID, nil
				}
			}
		}
		repositoryID, err := repositoryIDByRef(ctx, deps.db, repoRef)
		if err == nil && repositoryID != 0 && deps.recorder != nil {
			_ = deps.recorder.NoteRepositoryWebhook(ctx, repositoryID, time.Now().UTC())
		}
		return repositoryID, err
	case "repository":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if deps.recorder != nil {
			_ = deps.recorder.NoteRepositoryWebhook(ctx, repo.ID, time.Now().UTC())
		}
		return repo.ID, nil
	case "issues":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
			Issue      gh.IssueResponse      `json:"issue"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := deps.db.WithContext(ctx).
				Where("repository_id = ? AND number = ?", repo.ID, payloadEnvelope.Issue.Number).
				Delete(&database.Issue{}).Error; err != nil {
				return 0, err
			}
			if deps.search != nil {
				if err := deps.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssue, payloadEnvelope.Issue.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if _, err := deps.projector.UpsertIssue(ctx, repo.ID, payloadEnvelope.Issue); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "issue_comment":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse   `json:"repository"`
			Issue      gh.IssueResponse        `json:"issue"`
			Comment    gh.IssueCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if _, err := deps.projector.UpsertIssue(ctx, repo.ID, payloadEnvelope.Issue); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := deps.db.WithContext(ctx).
				Where("github_id = ?", payloadEnvelope.Comment.ID).
				Delete(&database.IssueComment{}).Error; err != nil {
				return 0, err
			}
			if deps.search != nil {
				if err := deps.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssueComment, payloadEnvelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := deps.projector.UpsertIssueComment(ctx, repo.ID, payloadEnvelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse  `json:"repository"`
			PullRequest gh.PullRequestResponse `json:"pull_request"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if deps.recorder != nil {
			seenAt := time.Now().UTC()
			if err := deps.recorder.NoteRepositoryWebhook(ctx, repo.ID, seenAt); err != nil {
				return 0, err
			}
			if err := deps.recorder.EnqueuePullRequestRefresh(ctx, repo.ID, payloadEnvelope.PullRequest.Number, seenAt); err != nil {
				return 0, err
			}
			if pullRequestWebhookNeedsInventoryRefresh(action, payload) {
				if err := deps.recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt); err != nil {
					return 0, err
				}
			}
		}
		return repo.ID, nil
	case "pull_request_review":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse        `json:"repository"`
			PullRequest gh.PullRequestResponse       `json:"pull_request"`
			Review      gh.PullRequestReviewResponse `json:"review"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if err := deps.projector.UpsertPullRequestReview(ctx, repo.ID, payloadEnvelope.PullRequest.Number, payloadEnvelope.Review); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request_review_comment":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse               `json:"repository"`
			PullRequest gh.PullRequestResponse              `json:"pull_request"`
			Comment     gh.PullRequestReviewCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := deps.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := deps.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := deps.db.WithContext(ctx).
				Where("github_id = ?", payloadEnvelope.Comment.ID).
				Delete(&database.PullRequestReviewComment{}).Error; err != nil {
				return 0, err
			}
			if deps.search != nil {
				if err := deps.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypePullRequestReviewComment, payloadEnvelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := deps.projector.UpsertPullRequestReviewComment(ctx, repo.ID, payloadEnvelope.PullRequest.Number, payloadEnvelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	default:
		return repositoryIDByRef(ctx, deps.db, repoRef)
	}
}
