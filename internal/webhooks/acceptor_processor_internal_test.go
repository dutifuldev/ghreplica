package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recordingDeliveryDispatcher struct {
	deliveryIDs []string
}

func (d *recordingDeliveryDispatcher) EnqueueWebhookDeliveryTx(_ context.Context, _ *sql.Tx, deliveryID string) error {
	d.deliveryIDs = append(d.deliveryIDs, deliveryID)
	return nil
}

type failingDeliveryDispatcher struct {
	err error
}

func (d *failingDeliveryDispatcher) EnqueueWebhookDeliveryTx(context.Context, *sql.Tx, string) error {
	return d.err
}

type immediateRecordingProjector struct {
	recordingProjector
	recordingRecorder
}

type immediateFailingProjector struct {
	failingProjector
	recordingRecorder
}

func TestAcceptorHandleWebhookConfigurationAndDeduplication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	payload := []byte(`{"repository":{"id":101,"full_name":"acme/widgets"}}`)

	err := (&Acceptor{}).HandleWebhook(ctx, "delivery-1", "workflow_job", http.Header{}, payload)
	require.ErrorContains(t, err, "SQL database handle is not configured")

	db := openWebhooksInternalTestDB(t)
	acceptor := NewAcceptor(db, nil)
	err = acceptor.HandleWebhook(ctx, "delivery-2", "workflow_job", http.Header{}, payload)
	require.ErrorContains(t, err, "dispatcher is not configured")

	dispatcher := &recordingDeliveryDispatcher{}
	acceptor.SetDispatcher(dispatcher)

	require.NoError(t, acceptor.HandleWebhook(ctx, "delivery-3", "workflow_job", http.Header{}, payload))
	require.NoError(t, acceptor.HandleWebhook(ctx, "delivery-3", "workflow_job", http.Header{}, payload))
	require.Equal(t, []string{"delivery-3"}, dispatcher.deliveryIDs)

	var count int64
	require.NoError(t, db.Model(&database.WebhookDelivery{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestProcessorProcessWebhookDeliveryBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	projector := &recordingProjector{repositoryID: 42}
	recorder := &recordingRecorder{}
	processor := NewProcessor(db, projector, nil, recorder)

	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "missing"))

	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "processed",
		Event:       "workflow_job",
		PayloadJSON: []byte(`{}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
		ProcessedAt: &now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "processed"))

	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "nil-repo",
		Event:       "workflow_job",
		PayloadJSON: []byte(`{}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "nil-repo"))

	var nilRepoDelivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "nil-repo").First(&nilRepoDelivery).Error)
	require.NotNil(t, nilRepoDelivery.ProcessedAt)

	repo := database.Repository{ID: 10, GitHubID: 101, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repo).Error)
	trackedDisabled := database.TrackedRepository{
		ID:           1,
		Owner:        "acme",
		Name:         "widgets",
		FullName:     "acme/widgets",
		Enabled:      false,
		RepositoryID: &repo.ID,
	}
	require.NoError(t, db.Create(&trackedDisabled).Error)

	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "tracked-disabled",
		Event:       "workflow_job",
		PayloadJSON: []byte(`{"repository":{"id":101,"full_name":"acme/widgets"}}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "tracked-disabled"))

	var disabledDelivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "tracked-disabled").First(&disabledDelivery).Error)
	require.NotNil(t, disabledDelivery.RepositoryID)
	require.Equal(t, repo.ID, *disabledDelivery.RepositoryID)

	projectedRepo := database.Repository{ID: 42, GitHubID: 202, OwnerLogin: "acme", Name: "projected", FullName: "acme/projected"}
	require.NoError(t, db.Create(&projectedRepo).Error)
	trackedEnabled := database.TrackedRepository{
		ID:                       2,
		Owner:                    "acme",
		Name:                     "projected",
		FullName:                 "acme/projected",
		Enabled:                  true,
		WebhookProjectionEnabled: true,
		RepositoryID:             &projectedRepo.ID,
		IssuesCompleteness:       "empty",
		PullsCompleteness:        "empty",
		CommentsCompleteness:     "empty",
		ReviewsCompleteness:      "empty",
	}
	require.NoError(t, db.Create(&trackedEnabled).Error)

	issuePayload, err := json.Marshal(map[string]any{
		"action": "opened",
		"repository": map[string]any{
			"id":        projectedRepo.GitHubID,
			"full_name": projectedRepo.FullName,
		},
		"issue": map[string]any{
			"id":         303,
			"number":     7,
			"title":      "Projected issue",
			"state":      "open",
			"html_url":   "https://github.com/acme/projected/issues/7",
			"url":        "https://api.github.com/repos/acme/projected/issues/7",
			"created_at": now,
			"updated_at": now,
		},
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "projected-issue",
		Event:       "issues",
		PayloadJSON: issuePayload,
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "projected-issue"))

	var projectedDelivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "projected-issue").First(&projectedDelivery).Error)
	require.NotNil(t, projectedDelivery.ProcessedAt)
	require.NotNil(t, projectedDelivery.RepositoryID)
	require.Equal(t, projectedRepo.ID, *projectedDelivery.RepositoryID)
	require.Equal(t, []int{7}, projector.upsertedIssues)

	var tracked database.TrackedRepository
	require.NoError(t, db.First(&tracked, trackedEnabled.ID).Error)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
}

func TestRepositoryLookupHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	repo := database.Repository{ID: 9, GitHubID: 999, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repo).Error)

	repositoryID, err := repositoryIDByRef(ctx, db, &repositoryRef{GitHubID: 999})
	require.NoError(t, err)
	require.Equal(t, repo.ID, repositoryID)

	repositoryID, err = repositoryIDByRef(ctx, db, &repositoryRef{FullName: "acme/widgets"})
	require.NoError(t, err)
	require.Equal(t, repo.ID, repositoryID)

	repositoryID, err = repositoryIDByRef(ctx, db, &repositoryRef{FullName: "missing/repo"})
	require.NoError(t, err)
	require.Zero(t, repositoryID)

	worker := NewDeliveryCleanupWorker(db, time.Minute, 0, 0)
	require.Equal(t, 15*time.Minute, worker.pollInterval)
	require.Equal(t, 500, worker.batchSize)
}

func TestAcceptorBuildAndInsertHelpers(t *testing.T) {
	t.Parallel()

	db := openWebhooksInternalTestDB(t)
	acceptor := NewAcceptor(db, nil)

	delivery, err := buildWebhookDelivery("delivery-helpers", http.Header{"X-Test": []string{"yes"}}, decodedWebhookEvent{
		Event:  "ping",
		Action: "ignored",
	}, []byte(`{"zen":"keep it logically awesome"}`), time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, "delivery-helpers", delivery.DeliveryID)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	tx, err := sqlDB.Begin()
	require.NoError(t, err)
	inserted, err := acceptor.insertWebhookDeliveryTx(context.Background(), tx, delivery)
	require.NoError(t, err)
	require.True(t, inserted)
	require.NoError(t, tx.Commit())

	tx, err = sqlDB.Begin()
	require.NoError(t, err)
	inserted, err = acceptor.insertWebhookDeliveryTx(context.Background(), tx, delivery)
	require.NoError(t, err)
	require.False(t, inserted)
	require.NoError(t, tx.Commit())
}

func TestProcessorProjectEventDirectBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	projector := &recordingProjector{repositoryID: 77}
	recorder := &recordingRecorder{}
	processor := NewProcessor(db, projector, nil, recorder)
	now := time.Now().UTC()

	result, err := processor.projectEvent(ctx, webhookEventPolicy{
		project: projectPushWebhookEvent,
	}, decodedWebhookEvent{
		Event:   "push",
		RepoRef: &repositoryRef{FullName: "acme/widgets"},
		Payload: pushWebhookPayload{
			Repository: gh.RepositoryResponse{FullName: "acme/widgets"},
			Ref:        "refs/heads/main",
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint(77), result.repositoryID)
	require.Equal(t, "refs/heads/main", result.followUp.staleBaseRef)

	issueCommentDeletePayload, err := json.Marshal(map[string]any{
		"action": "deleted",
		"repository": map[string]any{
			"id":        77,
			"full_name": "acme/widgets",
		},
		"issue": map[string]any{
			"id":         9001,
			"number":     7,
			"title":      "Issue",
			"state":      "open",
			"html_url":   "https://github.com/acme/widgets/issues/7",
			"url":        "https://api.github.com/repos/acme/widgets/issues/7",
			"created_at": now,
			"updated_at": now,
		},
		"comment": map[string]any{
			"id":         9101,
			"body":       "obsolete",
			"html_url":   "https://github.com/acme/widgets/issues/7#issuecomment-9101",
			"url":        "https://api.github.com/repos/acme/widgets/issues/comments/9101",
			"created_at": now,
			"updated_at": now,
		},
	})
	require.NoError(t, err)
	decoded, err := decodeWebhookEvent("issue_comment", issueCommentDeletePayload)
	require.NoError(t, err)
	result, err = processor.projectEvent(ctx, webhookEventPolicy{project: projectIssueCommentWebhookEvent}, decoded)
	require.NoError(t, err)
	require.Equal(t, uint(77), result.repositoryID)
	require.Equal(t, []int{7}, projector.upsertedIssues)
	require.Equal(t, []int64{9101}, projector.deletedIssueComments)

	reviewCommentCreatePayload, err := json.Marshal(map[string]any{
		"action": "created",
		"repository": map[string]any{
			"id":        77,
			"full_name": "acme/widgets",
		},
		"pull_request": map[string]any{
			"id":         9201,
			"number":     8,
			"title":      "PR",
			"state":      "open",
			"html_url":   "https://github.com/acme/widgets/pull/8",
			"url":        "https://api.github.com/repos/acme/widgets/pulls/8",
			"issue_url":  "https://api.github.com/repos/acme/widgets/issues/8",
			"diff_url":   "https://github.com/acme/widgets/pull/8.diff",
			"patch_url":  "https://github.com/acme/widgets/pull/8.patch",
			"created_at": now,
			"updated_at": now,
			"head":       map[string]any{"ref": "feature", "sha": "head"},
			"base":       map[string]any{"ref": "main", "sha": "base"},
		},
		"comment": map[string]any{
			"id":               9301,
			"body":             "nice",
			"path":             "main.go",
			"diff_hunk":        "@@",
			"pull_request_url": "https://api.github.com/repos/acme/widgets/pulls/8",
			"html_url":         "https://github.com/acme/widgets/pull/8#discussion_r9301",
			"url":              "https://api.github.com/repos/acme/widgets/pulls/comments/9301",
			"created_at":       now,
			"updated_at":       now,
		},
	})
	require.NoError(t, err)
	decoded, err = decodeWebhookEvent("pull_request_review_comment", reviewCommentCreatePayload)
	require.NoError(t, err)
	result, err = processor.projectEvent(ctx, webhookEventPolicy{project: projectPullRequestReviewCommentWebhookEvent}, decoded)
	require.NoError(t, err)
	require.Equal(t, uint(77), result.repositoryID)
	require.Contains(t, projector.upsertedPullRequests, 8)
	require.Equal(t, []int64{9301}, projector.upsertedReviewComments)

	service := NewService(db, nil, Dependencies{})
	require.NotNil(t, service)
}

func TestProcessorRejectsMalformedStoredPayload(t *testing.T) {
	t.Parallel()

	db := openWebhooksInternalTestDB(t)
	processor := NewProcessor(db, &recordingProjector{repositoryID: 1}, nil, &recordingRecorder{})
	now := time.Now().UTC()

	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "malformed",
		Event:       "issues",
		PayloadJSON: []byte(`{"action":`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)

	require.Error(t, processor.ProcessWebhookDelivery(context.Background(), "malformed"))
}

func TestProcessorPingProjectionAndUnsupportedEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	projector := &recordingProjector{repositoryID: 9}
	recorder := &recordingRecorder{}
	processor := NewProcessor(db, projector, nil, recorder)
	now := time.Now().UTC()

	repo := database.Repository{ID: 9, GitHubID: 101, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:                       3,
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		RepositoryID:             &repo.ID,
		Enabled:                  true,
		WebhookProjectionEnabled: true,
	}).Error)

	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "ping",
		Event:       "ping",
		PayloadJSON: []byte(`{"zen":"keep it logically awesome","repository":{"id":101,"full_name":"acme/widgets"}}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "ping"))

	var pingDelivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "ping").First(&pingDelivery).Error)
	require.NotNil(t, pingDelivery.ProcessedAt)
	require.NotNil(t, pingDelivery.RepositoryID)
	require.Equal(t, repo.ID, *pingDelivery.RepositoryID)

	decoded, err := decodeWebhookEvent("ping", []byte(`{"zen":"keep it logically awesome","repository":{"id":101,"full_name":"acme/widgets"}}`))
	require.NoError(t, err)
	result, err := processor.projectEvent(ctx, webhookEventPolicy{project: projectPingWebhookEvent}, decoded)
	require.NoError(t, err)
	require.Equal(t, repo.ID, result.repositoryID)
	require.True(t, result.followUp.noteRepositoryWebhook)

	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "unsupported",
		Event:       "workflow_job",
		PayloadJSON: []byte(`{"repository":{"id":101,"full_name":"acme/widgets"}}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)
	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "unsupported"))

	var unsupported database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "unsupported").First(&unsupported).Error)
	require.NotNil(t, unsupported.ProcessedAt)
	require.Nil(t, unsupported.RepositoryID)
}

func TestAcceptorImmediateProjectionWritesRepositoryID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	dispatcher := &recordingDeliveryDispatcher{}
	immediate := &immediateRecordingProjector{
		recordingProjector: recordingProjector{repositoryID: 42},
	}
	acceptor := NewAcceptor(db, func(*gorm.DB) ImmediateWebhookProjector { return immediate })
	acceptor.SetDispatcher(dispatcher)

	payload := []byte(`{
		"action":"opened",
		"repository":{"id":42,"full_name":"acme/widgets"},
		"pull_request":{
			"id":501,
			"number":9,
			"state":"open",
			"title":"PR",
			"head":{"ref":"feature","sha":"head"},
			"base":{"ref":"main","sha":"base"}
		}
	}`)
	require.NoError(t, acceptor.HandleWebhook(ctx, "delivery-immediate", "pull_request", http.Header{}, payload))

	var delivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "delivery-immediate").First(&delivery).Error)
	require.NotNil(t, delivery.RepositoryID)
	require.Equal(t, uint(42), *delivery.RepositoryID)
	require.Equal(t, []string{"delivery-immediate"}, dispatcher.deliveryIDs)
	require.Equal(t, []int{9}, immediate.upsertedPullRequests)
	require.Equal(t, []int{9}, immediate.enqueuedPulls)
	require.Equal(t, []uint{42}, immediate.markedInventory)
}

func TestAcceptorRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	db := openWebhooksInternalTestDB(t)
	acceptor := NewAcceptor(db, nil)
	acceptor.SetDispatcher(&recordingDeliveryDispatcher{})

	err := acceptor.HandleWebhook(context.Background(), "delivery-bad", "issues", http.Header{}, []byte(`{"action":`))
	require.Error(t, err)
}

func TestProcessorAssignsRepositoryIDWithoutTrackedRepositoryLink(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	processor := NewProcessor(db, &recordingProjector{repositoryID: 1}, nil, &recordingRecorder{})
	now := time.Now().UTC()

	repo := database.Repository{ID: 12, GitHubID: 212, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:                       4,
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		Enabled:                  true,
		WebhookProjectionEnabled: false,
	}).Error)
	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "lookup-only",
		Event:       "workflow_job",
		PayloadJSON: []byte(`{"repository":{"id":212,"full_name":"acme/widgets"}}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)

	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "lookup-only"))

	var delivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", "lookup-only").First(&delivery).Error)
	require.NotNil(t, delivery.ProcessedAt)
	require.NotNil(t, delivery.RepositoryID)
	require.Equal(t, repo.ID, *delivery.RepositoryID)
}

