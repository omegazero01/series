package server

import (
	"testing"

	"github.com/neouks/series/db"
)

// TestFindingRecordQueuesDurableReportJob verifies the logical boundary at the
// write side: recording a finding creates one report job and does not change
// the task's goal/status. The report worker is intentionally not started in
// this fixture; only the durable enqueue contract is tested.
func TestFindingRecordQueuesDurableReportJob(t *testing.T) {
	s, in, _ := suppressionFixture(t)
	out, err := s.findingRecorder().Record(t.Context(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.FindingID <= 0 {
		t.Fatalf("missing recorded finding: %+v", out)
	}
	var job db.FindingReportJob
	var nodeID int64
	if err := s.m.pg.QueryRow(`SELECT finding_id,COALESCE(node_id,0),COALESCE(task_id,0),status,source,message
FROM finding_report_jobs WHERE finding_id=$1`, out.FindingID).Scan(
		&job.FindingID, &nodeID, &job.TaskID, &job.Status, &job.Source, &job.Message,
	); err != nil {
		t.Fatal(err)
	}
	if job.FindingID != out.FindingID || nodeID != out.NodeID || job.TaskID != in.TaskID {
		t.Fatalf("job IDs: job=%+v out=%+v", job, out)
	}
	if job.Status != db.FindingReportJobPending || job.Source != "initial" {
		t.Fatalf("job state: %+v", job)
	}
	if job.Message == "" {
		t.Fatal("report job lost its original trigger message")
	}

	var goal, status string
	if err := s.m.pg.QueryRow(`SELECT goal,status FROM tasks WHERE id=$1`, in.TaskID).Scan(&goal, &status); err != nil {
		t.Fatal(err)
	}
	if goal != "goal" {
		t.Fatalf("report job changed task goal: %q", goal)
	}
	if status == "" {
		t.Fatal("task status disappeared")
	}
}
