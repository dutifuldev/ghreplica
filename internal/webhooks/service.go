package webhooks

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"gorm.io/gorm"
)

type WebhookProjector interface {
	UpsertRepository(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error)
	UpsertIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) (database.Issue, error)
	UpsertPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) error
	UpsertIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error
	UpsertPullRequestReview(ctx context.Context, repositoryID uint, pullNumber int, review gh.PullRequestReviewResponse) error
	UpsertPullRequestReviewComment(ctx context.Context, repositoryID uint, pullNumber int, comment gh.PullRequestReviewCommentResponse) error
}

type pullRequestIndexer interface {
	SyncPullRequestIndex(ctx context.Context, owner, repo string, repositoryID uint, pull gh.PullRequestResponse) error
}

type BaseRefStaler interface {
	MarkBaseRefStale(ctx context.Context, repositoryID uint, ref string) error
}

type RepoChangeWebhookRecorder interface {
	NoteRepositoryWebhook(ctx context.Context, repositoryID uint, seenAt time.Time) error
	EnqueuePullRequestRefresh(ctx context.Context, repositoryID uint, number int, seenAt time.Time) error
	MarkInventoryNeedsRefresh(ctx context.Context, repositoryID uint, seenAt time.Time) error
}

type DeliveryDispatcher interface {
	EnqueueWebhookDeliveryTx(ctx context.Context, tx *sql.Tx, deliveryID string) error
}

type Dependencies struct {
	Projector WebhookProjector
	Staler    BaseRefStaler
	Recorder  RepoChangeWebhookRecorder
	Search    *searchindex.Service
}

type Service struct {
	acceptor  *Acceptor
	processor *Processor
}

var supportedWebhookEvents = map[string]struct{}{
	"ping":                        {},
	"issues":                      {},
	"issue_comment":               {},
	"pull_request":                {},
	"pull_request_review":         {},
	"pull_request_review_comment": {},
	"push":                        {},
	"repository":                  {},
}

func NewService(acceptorDB, processorDB *gorm.DB, deps Dependencies) *Service {
	if processorDB == nil {
		processorDB = acceptorDB
	}
	search := deps.Search
	if search == nil {
		search = searchindex.NewService(processorDB)
	}
	return &Service{
		acceptor:  NewAcceptor(acceptorDB),
		processor: NewProcessor(processorDB, deps.Projector, deps.Staler, deps.Recorder, search),
	}
}

func (s *Service) SetDispatcher(dispatcher DeliveryDispatcher) {
	s.acceptor.SetDispatcher(dispatcher)
}

func (s *Service) HandleWebhook(ctx context.Context, deliveryID, event string, headers http.Header, payload []byte) error {
	return s.acceptor.HandleWebhook(ctx, deliveryID, event, headers, payload)
}

func (s *Service) ProcessWebhookDelivery(ctx context.Context, deliveryID string) error {
	return s.processor.ProcessWebhookDelivery(ctx, deliveryID)
}
