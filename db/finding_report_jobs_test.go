package db

import (
	"testing"
	"time"
)

// TestFindingReportJobLifecycle verifies the durable report queue: one job per
// finding, atomic claiming, retry accounting, and manual reset without creating
// a second finding.
func TestFindingReportJobLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	tk, err := d.CreateTask("报告任务测试", "测试", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	var nodeID int64
	if err := d.QueryRow(`SELECT id FROM exploration_nodes WHERE exploration_id=$1 ORDER BY id LIMIT 1`, tk.ExplorationID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	fid, err := d.AddFinding(tk.ID, nodeID, "test-report-job", "报告任务测试", "low", "summary", "evidence", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM findings WHERE id=$1`, fid)

	job, err := d.QueueFindingReportJob(t.Context(), FindingReportJob{
		FindingID: fid, NodeID: nodeID, TaskID: tk.ID, Title: "first", Message: "first message",
		Source: "test",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID == 0 || job.Status != FindingReportJobPending {
		t.Fatalf("queued job: %+v", job)
	}
	again, err := d.QueueFindingReportJob(t.Context(), FindingReportJob{
		FindingID: fid, NodeID: nodeID, TaskID: tk.ID, Title: "duplicate", Message: "duplicate",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != job.ID || again.Message != "first message" {
		t.Fatalf("automatic enqueue duplicated or overwrote job: %+v", again)
	}

	claimed, err := d.ClaimFindingReportJob(t.Context(), time.Minute, fid)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != job.ID || claimed.Status != FindingReportJobRunning || claimed.Attempts != 1 {
		t.Fatalf("claim: %+v", claimed)
	}
	if err := d.FinishFindingReportJob(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	afterFinish, err := d.QueueFindingReportJob(t.Context(), FindingReportJob{
		FindingID: fid, NodeID: nodeID, TaskID: tk.ID, Title: "manual", Message: "manual message",
		Source: "manual",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if afterFinish.ID != job.ID || afterFinish.Status != FindingReportJobPending || afterFinish.Attempts != 0 || afterFinish.Message != "manual message" {
		t.Fatalf("manual reset: %+v", afterFinish)
	}
	claimed, err = d.ClaimFindingReportJob(t.Context(), time.Minute, fid)
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("re-claim after manual reset: %+v err=%v", claimed, err)
	}
	if err := d.FailFindingReportJob(t.Context(), job.ID, "network", true, time.Hour); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := d.QueryRow(`SELECT status,attempts FROM finding_report_jobs WHERE id=$1`, job.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != FindingReportJobRetry || attempts != 1 {
		t.Fatalf("transient failure: status=%s attempts=%d", status, attempts)
	}
	if _, err := d.Exec(`UPDATE finding_report_jobs SET status='paused' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := d.QueueFindingReportJob(t.Context(), FindingReportJob{
		FindingID: fid, NodeID: nodeID, TaskID: tk.ID, Title: "resume", Message: "resume message",
		Source: "manual",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != FindingReportJobPending || resumed.Attempts != 0 {
		t.Fatalf("paused job did not resume: %+v", resumed)
	}
}
