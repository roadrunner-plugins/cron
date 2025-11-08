package cron

import (
	"context"
	"sync"
	"time"

	"github.com/adhocore/gronx"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	// PluginName is the name of the cron plugin
	PluginName = "cron"

	// defaultGracePeriod is the default time to wait for running jobs during shutdown
	defaultGracePeriod = 30 * time.Second

	// defaultTimezone is the default timezone for cron schedule interpretation
	defaultTimezone = "UTC"

	// defaultMaxLogSize is the default maximum log file size before rotation (10MB)
	defaultMaxLogSize = 10 * 1024 * 1024

	// defaultMaxLogFiles is the default number of rotated log files to keep
	defaultMaxLogFiles = 5

	// forcefulKillDelay is the time to wait after SIGTERM before sending SIGKILL
	forcefulKillDelay = 5 * time.Second

	// stderrBufferSize is the maximum size of stderr to buffer for error reporting
	stderrBufferSize = 1024
)

// Configurer interface for configuration access
type Configurer interface {
	UnmarshalKey(name string, out interface{}) error
	Has(name string) bool
}

// Logger interface for logging
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// StatProvider interface for Prometheus metrics
type StatProvider interface {
	MetricsCollector() []interface{}
}

// Plugin represents the cron scheduler plugin
type Plugin struct {
	log *zap.Logger
	cfg *Config

	// Scheduler state
	jobs          []*Job
	location      *time.Location
	gracePeriod   time.Duration
	stopChan      chan struct{}
	wg            sync.WaitGroup
	metricsClient *metricsClient

	// Shutdown context
	ctx    context.Context
	cancel context.CancelFunc
}

// Init initializes the cron plugin
func (p *Plugin) Init(cfg Configurer, log Logger) error {
	const op = errors.Op("cron_plugin_init")

	// Check if plugin is configured
	if !cfg.Has(PluginName) {
		return errors.E(op, errors.Disabled)
	}

	// Initialize logger
	p.log = log.NamedLogger(PluginName)

	// Parse configuration
	p.cfg = &Config{}
	err := cfg.UnmarshalKey(PluginName, p.cfg)
	if err != nil {
		return errors.E(op, err)
	}

	// Validate configuration
	err = p.cfg.Validate()
	if err != nil {
		return errors.E(op, err)
	}

	// Set defaults
	if p.cfg.Timezone == "" {
		p.cfg.Timezone = defaultTimezone
	}

	if p.cfg.GracePeriod == "" {
		p.gracePeriod = defaultGracePeriod
	} else {
		duration, err := time.ParseDuration(p.cfg.GracePeriod)
		if err != nil {
			return errors.E(op, err)
		}
		p.gracePeriod = duration
	}

	// Load timezone
	location, err := time.LoadLocation(p.cfg.Timezone)
	if err != nil {
		return errors.E(op, err)
	}
	p.location = location

	// Initialize jobs
	p.jobs = make([]*Job, 0, len(p.cfg.Jobs))
	gron := gronx.New()

	for _, jobCfg := range p.cfg.Jobs {
		// Validate cron expression
		if !gron.IsValid(jobCfg.Schedule) {
			return errors.E(op, errors.Errorf("invalid cron expression for job '%s': %s", jobCfg.Name, jobCfg.Schedule))
		}

		// Set job defaults
		if jobCfg.MaxLogSize == 0 {
			jobCfg.MaxLogSize = defaultMaxLogSize
		}
		if jobCfg.MaxLogFiles == 0 {
			jobCfg.MaxLogFiles = defaultMaxLogFiles
		}

		// Create job
		job := &Job{
			config:   jobCfg,
			gron:     gron,
			location: p.location,
			log:      p.log,
			mutex:    &sync.Mutex{},
			running:  false,
		}

		// Initialize log writer if log file is configured
		if jobCfg.LogFile != "" {
			writer, err := newRotatingWriter(jobCfg.LogFile, jobCfg.MaxLogSize, jobCfg.MaxLogFiles)
			if err != nil {
				return errors.E(op, err)
			}
			job.logWriter = writer
		}

		p.jobs = append(p.jobs, job)

		p.log.Debug("scheduled job",
			zap.String("name", jobCfg.Name),
			zap.String("schedule", jobCfg.Schedule),
			zap.String("command", jobCfg.Command),
		)
	}

	p.log.Info("cron plugin initialized", zap.Int("jobs_count", len(p.jobs)))

	// Create shutdown context
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.stopChan = make(chan struct{})

	return nil
}

// Serve starts the cron scheduler
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	// Initialize metrics
	p.metricsClient = newMetricsClient(p.jobs)

	// Start scheduler for each job
	for _, job := range p.jobs {
		p.wg.Add(1)
		go p.scheduleJob(job)
	}

	return errCh
}

