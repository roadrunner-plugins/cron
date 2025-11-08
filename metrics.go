package cron

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsClient handles Prometheus metrics for the cron plugin
type metricsClient struct {
	// Metrics
	executionsTotal *prometheus.CounterVec
	skippedTotal    *prometheus.CounterVec
	timeoutTotal    *prometheus.CounterVec
	durationSeconds *prometheus.HistogramVec
	runningJobs     *prometheus.GaugeVec
}

// newMetricsClient creates and initializes a new metrics client
func newMetricsClient(jobs []*Job) *metricsClient {
	m := &metricsClient{}

	// Counter: total executions
	m.executionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "rr_cron",
			Name:      "executions_total",
			Help:      "Total number of cron job executions",
		},
		[]string{"job_name", "status"},
	)

	// Counter: skipped executions
	m.skippedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "rr_cron",
			Name:      "skipped_total",
			Help:      "Total number of skipped cron job executions due to overlap prevention",
		},
		[]string{"job_name"},
	)

	// Counter: timeout executions
	m.timeoutTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "rr_cron",
			Name:      "timeout_total",
			Help:      "Total number of cron job executions terminated due to timeout",
		},
		[]string{"job_name"},
	)

	// Histogram: execution duration
	m.durationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "rr_cron",
			Name:      "execution_duration_seconds",
			Help:      "Cron job execution duration in seconds",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
		},
		[]string{"job_name"},
	)

	// Gauge: running jobs
	m.runningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "rr_cron",
			Name:      "running_jobs",
			Help:      "Number of currently running cron job instances",
		},
		[]string{"job_name"},
	)

	// Initialize gauges to 0 for all jobs
	for _, job := range jobs {
		m.runningJobs.WithLabelValues(job.config.Name).Set(0)
	}

	return m
}

// recordExecution records a job execution
func (m *metricsClient) recordExecution(jobName string, status string) {
	if m == nil || m.executionsTotal == nil {
		return
	}
	m.executionsTotal.WithLabelValues(jobName, status).Inc()
}

// recordSkipped records a skipped execution
func (m *metricsClient) recordSkipped(jobName string) {
	if m == nil || m.skippedTotal == nil {
		return
	}
	m.skippedTotal.WithLabelValues(jobName).Inc()
}

// recordTimeout records a timeout
func (m *metricsClient) recordTimeout(jobName string) {
	if m == nil || m.timeoutTotal == nil {
		return
	}
	m.timeoutTotal.WithLabelValues(jobName).Inc()
}

// recordDuration records execution duration
func (m *metricsClient) recordDuration(jobName string, duration time.Duration) {
	if m == nil || m.durationSeconds == nil {
		return
	}
	m.durationSeconds.WithLabelValues(jobName).Observe(duration.Seconds())
}

// setRunning sets the number of running instances
func (m *metricsClient) setRunning(jobName string, value float64) {
	if m == nil || m.runningJobs == nil {
		return
	}
	m.runningJobs.WithLabelValues(jobName).Set(value)
}
