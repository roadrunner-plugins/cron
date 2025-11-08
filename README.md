# RoadRunner Cron Plugin

A native RoadRunner plugin for executing scheduled tasks using cron expressions. Eliminates the need for external cron
daemons by managing scheduled command execution within the RoadRunner process itself.

## Features

- **Standard Cron Syntax**: Supports both 5-field and 6-field (with seconds) cron expressions
- **Special Expressions**: Built-in support for `@daily`, `@hourly`, `@every`, and more
- **Overlap Prevention**: Configurable control over concurrent job executions
- **Timeout Management**: Graceful termination with SIGTERM/SIGKILL progression
- **Log Rotation**: Size-based automatic log rotation with configurable retention
- **Prometheus Metrics**: Comprehensive observability including execution counts, durations, and errors
- **Graceful Shutdown**: Respects running jobs during RoadRunner shutdown

## Configuration

Add a `cron` section to your `.rr.yaml`:

```yaml
version: "3"

cron:
  # Global settings
  timezone: "UTC"              # Timezone for schedule interpretation (default: UTC)
  grace_period: "30s"          # Time to wait for jobs during shutdown (default: 30s)

  # Scheduled jobs
  jobs:
    - name: "cache-warmup"
      command: "php artisan cache:warmup"
      schedule: "0 */6 * * *"  # Every 6 hours
      allow_overlap: false     # Prevent concurrent executions (default: false)
      timeout: "5m"            # Maximum execution time
      log_file: "/var/log/cron/cache-warmup.log"
      max_log_size: 10485760   # 10MB (default)
      max_log_files: 5         # Keep 5 rotated files (default)

    - name: "queue-cleanup"
      command: "php artisan queue:prune-failed --hours=48"
      schedule: "@daily"
      timeout: "10m"
      log_file: "/var/log/cron/queue-cleanup.log"

    - name: "health-check"
      command: "php artisan app:health-check"
      schedule: "@every 30s"   # Every 30 seconds
      allow_overlap: true      # Allow concurrent checks
      timeout: "5s"
      log_file: "/var/log/cron/health-check.log"

# Optional: Enable metrics
metrics:
  address: "0.0.0.0:2112"
```

## Cron Expression Syntax

### Standard 5-field format:

```
* * * * *
│ │ │ │ │
│ │ │ │ └─── Day of week (0-6, Sunday=0)
│ │ │ └───── Month (1-12)
│ │ └─────── Day of month (1-31)
│ └───────── Hour (0-23)
└─────────── Minute (0-59)
```

### Extended 6-field format (with seconds):

```
* * * * * *
│ │ │ │ │ │
│ │ │ │ │ └─── Day of week (0-6)
│ │ │ │ └───── Month (1-12)
│ │ │ └─────── Day of month (1-31)
│ └───────── Hour (0-23)
└─────────── Minute (0-59)
Second (0-59)
```

### Special expressions:

- `@yearly` / `@annually` - Run once a year at midnight on January 1st
- `@monthly` - Run once a month at midnight on the first day
- `@weekly` - Run once a week at midnight on Sunday
- `@daily` / `@midnight` - Run once a day at midnight
- `@hourly` - Run at the beginning of every hour
- `@every <duration>` - Run at fixed intervals (e.g., `@every 5m`, `@every 30s`)

### Examples:

```yaml
schedule: "*/15 * * * *"      # Every 15 minutes
schedule: "0 2 * * *"         # Daily at 2 AM
schedule: "0 0 * * 0"         # Every Sunday at midnight
schedule: "30 3 15 * *"       # 3:30 AM on the 15th of each month
schedule: "@every 1h30m"      # Every 1 hour 30 minutes
```

## Configuration Options

### Job Configuration

| Field           | Type     | Required | Default    | Description                                    |
|-----------------|----------|----------|------------|------------------------------------------------|
| `name`          | string   | Yes      | -          | Unique identifier for the job                  |
| `command`       | string   | Yes      | -          | Shell command to execute                       |
| `schedule`      | string   | Yes      | -          | Cron expression                                |
| `allow_overlap` | bool     | No       | `false`    | Allow concurrent executions                    |
| `timeout`       | duration | No       | none       | Maximum execution time                         |
| `log_file`      | string   | No       | -          | Path to log file (output discarded if not set) |
| `max_log_size`  | int64    | No       | `10485760` | Max log file size in bytes (10MB)              |
| `max_log_files` | int      | No       | `5`        | Number of rotated log files to keep            |

### Global Configuration

