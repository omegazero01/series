package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Finding report job states. A job is deliberately separate from the finding:
// the finding is the evidence record, the job only remembers who should write
// the report and whether that work is still pending.
const (
	FindingReportJobPending = "pending"
	FindingReportJobRunning = "running"
	FindingReportJobRetry   = "retry"
	FindingReportJobDone    = "done"
	FindingReportJobFailed  = "failed"
	FindingReportJobBlocked = "blocked"
)

// FindingReportJob is one durable "write this finding's report" work item.
// Message holds the original trigger context so retries and manual supplements
// do not have to invent a fresh, ID-only reporter conversation.
type FindingReportJob struct {
	ID           int64
	FindingID    int64
	NodeID       int64
	TaskID       int64
	Title        string
	Message      string
	TaskDesc     string
	TaskGoal     string
	LLMProfileID *int64
	Source       string
	Status       string
	Attempts     int
	MaxAttempts  int
	NextRunAt    time.Time
	LeaseUntil   *time.Time
	LastError    string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const findingReportJobCols = `id,finding_id,COALESCE(node_id,0),COALESCE(task_id,0),title,message,
	task_desc,task_goal,llm_profile_id,source,status,attempts,max_attempts,next_run_at,
	lease_until,last_error,started_at,finished_at,created_at,updated_at`

func scanFindingReportJob(sc interface{ Scan(...any) error }) (*FindingReportJob, error) {
	var j FindingReportJob
	var nodeID, taskID sql.NullInt64
	var profileID sql.NullInt64
	var lease, started, finished sql.NullTime
	if err := sc.Scan(
		&j.ID, &j.FindingID, &nodeID, &taskID, &j.Title, &j.Message,
		&j.TaskDesc, &j.TaskGoal, &profileID, &j.Source, &j.Status, &j.Attempts, &j.MaxAttempts, &j.NextRunAt,
		&lease, &j.LastError, &started, &finished, &j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		return nil, err
	}
	j.NodeID = nodeID.Int64
	j.TaskID = taskID.Int64
	if profileID.Valid {
		v := profileID.Int64
		j.LLMProfileID = &v
	}
	if lease.Valid {
		v := lease.Time
		j.LeaseUntil = &v
	}
	if started.Valid {
		v := started.Time
		j.StartedAt = &v
	}
	if finished.Valid {
		v := finished.Time
		j.FinishedAt = &v
	}
	return &j, nil
}

// QueueFindingReportJob inserts the one report job for a finding. reset is used
// by the manual supplement path: a completed/failed/blocked job becomes pending
// again, but a currently running job is never duplicated. Automatic creation
// uses reset=false and is a no-op when the finding already has a job.
func (d *DB) QueueFindingReportJob(ctx context.Context, j FindingReportJob, reset bool) (*FindingReportJob, error) {
	if j.FindingID <= 0 {
		return nil, errors.New("finding_report_jobs: finding_id is required")
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 8
	}
	if j.Source == "" {
		j.Source = "initial"
	}
	if j.Status == "" {
		j.Status = FindingReportJobPending
	}
	var nodeID, taskID any
	if j.NodeID > 0 {
		nodeID = j.NodeID
	}
	if j.TaskID > 0 {
		taskID = j.TaskID
	}
	var profileID any
	if j.LLMProfileID != nil && *j.LLMProfileID > 0 {
		profileID = *j.LLMProfileID
	}

	if !reset {
		row := d.QueryRowContext(ctx, `
INSERT INTO finding_report_jobs
 (finding_id,node_id,task_id,title,message,task_desc,task_goal,llm_profile_id,source,status,max_attempts,next_run_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
ON CONFLICT (finding_id) DO NOTHING
RETURNING `+findingReportJobCols,
			j.FindingID, nodeID, taskID, j.Title, j.Message, j.TaskDesc, j.TaskGoal, profileID, j.Source, j.Status, j.MaxAttempts)
		out, err := scanFindingReportJob(row)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		row = d.QueryRowContext(ctx, `SELECT `+findingReportJobCols+` FROM finding_report_jobs WHERE finding_id=$1`, j.FindingID)
		return scanFindingReportJob(row)
	}

	row := d.QueryRowContext(ctx, `
INSERT INTO finding_report_jobs
 (finding_id,node_id,task_id,title,message,task_desc,task_goal,llm_profile_id,source,status,max_attempts,next_run_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
ON CONFLICT (finding_id) DO UPDATE SET
 title=EXCLUDED.title,
 message=EXCLUDED.message,
 task_desc=EXCLUDED.task_desc,
 task_goal=EXCLUDED.task_goal,
 llm_profile_id=COALESCE(EXCLUDED.llm_profile_id, finding_report_jobs.llm_profile_id),
 source=EXCLUDED.source,
 status=CASE
   WHEN finding_report_jobs.status IN ('done','failed','blocked','paused') THEN 'pending'
   ELSE finding_report_jobs.status
 END,
 attempts=CASE
   WHEN finding_report_jobs.status IN ('done','failed','blocked','paused') THEN 0
   ELSE finding_report_jobs.attempts
 END,
 next_run_at=CASE
   WHEN finding_report_jobs.status IN ('done','failed','blocked','paused') THEN now()
   ELSE finding_report_jobs.next_run_at
 END,
 last_error=CASE
   WHEN finding_report_jobs.status IN ('done','failed','blocked','paused') THEN ''
   ELSE finding_report_jobs.last_error
 END,
 finished_at=CASE
   WHEN finding_report_jobs.status IN ('done','failed','blocked','paused') THEN NULL
   ELSE finding_report_jobs.finished_at
 END,
 updated_at=now()
RETURNING `+findingReportJobCols,
		j.FindingID, nodeID, taskID, j.Title, j.Message, j.TaskDesc, j.TaskGoal, profileID, j.Source, j.Status, j.MaxAttempts)
	return scanFindingReportJob(row)
}

// ClaimFindingReportJob atomically leases one due report job. It never waits for
// a row, so the caller can use it from a small side worker without holding any
// task or worker scheduling resource.
func (d *DB) ClaimFindingReportJob(ctx context.Context, lease time.Duration, findingID ...int64) (*FindingReportJob, error) {
	if lease <= 0 {
		lease = 30 * time.Minute
	}
	filter := ""
	args := []any{lease.Seconds()}
	if len(findingID) > 0 && findingID[0] > 0 {
		filter = " AND finding_id=$2"
		args = append(args, findingID[0])
	}
	row := d.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id
    FROM finding_report_jobs
    WHERE status IN ('pending','retry')
      AND next_run_at <= now()
      AND attempts < max_attempts
      AND (lease_until IS NULL OR lease_until < now())`+filter+`
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE finding_report_jobs j
SET status='running',
    attempts=j.attempts+1,
    lease_until=now()+($1::double precision * interval '1 second'),
    started_at=COALESCE(j.started_at,now()),
    updated_at=now()
FROM candidate c
WHERE j.id=c.id
RETURNING j.id,j.finding_id,COALESCE(j.node_id,0),COALESCE(j.task_id,0),j.title,j.message,
	j.task_desc,j.task_goal,j.llm_profile_id,j.source,j.status,j.attempts,j.max_attempts,j.next_run_at,
	j.lease_until,j.last_error,j.started_at,j.finished_at,j.created_at,j.updated_at`, args...)
	out, err := scanFindingReportJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

// FinishFindingReportJob marks a successful report write. The report text itself
// remains on the finding; the job only records that the work is complete.
func (d *DB) FinishFindingReportJob(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `
UPDATE finding_report_jobs
SET status='done', lease_until=NULL, finished_at=now(), last_error='', updated_at=now()
WHERE id=$1`, id)
	return err
}

// FailFindingReportJob records a failed attempt. Transient failures become
// retry jobs with a bounded backoff; permanent failures become failed/blocked
// and are never silently retried forever.
func (d *DB) FailFindingReportJob(ctx context.Context, id int64, reason string, retry bool, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	_, err := d.ExecContext(ctx, `
UPDATE finding_report_jobs
SET status=CASE WHEN $2::boolean AND attempts < max_attempts THEN 'retry' ELSE 'failed' END,
    lease_until=NULL,
    next_run_at=CASE WHEN $2::boolean AND attempts < max_attempts
                     THEN now()+($3::double precision * interval '1 second')
                     ELSE next_run_at END,
    last_error=$4,
    finished_at=CASE WHEN $2::boolean AND attempts < max_attempts THEN NULL ELSE now() END,
    updated_at=now()
WHERE id=$1`, id, retry, delay.Seconds(), reason)
	return err
}

// RecoverFindingReportJobs requeues jobs that were left running when the
// previous process stopped. It is called once at startup, before the report
// worker starts claiming work.
func (d *DB) RecoverFindingReportJobs(ctx context.Context) (int64, error) {
	res, err := d.ExecContext(ctx, `
UPDATE finding_report_jobs
SET status='retry', lease_until=NULL, next_run_at=now(),
    last_error='报告进程重启，任务已重新排队', updated_at=now()
WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
