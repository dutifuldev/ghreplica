package webhookjobs

import (
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
)

func TestConfigWithDefaults(t *testing.T) {
	cfg := (Config{}).withDefaults()
	require.Equal(t, defaultQueueConcurrency, cfg.QueueConcurrency)
	require.Equal(t, defaultJobTimeout, cfg.JobTimeout)
	require.Equal(t, defaultMaxAttempts, cfg.MaxAttempts)

	cfg = (Config{
		QueueConcurrency: 3,
		JobTimeout:       time.Minute,
		MaxAttempts:      9,
	}).withDefaults()
	require.Equal(t, 3, cfg.QueueConcurrency)
	require.Equal(t, time.Minute, cfg.JobTimeout)
	require.Equal(t, 9, cfg.MaxAttempts)
}

func TestGitHubWebhookProcessArgsKind(t *testing.T) {
	require.Equal(t, JobKindGitHubWebhookProcess, (GitHubWebhookProcessArgs{}).Kind())
}

func TestCappedRetryPolicyNextRetryHonorsCap(t *testing.T) {
	policy := &cappedRetryPolicy{capDelay: 2 * time.Minute}
	before := time.Now().UTC()
	retryAt := policy.NextRetry(&rivertype.JobRow{})
	require.False(t, retryAt.Before(before))
	require.LessOrEqual(t, retryAt.Sub(before), 2*time.Minute+3*time.Second)

	job := &rivertype.JobRow{
		Errors: make([]rivertype.AttemptError, 12),
	}
	before = time.Now().UTC()
	retryAt = policy.NextRetry(job)
	require.False(t, retryAt.Before(before))
	require.LessOrEqual(t, retryAt.Sub(before), 2*time.Minute+3*time.Second)
}
