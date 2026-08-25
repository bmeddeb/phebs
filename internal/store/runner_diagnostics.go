package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/diagnostics"
)

const (
	JobLifecycleSchema        = "phebs-job-lifecycle-v1"
	MaxJobLifecycleReportSize = 16 << 10
)

// JobLifecycleSink is an injectable operational-log seam. Reporting is
// advisory: failures and panics never change queue state or handler outcome.
type JobLifecycleSink func(report []byte) error

// JobLifecycleReport is one bounded, source-free queue transition. It omits
// handler errors deliberately; classified failures remain in the existing log
// and durable job row without expanding this receipt into arbitrary text.
type JobLifecycleReport struct {
	Schema        string  `json:"schema"`
	Event         string  `json:"event"`
	JobID         string  `json:"job_id"`
	Kind          JobKind `json:"kind"`
	Target        string  `json:"target"`
	Attempt       int     `json:"attempt"`
	QueueWaitMS   int64   `json:"queue_wait_ms"`
	HandleMS      int64   `json:"handle_ms"`
	Outcome       string  `json:"outcome"`
	NextNotBefore string  `json:"next_not_before,omitempty"`
}

func (r *Runner) emitLifecycle(
	event string,
	job Job,
	handleDuration time.Duration,
	outcome string,
	nextNotBefore *time.Time,
) {
	if r == nil || (!r.Diagnostics && r.LifecycleReports == nil) {
		return
	}
	report := JobLifecycleReport{
		Schema: JobLifecycleSchema, Event: event,
		JobID: job.ID, Kind: job.Kind, Target: job.Target,
		Attempt: job.Attempts + 1, QueueWaitMS: jobQueueWaitMS(job),
		HandleMS: durationMS(handleDuration), Outcome: outcome,
	}
	if nextNotBefore != nil {
		report.NextNotBefore = nextNotBefore.UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(report)
	if err != nil {
		diagnostics.Logf("encode job lifecycle: %v", err)
		r.failLifecycleReport(errors.New("encode job lifecycle"))
		return
	}
	if len(data) > MaxJobLifecycleReportSize {
		diagnostics.Logf("encode job lifecycle: report exceeds %d bytes", MaxJobLifecycleReportSize)
		r.failLifecycleReport(errors.New("job lifecycle report exceeds its bound"))
		return
	}
	sink := r.LifecycleReports
	if sink == nil {
		sink = func(report []byte) error {
			diagnostics.Logf("job lifecycle: %s", report)
			return nil
		}
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				diagnostics.Logf("job lifecycle sink panic (%T)", recovered)
				r.failLifecycleReport(errors.New("job lifecycle sink panicked"))
			}
		}()
		if err := sink(data); err != nil {
			diagnostics.Logf("job lifecycle sink error (%T)", err)
			r.failLifecycleReport(errors.New("job lifecycle sink failed"))
		}
	}()
}

func (r *Runner) failLifecycleReport(err error) {
	if r == nil || r.LifecycleReportFailure == nil || err == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			diagnostics.Logf("job lifecycle failure callback panic (%T)", recovered)
		}
	}()
	r.LifecycleReportFailure(err)
}

func jobQueueWaitMS(job Job) int64 {
	if job.ClaimedAt == nil || job.CreatedAt.IsZero() {
		return 0
	}
	eligibleAt := job.CreatedAt
	if job.NotBefore != nil && job.NotBefore.After(eligibleAt) {
		eligibleAt = *job.NotBefore
	}
	if job.ClaimedAt.Before(eligibleAt) {
		return 0
	}
	return durationMS(job.ClaimedAt.Sub(eligibleAt))
}

func durationMS(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}