// Stop gracefully stops the cron scheduler
func (p *Plugin) Stop(ctx context.Context) error {
	p.log.Info("stopping cron scheduler")

	// Signal all schedulers to stop
	close(p.stopChan)

	// Cancel context to stop all running commands
	p.cancel()

	// Count running jobs
	runningCount := 0
	for _, job := range p.jobs {
		job.mutex.Lock()
		if job.running {
			runningCount++
		}
		job.mutex.Unlock()
	}

	if runningCount > 0 {
		p.log.Info("waiting for running jobs",
			zap.Int("count", runningCount),
			zap.Duration("grace_period", p.gracePeriod),
		)

		// Wait for jobs with grace period
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			p.log.Info("all jobs completed gracefully")
		case <-time.After(p.gracePeriod):
			p.log.Warn("grace period expired, forcefully terminating remaining jobs")
			// Forceful termination happens via context cancellation in executeCommand
		case <-ctx.Done():
			p.log.Warn("shutdown context cancelled")
			return ctx.Err()
		}
	}

	// Close all log writers
	for _, job := range p.jobs {
		if job.logWriter != nil {
			if err := job.logWriter.Close(); err != nil {
				p.log.Error("failed to close log writer",
					zap.String("job", job.config.Name),
					zap.Error(err),
				)
			}
		}
	}

	p.log.Info("cron scheduler stopped")
	return nil
}

// Name returns the plugin name
func (p *Plugin) Name() string {
	return PluginName
}

// Weight returns the plugin weight for dependency resolution
func (p *Plugin) Weight() uint {
	return 10 // Low priority, no dependencies on worker pools
}

// MetricsCollector returns prometheus collectors
func (p *Plugin) MetricsCollector() []interface{} {
	if p.metricsClient == nil {
		return nil
	}

	return []interface{}{
		p.metricsClient.executionsTotal,
		p.metricsClient.skippedTotal,
		p.metricsClient.timeoutTotal,
		p.metricsClient.durationSeconds,
		p.metricsClient.runningJobs,
	}
}

// scheduleJob runs the scheduler loop for a single job
func (p *Plugin) scheduleJob(job *Job) {
	defer p.wg.Done()

	for {
		// Calculate next execution time
		next := job.nextExecution()
		if next.IsZero() {
			p.log.Error("failed to calculate next execution time",
				zap.String("job", job.config.Name),
			)
			return
		}

		// Calculate duration until next execution
		now := time.Now().In(p.location)
		duration := next.Sub(now)

		p.log.Debug("job scheduled",
			zap.String("job", job.config.Name),
			zap.Time("next", next),
			zap.Duration("in", duration),
		)

		// Wait until scheduled time or stop signal
		timer := time.NewTimer(duration)
		select {
		case <-timer.C:
			// Time to execute
			p.executeJob(job)
		case <-p.stopChan:
			// Stop signal received
			timer.Stop()
			return
		}
	}
}

// executeJob executes a single job
func (p *Plugin) executeJob(job *Job) {
	// Try to acquire execution lock
	if !job.config.AllowOverlap {
		if !job.mutex.TryLock() {
			// Job is already running, skip this execution
			p.log.Debug("skipping job - previous execution still running",
				zap.String("job", job.config.Name),
			)
			if p.metricsClient != nil {
				p.metricsClient.recordSkipped(job.config.Name)
			}
			return
		}
		defer job.mutex.Unlock()
	}

	// Mark job as running
	job.mutex.Lock()
	job.running = true
	job.mutex.Unlock()

	if p.metricsClient != nil {
		p.metricsClient.setRunning(job.config.Name, 1)
	}

	// Execute command
	startTime := time.Now()
	p.log.Debug("executing job",
		zap.String("job", job.config.Name),
		zap.String("command", job.config.Command),
	)

	exitCode, err := job.execute(p.ctx)

	duration := time.Since(startTime)

	// Mark job as not running
	job.mutex.Lock()
	job.running = false
	job.mutex.Unlock()

	if p.metricsClient != nil {
		p.metricsClient.setRunning(job.config.Name, 0)
		p.metricsClient.recordDuration(job.config.Name, duration)
	}

	// Handle execution result
	if err != nil {
		if err == context.DeadlineExceeded {
			// Timeout
			p.log.Warn("job timed out",
				zap.String("job", job.config.Name),
				zap.Duration("duration", duration),
			)
			if p.metricsClient != nil {
				p.metricsClient.recordTimeout(job.config.Name)
			}
		} else if err == context.Canceled {
			// Shutdown
			p.log.Debug("job canceled during shutdown",
				zap.String("job", job.config.Name),
			)
		} else {
			// Other error
			p.log.Error("job failed",
				zap.String("job", job.config.Name),
				zap.Error(err),
				zap.Duration("duration", duration),
			)
			if p.metricsClient != nil {
				p.metricsClient.recordExecution(job.config.Name, "failure")
			}
		}
		return
	}

	// Check exit code
	if exitCode == 0 {
		p.log.Debug("job completed successfully",
			zap.String("job", job.config.Name),
			zap.Duration("duration", duration),
		)
		if p.metricsClient != nil {
			p.metricsClient.recordExecution(job.config.Name, "success")
		}
	} else {
		p.log.Error("job failed with non-zero exit code",
			zap.String("job", job.config.Name),
			zap.Int("exit_code", exitCode),
			zap.Duration("duration", duration),
		)
		if p.metricsClient != nil {
			p.metricsClient.recordExecution(job.config.Name, "failure")
		}
	}
}
