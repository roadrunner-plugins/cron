package cron

import (
	"fmt"
	"time"
)

// Config represents the cron plugin configuration
type Config struct {
	// Timezone for cron schedule interpretation (default: UTC)
	Timezone string `mapstructure:"timezone"`

	// GracePeriod is the time to wait for running jobs during shutdown (default: 30s)
	GracePeriod string `mapstructure:"grace_period"`

	// Jobs is the list of scheduled jobs
	Jobs []*JobConfig `mapstructure:"jobs"`
}

// JobConfig represents a single scheduled job configuration
type JobConfig struct {
	// Name is the unique identifier for the job
	Name string `mapstructure:"name"`

	// Command is the shell command to execute
	Command string `mapstructure:"command"`

	// Schedule is the cron expression defining when to run
	Schedule string `mapstructure:"schedule"`

	// AllowOverlap allows concurrent executions of the same job (default: false)
	AllowOverlap bool `mapstructure:"allow_overlap"`

	// Timeout is the maximum execution time before SIGTERM is sent
	// Format: "1h", "30m", "90s", etc.
	Timeout string `mapstructure:"timeout"`

	// LogFile is the path to the file where command output will be written
	// If empty, output is discarded
	LogFile string `mapstructure:"log_file"`

	// MaxLogSize is the maximum size of the log file before rotation (in bytes)
	// Default: 10MB
	MaxLogSize int64 `mapstructure:"max_log_size"`

	// MaxLogFiles is the number of rotated log files to keep
	// Default: 5
	MaxLogFiles int `mapstructure:"max_log_files"`
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if len(c.Jobs) == 0 {
		return fmt.Errorf("no jobs configured")
	}

	// Check for duplicate job names
	names := make(map[string]bool)
	for _, job := range c.Jobs {
		if err := job.Validate(); err != nil {
			return fmt.Errorf("invalid job '%s': %w", job.Name, err)
		}

		if names[job.Name] {
			return fmt.Errorf("duplicate job name: %s", job.Name)
		}
		names[job.Name] = true
	}

	// Validate grace period if set
	if c.GracePeriod != "" {
		if _, err := time.ParseDuration(c.GracePeriod); err != nil {
			return fmt.Errorf("invalid grace_period format: %w", err)
		}
	}

	return nil
}

// Validate validates the job configuration
func (j *JobConfig) Validate() error {
	if j.Name == "" {
		return fmt.Errorf("job name is required")
	}

	if j.Command == "" {
		return fmt.Errorf("job command is required")
	}

	if j.Schedule == "" {
		return fmt.Errorf("job schedule is required")
	}

	// Validate timeout if set
	if j.Timeout != "" {
		if _, err := time.ParseDuration(j.Timeout); err != nil {
			return fmt.Errorf("invalid timeout format: %w", err)
		}
	}

	// Validate log size limits
	if j.MaxLogSize < 0 {
		return fmt.Errorf("max_log_size cannot be negative")
	}

	if j.MaxLogFiles < 0 {
		return fmt.Errorf("max_log_files cannot be negative")
	}

	return nil
}
