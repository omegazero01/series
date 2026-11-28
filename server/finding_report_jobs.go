package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/neouks/series/db"
)

// finding_report_jobs is a side-channel queue. It deliberately does not use
// StartTriggeredRun or the work-agent scheduler: a report job may read finding
// evidence and write report fields, but it cannot add intents/assets, run a
// target request, change task state, or wake the planner. This keeps report
// generation logically downstream of penetration testing.
const (
	findingReportJobLease   = 30 * time.Minute
	findingReportJobMaxWait = 2 * time.Second
)

// buildInitialFindingReportJobMessage preserves the original report trigger
// context (task/finding/node IDs, the report_finding input summary and the tool
// response) so a retry is not a fresh ID-only conversation.
func buildInitialFindingReportJobMessage(out *db.RecordedFinding, in db.RecordFindingInput) string {
	summary := trunc(in.Summary, 3000)
	evidence := trunc(in.Evidence, 6000)
	input, _ := json.Marshal(map[string]any{
		"vulnclass":    in.VulnClass,
		"name":         in.Name,
		"severity":     in.Severity,
		"summary":      summary,
		"evidence":     evidence,
		"reproduction": in.Reproduction,
		"intent_id":    in.IntentID,
		"asset_ids":    in.AssetIDs,
	})
	msg := reporterToolCallMessage + fmt.Sprintf(
		"\n\n【本次由工具调用触发】\n工具: report_finding\n入参: %s\n返回: finding recorded: %d\n"+
			"\n【报告任务】任务 #%d，finding_id=%d，finding_node_id=%d。请先读现场复现和证据，再写报告；不要新建漏洞，也不要重新请求目标。",
		trunc(string(input), 5000), out.NodeID, in.TaskID, out.FindingID, out.NodeID,
	)
	return msg
}

// buildManualFindingReportJobMessage is used for old findings that have no
// original job, and for an explicit reissue from the findings page.
func buildManualFindingReportJobMessage(f *db.DBFinding) string {
	var nodeID int64
	if f.NodeID != nil {
		nodeID = *f.NodeID
	}
	var taskID int64
	if f.TaskID != nil {
		taskID = *f.TaskID
	}
	return fmt.Sprintf(
		"【补出提交报告】请为下面这个已登记的漏洞补出或重新出具提交版报告，不要调用 report_finding 新建漏洞。\n"+
			"finding_id=%d（独立漏洞记录 ID）\n"+
			"finding_node_id=%d（探索节点 ID）\n"+
			"task_id=%d\n"+
			"先用 context_get 读取当前漏洞和现场复现记录，再用 get_finding_traffic(finding_id=%d) 读取已有流量证据及 version；空清单正常，照常写报告。\n"+
			"按报告撰写规范写完后，调用 update_finding_report(finding_node_id=%d, report=完整 Markdown, evidence_version=实际读取的版本) 保存。\n"+
			"如果已有旧报告，整体覆盖；不要修改漏洞名称、等级、类型和证据原文。",
		f.ID, nodeID, taskID, f.ID, nodeID,
	)
}

