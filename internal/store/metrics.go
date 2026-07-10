package store

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	jobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "phebs_jobs_total",
		Help: "Job executions by final disposition (done, failed, requeued, reaped).",
	}, []string{"kind", "result"})

	jobErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "phebs_job_errors_total",
		Help: "Job failures by taxonomy class (T3.3).",
	}, []string{"kind", "class"})
)

func recordJob(kind JobKind, result string) {
	jobsTotal.WithLabelValues(string(kind), result).Inc()
}

func recordJobError(kind JobKind, err error) {
	jobErrors.WithLabelValues(string(kind), string(Classify(err))).Inc()
}