| Field          | Type     | Required | Default | Description                                   |
|----------------|----------|----------|---------|-----------------------------------------------|
| `timezone`     | string   | No       | `UTC`   | Timezone for schedule interpretation          |
| `grace_period` | duration | No       | `30s`   | Time to wait for running jobs during shutdown |

## Log Output

When `log_file` is configured, the plugin writes command output with execution markers:

```bash
=== Job 'cache-warmup' started at 2024-01-15T10:00:00Z ===
Cache warmup started...
Processing 1500 items...
Cache warmup completed successfully
=== Job 'cache-warmup' completed at 2024-01-15T10:02:15Z ===

=== Job 'cache-warmup' started at 2024-01-15T16:00:00Z ===
Cache warmup started...
Error: Connection timeout
=== Job 'cache-warmup' failed at 2024-01-15T16:00:30Z: exit status 1 ===
```

Log files are automatically rotated when they reach `max_log_size`. Old logs are kept according to `max_log_files`
setting.

## Prometheus Metrics

The plugin exposes the following metrics when the metrics plugin is enabled:

### Counters:

- `roadrunner_cron_executions_total{job_name, status}` - Total executions (status: success, failure)
- `roadrunner_cron_skipped_total{job_name}` - Skipped executions due to overlap prevention
- `roadrunner_cron_timeout_total{job_name}` - Executions terminated due to timeout

### Histograms:

- `roadrunner_cron_execution_duration_seconds{job_name}` - Execution duration
    - Buckets: 0.1, 0.5, 1, 5, 10, 30, 60, 120, 300 seconds

### Gauges:

- `roadrunner_cron_running_jobs{job_name}` - Currently running job instances

### Example Queries:

**Job success rate:**

```promql
rate(roadrunner_cron_executions_total{status="success"}[5m]) 
/ 
rate(roadrunner_cron_executions_total[5m])
```

**Jobs timing out:**

```promql
increase(roadrunner_cron_timeout_total[1h])
```

**Average execution duration:**

```promql
rate(roadrunner_cron_execution_duration_seconds_sum[5m]) 
/ 
rate(roadrunner_cron_execution_duration_seconds_count[5m])
```

**Jobs currently running:**

```promql
sum(roadrunner_cron_running_jobs) by (job_name)
```

## Behavior Details

### Overlap Prevention

When `allow_overlap: false` (default):

- If a job is already running, subsequent scheduled executions are skipped
- Skipped executions are logged and counted in metrics
- No queuing of missed executions

When `allow_overlap: true`:

- Multiple instances can run concurrently
- No limit on concurrent executions (use with caution for resource-intensive commands)

### Timeout Handling

When a job exceeds its configured timeout:

1. SIGTERM is sent to the process
2. Plugin waits 5 seconds for graceful shutdown
3. If still running, SIGKILL is sent
4. Timeout is logged and recorded in metrics

### Graceful Shutdown

When RoadRunner stops:

1. No new job executions are scheduled
2. SIGTERM is sent to all running processes
3. Plugin waits for `grace_period` (default: 30s)
4. Remaining processes are terminated with SIGKILL
5. All log files are properly closed

## Logging

Enable debug logging to see detailed execution information:

```yaml
logs:
  level: debug
```

Debug messages include:

- Job scheduling: "Job scheduled: next execution at..."
- Execution start: "Executing job 'name': command"
- Successful completion: "Job 'name' completed successfully in 1.5s"
- Failures: "Job 'name' failed with exit code 1"
- Skipped executions: "Skipping job 'name' - previous execution still running"
- Timeouts: "Job 'name' timed out after 5m"

## Best Practices

1. **Use meaningful job names**: They appear in logs and metrics
2. **Set appropriate timeouts**: Prevent runaway processes
3. **Configure log rotation**: Especially for frequently-running jobs
4. **Monitor metrics**: Set up alerts for failures and timeouts
5. **Test schedules**: Use `@every` for testing before deploying complex cron expressions
6. **Avoid overlap for heavy jobs**: Set `allow_overlap: false` for resource-intensive commands
7. **Stagger schedules**: Don't schedule all jobs at the same time (e.g., avoid all jobs at midnight)
8. **Handle SIGTERM in scripts**: Make long-running commands respond to termination signals

## Limitations

- Jobs are not persisted across RoadRunner restarts
- No distributed coordination (for multi-instance deployments)
- No built-in retry logic for failed jobs
- All jobs use the same timezone (no per-job timezone support)
- Command execution happens in shell context (security consideration)

## License

MIT License