func TestProcessorProjectionBackfillsTrackedRepositoryID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openWebhooksInternalTestDB(t)
	now := time.Now().UTC()
	repo := database.Repository{ID: 15, GitHubID: 315, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:                       5,
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		Enabled:                  true,
		WebhookProjectionEnabled: true,
		IssuesCompleteness:       "empty",
	}).Error)

	processor := NewProcessor(db, &recordingProjector{repositoryID: repo.ID}, nil, &recordingRecorder{})
	require.NoError(t, db.Create(&database.WebhookDelivery{
		DeliveryID:  "projected-link",
		Event:       "issues",
		PayloadJSON: []byte(`{"action":"opened","repository":{"id":315,"full_name":"acme/widgets"},"issue":{"id":401,"number":7,"title":"Issue","state":"open"}}`),
		HeadersJSON: []byte(`{}`),
		ReceivedAt:  now,
	}).Error)

	require.NoError(t, processor.ProcessWebhookDelivery(ctx, "projected-link"))

	var tracked database.TrackedRepository
	require.NoError(t, db.First(&tracked, 5).Error)
	require.NotNil(t, tracked.RepositoryID)
	require.Equal(t, repo.ID, *tracked.RepositoryID)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
}

func TestAcceptorRollsBackOnProjectionAndDispatchErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	payload := []byte(`{
		"action":"opened",
		"repository":{"id":42,"full_name":"acme/widgets"},
		"issue":{"id":501,"number":9,"title":"Issue","state":"open"}
	}`)

	t.Run("projection error", func(t *testing.T) {
		db := openWebhooksInternalTestDB(t)
		acceptor := NewAcceptor(db, func(*gorm.DB) ImmediateWebhookProjector {
			return &immediateFailingProjector{
				failingProjector: failingProjector{
					recordingProjector: recordingProjector{repositoryID: 42},
					issueErr:           errors.New("issue failed"),
				},
			}
		})
		acceptor.SetDispatcher(&recordingDeliveryDispatcher{})

		err := acceptor.HandleWebhook(ctx, "projection-fail", "issues", http.Header{}, payload)
		require.ErrorContains(t, err, "issue failed")

		var count int64
		require.NoError(t, db.Model(&database.WebhookDelivery{}).Where("delivery_id = ?", "projection-fail").Count(&count).Error)
		require.Zero(t, count)
	})

	t.Run("dispatch error", func(t *testing.T) {
		db := openWebhooksInternalTestDB(t)
		acceptor := NewAcceptor(db, func(*gorm.DB) ImmediateWebhookProjector {
			return &immediateRecordingProjector{
				recordingProjector: recordingProjector{repositoryID: 42},
			}
		})
		acceptor.SetDispatcher(&failingDeliveryDispatcher{err: errors.New("dispatch failed")})

		err := acceptor.HandleWebhook(ctx, "dispatch-fail", "issues", http.Header{}, payload)
		require.ErrorContains(t, err, "dispatch failed")

		var count int64
		require.NoError(t, db.Model(&database.WebhookDelivery{}).Where("delivery_id = ?", "dispatch-fail").Count(&count).Error)
		require.Zero(t, count)
	})
}

func openWebhooksInternalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "webhooks-internal.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))
	return db
}
