package cron

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/adhocore/gronx"
	"go.uber.org/zap"
)

// Job represents a scheduled job
type Job struct {
	config   *JobConfig
	gron     *gronx.Gronx
	location *time.Location
	log      *zap.Logger

	// Execution state
	mutex     *sync.Mutex
	running   bool
	logWriter io.WriteCloser
}

// nextExecution calculates the next execution time for the job
func (j *Job) nextExecution() time.Time {
	now := time.Now().In(j.location)

	// Handle special expressions
	switch j.config.Schedule {
	case "@yearly", "@annually":
		return j.nextYearly(now)
	case "@monthly":
		return j.nextMonthly(now)
	case "@weekly":
		return j.nextWeekly(now)
	case "@daily", "@midnight":
		return j.nextDaily(now)
	case "@hourly":
		return j.nextHourly(now)
	}

	// Handle @every expressions
	if len(j.config.Schedule) > 7 && j.config.Schedule[:6] == "@every" {
		durationStr := j.config.Schedule[7:]
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return time.Time{}
		}
		return now.Add(duration)
	}

	// Standard cron expression - find next matching time
	// Check every minute for the next 2 years (conservative upper bound)
	maxIterations := 365 * 24 * 60 * 2
	checkTime := now.Add(time.Minute)

	for i := 0; i < maxIterations; i++ {
		if j.gron.IsDue(j.config.Schedule, checkTime) {
			return checkTime
		}
		checkTime = checkTime.Add(time.Minute)
	}

	// Should never happen with valid cron expression
	return time.Time{}
}

// Helper methods for special expressions
func (j *Job) nextYearly(now time.Time) time.Time {
	next := time.Date(now.Year()+1, 1, 1, 0, 0, 0, 0, j.location)
	return next
}

func (j *Job) nextMonthly(now time.Time) time.Time {
	year := now.Year()
	month := now.Month() + 1
	if month > 12 {
		month = 1
		year++
	}
	next := time.Date(year, month, 1, 0, 0, 0, 0, j.location)
	return next
}

func (j *Job) nextWeekly(now time.Time) time.Time {
	// Next Sunday at midnight
	daysUntilSunday := (7 - int(now.Weekday())) % 7
	if daysUntilSunday == 0 {
		daysUntilSunday = 7
	}
	next := time.Date(now.Year(), now.Month(), now.Day()+daysUntilSunday, 0, 0, 0, 0, j.location)
	return next
}

func (j *Job) nextDaily(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, j.location)
	return next
}

func (j *Job) nextHourly(now time.Time) time.Time {
	next := now.Add(time.Hour)
	next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), 0, 0, 0, j.location)
	return next
}

// execute runs the job command
func (j *Job) execute(parentCtx context.Context) (int, error) {
	// Create execution context with timeout if configured
	var ctx context.Context
	var cancel context.CancelFunc

	if j.config.Timeout != "" {
		timeout, err := time.ParseDuration(j.config.Timeout)
		if err != nil {
			return -1, fmt.Errorf("invalid timeout: %w", err)
		}
		ctx, cancel = context.WithTimeout(parentCtx, timeout)
	} else {
		ctx, cancel = context.WithCancel(parentCtx)
	}
	defer cancel()

	// Create command
	cmd := exec.CommandContext(ctx, "sh", "-c", j.config.Command)

	// Set up output handling
	if j.logWriter != nil {
		// Write to log file
		cmd.Stdout = j.logWriter
		cmd.Stderr = j.logWriter

		// Write execution header
		timestamp := time.Now().Format(time.RFC3339)
		fmt.Fprintf(j.logWriter, "\n=== Job '%s' started at %s ===\n", j.config.Name, timestamp)
	} else {
		// Discard output
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start command: %w", err)
	}

	// Wait for command to complete
	err := cmd.Wait()

	// Write execution footer
	if j.logWriter != nil {
		timestamp := time.Now().Format(time.RFC3339)
		if err != nil {
			fmt.Fprintf(j.logWriter, "=== Job '%s' failed at %s: %v ===\n", j.config.Name, timestamp, err)
		} else {
			fmt.Fprintf(j.logWriter, "=== Job '%s' completed at %s ===\n", j.config.Name, timestamp)
		}
	}

	// Handle execution result
	if ctx.Err() == context.DeadlineExceeded {
		// Timeout - try graceful shutdown
		if cmd.Process != nil {
			// Send SIGTERM
			_ = cmd.Process.Signal(os.Interrupt)

			// Wait for graceful shutdown
			gracefulDone := make(chan struct{})
			go func() {
				cmd.Wait()
				close(gracefulDone)
			}()

			select {
			case <-gracefulDone:
				// Graceful shutdown succeeded
			case <-time.After(forcefulKillDelay):
				// Force kill
				_ = cmd.Process.Kill()
			}
		}
		return -1, context.DeadlineExceeded
	}

	if ctx.Err() == context.Canceled {
		// Shutdown signal
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			time.Sleep(forcefulKillDelay)
			_ = cmd.Process.Kill()
		}
		return -1, context.Canceled
	}

	// Get exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}

	return 0, nil
}