// disableLegacyReporterTrigger removes the old in-memory P3 path for the
// built-in reporter. It runs once on an upgraded database; a user-created
// trigger for a different agent is not touched.
func (s *Server) disableLegacyReporterTrigger() {
	const flag = "finding_report_jobs_legacy_trigger_disabled_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.DisableReporterToolTriggers(); err != nil {
		log.Printf("[report-job] disable legacy reporter trigger: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

func (s *Server) enqueueInitialFindingReportJob(out *db.RecordedFinding, in db.RecordFindingInput) {
	if s == nil || s.m == nil || s.m.pg == nil || out == nil || out.FindingID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	profileID := s.triggerLLMProfilePin(in.TaskID)
	taskDesc, taskGoal := "", ""
	if in.TaskID > 0 {
		if task := s.m.ResolveTask(strconv.FormatInt(in.TaskID, 10)); task != nil {
			taskDesc, taskGoal = task.Description, task.Goal
		}
	}
	job := db.FindingReportJob{
		FindingID:    out.FindingID,
		NodeID:       out.NodeID,
		TaskID:       in.TaskID,
		Title:        fmt.Sprintf("报告撰写 · finding#%d", out.FindingID),
		Message:      buildInitialFindingReportJobMessage(out, in),
		TaskDesc:     taskDesc,
		TaskGoal:     taskGoal,
		LLMProfileID: profileID,
		Source:       "initial",
	}
	if _, err := s.m.pg.QueueFindingReportJob(ctx, job, false); err != nil {
		// A report queue failure must never fail the finding. The finding remains
		// usable and the user can later use the explicit supplement button.
		log.Printf("[report-job] enqueue finding#%d failed: %v", out.FindingID, err)
	}
}

func (s *Server) enqueueManualFindingReportJob(ctx context.Context, f *db.DBFinding) error {
	if f == nil || f.ID <= 0 {
		return fmt.Errorf("漏洞不存在")
	}
	if f.NodeID == nil {
		return fmt.Errorf("漏洞没有探索节点，无法写回报告")
	}
	var taskID int64
	if f.TaskID != nil {
		taskID = *f.TaskID
	}
	profileID := s.triggerLLMProfilePin(taskID)
	job := db.FindingReportJob{
		FindingID:    f.ID,
		NodeID:       *f.NodeID,
		TaskID:       taskID,
		Title:        fmt.Sprintf("报告撰写 · finding#%d", f.ID),
		Message:      buildManualFindingReportJobMessage(f),
		TaskDesc:     f.TaskDescription,
		LLMProfileID: profileID,
		Source:       "manual",
	}
	_, err := s.m.pg.QueueFindingReportJob(ctx, job, true)
	return err
}

// runFindingReportWorker owns report execution. It has no relationship to the
// planner/worker queue and processes one job at a time. A report crash therefore
// leaves the finding and the penetration task untouched; the next process run
// recovers the job from the database.
func (s *Server) runFindingReportWorker(ctx context.Context) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return
	}
	if n, err := s.m.pg.RecoverFindingReportJobs(ctx); err != nil {
		log.Printf("[report-job] recover interrupted jobs: %v", err)
	} else if n > 0 {
		log.Printf("[report-job] recovered %d interrupted report job(s)", n)
	}
	ticker := time.NewTicker(findingReportJobMaxWait)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			job, err := s.m.pg.ClaimFindingReportJob(ctx, findingReportJobLease)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[report-job] claim: %v", err)
				}
				break
			}
			if job == nil {
				break
			}
			s.runFindingReportJob(ctx, job)
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func (s *Server) runFindingReportJob(ctx context.Context, job *db.FindingReportJob) {
	if job == nil {
		return
	}
	before, _ := s.m.pg.GetFinding(job.FindingID)
	beforeReport := ""
	if before != nil {
		beforeReport = strings.TrimSpace(before.Report)
	}

	item := triggeredRun{
		agentKey:     "reporter",
		title:        job.Title,
		message:      job.Message,
		taskID:       job.TaskID,
		taskDesc:     job.TaskDesc,
		taskGoal:     job.TaskGoal,
		llmProfileID: job.LLMProfileID,
	}
	s.runTriggeredRun(item)

	if ctx.Err() != nil {
		// Leave the lease to expire; startup recovery will requeue it.
		return
	}
	after, err := s.m.pg.GetFinding(job.FindingID)
	if err != nil {
		s.failFindingReportJob(ctx, job, "读取报告结果失败: "+err.Error(), true)
		return
	}
	if after != nil {
		afterReport := strings.TrimSpace(after.Report)
		if afterReport != "" && (beforeReport == "" || afterReport != beforeReport) {
			if err := s.m.pg.FinishFindingReportJob(ctx, job.ID); err != nil {
				log.Printf("[report-job] finish #%d: %v", job.ID, err)
			}
			return
		}
	}
	s.failFindingReportJob(ctx, job, "报告会话结束但没有写入新的提交版报告", true)
}

func (s *Server) failFindingReportJob(ctx context.Context, job *db.FindingReportJob, reason string, retry bool) {
	delay := time.Duration(30*float64(maxInt(job.Attempts, 1))) * time.Second
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	if err := s.m.pg.FailFindingReportJob(ctx, job.ID, trunc(reason, 2000), retry, delay); err != nil {
		log.Printf("[report-job] mark failed #%d: %v", job.ID, err)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
