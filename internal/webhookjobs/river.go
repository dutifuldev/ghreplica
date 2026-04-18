package webhookjobs

import (
	"context"
	"database/sql"
	"math"
	"math/rand/v2"
	"time"

	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
)

const (
	QueueWebhookProjection      = "webhook_projection"
	JobKindGitHubWebhookProcess = "github_webhook_process"

	defaultQueueConcurrency = 6
	defaultJobTimeout       = 30 * time.Second
	defaultMaxAttempts      = 8
	defaultUniquePeriod     = 7 * 24 * time.Hour
	defaultRetryCap         = 30 * time.Minute
)

type Config struct {
	QueueConcurrency int
	JobTimeout       time.Duration
	MaxAttempts      int
}

func (c Config) withDefaults() Config {
	if c.QueueConcurrency <= 0 {
		c.QueueConcurrency = defaultQueueConcurrency
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = defaultJobTimeout
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultMaxAttempts
	}
	return c
}

type GitHubWebhookProcessArgs struct {
	DeliveryID string `json:"delivery_id" river:"unique"`
}

func (GitHubWebhookProcessArgs) Kind() string { return JobKindGitHubWebhookProcess }

type GitHubWebhookProcessWorker struct {
	river.WorkerDefaults[GitHubWebhookProcessArgs]

	processor *webhooks.Service
}

func (w *GitHubWebhookProcessWorker) Work(ctx context.Context, job *river.Job[GitHubWebhookProcessArgs]) error {
	return w.processor.ProcessWebhookDelivery(ctx, job.Args.DeliveryID)
}

type Dispatcher struct {
	client      *river.Client[*sql.Tx]
	maxAttempts int
}

func (d *Dispatcher) EnqueueWebhookDeliveryTx(ctx context.Context, tx *sql.Tx, deliveryID string) error {
	_, err := d.client.InsertTx(ctx, tx, GitHubWebhookProcessArgs{DeliveryID: deliveryID}, &river.InsertOpts{
		MaxAttempts: d.maxAttempts,
		Queue:       QueueWebhookProjection,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: defaultUniquePeriod,
		},
	})
	return err
}

func NewClient(sqlDB *sql.DB, processor *webhooks.Service, cfg Config) (*river.Client[*sql.Tx], *Dispatcher, error) {
	cfg = cfg.withDefaults()

	workers := river.NewWorkers()
	river.AddWorker(workers, &GitHubWebhookProcessWorker{processor: processor})

	client, err := river.NewClient(riverdatabasesql.New(sqlDB), &river.Config{
		JobTimeout:  cfg.JobTimeout,
		MaxAttempts: cfg.MaxAttempts,
		Queues: map[string]river.QueueConfig{
			QueueWebhookProjection: {MaxWorkers: cfg.QueueConcurrency},
		},
		RetryPolicy: &cappedRetryPolicy{capDelay: defaultRetryCap},
		Workers:     workers,
	})
	if err != nil {
		return nil, nil, err
	}

	return client, &Dispatcher{client: client, maxAttempts: cfg.MaxAttempts}, nil
}

type cappedRetryPolicy struct {
	capDelay time.Duration
}

func (p *cappedRetryPolicy) NextRetry(job *rivertype.JobRow) time.Time {
	now := time.Now().UTC()
	attempt := len(job.Errors) + 1

	maxSeconds := p.capDelay.Seconds()
	if maxSeconds <= 0 {
		maxSeconds = defaultRetryCap.Seconds()
	}

	retrySeconds := math.Pow(float64(attempt), 4)
	retrySeconds = min(retrySeconds, maxSeconds)
	retrySeconds += retrySeconds * (rand.Float64()*0.2 - 0.1)
	retrySeconds = min(retrySeconds, maxSeconds)
	if retrySeconds < 0 {
		retrySeconds = 0
	}

	return now.Add(time.Duration(retrySeconds * float64(time.Second)))
}
